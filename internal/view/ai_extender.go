package view

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/derailed/k9s/internal/ai"
	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/slogs"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/tcell/v2"
)

// AIExtender adds AI-powered actions to resource viewers.
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
	if ai.Client == nil || !ai.Client.IsEnabled() {
		return
	}
	aa.Bulk(ui.KeyMap{
		ui.KeyShiftA: ui.NewKeyAction("AI Diagnose", e.diagnoseCmd, true),
		tcell.KeyCtrlI: ui.NewKeyAction("AI Chat", e.chatCmd, true),
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

	chat := NewAIChatView(e.App())
	if err := e.App().inject(chat, false); err != nil {
		e.App().Flash().Err(err)
		return nil
	}

	chat.SendDiagnostic(kind, name, ns)

	return nil
}

func (e *AIExtender) chatCmd(*tcell.EventKey) *tcell.EventKey {
	chat := NewAIChatView(e.App())
	if err := e.App().inject(chat, false); err != nil {
		e.App().Flash().Err(err)
		return nil
	}

	return nil
}

func (e *AIExtender) explainCmd(*tcell.EventKey) *tcell.EventKey {
	path := e.GetTable().GetSelectedItem()
	if path == "" {
		return nil
	}

	ns, name := client.Namespaced(path)
	kind := e.GVR().R()

	chat := NewAIChatView(e.App())
	if err := e.App().inject(chat, false); err != nil {
		e.App().Flash().Err(err)
		return nil
	}

	go e.sendExplainPrompt(chat, kind, name, ns)

	return nil
}

func (e *AIExtender) sendExplainPrompt(chat *AIChatView, kind, name, ns string) {
	if ai.Client == nil || !ai.Client.IsEnabled() {
		chat.appendMessage("system", "[red]AI is not enabled.[-]")
		return
	}

	prompt := fmt.Sprintf(
		"Explain the %s '%s' in namespace '%s'. Describe its current state, configuration, and how it relates to other resources in the cluster. Highlight anything unusual.",
		kind, name, ns,
	)

	chat.appendMessage("user", prompt)

	var response strings.Builder
	err := ai.Client.Send(context.Background(), prompt, &chatListener{
		view:     chat,
		response: &response,
	})

	if err != nil {
		slog.Error("AI explain failed", slogs.Error, err)
		chat.appendMessage("assistant", fmt.Sprintf("[red]Error: %v[-]", err))
		return
	}

	chat.app.QueueUpdateDraw(func() {
		if resp := response.String(); resp != "" {
			chat.appendMessage("assistant", resp)
		}
	})
}
