// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ai

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/derailed/k9s/internal/config"
	"github.com/derailed/k9s/internal/slogs"
	copilot "github.com/github/copilot-sdk/go"
)

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

// Client manages the GitHub Copilot SDK lifecycle.
var Client *AIClient

// ModelInfo describes an available model.
type ModelInfo struct {
	ID   string
	Name string
}

// AIClient wraps the Copilot SDK client with k9s-specific configuration.
type AIClient struct {
	client      *copilot.Client
	session     *copilot.Session
	cfg         config.AI
	tools       []copilot.Tool
	allTools    []copilot.Tool
	skills      *SkillRegistry
	initialized bool
	mx          sync.RWMutex
	log         *slog.Logger
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
	if suffix := c.skills.SystemMessageSuffix(c.cfg.ActiveSkill); suffix != "" {
		systemMsg += "\n\n" + suffix
	}
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
				return &copilot.PreToolUseHookOutput{
					PermissionDecision: "allow",
					ModifiedArgs:       input.ToolArgs,
				}, nil
			},
			OnPostToolUse: func(input copilot.PostToolUseHookInput, inv copilot.HookInvocation) (*copilot.PostToolUseHookOutput, error) {
				c.log.Debug("Tool completed", "tool", input.ToolName)
				return &copilot.PostToolUseHookOutput{}, nil
			},
			OnErrorOccurred: func(input copilot.ErrorOccurredHookInput, inv copilot.HookInvocation) (*copilot.ErrorOccurredHookOutput, error) {
				c.log.Error("Session error", "context", input.ErrorContext, "error", input.Error)
				return &copilot.ErrorOccurredHookOutput{
					ErrorHandling: "retry",
				}, nil
			},
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

func k9sSystemMessage() string {
	return `You are an expert Kubernetes cluster assistant integrated into K9s, a terminal-based Kubernetes management UI.

Available tools:
- get_resource: Fetch a specific resource by GVR, name, and namespace (returns YAML)
- list_resources: List resources of a given type with optional label selectors and limits
- describe_resource: Get full kubectl-style description including events and conditions
- get_logs: Fetch container logs (supports tail, previous containers for crash analysis)
- get_events: Fetch cluster events filtered by namespace, resource, or type (Normal/Warning)
- get_cluster_health: High-level cluster overview — node count, pod status summary, server version
- get_pod_diagnostics: Comprehensive pod diagnostics — phase, container states, restarts, exit codes, probes, resource limits
- check_rbac: Verify if the current user can perform a specific verb on a resource

Diagnostic workflow:
1. Start with get_pod_diagnostics or describe_resource to understand the current state
2. Check get_events for Warnings related to the resource
3. If containers are crashing, use get_logs with previous=true to get crash logs
4. Check get_cluster_health for cluster-wide issues (node pressure, resource exhaustion)
5. Use check_rbac if permission issues are suspected

Guidelines:
- Be concise and actionable — users are SREs/DevOps engineers in a terminal
- When diagnosing issues, start with the most likely root cause
- Provide kubectl commands or YAML patches when suggesting fixes
- Use bullet points and short paragraphs for readability in a terminal
- Flag security concerns when you notice them (exposed secrets, overly permissive RBAC, missing network policies)
- If you need more information to diagnose, use the available tools to fetch it — do not ask the user to run commands manually
- Always consider the Kubernetes context (namespace, cluster) when answering
- When listing resources, use sensible limits to avoid overwhelming output`
}
