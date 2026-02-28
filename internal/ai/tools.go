// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/dao"
	"github.com/derailed/k9s/internal/render"
	copilot "github.com/github/copilot-sdk/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"gopkg.in/yaml.v3"
)

// ToolFactory creates Copilot tools backed by a live K8s cluster connection.
type ToolFactory struct {
	factory dao.Factory
	conn    client.Connection
	log     *slog.Logger
}

// NewToolFactory creates a new tool factory.
func NewToolFactory(factory dao.Factory, conn client.Connection, log *slog.Logger) *ToolFactory {
	if log == nil {
		log = slog.Default()
	}
	return &ToolFactory{
		factory: factory,
		conn:    conn,
		log:     log,
	}
}

// BuildTools returns all Kubernetes-aware tools for the Copilot session.
func (tf *ToolFactory) BuildTools() []copilot.Tool {
	return []copilot.Tool{
		tf.getResourceTool(),
		tf.listResourcesTool(),
		tf.describeResourceTool(),
		tf.getLogsTool(),
		tf.getEventsTool(),
		tf.getClusterHealthTool(),
		tf.getPodDiagnosticsTool(),
		tf.checkRBACTool(),
	}
}

// --- get_resource tool ---

type getResourceParams struct {
	GVR       string `json:"gvr" jsonschema:"Group/Version/Resource identifier, e.g. v1/pods, apps/v1/deployments"`
	Name      string `json:"name" jsonschema:"Resource name"`
	Namespace string `json:"namespace" jsonschema:"Kubernetes namespace (empty for cluster-scoped)"`
}

func (tf *ToolFactory) getResourceTool() copilot.Tool {
	return copilot.DefineTool(
		"get_resource",
		"Fetch a specific Kubernetes resource by GVR, name, and namespace. Returns the resource as YAML.",
		func(params getResourceParams, inv copilot.ToolInvocation) (any, error) {
			gvr := client.NewGVR(params.GVR)
			path := params.Name
			if params.Namespace != "" {
				path = params.Namespace + "/" + params.Name
			}

			obj, err := tf.factory.Get(gvr, path, true, labels.Everything())
			if err != nil {
				return nil, fmt.Errorf("failed to get %s %s: %w", params.GVR, path, err)
			}

			return objectToYAML(obj)
		},
	)
}

// --- list_resources tool ---

type listResourcesParams struct {
	GVR           string `json:"gvr" jsonschema:"Group/Version/Resource identifier, e.g. v1/pods, apps/v1/deployments"`
	Namespace     string `json:"namespace" jsonschema:"Kubernetes namespace (empty for all namespaces)"`
	LabelSelector string `json:"labelSelector,omitempty" jsonschema:"Label selector to filter resources, e.g. app=web"`
	Limit         int    `json:"limit,omitempty" jsonschema:"Maximum number of resources to return (default 50)"`
}

func (tf *ToolFactory) listResourcesTool() copilot.Tool {
	return copilot.DefineTool(
		"list_resources",
		"List Kubernetes resources of a given type. Returns a summary table with key fields (name, namespace, status, age).",
		func(params listResourcesParams, inv copilot.ToolInvocation) (any, error) {
			gvr := client.NewGVR(params.GVR)
			ns := params.Namespace

			sel := labels.Everything()
			if params.LabelSelector != "" {
				var err error
				sel, err = labels.Parse(params.LabelSelector)
				if err != nil {
					return nil, fmt.Errorf("invalid label selector %q: %w", params.LabelSelector, err)
				}
			}

			objs, err := tf.factory.List(gvr, ns, true, sel)
			if err != nil {
				return nil, fmt.Errorf("failed to list %s in %s: %w", params.GVR, ns, err)
			}

			limit := params.Limit
			if limit <= 0 {
				limit = 50
			}

			var results []map[string]string
			for i, obj := range objs {
				if i >= limit {
					break
				}
				u, ok := obj.(*unstructured.Unstructured)
				if !ok {
					continue
				}
				item := map[string]string{
					"name":      u.GetName(),
					"namespace": u.GetNamespace(),
				}
				// Extract status phase if available
				if status, found, _ := unstructured.NestedString(u.Object, "status", "phase"); found {
					item["status"] = status
				}
				if t := u.GetCreationTimestamp(); !t.IsZero() {
					item["age"] = render.ToAge(metav1.Time(t))
				}
				results = append(results, item)
			}

			summary := fmt.Sprintf("Found %d %s resources", len(objs), params.GVR)
			if len(objs) > limit {
				summary += fmt.Sprintf(" (showing first %d)", limit)
			}

			return map[string]any{
				"summary":   summary,
				"resources": results,
			}, nil
		},
	)
}

// --- describe_resource tool ---

type describeResourceParams struct {
	GVR       string `json:"gvr" jsonschema:"Group/Version/Resource identifier"`
	Name      string `json:"name" jsonschema:"Resource name"`
	Namespace string `json:"namespace" jsonschema:"Kubernetes namespace"`
}

func (tf *ToolFactory) describeResourceTool() copilot.Tool {
	return copilot.DefineTool(
		"describe_resource",
		"Get the full kubectl-style description of a Kubernetes resource, including events and conditions.",
		func(params describeResourceParams, inv copilot.ToolInvocation) (any, error) {
			gvr := client.NewGVR(params.GVR)
			path := params.Name
			if params.Namespace != "" {
				path = params.Namespace + "/" + params.Name
			}

			desc, err := dao.Describe(tf.conn, gvr, path)
			if err != nil {
				return nil, fmt.Errorf("failed to describe %s %s: %w", params.GVR, path, err)
			}

			return desc, nil
		},
	)
}

// --- get_logs tool ---

type getLogsParams struct {
	PodName   string `json:"podName" jsonschema:"Pod name"`
	Namespace string `json:"namespace" jsonschema:"Pod namespace"`
	Container string `json:"container,omitempty" jsonschema:"Container name (empty for all containers)"`
	TailLines int64  `json:"tailLines,omitempty" jsonschema:"Number of lines from the end (default 100)"`
	Previous  bool   `json:"previous,omitempty" jsonschema:"If true, return previous container logs (useful for crash analysis)"`
}

func (tf *ToolFactory) getLogsTool() copilot.Tool {
	return copilot.DefineTool(
		"get_logs",
		"Fetch container logs for a pod. Essential for diagnosing CrashLoopBackOff, application errors, and runtime issues.",
		func(params getLogsParams, inv copilot.ToolInvocation) (any, error) {
			dial, err := tf.conn.Dial()
			if err != nil {
				return nil, fmt.Errorf("failed to connect to cluster: %w", err)
			}

			tailLines := params.TailLines
			if tailLines <= 0 {
				tailLines = 100
			}

			opts := &metav1.PodLogOptions{
				Container: params.Container,
				TailLines: &tailLines,
				Previous:  params.Previous,
			}

			if params.Container == "" {
				// If no container specified, omit so we get the default
				opts.Container = ""
			}

			req := dial.CoreV1().Pods(params.Namespace).GetLogs(params.PodName, (*metav1.PodLogOptions)(nil))
			// Use typed pod log options
			req = dial.CoreV1().Pods(params.Namespace).GetLogs(params.PodName, &metav1.PodLogOptions{
				Container: params.Container,
				TailLines: &tailLines,
				Previous:  params.Previous,
			})

			stream, err := req.Stream(context.Background())
			if err != nil {
				return nil, fmt.Errorf("failed to stream logs for %s/%s: %w", params.Namespace, params.PodName, err)
			}
			defer stream.Close()

			var buf bytes.Buffer
			maxBytes := int64(256 * 1024) // 256KB limit
			limited := &io.LimitedReader{R: stream, N: maxBytes}
			if _, err := buf.ReadFrom(limited); err != nil {
				return nil, fmt.Errorf("failed to read logs: %w", err)
			}

			return buf.String(), nil
		},
	)
}

// --- get_events tool ---

type getEventsParams struct {
	Namespace    string `json:"namespace,omitempty" jsonschema:"Namespace to filter events (empty for all)"`
	ResourceName string `json:"resourceName,omitempty" jsonschema:"Filter events by involved object name"`
	EventType    string `json:"eventType,omitempty" jsonschema:"Filter by event type: Normal or Warning"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum number of events to return (default 30)"`
}

func (tf *ToolFactory) getEventsTool() copilot.Tool {
	return copilot.DefineTool(
		"get_events",
		"Fetch Kubernetes events, optionally filtered by namespace, resource, or type. Events reveal scheduling failures, image pulls, OOM kills, and more.",
		func(params getEventsParams, inv copilot.ToolInvocation) (any, error) {
			dial, err := tf.conn.Dial()
			if err != nil {
				return nil, fmt.Errorf("failed to connect to cluster: %w", err)
			}

			ns := params.Namespace
			opts := metav1.ListOptions{}
			if params.ResourceName != "" {
				opts.FieldSelector = "involvedObject.name=" + params.ResourceName
			}

			events, err := dial.CoreV1().Events(ns).List(context.Background(), opts)
			if err != nil {
				return nil, fmt.Errorf("failed to list events: %w", err)
			}

			limit := params.Limit
			if limit <= 0 {
				limit = 30
			}

			var results []map[string]string
			for i := len(events.Items) - 1; i >= 0 && len(results) < limit; i-- {
				ev := events.Items[i]
				if params.EventType != "" && ev.Type != params.EventType {
					continue
				}
				results = append(results, map[string]string{
					"type":      ev.Type,
					"reason":    ev.Reason,
					"message":   ev.Message,
					"object":    ev.InvolvedObject.Kind + "/" + ev.InvolvedObject.Name,
					"count":     fmt.Sprintf("%d", ev.Count),
					"firstSeen": ev.FirstTimestamp.String(),
					"lastSeen":  ev.LastTimestamp.String(),
				})
			}

			return map[string]any{
				"total":  len(events.Items),
				"events": results,
			}, nil
		},
	)
}

// --- get_cluster_health tool ---

type getClusterHealthParams struct{}

func (tf *ToolFactory) getClusterHealthTool() copilot.Tool {
	return copilot.DefineTool(
		"get_cluster_health",
		"Get a high-level cluster health overview: node count, pod counts by status, resource utilization summary.",
		func(params getClusterHealthParams, inv copilot.ToolInvocation) (any, error) {
			dial, err := tf.conn.Dial()
			if err != nil {
				return nil, fmt.Errorf("failed to connect to cluster: %w", err)
			}

			// Get nodes
			nodes, err := dial.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to list nodes: %w", err)
			}
			readyNodes := 0
			for _, n := range nodes.Items {
				for _, cond := range n.Status.Conditions {
					if cond.Type == "Ready" && cond.Status == "True" {
						readyNodes++
					}
				}
			}

			// Get pods across all namespaces for status summary
			pods, err := dial.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
			if err != nil {
				return nil, fmt.Errorf("failed to list pods: %w", err)
			}

			statusCounts := make(map[string]int)
			for _, p := range pods.Items {
				phase := string(p.Status.Phase)
				// Override with more specific status
				for _, cs := range p.Status.ContainerStatuses {
					if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
						phase = cs.State.Waiting.Reason
						break
					}
					if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
						phase = cs.State.Terminated.Reason
						break
					}
				}
				statusCounts[phase]++
			}

			result := map[string]any{
				"nodes": map[string]any{
					"total": len(nodes.Items),
					"ready": readyNodes,
				},
				"pods": map[string]any{
					"total":         len(pods.Items),
					"statusSummary": statusCounts,
				},
			}

			// Try to get server version
			if v, err := tf.conn.ServerVersion(); err == nil {
				result["serverVersion"] = v
			}

			return result, nil
		},
	)
}

// --- get_pod_diagnostics tool ---

type getPodDiagnosticsParams struct {
	PodName   string `json:"podName" jsonschema:"Pod name"`
	Namespace string `json:"namespace" jsonschema:"Pod namespace"`
}

func (tf *ToolFactory) getPodDiagnosticsTool() copilot.Tool {
	return copilot.DefineTool(
		"get_pod_diagnostics",
		"Get comprehensive diagnostics for a specific pod: phase, container states, restart counts, exit codes, resource usage, probe status, and recent events.",
		func(params getPodDiagnosticsParams, inv copilot.ToolInvocation) (any, error) {
			dial, err := tf.conn.Dial()
			if err != nil {
				return nil, fmt.Errorf("failed to connect: %w", err)
			}

			pod, err := dial.CoreV1().Pods(params.Namespace).Get(
				context.Background(), params.PodName, metav1.GetOptions{},
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get pod %s/%s: %w", params.Namespace, params.PodName, err)
			}

			diag := map[string]any{
				"name":      pod.Name,
				"namespace": pod.Namespace,
				"phase":     string(pod.Status.Phase),
				"node":      pod.Spec.NodeName,
				"qos":       string(pod.Status.QOSClass),
				"age":       render.ToAge(metav1.Time(pod.CreationTimestamp)),
			}

			if pod.DeletionTimestamp != nil {
				diag["phase"] = "Terminating"
			}

			// Container diagnostics
			var containers []map[string]any
			for _, cs := range pod.Status.ContainerStatuses {
				c := map[string]any{
					"name":         cs.Name,
					"ready":        cs.Ready,
					"restartCount": cs.RestartCount,
					"image":        cs.Image,
				}

				if cs.State.Running != nil {
					c["state"] = "Running"
					c["startedAt"] = cs.State.Running.StartedAt.String()
				} else if cs.State.Waiting != nil {
					c["state"] = "Waiting"
					c["reason"] = cs.State.Waiting.Reason
					c["message"] = cs.State.Waiting.Message
				} else if cs.State.Terminated != nil {
					c["state"] = "Terminated"
					c["reason"] = cs.State.Terminated.Reason
					c["exitCode"] = cs.State.Terminated.ExitCode
					c["signal"] = cs.State.Terminated.Signal
					c["message"] = cs.State.Terminated.Message
				}

				// Last termination state (useful for CrashLoopBackOff)
				if cs.LastTerminationState.Terminated != nil {
					lt := cs.LastTerminationState.Terminated
					c["lastTermination"] = map[string]any{
						"reason":     lt.Reason,
						"exitCode":   lt.ExitCode,
						"signal":     lt.Signal,
						"message":    lt.Message,
						"finishedAt": lt.FinishedAt.String(),
					}
				}

				containers = append(containers, c)
			}
			diag["containers"] = containers

			// Conditions
			var conditions []map[string]string
			for _, cond := range pod.Status.Conditions {
				conditions = append(conditions, map[string]string{
					"type":    string(cond.Type),
					"status":  string(cond.Status),
					"reason":  cond.Reason,
					"message": cond.Message,
				})
			}
			diag["conditions"] = conditions

			// Resource requests/limits
			for _, c := range pod.Spec.Containers {
				for _, cd := range containers {
					if cd["name"] == c.Name {
						if c.Resources.Requests != nil {
							cd["resourceRequests"] = map[string]string{
								"cpu":    c.Resources.Requests.Cpu().String(),
								"memory": c.Resources.Requests.Memory().String(),
							}
						}
						if c.Resources.Limits != nil {
							cd["resourceLimits"] = map[string]string{
								"cpu":    c.Resources.Limits.Cpu().String(),
								"memory": c.Resources.Limits.Memory().String(),
							}
						}
						// Probes
						if c.LivenessProbe != nil {
							cd["livenessProbe"] = "configured"
						}
						if c.ReadinessProbe != nil {
							cd["readinessProbe"] = "configured"
						}
						if c.StartupProbe != nil {
							cd["startupProbe"] = "configured"
						}
					}
				}
			}

			return diag, nil
		},
	)
}

// --- check_rbac tool ---

type checkRBACParams struct {
	Namespace string `json:"namespace" jsonschema:"Namespace to check (empty for cluster scope)"`
	Verb      string `json:"verb" jsonschema:"Action verb: get, list, create, update, delete, watch"`
	Resource  string `json:"resource" jsonschema:"Resource type, e.g. pods, deployments, secrets"`
}

func (tf *ToolFactory) checkRBACTool() copilot.Tool {
	return copilot.DefineTool(
		"check_rbac",
		"Check if the current user has permission to perform a specific action on a resource in a namespace.",
		func(params checkRBACParams, inv copilot.ToolInvocation) (any, error) {
			ns := params.Namespace
			gvr := client.NewGVR(params.Resource)
			ok, err := tf.conn.CanI(ns, gvr, "", []string{params.Verb})
			if err != nil {
				return nil, fmt.Errorf("RBAC check failed: %w", err)
			}

			return map[string]any{
				"allowed":   ok,
				"namespace": ns,
				"verb":      params.Verb,
				"resource":  params.Resource,
			}, nil
		},
	)
}

// --- Helpers ---

func objectToYAML(obj runtime.Object) (string, error) {
	data, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return "", fmt.Errorf("failed to convert to unstructured: %w", err)
	}

	// Remove managed fields to reduce noise
	if md, ok := data["metadata"].(map[string]any); ok {
		delete(md, "managedFields")
	}

	b, err := yaml.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("failed to marshal YAML: %w", err)
	}

	return string(b), nil
}

