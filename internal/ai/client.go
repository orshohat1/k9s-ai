// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package ai

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

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

	return c.cfg.Enabled
}

// Init initializes the Copilot SDK client and starts the CLI server.
func (c *AIClient) Init(ctx context.Context) error {
	c.mx.Lock()
	defer c.mx.Unlock()

	if !c.cfg.Enabled {
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
		Model:     c.cfg.Model,
		Streaming: c.cfg.Streaming,
		Tools:     c.tools,
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

// Send sends a prompt to the AI and streams the response to the listener.
func (c *AIClient) Send(ctx context.Context, prompt string, listener Listener) error {
	if !c.IsEnabled() {
		return fmt.Errorf("AI features are disabled. Enable in config: k9s.ai.enabled=true")
	}

	session, err := c.EnsureSession(ctx)
	if err != nil {
		return err
	}

	listener.AIResponseStart()

	done := make(chan struct{})
	var fullContent string
	var responseErr error

	unsubscribe := session.On(func(event copilot.SessionEvent) {
		switch event.Type {
		case "assistant.message_delta":
			if event.Data.DeltaContent != nil {
				listener.AIResponseDelta(*event.Data.DeltaContent)
			}
		case "assistant.message":
			if event.Data.Content != nil {
				fullContent = *event.Data.Content
			}
		case "assistant.reasoning_delta":
			if event.Data.DeltaContent != nil {
				listener.AIReasoningDelta(*event.Data.DeltaContent)
			}
		case "assistant.reasoning":
			if event.Data.Content != nil {
				listener.AIReasoningComplete(*event.Data.Content)
			}
		case "session.idle":
			close(done)
		}
	})
	defer unsubscribe()

	if _, err := session.Send(ctx, copilot.MessageOptions{
		Prompt: prompt,
	}); err != nil {
		responseErr = fmt.Errorf("AI send failed: %w", err)
		listener.AIResponseFailed(responseErr)
		return responseErr
	}

	select {
	case <-done:
		listener.AIResponseComplete(fullContent)
	case <-ctx.Done():
		responseErr = ctx.Err()
		listener.AIResponseFailed(responseErr)
	}

	return responseErr
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
