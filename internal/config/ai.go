// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

import "os"

// AI tracks AI/Copilot configuration options.
type AI struct {
	Enabled         bool        `json:"enabled" yaml:"enabled"`
	Model           string      `json:"model" yaml:"model"`
	Provider        *AIProvider `json:"provider,omitempty" yaml:"provider,omitempty"`
	Streaming       bool        `json:"streaming" yaml:"streaming"`
	MaxContextLines int         `json:"maxContextLines" yaml:"maxContextLines"`
	AutoDiagnose    bool        `json:"autoDiagnose" yaml:"autoDiagnose"`
	ReasoningEffort string      `json:"reasoningEffort,omitempty" yaml:"reasoningEffort,omitempty"`
	ActiveSkill     string      `json:"activeSkill,omitempty" yaml:"activeSkill,omitempty"`
	GitHubToken     string      `json:"githubToken,omitempty" yaml:"githubToken,omitempty"`
}

// AIProvider tracks BYOK (Bring Your Own Key) provider configuration.
type AIProvider struct {
	Type        string              `json:"type" yaml:"type"`
	BaseURL     string              `json:"baseURL" yaml:"baseURL"`
	APIKey      string              `json:"apiKey,omitempty" yaml:"apiKey,omitempty"`
	BearerToken string              `json:"bearerToken,omitempty" yaml:"bearerToken,omitempty"`
	WireAPI     string              `json:"wireApi,omitempty" yaml:"wireApi,omitempty"`
	Azure       *AzureProviderOpts  `json:"azure,omitempty" yaml:"azure,omitempty"`
}

// AzureProviderOpts tracks Azure-specific provider configuration.
type AzureProviderOpts struct {
	APIVersion string `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
}

// ResolveAPIKey returns the API key from config or the K9S_AI_API_KEY env var.
func (p *AIProvider) ResolveAPIKey() string {
	if p.APIKey != "" {
		return p.APIKey
	}
	return os.Getenv("K9S_AI_API_KEY")
}

// ResolveBearerToken returns the bearer token from config or K9S_AI_BEARER_TOKEN env var.
func (p *AIProvider) ResolveBearerToken() string {
	if p.BearerToken != "" {
		return p.BearerToken
	}
	return os.Getenv("K9S_AI_BEARER_TOKEN")
}

// ResolveGitHubToken returns the GitHub token from config.
// When empty the Copilot SDK falls back to gh CLI auth automatically.
func (a AI) ResolveGitHubToken() string {
	return a.GitHubToken
}

// NewAI creates a new default AI configuration.
func NewAI() AI {
	return AI{
		Enabled:         true,
		Model:           "gpt-4.1",
		Streaming:       true,
		MaxContextLines: 500,
		AutoDiagnose:    false,
	}
}

// Validate checks and corrects AI configuration.
func (a AI) Validate() AI {
	if a.Model == "" {
		a.Model = "gpt-4.1"
	}
	if a.MaxContextLines <= 0 {
		a.MaxContextLines = 500
	}
	switch a.ReasoningEffort {
	case "", "low", "medium", "high", "xhigh":
	default:
		a.ReasoningEffort = ""
	}

	return a
}
