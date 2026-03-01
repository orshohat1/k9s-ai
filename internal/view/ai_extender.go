// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"fmt"

	"github.com/derailed/k9s/internal/ai"
	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/tcell/v2"
)

// AIExtender adds AI-powered actions to resource viewers.
// It wraps workload-oriented views with Diagnose/Explain/Chat keybindings.
type AIExtender struct {
	ResourceViewer
}

// NewAIExtender returns a new AI extender wrapping the given viewer.
func NewAIExtender(v ResourceViewer) ResourceViewer {
	e := AIExtender{
		ResourceViewer: v,
	}
	e.AddBindKeysFn(e.bindKeys)

	return &e
}

func (e *AIExtender) bindKeys(aa *ui.KeyActions) {
	aa.Bulk(ui.KeyMap{
		ui.KeyShiftA: ui.NewKeyAction("AI Diagnose", e.diagnoseCmd, true),
		ui.KeyShiftX: ui.NewKeyAction("AI Explain", e.explainCmd, true),
	})
}

func (e *AIExtender) diagnoseCmd(*tcell.EventKey) *tcell.EventKey {
	path := e.GetTable().GetSelectedItem()
	if path == "" {
		return nil
	}

	ns, name := client.Namespaced(path)
	kind := e.GVR().R()

	chat := NewAIChatView()
	chat.SetResourceContext(kind, name, ns)
	if err := e.App().inject(chat, false); err != nil {
		e.App().Flash().Err(err)
		return nil
	}

	chat.SendDiagnostic(kind, name, ns)

	return nil
}

func (e *AIExtender) explainCmd(*tcell.EventKey) *tcell.EventKey {
	path := e.GetTable().GetSelectedItem()
	if path == "" {
		return nil
	}

	ns, name := client.Namespaced(path)
	kind := e.GVR().R()

	chat := NewAIChatView()
	chat.SetResourceContext(kind, name, ns)
	if err := e.App().inject(chat, false); err != nil {
		e.App().Flash().Err(err)
		return nil
	}

	go sendExplainPrompt(chat, kind, name, ns)

	return nil
}

func sendExplainPrompt(chat *AIChatView, kind, name, ns string) {
	if ai.Client == nil || !ai.Client.IsEnabled() {
		chat.appendError("AI is not enabled.")
		return
	}

	prompt := fmt.Sprintf(
		"Explain the %s '%s' in namespace '%s'. Describe its current state, configuration, and how it relates to other resources in the cluster. Highlight anything unusual.",
		kind, name, ns,
	)

	chat.appendMessage("user", prompt)
	chat.sendMessage(prompt)
}
