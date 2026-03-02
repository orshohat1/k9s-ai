// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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
	byokTitle = "BYOK Setup"
)

// BYOKView provides an interactive form for configuring BYOK providers.
type BYOKView struct {
	*tview.Flex

	app            *App
	form           *tview.Form
	actions        *ui.KeyActions
	savedHints     model.MenuHints
	fieldNormalBg  tcell.Color
	fieldFocusBg   tcell.Color
}

var _ model.Component = (*BYOKView)(nil)

// NewBYOKView returns a new BYOK configuration view.
func NewBYOKView() *BYOKView {
	return &BYOKView{
		Flex:    tview.NewFlex().SetDirection(tview.FlexRow),
		form:    tview.NewForm(),
		actions: ui.NewKeyActions(),
	}
}

func (*BYOKView) SetCommand(*cmd.Interpreter)            {}
func (*BYOKView) SetFilter(string, bool)                 {}
func (*BYOKView) SetLabelSelector(labels.Selector, bool) {}

// Init initializes the BYOK view.
func (v *BYOKView) Init(ctx context.Context) error {
	var err error
	if v.app, err = extractApp(ctx); err != nil {
		return err
	}

	v.SetBorder(true)
	v.SetBorderPadding(1, 1, 2, 2)

	v.buildForm()

	v.AddItem(v.form, 0, 1, true)

	v.bindKeys()
	v.StylesChanged(v.app.Styles)
	v.updateTitle()

	return nil
}

// Focus delegates focus to the form so tview dispatches key events directly
// to the form rather than the enclosing Flex.
func (v *BYOKView) Focus(delegate func(p tview.Primitive)) {
	delegate(v.form)
}

// HasFocus returns true if the form has focus.
func (v *BYOKView) HasFocus() bool {
	return v.form.HasFocus()
}

func (v *BYOKView) buildForm() {
	aiCfg := v.app.Config.K9s.AI

	// Pre-fill from existing config.
	providerType := ""
	baseURL := ""
	apiKey := ""
	modelName := aiCfg.Model

	if aiCfg.Provider != nil {
		providerType = aiCfg.Provider.Type
		baseURL = aiCfg.Provider.BaseURL
		apiKey = aiCfg.Provider.APIKey
	}

	providerTypes := []string{"openai", "azure", "anthropic"}
	initialIdx := 0
	for i, pt := range providerTypes {
		if pt == providerType {
			initialIdx = i
			break
		}
	}

	ds := v.app.Styles.Dialog()
	fieldBg := ds.ButtonBgColor.Color()     // contrasting bg for fields (dark slate blue)
	fieldFocusBg := ds.ButtonFocusBgColor.Color() // bright bg for focused field (dodger blue)
	fieldFg := ds.FieldFgColor.Color()

	v.form.Clear(true)
	v.form.SetItemPadding(1)
	v.form.SetFieldBackgroundColor(fieldBg)
	v.form.SetFieldTextColor(fieldFg)
	v.form.SetButtonBackgroundColor(ds.ButtonBgColor.Color())
	v.form.SetButtonTextColor(ds.ButtonFgColor.Color())
	v.form.SetLabelColor(ds.LabelFgColor.Color())

	v.form.AddDropDown("Provider Type ", providerTypes, initialIdx, nil)
	dd := v.form.GetFormItemByLabel("Provider Type ").(*tview.DropDown)
	dd.SetFieldBackgroundColor(fieldBg)
	dd.SetFieldTextColor(fieldFg)
	dd.SetListStyles(
		ds.FgColor.Color(), ds.BgColor.Color(),
		ds.ButtonFocusFgColor.Color(), fieldFocusBg,
	)

	v.form.AddInputField("Base URL      ", baseURL, 60, nil, nil)
	v.form.AddPasswordField("API Key       ", apiKey, 60, '*', nil)
	v.form.AddInputField("Model         ", modelName, 40, nil, nil)

	// Wire focus-tracking: highlight focused field, dim others.
	v.trackFieldFocus(fieldBg, fieldFocusBg)

	v.form.AddButton("Save & Apply", v.saveConfig)
	v.form.AddButton("Remove BYOK", v.removeBYOK)
	v.form.AddButton("Cancel", v.cancel)

	for i := range 3 {
		if b := v.form.GetButton(i); b != nil {
			b.SetBackgroundColorActivated(ds.ButtonFocusBgColor.Color())
			b.SetLabelColorActivated(ds.ButtonFocusFgColor.Color())
		}
	}
}

// trackFieldFocus highlights the focused form field and dims the rest.
// It polls the form's focused item index on every Draw cycle.
func (v *BYOKView) trackFieldFocus(normalBg, focusBg tcell.Color) {
	v.fieldNormalBg = normalBg
	v.fieldFocusBg = focusBg
}

// Draw overrides Flex.Draw to update field highlight before rendering.
func (v *BYOKView) Draw(screen tcell.Screen) {
	if v.form != nil {
		fi, _ := v.form.GetFocusedItemIndex()
		for i := range v.form.GetFormItemCount() {
			item := v.form.GetFormItem(i)
			if i == fi {
				switch w := item.(type) {
				case *tview.InputField:
					w.SetFieldBackgroundColor(v.fieldFocusBg)
				case *tview.DropDown:
					w.SetFieldBackgroundColor(v.fieldFocusBg)
				}
			} else {
				switch w := item.(type) {
				case *tview.InputField:
					w.SetFieldBackgroundColor(v.fieldNormalBg)
				case *tview.DropDown:
					w.SetFieldBackgroundColor(v.fieldNormalBg)
				}
			}
		}
	}
	v.Flex.Draw(screen)
}

func (v *BYOKView) saveConfig() {
	// Read form values.
	_, providerType := v.form.GetFormItemByLabel("Provider Type ").(*tview.DropDown).GetCurrentOption()
	baseURL := v.form.GetFormItemByLabel("Base URL      ").(*tview.InputField).GetText()
	apiKey := v.form.GetFormItemByLabel("API Key       ").(*tview.InputField).GetText()
	modelName := v.form.GetFormItemByLabel("Model         ").(*tview.InputField).GetText()

	providerType = strings.TrimSpace(providerType)
	baseURL = strings.TrimSpace(baseURL)
	apiKey = strings.TrimSpace(apiKey)
	modelName = strings.TrimSpace(modelName)

	if baseURL == "" {
		v.app.Flash().Errf("Base URL is required for BYOK configuration")
		return
	}
	if modelName == "" {
		v.app.Flash().Errf("Model name is required")
		return
	}

	// Update config in memory.
	v.app.Config.K9s.AI.Provider = &config.AIProvider{
		Type:    providerType,
		BaseURL: baseURL,
		APIKey:  apiKey,
	}
	v.app.Config.K9s.AI.Model = modelName

	// Persist to disk.
	if err := v.app.Config.SaveFile(config.AppConfigFile); err != nil {
		slog.Error("Failed to save BYOK config", slogs.Error, err)
		v.app.Flash().Errf("Failed to save config: %v", err)
		return
	}

	// Reinitialize AI client with the new provider.
	v.reinitAI()

	v.app.Flash().Infof("BYOK configured: %s (%s) — model: %s", providerType, baseURL, modelName)
	slog.Info("BYOK provider configured", "type", providerType, "baseURL", baseURL, "model", modelName)

	v.app.Content.Pop()
}

func (v *BYOKView) removeBYOK() {
	v.app.Config.K9s.AI.Provider = nil

	if err := v.app.Config.SaveFile(config.AppConfigFile); err != nil {
		slog.Error("Failed to save config after removing BYOK", slogs.Error, err)
		v.app.Flash().Errf("Failed to save config: %v", err)
		return
	}

	// Reinitialize AI client without BYOK.
	v.reinitAI()

	// Re-show :ai models hint since we're back to Copilot.
	v.app.Menu().SetPersistentHints(model.MenuHints{
		{Mnemonic: ":ai", Description: "AI Chat", Visible: true},
		{Mnemonic: ":ai models", Description: "AI Models", Visible: true},
	})

	v.app.Flash().Info("BYOK removed — switched back to GitHub Copilot")
	slog.Info("BYOK provider removed, reverting to Copilot")

	v.app.Content.Pop()
}

func (v *BYOKView) reinitAI() {
	// Stop existing client.
	if ai.Client != nil {
		ai.Client.Stop()
		ai.Client = nil
	}

	// Create new client with updated config.
	aiClient := ai.NewAIClient(v.app.Config.K9s.AI, slog.Default())
	ai.Client = aiClient

	if err := aiClient.Init(context.Background()); err != nil {
		slog.Error("AI client reinit failed", slogs.Error, err)
		v.app.Flash().Warn("AI reinit failed (will retry on use): " + err.Error())
		return
	}

	// Re-wire tools.
	if v.app.Conn() != nil && v.app.Conn().ConnectionOK() {
		if factory := v.app.factory; factory != nil {
			tf := ai.NewToolFactory(factory, v.app.Conn(), slog.Default())
			aiClient.SetTools(tf.BuildTools())
		}
	}
}

func (v *BYOKView) cancel() {
	v.app.Content.Pop()
}

// StylesChanged applies current skin styles.
func (v *BYOKView) StylesChanged(s *config.Styles) {
	ds := s.Dialog()
	bg := ds.BgColor.Color()
	v.SetBackgroundColor(bg)
	v.form.SetBackgroundColor(bg)
	v.SetBorderColor(ds.FgColor.Color())
}

func (v *BYOKView) updateTitle() {
	styles := v.app.Styles.Frame()
	mode := "new"
	if v.app.Config.K9s.AI.IsBYOK() {
		mode = fmt.Sprintf("editing — %s", v.app.Config.K9s.AI.Provider.Type)
	}
	title := ui.SkinTitle(fmt.Sprintf(" BYOK Setup [hilite:bg:b](%s)[fg:bg:-] ", mode), &styles)
	v.SetTitle(title)
}

// InCmdMode checks if prompt is active.
func (*BYOKView) InCmdMode() bool { return false }

// Name returns the component name.
func (*BYOKView) Name() string { return byokTitle }

// Start starts the BYOK view.
func (v *BYOKView) Start() {
	v.app.Styles.AddListener(v)
	// Suppress persistent :ai/:byok hints on this screen.
	v.savedHints = v.app.Menu().GetPersistentHints()
	v.app.Menu().SetPersistentHints(nil)
}

// Stop stops the BYOK view.
func (v *BYOKView) Stop() {
	v.app.Styles.RemoveListener(v)
	// Restore persistent hints.
	v.app.Menu().SetPersistentHints(v.savedHints)
}

// Hints returns menu hints — only escape, suppresses persistent hints.
func (v *BYOKView) Hints() model.MenuHints {
	return model.MenuHints{
		{Mnemonic: "Tab", Description: "Next Field", Visible: true},
		{Mnemonic: "Enter", Description: "Select/Activate", Visible: true},
		{Mnemonic: "Esc", Description: "Back", Visible: true},
	}
}

// ExtraHints returns additional hints.
func (*BYOKView) ExtraHints() map[string]string { return nil }

// Actions returns menu actions.
func (v *BYOKView) Actions() *ui.KeyActions {
	return v.actions
}

func (v *BYOKView) bindKeys() {
	v.actions.Bulk(ui.KeyMap{
		tcell.KeyEscape: ui.NewKeyAction("Back", func(*tcell.EventKey) *tcell.EventKey {
			v.app.Content.Pop()
			return nil
		}, true),
	})
}
