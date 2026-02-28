// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/derailed/k9s/internal/ai"
	"github.com/derailed/k9s/internal/config"
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
	aiChatTitleFmt = " AI Chat [hilite:bg:b](%s)[fg:bg:-] "
	aiPromptLabel  = "[::b]> [::]"
	aiThinking     = "[yellow::b]AI is thinking...[-::-]"
)

// AIChatView represents the AI chat interface.
type AIChatView struct {
	*tview.Flex

	app        *App
	output     *tview.TextView
	input      *tview.InputField
	indicator  *AIChatIndicator
	actions    *ui.KeyActions
	history    []chatMessage
	streaming  bool
	fullScreen bool
	mu         sync.Mutex
}

type chatMessage struct {
	role    string
	content string
}

var _ model.Component = (*AIChatView)(nil)

// NewAIChatView returns a new AI chat view.
func NewAIChatView() *AIChatView {
	return &AIChatView{
		Flex:    tview.NewFlex().SetDirection(tview.FlexRow),
		output:  tview.NewTextView(),
		input:   tview.NewInputField(),
		actions: ui.NewKeyActions(),
	}
}

func (*AIChatView) SetCommand(*cmd.Interpreter)            {}
func (*AIChatView) SetFilter(string, bool)                 {}
func (*AIChatView) SetLabelSelector(labels.Selector, bool) {}

// Init initializes the chat view.
func (v *AIChatView) Init(ctx context.Context) error {
	var err error
	if v.app, err = extractApp(ctx); err != nil {
		return err
	}

	v.SetBorder(true)
	v.SetBorderPadding(0, 0, 1, 1)

	// Indicator bar (status line at top)
	v.indicator = NewAIChatIndicator(v.app.Styles)
	v.AddItem(v.indicator, 1, 1, false)

	// Configure output area
	v.output.SetDynamicColors(true)
	v.output.SetScrollable(true)
	v.output.SetWrap(true)
	v.output.SetWordWrap(true)
	v.output.SetChangedFunc(func() {
		v.app.Draw()
	})

	// Configure input area
	v.input.SetLabel(aiPromptLabel)
	v.input.SetDoneFunc(v.handleInput)
	v.input.SetPlaceholder("Ask about your cluster, e.g. 'Why is my pod crashing?'")

	v.AddItem(v.output, 0, 1, false)
	v.AddItem(v.input, 1, 0, true)

	v.bindKeys()
	v.SetInputCapture(v.keyboard)
	v.StylesChanged(v.app.Styles)
	v.updateTitle()
	v.printWelcome()

	return nil
}

// StylesChanged applies current skin styles.
func (v *AIChatView) StylesChanged(s *config.Styles) {
	views := s.Views()
	v.SetBackgroundColor(views.Log.BgColor.Color())
	v.output.SetTextColor(views.Log.FgColor.Color())
	v.output.SetBackgroundColor(views.Log.BgColor.Color())

	v.input.SetLabelColor(s.Frame().Title.HighlightColor.Color())
	v.input.SetFieldBackgroundColor(views.Log.BgColor.Color())
	v.input.SetFieldTextColor(views.Log.FgColor.Color())
	v.input.SetPlaceholderTextColor(s.Frame().Menu.FgColor.Color())
}

func (v *AIChatView) updateTitle() {
	styles := v.app.Styles.Frame()
	modelName := "copilot"
	if ai.Client != nil && v.app.Config != nil && v.app.Config.K9s.AI.Model != "" {
		modelName = v.app.Config.K9s.AI.Model
	}
	skillInfo := ""
	if ai.Client != nil {
		if skill := ai.Client.ActiveSkill(); skill != "" {
			skillInfo = fmt.Sprintf(" | skill:%s", skill)
		}
	}
	title := ui.SkinTitle(fmt.Sprintf(aiChatTitleFmt, modelName+skillInfo), &styles)
	v.SetTitle(title)
}

// InCmdMode checks if prompt is active.
func (*AIChatView) InCmdMode() bool {
	return false
}

// Name returns the component name.
func (*AIChatView) Name() string { return aiChatTitle }

// Start starts the chat view.
func (v *AIChatView) Start() {
	v.app.Styles.AddListener(v)
	v.app.SetFocus(v.input)
}

// Stop stops the chat view.
func (v *AIChatView) Stop() {
	v.app.Styles.RemoveListener(v)
}

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
		tcell.KeyCtrlR:  ui.NewKeyAction("Reset", v.resetCmd, false),
		tcell.KeyCtrlS:  ui.NewKeyAction("Save", v.saveCmd, false),
		tcell.KeyCtrlF:  ui.NewKeyAction("FullScreen", v.toggleFullScreenCmd, false),
		tcell.KeyCtrlN:  ui.NewKeyAction("Models", v.modelsCmd, false),
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

func (v *AIChatView) saveCmd(*tcell.EventKey) *tcell.EventKey {
	path, err := saveData(v.app.Config.K9s.ContextScreenDumpDir(), "ai-chat", v.output.GetText(true))
	if err != nil {
		v.app.Flash().Err(err)
		return nil
	}
	v.app.Flash().Infof("Chat saved to %s", path)
	return nil
}

func (v *AIChatView) toggleFullScreenCmd(*tcell.EventKey) *tcell.EventKey {
	v.fullScreen = !v.fullScreen
	v.SetFullScreen(v.fullScreen)
	v.SetBorder(!v.fullScreen)
	if v.fullScreen {
		v.output.SetBorderPadding(0, 0, 0, 0)
	} else {
		v.output.SetBorderPadding(0, 0, 0, 0)
	}
	return nil
}

func (v *AIChatView) modelsCmd(*tcell.EventKey) *tcell.EventKey {
	modelsView := NewAIModelsView()
	if err := v.app.inject(modelsView, false); err != nil {
		v.app.Flash().Err(err)
	}
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
		v.app.QueueUpdateDraw(func() {
			v.indicator.SetStatus("ready")
		})
	}()

	if ai.Client == nil || !ai.Client.IsEnabled() {
		v.appendMessage("system", "[red]AI is not enabled. Set `ai.enabled: true` in your k9s config.[-]")
		return
	}

	// Show thinking indicator
	v.app.QueueUpdateDraw(func() {
		v.indicator.SetStatus("thinking")
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

	s := v.app.Styles
	var prefix string
	switch role {
	case "user":
		prefix = fmt.Sprintf("[%s::b]You:[-::-] ", s.Frame().Title.HighlightColor)
	case "assistant":
		prefix = fmt.Sprintf("[%s::b]Copilot:[-::-] ", s.Frame().Status.AddColor)
	case "system":
		prefix = fmt.Sprintf("[%s::b]System:[-::-] ", s.Frame().Status.ModifyColor)
	case "reasoning":
		prefix = fmt.Sprintf("[%s::di]Reasoning:[-::-] ", s.Frame().Menu.FgColor)
	}

	v.app.QueueUpdateDraw(func() {
		fmt.Fprintf(v.output, "\n%s%s\n", prefix, content)
		v.output.ScrollToEnd()
	})
}

func (v *AIChatView) printWelcome() {
	s := v.app.Styles
	highlightColor := s.Frame().Title.HighlightColor
	fgColor := s.Views().Log.FgColor
	dimColor := s.Frame().Menu.FgColor

	welcome := fmt.Sprintf(`[%s::b]K9s AI Chat (Powered by GitHub Copilot)[-::-]

[%s]Ask questions about your Kubernetes cluster:[-]
[%s]  * "Why is pod X crashing?"[-]
[%s]  * "Diagnose deployment Y"[-]
[%s]  * "What events are happening in namespace Z?"[-]
[%s]  * "Check RBAC for user admin"[-]
[%s]  * "Summarize cluster health"[-]

[%s]Esc=Back  Ctrl-C=Clear  Ctrl-R=Reset  Ctrl-S=Save  Ctrl-F=FullScreen[-]
`,
		highlightColor,
		fgColor,
		dimColor, dimColor, dimColor, dimColor, dimColor,
		dimColor,
	)
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

// ----------------------------------------------------------------------------
// AIChatIndicator

// AIChatIndicator represents the status bar for the AI chat view.
type AIChatIndicator struct {
	*tview.TextView

	styles *config.Styles
	status string
}

// NewAIChatIndicator returns a new chat indicator.
func NewAIChatIndicator(styles *config.Styles) *AIChatIndicator {
	ind := &AIChatIndicator{
		TextView: tview.NewTextView(),
		styles:   styles,
		status:   "ready",
	}
	ind.SetTextAlign(tview.AlignCenter)
	ind.SetDynamicColors(true)
	ind.StylesChanged(styles)
	ind.Refresh()
	return ind
}

// StylesChanged notifies the indicator of skin changes.
func (i *AIChatIndicator) StylesChanged(styles *config.Styles) {
	i.styles = styles
	i.SetBackgroundColor(styles.K9s.Views.Log.Indicator.BgColor.Color())
	i.SetTextColor(styles.K9s.Views.Log.Indicator.FgColor.Color())
	i.Refresh()
}

// SetStatus updates the displayed status.
func (i *AIChatIndicator) SetStatus(status string) {
	i.status = status
	i.Refresh()
}

// Refresh redraws the indicator bar.
func (i *AIChatIndicator) Refresh() {
	ind := i.styles.K9s.Views.Log.Indicator
	on := ind.ToggleOnColor
	off := ind.ToggleOffColor

	var statusColor config.Color
	switch i.status {
	case "thinking":
		statusColor = on
	default:
		statusColor = off
	}

	i.Clear()
	fmt.Fprintf(i, "[%s::b]<%s>[-::-]", statusColor, i.status)
}

// ----------------------------------------------------------------------------
// chatListener

// chatListener implements ai.Listener for streaming responses.
type chatListener struct {
	view     *AIChatView
	response *strings.Builder
}

func (l *chatListener) AIResponseStart() {
	l.view.app.QueueUpdateDraw(func() {
		l.view.clearThinkingIndicator()
		l.view.indicator.SetStatus("streaming")
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

func (l *chatListener) AIReasoningDelta(content string) {
	l.view.app.QueueUpdateDraw(func() {
		l.view.indicator.SetStatus("reasoning")
	})
}

func (l *chatListener) AIReasoningComplete(content string) {
	l.view.appendMessage("reasoning", content)
}
