// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"log/slog"
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
	aiModelsTitle    = "AI Models"
	aiModelsTitleFmt = " AI Models [hilite:bg:b](%d available)[fg:bg:-] "
)

// AIModelsView displays available AI models for selection.
type AIModelsView struct {
	*tview.Flex

	app     *App
	table   *tview.Table
	actions *ui.KeyActions
	models  []ai.ModelInfo
	mu      sync.Mutex
}

var _ model.Component = (*AIModelsView)(nil)

// NewAIModelsView returns a new model picker view.
func NewAIModelsView() *AIModelsView {
	return &AIModelsView{
		Flex:    tview.NewFlex().SetDirection(tview.FlexRow),
		table:   tview.NewTable(),
		actions: ui.NewKeyActions(),
	}
}

func (*AIModelsView) SetCommand(*cmd.Interpreter)            {}
func (*AIModelsView) SetFilter(string, bool)                 {}
func (*AIModelsView) SetLabelSelector(labels.Selector, bool) {}

// Init initializes the models view.
func (v *AIModelsView) Init(ctx context.Context) error {
	var err error
	if v.app, err = extractApp(ctx); err != nil {
		return err
	}

	v.SetBorder(true)
	v.SetBorderPadding(0, 0, 1, 1)

	// Table setup
	v.table.SetSelectable(true, false)
	v.table.SetSelectedStyle(tcell.StyleDefault.
		Foreground(tcell.ColorBlack).
		Background(tcell.ColorAqua))
	v.table.SetSelectedFunc(v.selectModel)

	v.AddItem(v.table, 0, 1, true)

	v.bindKeys()
	v.SetInputCapture(v.keyboard)
	v.StylesChanged(v.app.Styles)
	v.updateTitle()

	// Load models asynchronously.
	go v.loadModels(ctx)

	return nil
}

// StylesChanged applies current skin styles.
func (v *AIModelsView) StylesChanged(s *config.Styles) {
	views := s.Views()
	v.SetBackgroundColor(views.Table.BgColor.Color())
	v.table.SetBackgroundColor(views.Table.BgColor.Color())
}

func (v *AIModelsView) updateTitle() {
	styles := v.app.Styles.Frame()
	title := ui.SkinTitle(fmt.Sprintf(aiModelsTitleFmt, len(v.models)), &styles)
	v.SetTitle(title)
}

// InCmdMode checks if prompt is active.
func (*AIModelsView) InCmdMode() bool { return false }

// Name returns the component name.
func (*AIModelsView) Name() string { return aiModelsTitle }

// Start starts the models view.
func (v *AIModelsView) Start() {
	v.app.Styles.AddListener(v)
	v.app.SetFocus(v.table)
}

// Stop stops the models view.
func (v *AIModelsView) Stop() {
	v.app.Styles.RemoveListener(v)
}

// Hints returns menu hints.
func (v *AIModelsView) Hints() model.MenuHints {
	return v.actions.Hints()
}

// ExtraHints returns additional hints.
func (*AIModelsView) ExtraHints() map[string]string { return nil }

// Actions returns menu actions.
func (v *AIModelsView) Actions() *ui.KeyActions {
	return v.actions
}

func (v *AIModelsView) bindKeys() {
	v.actions.Bulk(ui.KeyMap{
		tcell.KeyEscape: ui.NewKeyAction("Back", v.backCmd, false),
		tcell.KeyEnter:  ui.NewKeyAction("Select", v.selectModelKey, false),
	})
}

func (v *AIModelsView) keyboard(evt *tcell.EventKey) *tcell.EventKey {
	if a, ok := v.actions.Get(ui.AsKey(evt)); ok {
		return a.Action(evt)
	}
	return evt
}

func (v *AIModelsView) backCmd(evt *tcell.EventKey) *tcell.EventKey {
	v.app.Content.Pop()
	return nil
}

func (v *AIModelsView) selectModelKey(evt *tcell.EventKey) *tcell.EventKey {
	row, _ := v.table.GetSelection()
	v.selectModel(row, 0)
	return nil
}

func (v *AIModelsView) selectModel(row, _ int) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if row < 1 || row > len(v.models) {
		return
	}
	selected := v.models[row-1]

	if ai.Client == nil {
		v.app.Flash().Errf("AI client not available")
		return
	}

	ai.Client.SetModel(selected.ID)
	v.app.Flash().Infof("Model switched to: %s", selected.Name)
	slog.Info("AI model changed", slogs.Subsys, "ai", "model", selected.ID)

	v.app.Content.Pop()
}

func (v *AIModelsView) loadModels(ctx context.Context) {
	if ai.Client == nil {
		v.app.QueueUpdateDraw(func() {
			v.showError("AI client not initialized")
		})
		return
	}

	v.app.QueueUpdateDraw(func() {
		v.table.Clear()
		v.table.SetCell(0, 0, tview.NewTableCell("Loading models...").
			SetSelectable(false))
	})

	models, err := ai.Client.ListModels(ctx)
	if err != nil {
		slog.Error("Failed to list AI models", slogs.Error, err)
		v.app.QueueUpdateDraw(func() {
			v.showError(fmt.Sprintf("Failed to load models: %v", err))
		})
		return
	}

	v.mu.Lock()
	v.models = models
	v.mu.Unlock()

	activeModel := ai.Client.ActiveModel()

	v.app.QueueUpdateDraw(func() {
		v.table.Clear()

		// Header row.
		headers := []string{"", "MODEL ID", "NAME"}
		for col, h := range headers {
			cell := tview.NewTableCell(h).
				SetSelectable(false).
				SetExpansion(1).
				SetAttributes(tcell.AttrBold)
			v.table.SetCell(0, col, cell)
		}

		for i, m := range models {
			row := i + 1

			// Active indicator.
			indicator := " "
			if m.ID == activeModel {
				indicator = "✓"
			}

			v.table.SetCell(row, 0, tview.NewTableCell(indicator).SetExpansion(0))
			v.table.SetCell(row, 1, tview.NewTableCell(m.ID).SetExpansion(1))
			v.table.SetCell(row, 2, tview.NewTableCell(m.Name).SetExpansion(1))
		}

		if len(models) == 0 {
			v.table.SetCell(1, 0, tview.NewTableCell("No models available").
				SetSelectable(false))
		}

		v.table.Select(1, 0)
		v.updateTitle()
	})
}

func (v *AIModelsView) showError(msg string) {
	v.table.Clear()
	v.table.SetCell(0, 0, tview.NewTableCell(fmt.Sprintf("[red::b]%s[-::-]", msg)).
		SetSelectable(false))
}
