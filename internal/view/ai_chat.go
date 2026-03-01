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
	chatSeparator  = "─────────────────────────────────────────────"
	thinSeparator  = "─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─ ─"
)

// AIChatView represents the AI chat interface.
type AIChatView struct {
	*tview.Flex

	app            *App
	output         *tview.TextView
	input          *tview.InputField
	statusBar      *tview.TextView
	actions        *ui.KeyActions
	history        []chatMessage
	streaming      bool
	streamingHeader bool // true if we've printed the Copilot header for current stream
	fullScreen     bool
	resKind        string
	resName        string
	resNamespace   string
	mu             sync.Mutex
}

type chatMessage struct {
	role    string
	content string
	// activity is true for tool activity lines (not sent to AI, display-only).
	activity bool
}

// Package-level chat history that persists across view recreations.
// History is scoped by resource context so each workload has its own chat.
var (
	globalChatHistories map[string][]chatMessage
	globalChatMu        sync.Mutex
)

func init() {
	globalChatHistories = make(map[string][]chatMessage)
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
	// IMPORTANT: Set capture on the input field, not the Flex.
	// tview docs: "SetInputCapture will not have an effect on composing
	// primitives such as Flex" — only the focused primitive gets events.
	v.input.SetInputCapture(v.keyboard)
	v.StylesChanged(v.app.Styles)
	v.updateTitle()
	v.setStatusReady()

	// Restore previous chat history if available; otherwise show welcome.
	if !v.restoreHistory() {
		v.printWelcome()
		v.applyResourceContext()
	}

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
		tcell.KeyPgUp:   ui.NewKeyAction("PgUp", nil, false),
		tcell.KeyPgDn:   ui.NewKeyAction("PgDn", nil, false),
	})
}

func (v *AIChatView) keyboard(evt *tcell.EventKey) *tcell.EventKey {
	// Scroll output while input retains focus.
	switch evt.Key() {
	case tcell.KeyPgUp:
		row, col := v.output.GetScrollOffset()
		v.output.ScrollTo(row-10, col)
		return nil
	case tcell.KeyPgDn:
		row, col := v.output.GetScrollOffset()
		v.output.ScrollTo(row+10, col)
		return nil
	case tcell.KeyUp:
		row, col := v.output.GetScrollOffset()
		v.output.ScrollTo(row-1, col)
		return nil
	case tcell.KeyDown:
		row, col := v.output.GetScrollOffset()
		v.output.ScrollTo(row+1, col)
		return nil
	}

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
	scope := v.chatScope()
	globalChatMu.Lock()
	delete(globalChatHistories, scope)
	globalChatMu.Unlock()
	v.printWelcome()
	return nil
}

func (v *AIChatView) resetCmd(*tcell.EventKey) *tcell.EventKey {
	if ai.Client != nil {
		ai.Client.ResetSession()
	}
	v.output.Clear()
	v.history = nil
	scope := v.chatScope()
	globalChatMu.Lock()
	delete(globalChatHistories, scope)
	globalChatMu.Unlock()
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
	fmt.Fprintf(v.statusBar, " [green::b]● Ready[-::-]")
}

func (v *AIChatView) setStatusThinking() {
	v.statusBar.Clear()
	fmt.Fprintf(v.statusBar, " [yellow::b]○ Thinking...[-::-]  [gray::-]please wait[-::-]")
}

func (v *AIChatView) setStatusReasoning() {
	v.statusBar.Clear()
	fmt.Fprintf(v.statusBar, " [magenta::b]○ Reasoning...[-::-]  [gray::-]model is thinking deeply[-::-]")
}

func (v *AIChatView) setStatusStreaming() {
	v.statusBar.Clear()
	fmt.Fprintf(v.statusBar, " [cyan::b]● Receiving response...[-::-]")
}

func (v *AIChatView) setStatusTool(toolName string) {
	v.statusBar.Clear()
	label := toolDisplayName(toolName)
	fmt.Fprintf(v.statusBar, " [orange::b]⚡ %s[-::-]", label)
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
	v.streamingHeader = false
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

	// Scope the prompt to the workload context if applicable.
	prompt := v.buildContextualPrompt(text)

	var streamedContent strings.Builder
	var streamMu sync.Mutex
	err := ai.Client.Send(context.Background(), prompt, &chatListener{
		view:            v,
		streamedContent: &streamedContent,
		mu:              &streamMu,
	})

	if err != nil {
		slog.Error("AI request failed", slogs.Error, err)
		v.appendError(err.Error())
		return
	}

	// Save the final response to history for persistence.
	streamMu.Lock()
	finalContent := streamedContent.String()
	streamMu.Unlock()

	if finalContent != "" {
		// Don't re-render — already streamed to output. Just persist.
		msg := chatMessage{role: "assistant", content: finalContent}
		v.history = append(v.history, msg)
		scope := v.chatScope()
		globalChatMu.Lock()
		globalChatHistories[scope] = append(globalChatHistories[scope], msg)
		globalChatMu.Unlock()

		// Re-render with proper markdown formatting (streaming was raw text).
		v.app.QueueUpdateDraw(func() {
			v.reRenderChat()
		})
	}
}

// --------------------------------------------------------------------------
// Message rendering

// chatScope returns the history scope key for this chat view.
func (v *AIChatView) chatScope() string {
	if v.resKind == "" || v.resName == "" {
		return "_global_"
	}
	if v.resNamespace != "" {
		return v.resKind + "/" + v.resNamespace + "/" + v.resName
	}
	return v.resKind + "/" + v.resName
}

// buildContextualPrompt wraps the user's question with workload context
// so the AI focuses on the specific resource, not the whole cluster.
func (v *AIChatView) buildContextualPrompt(text string) string {
	if v.resKind == "" || v.resName == "" {
		return text
	}

	ns := v.resNamespace
	if ns == "" {
		ns = "(cluster-scoped)"
	}

	return fmt.Sprintf(`[RESOURCE CONTEXT]
This chat is focused on the %s "%s" in namespace "%s".
Focus your analysis ONLY on this specific workload and its directly related resources:
- The %s itself and its pods/replicas
- Services that select or target it
- ConfigMaps, Secrets, and ServiceAccounts it references
- Ingress or NetworkPolicies related to it
- PersistentVolumeClaims it uses
- Events related to it and its pods

Do NOT analyze unrelated cluster-wide resources unless the user explicitly asks.
When using diagnostic tools, scope queries to this resource and its namespace.

[USER QUESTION]
%s`, v.resKind, v.resName, ns, v.resKind, text)
}

// reRenderChat clears and re-renders the full chat with proper formatting.
func (v *AIChatView) reRenderChat() {
	v.output.Clear()
	v.printWelcome()
	v.applyResourceContext()
	for _, msg := range v.history {
		v.renderMessage(msg.role, msg.content)
	}
	v.output.ScrollToEnd()
}

func (v *AIChatView) appendMessage(role, content string) {
	msg := chatMessage{role: role, content: content}
	v.history = append(v.history, msg)

	// Persist to scoped global store.
	scope := v.chatScope()
	globalChatMu.Lock()
	globalChatHistories[scope] = append(globalChatHistories[scope], msg)
	globalChatMu.Unlock()

	v.app.QueueUpdateDraw(func() {
		v.renderMessage(role, content)
		v.output.ScrollToEnd()
	})
}

// renderMessage writes a formatted message directly to the output.
// Must be called from the UI goroutine or during Init (before display).
func (v *AIChatView) renderMessage(role, content string) {
	s := v.app.Styles
	hlColor := s.Frame().Title.HighlightColor
	dimColor := s.Frame().Menu.FgColor

	switch role {
	case "user":
		fmt.Fprintf(v.output, "\n  [%s::d]%s[-::-]\n", dimColor, chatSeparator)
		fmt.Fprintf(v.output, "  [%s::b]▶ You[-::-]\n", hlColor)
		for _, line := range strings.Split(content, "\n") {
			fmt.Fprintf(v.output, "    %s\n", line)
		}

	case "assistant":
		fmt.Fprintf(v.output, "\n  [%s::d]%s[-::-]\n", dimColor, chatSeparator)
		fmt.Fprintf(v.output, "  [%s::b]✦ Copilot[-::-]\n", s.Frame().Status.AddColor)
		v.renderFormattedContent(content)

	case "system":
		fmt.Fprintf(v.output, "\n    [gray::d]%s[-::-]\n", content)

	case "reasoning":
		fmt.Fprintf(v.output, "    [%s::d]○ %s[-::-]\n", dimColor, content)

	case "activity":
		fmt.Fprintf(v.output, "    [%s::d]⚡ %s[-::-]\n", dimColor, content)
	}
}

// restoreHistory replays persisted chat messages into the view.
// Returns true if history was restored.
func (v *AIChatView) restoreHistory() bool {
	scope := v.chatScope()
	globalChatMu.Lock()
	src := globalChatHistories[scope]
	msgs := make([]chatMessage, len(src))
	copy(msgs, src)
	globalChatMu.Unlock()

	if len(msgs) == 0 {
		return false
	}

	v.history = msgs
	for _, msg := range msgs {
		v.renderMessage(msg.role, msg.content)
	}
	v.output.ScrollToEnd()

	return true
}

func (v *AIChatView) appendError(msg string) {
	v.app.QueueUpdateDraw(func() {
		fmt.Fprintf(v.output, "\n    [red::b]✖ Error:[-::-] [red::-]%s[-::-]\n", msg)
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
					fmt.Fprintf(v.output, "\n    [%s::d]┌─ %s ─────────────────────[-::-]\n", codeColor, lang)
				} else {
					fmt.Fprintf(v.output, "\n    [%s::d]┌──────────────────────────[-::-]\n", codeColor)
				}
			} else {
				fmt.Fprintf(v.output, "    [%s::d]└──────────────────────────[-::-]\n\n", codeColor)
			}
			continue
		}

		if inCodeBlock {
			fmt.Fprintf(v.output, "    [%s::d]│[-::-] [%s::-]%s[-::-]\n", codeColor, highlightColor, line)
			continue
		}

		// Headers: ## Foo -> bold.
		if strings.HasPrefix(trimmed, "#") {
			text := strings.TrimLeft(trimmed, "# ")
			fmt.Fprintf(v.output, "\n    [%s::b]%s[-::-]\n", highlightColor, text)
			continue
		}

		// Bullet points.
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			rest := trimmed[2:]
			rest = renderInlineFormatting(rest)
			fmt.Fprintf(v.output, "    [%s::-]•[-::-] %s\n", highlightColor, rest)
			continue
		}

		// Numbered lists.
		if matched, _ := regexp.MatchString(`^\d+\.\s`, trimmed); matched {
			formatted := renderInlineFormatting(trimmed)
			fmt.Fprintf(v.output, "    %s\n", formatted)
			continue
		}

		// Empty line.
		if trimmed == "" {
			fmt.Fprint(v.output, "\n")
			continue
		}

		// Regular text.
		formatted := renderInlineFormatting(trimmed)
		fmt.Fprintf(v.output, "    %s\n", formatted)
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
	addColor := s.Frame().Status.AddColor

	welcome := fmt.Sprintf(
		"\n  [%s::b]✦ K9s AI Assistant[-::-]\n"+
			"  [%s::d]Powered by GitHub Copilot[-::-]\n\n"+
			"  [%s::-]Ask me anything about your cluster:[-::-]\n\n"+
			"    [%s::-]•[-::-] Diagnose pod crashes, OOM kills, image pull errors\n"+
			"    [%s::-]•[-::-] Fix deployments by patching, scaling, or restarting\n"+
			"    [%s::-]•[-::-] Analyze events, logs, RBAC, and cluster health\n\n"+
			"  [%s::d]PgUp/PgDn scroll  ·  ↑↓ scroll  ·  Ctrl+C clear  ·  Ctrl+R reset[-::-]\n",
		addColor,
		dimColor,
		dimColor,
		hlColor, hlColor, hlColor,
		dimColor,
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
		label = fmt.Sprintf("%s/%s", v.resName, v.resNamespace)
	}

	fmt.Fprintf(v.output, "\n  [%s::d]%s[-::-]\n", dimColor, chatSeparator)
	fmt.Fprintf(v.output, "  [%s::b]Resource:[-::-]  [%s::-]%s[-::-] [%s::d](%s)[-::-]\n", hlColor, hlColor, label, dimColor, v.resKind)
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
// The prompt is shown as a user message so the user can see what was asked.
func (v *AIChatView) SendDiagnostic(resourceKind, resourceName, namespace string) {
	prompt := fmt.Sprintf(
		"Diagnose the %s '%s' in namespace '%s'. Check its status, recent events, logs if applicable, and suggest fixes for any issues.",
		resourceKind, resourceName, namespace,
	)

	v.appendMessage("user", prompt)
	go v.sendMessage(prompt)
}

// --------------------------------------------------------------------------
// chatListener implements ai.Listener for streaming responses.
// It writes streamed deltas directly to the output in real time so the user
// sees the response building up.

type chatListener struct {
	view            *AIChatView
	streamedContent *strings.Builder
	mu              *sync.Mutex
}

func (l *chatListener) AIResponseStart() {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusStreaming()
	})
}

func (l *chatListener) AIResponseDelta(delta string) {
	l.mu.Lock()
	l.streamedContent.WriteString(delta)
	l.mu.Unlock()

	// Stream the delta directly to the output so the user sees it live.
	l.view.app.QueueUpdateDraw(func() {
		// Print the Copilot header once at start of streaming.
		l.view.mu.Lock()
		if !l.view.streamingHeader {
			l.view.streamingHeader = true
			l.view.mu.Unlock()
			s := l.view.app.Styles
			dimColor := s.Frame().Menu.FgColor
			fmt.Fprintf(l.view.output, "\n  [%s::d]%s[-::-]\n", dimColor, chatSeparator)
			fmt.Fprintf(l.view.output, "  [%s::b]✦ Copilot[-::-]\n    ", s.Frame().Status.AddColor)
		} else {
			l.view.mu.Unlock()
		}
		// Write the raw delta text.
		fmt.Fprint(l.view.output, delta)
		l.view.output.ScrollToEnd()
		l.view.setStatusStreaming()
	})
}

func (l *chatListener) AIResponseComplete(text string) {
	if text != "" {
		l.mu.Lock()
		l.streamedContent.Reset()
		l.streamedContent.WriteString(text)
		l.mu.Unlock()
	}
	// Add trailing newline after streamed content.
	l.view.app.QueueUpdateDraw(func() {
		fmt.Fprint(l.view.output, "\n")
		l.view.output.ScrollToEnd()
	})
}

func (l *chatListener) AIResponseFailed(err error) {
	slog.Error("AI streaming failed", slogs.Error, err)
	l.view.appendError(err.Error())
}

func (l *chatListener) AIReasoningDelta(content string) {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusReasoning()
	})
}

func (l *chatListener) AIReasoningComplete(content string) {
	l.view.app.QueueUpdateDraw(func() {
		s := l.view.app.Styles
		dimColor := s.Frame().Menu.FgColor
		fmt.Fprintf(l.view.output, "    [%s::d]○ %s[-::-]\n", dimColor, content)
		l.view.output.ScrollToEnd()
	})
}

func (l *chatListener) AIToolStart(toolName string) {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusTool(toolName)
		s := l.view.app.Styles
		dimColor := s.Frame().Menu.FgColor
		label := toolDisplayName(toolName)
		fmt.Fprintf(l.view.output, "    [%s::d]⚡ %s[-::-]\n", dimColor, label)
		l.view.output.ScrollToEnd()

		// Also persist to history.
		msg := chatMessage{role: "activity", content: label, activity: true}
		l.view.history = append(l.view.history, msg)
		scope := l.view.chatScope()
		globalChatMu.Lock()
		globalChatHistories[scope] = append(globalChatHistories[scope], msg)
		globalChatMu.Unlock()
	})
}

func (l *chatListener) AIToolComplete(toolName string) {
	l.view.app.QueueUpdateDraw(func() {
		l.view.setStatusThinking()
	})
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
	case "patch_resource":
		return "Patching resource..."
	case "scale_resource":
		return "Scaling resource..."
	case "restart_resource":
		return "Restarting resource..."
	case "delete_resource":
		return "Deleting resource..."
	case "report_intent":
		return "Planning action..."
	default:
		return fmt.Sprintf("Running %s...", name)
	}
}
