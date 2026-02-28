// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package config

// AI tracks AI/Copilot configuration options.
type AI struct {
	Enabled         bool        `json:"enabled" yaml:"enabled"`
	Model           string      `json:"model" yaml:"model"`
	Provider        *AIProvider `json:"provider,omitempty" yaml:"provider,omitempty"`
	Streaming       bool        `json:"streaming" yaml:"streaming"`
	MaxContextLines int         `json:"maxContextLines" yaml:"maxContextLines"`
	AutoDiagnose    bool        `json:"autoDiagnose" yaml:"autoDiagnose"`
}

// AIProvider tracks BYOK (Bring Your Own Key) provider configuration.
type AIProvider struct {
	Type    string `json:"type" yaml:"type"`
	BaseURL string `json:"baseURL" yaml:"baseURL"`
	APIKey  string `json:"apiKey" yaml:"apiKey"`
}

// NewAI creates a new default AI configuration.
func NewAI() AI {
	return AI{
		Enabled:         false,
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

	return a
}
