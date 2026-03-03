# Observation Skill

> General observation patterns for Kubernetes cluster health, workload inspection, and log analysis.

Use these playbooks for day-to-day cluster monitoring and investigation.

---

## Quick Health Check

When the user asks about overall cluster health:
1. `get_cluster_health` — nodes, pod status summary
2. `get_events` with `eventType=Warning` — recent warnings
3. Summarize: healthy pods vs unhealthy, node status, top warnings

---

## Workload Deep-Dive

When focused on a specific deployment/workload:
1. `describe_resource` — full description including annotations, labels
2. `get_pod_diagnostics` for each pod (or just unhealthy ones)
3. `get_events` filtered to the resource name
4. `get_logs` for pods showing issues
5. Check related resources: Services, Ingress, ConfigMaps, Secrets

---

## Log Analysis

When investigating application-level issues:
1. `get_logs` with reasonable `tailLines` (100-200)
2. Look for patterns: stack traces, error keywords, timeout messages
3. If container crashed, always try `previous=true` first
4. Check multiple containers in multi-container pods
5. Correlate log timestamps with events
