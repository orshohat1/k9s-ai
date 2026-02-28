package view

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/derailed/k9s/internal/ai"
	"github.com/derailed/k9s/internal/model"
	"github.com/derailed/k9s/internal/slogs"
	"github.com/derailed/k9s/internal/ui"
	"github.com/derailed/k9s/internal/view/cmd"
	"github.com/derailed/tcell/v2"
	"github.com/derailed/tview"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	aiChatTitle    = "AI Chat"
	aiChatTitleFmt = "[fg:bg:b] AI Chat [hilite:bg:b](%s)[fg:bg:-] "
	aiPromptLabel  = "[::b]> [::]"
	aiThinking     = "[yellow::b]AI is thinking...[-::-]"
)

// AIChatView represents the AI chat interface.
type AIChatView struct {
	*tview.Flex

	app       *App
	output    *tview.TextView
	input     *tview.InputField
	actions   *ui.KeyActions
	history   []chatMessage
	streaming bool
	mu        sync.Mutex
}

type chatMessage struct {
	role    string
	content string
}

var _ model.Component = (*AIChatView)(nil)

// NewAIChatView returns a new AI chat view.
func NewAIChatView(app *App) *AIChatView {
	v := &AIChatView{
		Flex:    tview.NewFlex().SetDirection(tview.FlexRow),
		app:     app,
		output:  tview.NewTextView(),
		input:   tview.NewInputField(),
		actions: ui.NewKeyActions(),
	}

	return v
}

func (*AIChatView) SetCommand(*cmd.Interpreter)            {}
func (*AIChatView) SetFilter(string, bool)                 {}
func (*AIChatView) SetLabelSelector(labels.Selector, bool) {}

// Init initializes the chat view.
func (v *AIChatView) Init(_ context.Context) error {
	v.SetBorder(true)
	v.SetBorderPadding(0, 0, 1, 1)
	v.SetTitle(fmt.Sprintf(aiChatTitleFmt, "copilot"))
	v.SetTitleColor(tcell.ColorAqua)

	// Configure output
	v.output.SetDynamicColors(true)
	v.output.SetScrollable(true)
	v.output.SetWrap(true)
	v.output.SetWordWrap(true)
	v.output.SetChangedFunc(func() {
		v.app.Draw()
	})

	// Configure input
	v.input.SetLabel(aiPromptLabel)
	v.input.SetLabelColor(tcell.ColorAqua)
	v.input.SetFieldBackgroundColor(tcell.ColorBlack)
	v.input.SetFieldTextColor(tcell.ColorWhite)
	v.input.SetDoneFunc(v.handleInput)
	v.input.SetPlaceholder("Ask about your cluster, e.g. 'Why is my pod crashing?'")
	v.input.SetPlaceholderTextColor(tcell.ColorDimGray)

	v.AddItem(v.output, 0, 1, false)
	v.AddItem(v.input, 1, 0, true)

	v.bindKeys()
	v.SetInputCapture(v.keyboard)

	v.printWelcome()

	return nil
}

// InCmdMode checks if prompt is active.
func (*AIChatView) InCmdMode() bool {
	return false
}

// Name returns the component name.
func (*AIChatView) Name() string { return aiChatTitle }

// Start starts the chat view.
func (v *AIChatView) Start() {
	v.app.SetFocus(v.input)
}

// Stop stops the chat view.
func (*AIChatView) Stop() {}

// Hints returns menu hints.
func (v *AIChatView) Hints() model.MenuHints {
	return v.actions.Hints()
}

// ExtraHints returns additional hints.
func (*AIChatView) ExtraHints() map[string]string {
	return nil
}

// Actions returns menu actions.
func (v *AIChatView) Actions() *ui.KeyActions {
	return v.actions
}

func (v *AIChatView) bindKeys() {
	v.actions.Bulk(ui.KeyMap{
		tcell.KeyEscape: ui.NewKeyAction("Back", v.backCmd, false),
		tcell.KeyCtrlC:  ui.NewKeyAction("Clear", v.clearCmd, false),
		tcell.KeyCtrlR:  ui.NewKeyAction("Reset Session", v.resetCmd, false),
	})
}

func (v *AIChatView) keyboard(evt *tcell.EventKey) *tcell.EventKey {
	if a, ok := v.actions.Get(ui.AsKey(evt)); ok {
		return a.Action(evt)
	}
	return evt
}

func (v *AIChatView) backCmd(*tcell.EventKey) *tcell.EventKey {
	v.app.Content.Pop()
	return nil
}

func (v *AIChatView) clearCmd(*tcell.EventKey) *tcell.EventKey {
	v.output.Clear()
	v.history = nil
	v.printWelcome()
	return nil
}

func (v *AIChatView) resetCmd(*tcell.EventKey) *tcell.EventKey {
	if ai.Client != nil {
		ai.Client.ResetSession()
	}
	v.output.Clear()
	v.history = nil
	v.printWelcome()
	v.app.Flash().Info("AI session reset")
	return nil
}

func (v *AIChatView) handleInput(key tcell.Key) {
	if key != tcell.KeyEnter {
		return
	}

	text := strings.TrimSpace(v.input.GetText())
	if text == "" {
		return
	}
	v.input.SetText("")

	v.appendMessage("user", text)

	go v.sendMessage(text)
}

func (v *AIChatView) sendMessage(text string) {
	v.mu.Lock()
	if v.streaming {
		v.mu.Unlock()
		return
	}
	v.streaming = true
	v.mu.Unlock()

	defer func() {
		v.mu.Lock()
		v.streaming = false
		v.mu.Unlock()
	}()

	if ai.Client == nil || !ai.Client.IsEnabled() {
		v.appendMessage("system", "[red]AI is not enabled. Set `ai.enabled: true` in your k9s config.[-]")
		return
	}

	// Show thinking indicator
	v.app.QueueUpdateDraw(func() {
		fmt.Fprintf(v.output, "\n%s", aiThinking)
	})

	var response strings.Builder
	err := ai.Client.Send(context.Background(), text, &chatListener{
		view:     v,
		response: &response,
	})

	if err != nil {
		slog.Error("AI request failed", slogs.Error, err)
		v.app.QueueUpdateDraw(func() {
			v.clearThinkingIndicator()
			v.appendMessage("assistant", fmt.Sprintf("[red]Error: %v[-]", err))
		})
		return
	}

	v.app.QueueUpdateDraw(func() {
		v.clearThinkingIndicator()
		if resp := response.String(); resp != "" {
			v.appendMessage("assistant", resp)
		}
	})
}

func (v *AIChatView) clearThinkingIndicator() {
	text := v.output.GetText(false)
	if idx := strings.LastIndex(text, aiThinking); idx >= 0 {
		v.output.Clear()
		fmt.Fprint(v.output, text[:idx])
	}
}

func (v *AIChatView) appendMessage(role, content string) {
	v.history = append(v.history, chatMessage{role: role, content: content})

	var prefix string
	switch role {
	case "user":
		prefix = "[aqua::b]You:[-::-] "
	case "assistant":
		prefix = "[green::b]Copilot:[-::-] "
	case "system":
		prefix = "[yellow::b]System:[-::-] "
	}

	v.app.QueueUpdateDraw(func() {
		fmt.Fprintf(v.output, "\n%s%s\n", prefix, content)
		v.output.ScrollToEnd()
	})
}

func (v *AIChatView) printWelcome() {
	welcome := `[aqua::b]🤖 K9s AI Chat (Powered by GitHub Copilot)[-::-]

[white]Ask questions about your Kubernetes cluster:[-]
[dim]  • "Why is pod X crashing?"[-]
[dim]  • "Diagnose deployment Y"[-]
[dim]  • "What events are happening in namespace Z?"[-]
[dim]  • "Check RBAC for user admin"[-]
[dim]  • "Summarize cluster health"[-]

[dim]Shortcuts: Esc=Back  Ctrl-C=Clear  Ctrl-R=Reset Session[-]
`
	fmt.Fprint(v.output, welcome)
}

// SendDiagnostic sends a diagnostic prompt with pre-filled context.
func (v *AIChatView) SendDiagnostic(resourceKind, resourceName, namespace string) {
	prompt := fmt.Sprintf(
		"Diagnose the %s '%s' in namespace '%s'. Check its status, recent events, logs if applicable, and suggest fixes for any issues.",
		resourceKind, resourceName, namespace,
	)

	v.appendMessage("user", prompt)
	go v.sendMessage(prompt)
}

// chatListener implements ai.Listener for streaming responses.
type chatListener struct {
	view     *AIChatView
	response *strings.Builder
}

func (l *chatListener) AIResponseStart() {
	l.view.app.QueueUpdateDraw(func() {
		l.view.clearThinkingIndicator()
	})
}

func (l *chatListener) AIResponseDelta(delta string) {
	l.response.WriteString(delta)
}

func (l *chatListener) AIResponseComplete(text string) {
	l.response.Reset()
	l.response.WriteString(text)
}

func (l *chatListener) AIResponseFailed(err error) {
	slog.Error("AI streaming failed", slogs.Error, err)
}
