// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/tcell/v2"
)

// AIExtender adds AI-powered actions to resource viewers.
// It wraps workload-oriented views with an AI Chat keybinding.
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
		ui.KeyShiftA: ui.NewKeyAction("AI Chat", e.aiChatCmd, true),
	})
}

func (e *AIExtender) aiChatCmd(*tcell.EventKey) *tcell.EventKey {
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

	return nil
}
