// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ai

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/slogs"
	copilot "github.com/github/copilot-sdk/go"
)

//go:embed skills/diagnostics/SKILL.md skills/security/SKILL.md skills/optimization/SKILL.md skills/observation/SKILL.md
var skillsFS embed.FS

// SkillPlaybook loads the embedded SKILL.md content for a given skill name.
// Returns empty string if the skill file is not found.
func SkillPlaybook(skillName string) string {
	path := "skills/" + skillName + "/SKILL.md"
	data, err := skillsFS.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// Listener receives AI response events.
type Listener interface {
	// AIResponseStart is called when a response begins streaming.
	AIResponseStart()
	// AIResponseDelta is called for each streamed chunk.
	AIResponseDelta(content string)
	// AIResponseComplete is called when the full response is available.
	AIResponseComplete(content string)
	// AIResponseFailed is called when an error occurs.
	AIResponseFailed(err error)
	// AIReasoningDelta is called for each reasoning chunk (if model supports it).
	AIReasoningDelta(content string)
	// AIReasoningComplete is called when reasoning is done.
	AIReasoningComplete(content string)
	// AIToolStart is called when a tool begins executing.
	AIToolStart(toolName string)
	// AIToolComplete is called when a tool finishes executing.
	AIToolComplete(toolName string)
}

// ToolActivityFunc is called when a tool starts execution, for UI display.
// isMutation is true for tools that modify cluster resources.
type ToolActivityFunc func(toolName, description string, isMutation bool)

// ApprovalFunc is called for mutation tools. It must block until the user
// decides. Returns true to allow the mutation, false to deny.
type ApprovalFunc func(toolName, description string, args map[string]any) bool

// Client manages the GitHub Copilot SDK lifecycle.
var Client *AIClient

// ModelInfo describes an available model.
type ModelInfo struct {
	ID   string
	Name string
}

// AIClient wraps the Copilot SDK client with k9s-specific configuration.
type AIClient struct {
	client          *copilot.Client
	session         *copilot.Session
	cfg             config.AI
	tools           []copilot.Tool
	allTools        []copilot.Tool
	skills          *SkillRegistry
	initialized     bool
	approvalFn      ApprovalFunc
	toolActivityFn  ToolActivityFunc
	planPresented   bool // set after first mutation denied; persists across turns
	autoApprove     bool // set when user responds after a plan; mutations auto-allowed
	mx              sync.RWMutex
	log             *slog.Logger
}

// NewAIClient creates a new AI client instance.
func NewAIClient(cfg config.AI, logger *slog.Logger) *AIClient {
	if logger == nil {
		logger = slog.Default()
	}
	return &AIClient{
		cfg:    cfg,
		log:    logger,
		skills: NewSkillRegistry(),
	}
}

// IsEnabled returns true if AI features are enabled.
func (c *AIClient) IsEnabled() bool {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return c.cfg.IsEnabled()
}

// Init initializes the Copilot SDK client and starts the CLI server.
func (c *AIClient) Init(ctx context.Context) error {
	c.mx.Lock()
	defer c.mx.Unlock()

	if !c.cfg.IsEnabled() {
		c.log.Info("AI features disabled")
		return nil
	}
	if c.initialized {
		return nil
	}

	c.log.Info("🤖 Initializing AI/Copilot integration...")

	opts := &copilot.ClientOptions{
		LogLevel: "error",
	}

	// Resolve the copilot CLI binary (check PATH, cache, or auto-download).
	if cliPath := ResolveCopilotCLIPath(c.log); cliPath != "" {
		opts.CLIPath = cliPath
	}

	// Wire GitHub authentication.
	if token := c.cfg.ResolveGitHubToken(); token != "" {
		opts.GitHubToken = token
	}

	c.client = copilot.NewClient(opts)
	if err := c.client.Start(ctx); err != nil {
		c.log.Error("Failed to start Copilot CLI server", slogs.Error, err)
		return fmt.Errorf("copilot init failed: %w", err)
	}

	c.initialized = true
	c.log.Info("✅ AI/Copilot integration ready")

	return nil
}

// Stop shuts down the Copilot SDK client gracefully.
func (c *AIClient) Stop() {
	c.mx.Lock()
	defer c.mx.Unlock()

	if c.session != nil {
		_ = c.session.Destroy()
		c.session = nil
	}
	if c.client != nil {
		_ = c.client.Stop()
		c.client = nil
	}
	c.initialized = false
	c.log.Info("AI/Copilot integration stopped")
}

// SetTools registers the custom Kubernetes tools for the Copilot session.
func (c *AIClient) SetTools(tools []copilot.Tool) {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.allTools = tools
	c.tools = c.skills.FilterTools(c.cfg.ActiveSkill, tools)
}

// Skills returns the skill registry.
func (c *AIClient) Skills() *SkillRegistry {
	return c.skills
}

// SetApprovalFunc registers a callback that gates mutation tools behind user approval.
func (c *AIClient) SetApprovalFunc(fn ApprovalFunc) {
	c.mx.Lock()
	defer c.mx.Unlock()
	c.approvalFn = fn
}

// SetToolActivityFunc registers a callback for tool activity notifications.
func (c *AIClient) SetToolActivityFunc(fn ToolActivityFunc) {
	c.mx.Lock()
	defer c.mx.Unlock()
	c.toolActivityFn = fn
}

// ResetCallbacks clears all UI callbacks.
func (c *AIClient) ResetCallbacks() {
	c.mx.Lock()
	defer c.mx.Unlock()
	c.approvalFn = nil
	c.toolActivityFn = nil
}

// SetSkill switches the active skill and refilters tools.
func (c *AIClient) SetSkill(name string) {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.cfg.ActiveSkill = name
	c.tools = c.skills.FilterTools(name, c.allTools)

	// Destroy current session so next Send() creates one with new skill context.
	if c.session != nil {
		_ = c.session.Destroy()
		c.session = nil
	}
}

// ActiveSkill returns the currently active skill name (empty = all tools).
func (c *AIClient) ActiveSkill() string {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return c.cfg.ActiveSkill
}

// SetModel switches the active model and resets the session.
func (c *AIClient) SetModel(model string) {
	c.mx.Lock()
	defer c.mx.Unlock()

	c.cfg.Model = model
	if c.session != nil {
		_ = c.session.Destroy()
		c.session = nil
	}
}

// ActiveModel returns the currently active model name.
func (c *AIClient) ActiveModel() string {
	c.mx.RLock()
	defer c.mx.RUnlock()

	return c.cfg.Model
}

// ListModels returns the models available from the user's Copilot account.
func (c *AIClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Lazy retry: if Init() failed before, try again now.
	if !c.isInitialized() {
		if err := c.Init(ctx); err != nil {
			return nil, fmt.Errorf("AI not ready: %w", err)
		}
	}

	c.mx.RLock()
	defer c.mx.RUnlock()

	if !c.initialized || c.client == nil {
		return nil, fmt.Errorf("AI client not initialized")
	}

	models, err := c.client.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}

	var result []ModelInfo
	for _, m := range models {
		result = append(result, ModelInfo{
			ID:   m.ID,
			Name: m.Name,
		})
	}

	return result, nil
}

// createSession creates a new Copilot session with k9s system message and tools.
func (c *AIClient) createSession(ctx context.Context) (*copilot.Session, error) {
	if !c.initialized || c.client == nil {
		return nil, fmt.Errorf("AI client not initialized")
	}

	systemMsg := k9sSystemMessage()
	sessionCfg := &copilot.SessionConfig{
		Model:               c.cfg.Model,
		Streaming:            c.cfg.Streaming,
		Tools:               c.tools,
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
		SystemMessage: &copilot.SystemMessageConfig{
			Content: systemMsg,
		},
		InfiniteSessions: &copilot.InfiniteSessionConfig{
			Enabled:                        copilot.Bool(true),
			BackgroundCompactionThreshold:  copilot.Float64(0.80),
			BufferExhaustionThreshold:      copilot.Float64(0.95),
		},
		Hooks: &copilot.SessionHooks{
			OnPreToolUse: func(input copilot.PreToolUseHookInput, inv copilot.HookInvocation) (*copilot.PreToolUseHookOutput, error) {
				c.log.Debug("Tool invoked", "tool", input.ToolName)

				args, _ := input.ToolArgs.(map[string]any)
				desc := FormatToolDescription(input.ToolName, args)
				mutation := IsMutationTool(input.ToolName)

				// Notify UI of tool activity (step display).
				// For mutation tools, defer notification until after approval.
				c.mx.RLock()
				actFn := c.toolActivityFn
				c.mx.RUnlock()

				if !mutation && actFn != nil {
					actFn(input.ToolName, desc, mutation)
				}

				// Gate mutation tools: require the model to present its plan first.
				//
				// Flow:
				//  Turn 1 – model tries mutation → denied, must explain plan.
				//  Turn 2 – user says "apply" → autoApprove=true → mutations allowed.
				if mutation {
					c.mx.RLock()
					auto := c.autoApprove
					planned := c.planPresented
					c.mx.RUnlock()

					// User already confirmed after seeing the plan → auto-allow.
					if auto {
						if actFn != nil {
							actFn(input.ToolName, desc, mutation)
						}
						// Reset autoApprove now that a mutation has been consumed.
						// Next mutation will require a new plan cycle.
						c.mx.Lock()
						c.autoApprove = false
						c.mx.Unlock()
						c.log.Info("Mutation auto-approved (user confirmed after plan)", "tool", input.ToolName)
						return &copilot.PreToolUseHookOutput{
							PermissionDecision: "allow",
							ModifiedArgs:       input.ToolArgs,
							AdditionalContext:  "User approved this mutation. After applying, verify the result by checking the resource state.",
						}, nil
					}

					// First mutation attempt this turn → deny, ask model to present plan.
					if !planned {
						c.mx.Lock()
						c.planPresented = true
						c.mx.Unlock()
						c.log.Info("Mutation deferred — asking model to present plan first", "tool", input.ToolName)
						return &copilot.PreToolUseHookOutput{
							PermissionDecision:       "deny",
							PermissionDecisionReason: "STOP. Before making any changes, you MUST first present your complete plan to the user. " +
								"Explain every change you intend to make (resource names, namespaces, what will change and why). " +
								"Ask the user to confirm before proceeding. Do NOT call mutation tools until the user replies.",
						}, nil
					}

					// Plan was already presented in this same turn but model retried
					// without waiting for user confirmation → deny again.
					c.log.Info("Mutation denied — waiting for user confirmation", "tool", input.ToolName)
					return &copilot.PreToolUseHookOutput{
						PermissionDecision:       "deny",
						PermissionDecisionReason: "You already presented the plan. Wait for the user to confirm before calling mutation tools. Do NOT retry.",
					}, nil
				}

				return &copilot.PreToolUseHookOutput{
					PermissionDecision: "allow",
					ModifiedArgs:       input.ToolArgs,
				}, nil
			},
			OnPostToolUse: func(input copilot.PostToolUseHookInput, inv copilot.HookInvocation) (*copilot.PostToolUseHookOutput, error) {
				c.log.Debug("Tool completed", "tool", input.ToolName)
				return &copilot.PostToolUseHookOutput{}, nil
			},
			OnErrorOccurred: func() func(copilot.ErrorOccurredHookInput, copilot.HookInvocation) (*copilot.ErrorOccurredHookOutput, error) {
				retries := 0
				return func(input copilot.ErrorOccurredHookInput, inv copilot.HookInvocation) (*copilot.ErrorOccurredHookOutput, error) {
					c.log.Error("Session error", "context", input.ErrorContext, "error", input.Error, "retries", retries)
					if retries >= 2 {
						return &copilot.ErrorOccurredHookOutput{
							ErrorHandling: "throw",
						}, nil
					}
					retries++
					return &copilot.ErrorOccurredHookOutput{
						ErrorHandling: "retry",
					}, nil
				}
			}(),
		},
	}

	// Apply reasoning effort if configured.
	if c.cfg.ReasoningEffort != "" {
		sessionCfg.ReasoningEffort = c.cfg.ReasoningEffort
	}

	// Configure BYOK provider if specified.
	if c.cfg.Provider != nil && c.cfg.Provider.BaseURL != "" {
		prov := &copilot.ProviderConfig{
			Type:    c.cfg.Provider.Type,
			BaseURL: c.cfg.Provider.BaseURL,
		}
		if key := c.cfg.Provider.ResolveAPIKey(); key != "" {
			prov.APIKey = key
		}
		if token := c.cfg.Provider.ResolveBearerToken(); token != "" {
			prov.BearerToken = token
		}
		if c.cfg.Provider.WireAPI != "" {
			prov.WireApi = c.cfg.Provider.WireAPI
		}
		if c.cfg.Provider.Azure != nil && c.cfg.Provider.Azure.APIVersion != "" {
			prov.Azure = &copilot.AzureProviderOptions{
				APIVersion: c.cfg.Provider.Azure.APIVersion,
			}
		}
		sessionCfg.Provider = prov
	}

	session, err := c.client.CreateSession(ctx, sessionCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create AI session: %w", err)
	}

	return session, nil
}

// EnsureSession returns the current session or creates a new one.
func (c *AIClient) EnsureSession(ctx context.Context) (*copilot.Session, error) {
	c.mx.Lock()
	defer c.mx.Unlock()

	if c.session != nil {
		return c.session, nil
	}

	session, err := c.createSession(ctx)
	if err != nil {
		return nil, err
	}
	c.session = session

	return session, nil
}

// isInitialized returns true if the AI client has been successfully initialized.
func (c *AIClient) isInitialized() bool {
	c.mx.RLock()
	defer c.mx.RUnlock()
	return c.initialized
}

// Send sends a prompt to the AI and streams the response to the listener.
func (c *AIClient) Send(ctx context.Context, prompt string, listener Listener) error {
	if !c.IsEnabled() {
		return fmt.Errorf("AI features are disabled. Enable in config: k9s.ai.enabled=true")
	}

	// Lazy retry: if Init() failed before, try again now.
	if !c.isInitialized() {
		if err := c.Init(ctx); err != nil {
			return fmt.Errorf("AI not ready: %w", err)
		}
	}

	// Apply a generous timeout so we never hang indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	session, err := c.EnsureSession(ctx)
	if err != nil {
		return err
	}

	// If the model presented a mutation plan in a previous turn and the user
	// is now responding, treat this turn as user-approved (no dialog needed).
	// Keep autoApprove sticky — only clear it after a mutation is actually allowed.
	c.mx.Lock()
	if c.planPresented {
		c.autoApprove = true
		c.planPresented = false
	}
	c.mx.Unlock()

	listener.AIResponseStart()

	// Subscribe to events for live activity display (tools, reasoning, deltas).
	// The response itself is captured reliably via SendAndWait below.
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		c.log.Debug("Session event", "type", string(event.Type))

		switch event.Type {
		case copilot.AssistantMessageDelta, copilot.AssistantStreamingDelta:
			if event.Data.DeltaContent != nil {
				listener.AIResponseDelta(*event.Data.DeltaContent)
			}
		case copilot.AssistantReasoningDelta:
			if event.Data.DeltaContent != nil {
				listener.AIReasoningDelta(*event.Data.DeltaContent)
			}
		case copilot.AssistantReasoning:
			if event.Data.Content != nil {
				listener.AIReasoningComplete(*event.Data.Content)
			}
		case copilot.ToolExecutionStart:
			if event.Data.ToolName != nil {
				c.log.Debug("Tool start", "tool", *event.Data.ToolName)
				listener.AIToolStart(*event.Data.ToolName)
			}
		case copilot.ToolExecutionComplete:
			if event.Data.ToolName != nil {
				c.log.Debug("Tool complete", "tool", *event.Data.ToolName)
				listener.AIToolComplete(*event.Data.ToolName)
			}
		case copilot.SessionError:
			if event.Data.Message != nil {
				c.log.Error("Session error event", "msg", *event.Data.Message)
			}
		}
	})
	defer unsubscribe()

	c.log.Debug("Sending prompt via SendAndWait", "len", len(prompt))
	response, err := session.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: prompt,
	})
	if err != nil {
		c.log.Error("SendAndWait failed", "error", err)
		listener.AIResponseFailed(fmt.Errorf("AI request failed: %w", err))
		return err
	}

	content := ""
	if response != nil && response.Data.Content != nil {
		content = *response.Data.Content
	}
	c.log.Debug("SendAndWait completed", "hasContent", content != "", "contentLen", len(content))
	listener.AIResponseComplete(content)

	return nil
}

// ResetSession destroys the current session so a fresh one is created next time.
func (c *AIClient) ResetSession() {
	c.mx.Lock()
	defer c.mx.Unlock()

	if c.session != nil {
		_ = c.session.Destroy()
		c.session = nil
	}
}

// IsMutationTool returns true if the named tool modifies cluster resources.
func IsMutationTool(name string) bool {
	switch name {
	case "patch_resource", "scale_resource", "restart_resource", "delete_resource":
		return true
	}
	return false
}

// FormatToolDescription builds a human-readable one-liner for a tool invocation.
func FormatToolDescription(toolName string, args map[string]any) string {
	getStr := func(key string) string {
		if args == nil {
			return ""
		}
		v, _ := args[key].(string)
		return v
	}

	name := getStr("name")
	ns := getStr("namespace")
	gvr := getStr("gvr")
	resType := extractResourceType(gvr)

	inNs := ""
	if ns != "" {
		inNs = fmt.Sprintf(" in namespace %q", ns)
	}

	switch toolName {
	case "get_resource":
		if name != "" {
			return fmt.Sprintf("Fetching %s %q%s", resType, name, inNs)
		}
		return fmt.Sprintf("Fetching %s%s", resType, inNs)
	case "list_resources":
		if sel := getStr("labelSelector"); sel != "" {
			return fmt.Sprintf("Listing %s%s (selector: %s)", resType, inNs, sel)
		}
		return fmt.Sprintf("Listing %s%s", resType, inNs)
	case "describe_resource":
		return fmt.Sprintf("Describing %s %q%s", resType, name, inNs)
	case "get_logs":
		podName := getStr("podName")
		desc := fmt.Sprintf("Fetching logs for pod %q%s", podName, inNs)
		if c := getStr("container"); c != "" {
			desc += fmt.Sprintf(" (container: %s)", c)
		}
		if prev, ok := args["previous"].(bool); ok && prev {
			desc += " [previous]"
		}
		return desc
	case "get_events":
		if rn := getStr("resourceName"); rn != "" {
			return fmt.Sprintf("Checking events for %q%s", rn, inNs)
		}
		return fmt.Sprintf("Checking events%s", inNs)
	case "get_cluster_health":
		return "Checking cluster health"
	case "get_pod_diagnostics":
		return fmt.Sprintf("Running diagnostics on pod %q%s", getStr("podName"), inNs)
	case "check_rbac":
		return fmt.Sprintf("Checking RBAC: can %s %s%s", getStr("verb"), getStr("resource"), inNs)
	case "patch_resource":
		return fmt.Sprintf("Patching %s %q%s", resType, name, inNs)
	case "scale_resource":
		return fmt.Sprintf("Scaling %s %q to %v replicas%s", resType, name, args["replicas"], inNs)
	case "restart_resource":
		return fmt.Sprintf("Restarting %s %q%s", resType, name, inNs)
	case "delete_resource":
		return fmt.Sprintf("Deleting %s %q%s", resType, name, inNs)
	default:
		return fmt.Sprintf("Running %s", toolName)
	}
}

func extractResourceType(gvr string) string {
	if gvr == "" {
		return "resource"
	}
	parts := strings.Split(gvr, "/")
	return parts[len(parts)-1]
}

func k9sSystemMessage() string {
	return `You are an expert Kubernetes cluster assistant in K9s, a terminal UI.
You have read-only tools and mutation tools.
Use GVR format: 'apps/v1/deployments', 'v1/pods', 'batch/v1/jobs', etc.

Skill playbooks (load via get_skill_playbook):
- diagnostics: CrashLoopBackOff, OOMKilled, ImagePullBackOff, Pending, ConfigError
- security: RBAC audit, container security, network policies
- optimization: Right-sizing, scaling, cost
- observation: Health check, deep-dive, log analysis
Load the relevant playbook FIRST when diagnosing, auditing, or optimizing.

IMPORTANT — Mutation flow (patch, scale, restart, delete):
Mutation tools are gated. Your FIRST call to any mutation tool will be AUTOMATICALLY DENIED.
This is NOT an error — it is by design. When denied:
1. Present your complete plan to the user: list every change (resource, namespace, what changes, why).
2. Ask the user to confirm (e.g. "Shall I apply this?").
3. Do NOT call mutation tools again until the user replies.
When the user confirms, call the mutation tools again — they will be allowed.
If the user declines, present the fix as kubectl commands or YAML.

Workflow:
1. Use read-only tools to investigate. Keep tool calls minimal.
2. Present findings: root cause, current state, fix options.
3. STOP — do NOT call mutation tools unless the user explicitly asks to fix/apply/patch.
4. When user asks for a fix, call the mutation tool. It will be denied (see above). Present your plan.
5. After user confirms, call mutation tools again to apply.

Be concise. Use bullet points. Flag security concerns. Never ask the user to run kubectl.`
}
