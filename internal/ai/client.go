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
}

// Client manages the GitHub Copilot SDK lifecycle.
var Client *AIClient

// AIClient wraps the Copilot SDK client with k9s-specific configuration.
type AIClient struct {
	client      *copilot.Client
	session     *copilot.Session
	cfg         config.AI
	tools       []copilot.Tool
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
		cfg: cfg,
		log: logger,
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

	c.tools = tools
}

// createSession creates a new Copilot session with k9s system message and tools.
func (c *AIClient) createSession(ctx context.Context) (*copilot.Session, error) {
	if !c.initialized || c.client == nil {
		return nil, fmt.Errorf("AI client not initialized")
	}

	systemMsg := k9sSystemMessage()
	sessionCfg := &copilot.SessionConfig{
		Model:     c.cfg.Model,
		Streaming: c.cfg.Streaming,
		Tools:     c.tools,
		SystemMessage: &copilot.SystemMessageConfig{
			Content: systemMsg,
		},
	}

	// Configure BYOK provider if specified.
	if c.cfg.Provider != nil && c.cfg.Provider.BaseURL != "" {
		sessionCfg.Provider = &copilot.ProviderConfig{
			Type:    c.cfg.Provider.Type,
			BaseURL: c.cfg.Provider.BaseURL,
			APIKey:  c.cfg.Provider.APIKey,
		}
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

Your capabilities:
- Diagnose unhealthy pods, deployments, and other resources
- Analyze container logs for errors and patterns
- Explain Kubernetes resource configurations in plain language
- Suggest fixes for common issues (CrashLoopBackOff, OOMKilled, ImagePullBackOff, etc.)
- Recommend resource limits, probes, and best practices
- Help with RBAC troubleshooting
- Analyze cluster health and resource utilization

Guidelines:
- Be concise and actionable — users are SREs/DevOps engineers in a terminal
- When diagnosing issues, start with the most likely root cause
- Provide kubectl commands or YAML patches when suggesting fixes
- Use bullet points and short paragraphs for readability
- Flag security concerns when you notice them
- If you need more information to diagnose, use the available tools to fetch it
- Always consider the Kubernetes context (namespace, cluster) when answering`
}
