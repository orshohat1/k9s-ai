// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
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
)

// AIChatView represents the AI chat interface.
type AIChatView struct {
	*tview.Flex

	app          *App
	output       *tview.TextView
	input        *tview.InputField
	statusBar    *tview.TextView
	actions      *ui.KeyActions
	history      []chatMessage
	streaming    bool
	fullScreen   bool
	resKind      string
	resName      string
	resNamespace string
	mu           sync.Mutex
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

	// Configure output area.
	v.output.SetDynamicColors(true)
	v.output.SetScrollable(true)
	v.output.SetWrap(true)
	v.output.SetWordWrap(true)
	v.output.SetChangedFunc(func() {
		v.app.Draw()
	})

	// Status bar between output and input.
	v.statusBar = tview.NewTextView()
	v.statusBar.SetDynamicColors(true)
	v.statusBar.SetTextAlign(tview.AlignLeft)

	// Configure input area.
	v.input.SetLabel("[::b]> [::]")
	v.input.SetDoneFunc(v.handleInput)
	v.input.SetPlaceholder("Ask anything about your cluster...")

	v.AddItem(v.output, 0, 1, false)
	v.AddItem(v.statusBar, 1, 0, false)
	v.AddItem(v.input, 1, 0, true)

	v.bindKeys()
	v.SetInputCapture(v.keyboard)
	v.StylesChanged(v.app.Styles)
	v.updateTitle()
	v.setStatusReady()
	v.printWelcome()
	v.applyResourceContext()

	return nil
}

// StylesChanged applies current skin styles.
func (v *AIChatView) StylesChanged(s *config.Styles) {
	views := s.Views()
	v.SetBackgroundColor(views.Log.BgColor.Color())
	v.output.SetTextColor(views.Log.FgColor.Color())
	v.output.SetBackgroundColor(views.Log.BgColor.Color())
	v.statusBar.SetBackgroundColor(views.Log.BgColor.Color())

	v.input.SetLabelColor(s.Frame().Title.HighlightColor.Color())
	v.input.SetFieldBackgroundColor(views.Log.BgColor.Color())
	v.input.SetFieldTextColor(views.Log.FgColor.Color())
	v.input.SetPlaceholderTextColor(s.Frame().Menu.FgColor.Color())
}

func (v *AIChatView) updateTitle() {
	styles := v.app.Styles.Frame()
	modelName := "copilot"
	if ai.Client != nil {
		modelName = ai.Client.ActiveModel()
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
	v.updateTitle()
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
	return nil
}

func (v *AIChatView) modelsCmd(*tcell.EventKey) *tcell.EventKey {
	modelsView := NewAIModelsView()
	if err := v.app.inject(modelsView, false); err != nil {
		v.app.Flash().Err(err)
	}
	return nil
}

// --------------------------------------------------------------------------
// Status bar helpers

func (v *AIChatView) setStatusReady() {
	v.statusBar.Clear()
	fmt.Fprintf(v.statusBar, " [green::b]Ready[-::-]")
}

func (v *AIChatView) setStatusThinking() {
	v.statusBar.Clear()
	fmt.Fprintf(v.statusBar, " [yellow::b]Thinking...[-::-]  [gray::-](please wait)[-::-]")
}

func (v *AIChatView) setStatusReasoning() {
	v.statusBar.Clear()
	fmt.Fprintf(v.statusBar, " [magenta::b]Reasoning...[-::-]  [gray::-](model is thinking deeply)[-::-]")
}

func (v *AIChatView) setStatusStreaming() {
	v.statusBar.Clear()
	fmt.Fprintf(v.statusBar, " [cyan::b]Receiving response...[-::-]")
}

func (v *AIChatView) setStatusTool(toolName string) {
	v.statusBar.Clear()
	label := toolDisplayName(toolName)
	fmt.Fprintf(v.statusBar, " [orange::b]%s[-::-]  [gray::-](working)[-::-]", label)
}

// --------------------------------------------------------------------------
// Input handling

func (v *AIChatView) handleInput(key tcell.Key) {
	if key != tcell.KeyEnter {
		return
	}
	v.mu.Lock()
	busy := v.streaming
	v.mu.Unlock()
	if busy {
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

	// Disable input while processing.
	v.app.QueueUpdateDraw(func() {
		v.input.SetAcceptanceFunc(func(string, rune) bool { return false })
		v.input.SetPlaceholder("Waiting for response...")
		v.setStatusThinking()
	})

	defer func() {
		v.mu.Lock()
		v.streaming = false
		v.mu.Unlock()
		v.app.QueueUpdateDraw(func() {
			v.input.SetAcceptanceFunc(nil)
			v.restorePlaceholder()
			v.setStatusReady()
			v.app.SetFocus(v.input)
		})
	}()

	if ai.Client == nil {
		v.appendError("AI client not available. Check logs for initialization errors.")
		return
	}

	var response strings.Builder
	var respMu sync.Mutex
	err := ai.Client.Send(context.Background(), text, &chatListener{
		view:     v,
		response: &response,
		mu:       &respMu,
	})

	if err != nil {
		slog.Error("AI request failed", slogs.Error, err)
		v.appendError(err.Error())
		return
	}

	// AIResponseComplete already wrote to response; display it.
	respMu.Lock()
	resp := response.String()
	respMu.Unlock()

	if resp != "" {
		v.appendMessage("assistant", resp)
	}
}

// --------------------------------------------------------------------------
// Message rendering

func (v *AIChatView) appendMessage(role, content string) {
	v.history = append(v.history, chatMessage{role: role, content: content})

	s := v.app.Styles
	v.app.QueueUpdateDraw(func() {
		switch role {
		case "user":
			fmt.Fprintf(v.output, "\n  [%s::b]You[-::-]\n", s.Frame().Title.HighlightColor)
			fmt.Fprintf(v.output, "  %s\n", content)

		case "assistant":
			fmt.Fprintf(v.output, "\n  [%s::b]Copilot[-::-]\n", s.Frame().Status.AddColor)
			v.renderFormattedContent(content)

		case "system":
			fmt.Fprintf(v.output, "\n  [gray::-]%s[-::-]\n", content)

		case "reasoning":
			fmt.Fprintf(v.output, "\n  [%s::di]Thinking: %s[-::-]\n", s.Frame().Menu.FgColor, content)
		}
		v.output.ScrollToEnd()
	})
}

func (v *AIChatView) appendError(msg string) {
	v.app.QueueUpdateDraw(func() {
		fmt.Fprintf(v.output, "\n  [red::b]Error:[-::-] [red::-]%s[-::-]\n", msg)
		v.output.ScrollToEnd()
	})
}

// renderFormattedContent converts markdown-like content to a cleaner terminal display.
func (v *AIChatView) renderFormattedContent(content string) {
	lines := strings.Split(content, "\n")
	inCodeBlock := false

	s := v.app.Styles
	codeColor := s.Frame().Menu.FgColor
	highlightColor := s.Frame().Title.HighlightColor

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Handle code block fences.
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				lang := strings.TrimPrefix(trimmed, "```")
				lang = strings.TrimSpace(lang)
				if lang != "" {
					fmt.Fprintf(v.output, "  [%s::d]--- %s ---[-::-]\n", codeColor, lang)
				} else {
					fmt.Fprintf(v.output, "  [%s::d]-----------[-::-]\n", codeColor)
				}
			} else {
				fmt.Fprintf(v.output, "  [%s::d]-----------[-::-]\n", codeColor)
			}
			continue
		}

		if inCodeBlock {
			fmt.Fprintf(v.output, "  [%s::-]  %s[-::-]\n", highlightColor, line)
			continue
		}

		// Headers: ## Foo -> bold.
		if strings.HasPrefix(trimmed, "#") {
			text := strings.TrimLeft(trimmed, "# ")
			fmt.Fprintf(v.output, "\n  [%s::b]%s[-::-]\n", highlightColor, text)
			continue
		}

		// Bullet points.
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			rest := trimmed[2:]
			rest = renderInlineFormatting(rest)
			fmt.Fprintf(v.output, "  [%s::-]>[-::-] %s\n", highlightColor, rest)
			continue
		}

		// Numbered lists.
		if matched, _ := regexp.MatchString(`^\d+\.\s`, trimmed); matched {
			formatted := renderInlineFormatting(trimmed)
			fmt.Fprintf(v.output, "  %s\n", formatted)
			continue
		}

		// Regular text.
		formatted := renderInlineFormatting(line)
		fmt.Fprintf(v.output, "  %s\n", formatted)
	}
}

// renderInlineFormatting converts inline markdown to tview colors.
func renderInlineFormatting(text string) string {
	// Bold: **text** -> bold.
	boldRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
	text = boldRe.ReplaceAllString(text, "[::b]$1[-::-]")

	// Inline code: `text` -> highlighted.
	codeRe := regexp.MustCompile("`([^`]+)`")
	text = codeRe.ReplaceAllString(text, "[aqua::-]$1[-::-]")

	return text
}

// --------------------------------------------------------------------------
// Welcome / context

func (v *AIChatView) printWelcome() {
	s := v.app.Styles
	hlColor := s.Frame().Title.HighlightColor
	dimColor := s.Frame().Menu.FgColor

	welcome := fmt.Sprintf(
		"  [%s::b]K9s AI Assistant[-::-]  [%s::d](Powered by GitHub Copilot)[-::-]\n\n"+
			"  [%s::-]Ask anything about your Kubernetes cluster:[-::-]\n"+
			"  [%s::-]  > Why is pod X crashing?[-::-]\n"+
			"  [%s::-]  > Summarize cluster health[-::-]\n"+
			"  [%s::-]  > Check RBAC for user admin[-::-]\n",
		hlColor, dimColor,
		dimColor,
		dimColor, dimColor, dimColor,
	)
	fmt.Fprint(v.output, welcome)
}

// SetResourceContext sets the resource context for the chat view.
func (v *AIChatView) SetResourceContext(kind, name, ns string) {
	v.resKind = kind
	v.resName = name
	v.resNamespace = ns
}

func (v *AIChatView) applyResourceContext() {
	if v.resKind == "" || v.resName == "" {
		return
	}

	s := v.app.Styles
	hlColor := s.Frame().Title.HighlightColor
	dimColor := s.Frame().Menu.FgColor

	label := fmt.Sprintf("%s/%s", v.resKind, v.resName)
	if v.resNamespace != "" {
		label = fmt.Sprintf("%s/%s (ns: %s)", v.resKind, v.resName, v.resNamespace)
	}

	fmt.Fprintf(v.output, "\n  [%s::b]Resource:[-::-] [%s::-]%s[-::-]\n", hlColor, dimColor, label)
	v.input.SetPlaceholder(fmt.Sprintf("Ask about %s/%s...", v.resKind, v.resName))
}

func (v *AIChatView) restorePlaceholder() {
	if v.resKind != "" && v.resName != "" {
		v.input.SetPlaceholder(fmt.Sprintf("Ask about %s/%s...", v.resKind, v.resName))
	} else {
		v.input.SetPlaceholder("Ask anything about your cluster...")
	}
}

// SendDiagnostic sends a diagnostic prompt with pre-filled context.
// The full prompt is sent to the AI silently; the user sees a short
// system message while tool activity is displayed in real time.
func (v *AIChatView) SendDiagnostic(resourceKind, resourceName, namespace string) {
	prompt := fmt.Sprintf(
		"Diagnose the %s '%s' in namespace '%s'. Check its status, recent events, logs if applicable, and suggest fixes for any issues.",
		resourceKind, resourceName, namespace,
	)

	label := fmt.Sprintf("%s/%s", resourceKind, resourceName)
	if namespace != "" {
		label = fmt.Sprintf("%s/%s (ns: %s)", resourceKind, resourceName, namespace)
	}
	v.appendMessage("system", fmt.Sprintf("Diagnosing %s ...", label))
	go v.sendMessage(prompt)
}

// --------------------------------------------------------------------------
// chatListener implements ai.Listener for streaming responses.

type chatListener struct {
	view     *AIChatView
	response *strings.Builder
	mu       *sync.Mutex
}

func (l *chatListener) AIResponseStart() {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusStreaming()
	})
}

func (l *chatListener) AIResponseDelta(delta string) {
	l.mu.Lock()
	l.response.WriteString(delta)
	l.mu.Unlock()
}

func (l *chatListener) AIResponseComplete(text string) {
	if text != "" {
		l.mu.Lock()
		l.response.Reset()
		l.response.WriteString(text)
		l.mu.Unlock()
	}
}

func (l *chatListener) AIResponseFailed(err error) {
	slog.Error("AI streaming failed", slogs.Error, err)
}

func (l *chatListener) AIReasoningDelta(content string) {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusReasoning()
	})
}

func (l *chatListener) AIReasoningComplete(content string) {
	l.view.appendMessage("reasoning", content)
}

func (l *chatListener) AIToolStart(toolName string) {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusTool(toolName)
		l.view.appendActivity(toolDisplayName(toolName))
	})
}

func (l *chatListener) AIToolComplete(toolName string) {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusThinking()
	})
}

// --------------------------------------------------------------------------
// Activity rendering (behind-the-scenes visibility)

func (v *AIChatView) appendActivity(label string) {
	s := v.app.Styles
	dimColor := s.Frame().Menu.FgColor
	fmt.Fprintf(v.output, "  [%s::di]⚡ %s[-::-]\n", dimColor, label)
	v.output.ScrollToEnd()
}

// toolDisplayName maps internal tool names to user-friendly descriptions.
func toolDisplayName(name string) string {
	switch name {
	case "get_resource":
		return "Fetching resource..."
	case "list_resources":
		return "Listing resources..."
	case "describe_resource":
		return "Describing resource..."
	case "get_logs":
		return "Fetching logs..."
	case "get_events":
		return "Checking events..."
	case "get_cluster_health":
		return "Checking cluster health..."
	case "get_pod_diagnostics":
		return "Running pod diagnostics..."
	case "check_rbac":
		return "Checking RBAC permissions..."
	default:
		return fmt.Sprintf("Running %s...", name)
	}
}
