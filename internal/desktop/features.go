package desktop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/feature"
	"wada-desktop-go/internal/native"
	"wada-desktop-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

func (a *app) installFeatures(factories []feature.Factory) {
	a.registeredShortcuts = append([]shortcutSpec(nil), shortcutSpecs...)
	a.registeredSlashes = append([]slashCommand(nil), slashCommands...)
	a.registeredPanels = []feature.Panel{{ID: "Activity", Title: "Activity", Description: "Watch what the turn and the conversation have run, newest first.", Icon: icActivity, Render: func(c *ui.Context) { a.activityBody(c, paletteFor(c)) }}}

	host := feature.Host{
		OpenFolder: a.openProjectDialog, Lifetime: a.runtime.lifetime, Note: a.streamLine,
		ComposerBlocked: func() bool {
			return a.runtime.background == nil || a.layout.slashHelp || a.modulesModal() || len(a.slashMatches()) > 0
		},
		Context:    a.featureContext,
		Background: func(work func() func()) { a.runtime.background(work) },
		Post:       func(fn func()) { a.runtime.post(fn) },
		OpenPanel:  a.openTool,
		TogglePanel: func(id string) {
			if a.layout.browserTool == id && !a.layout.browserCollapsed {
				a.layout.browserCollapsed = true
				return
			}
			a.openTool(id)
		},
		Reveal:   func() { a.leaveFullView(false); a.layout.settingsOpen = false },
		Draft:    func() string { return a.conversation.draft },
		SetDraft: func(text string) { a.conversation.draft = text; a.layout.composerFocus = true },
		Copy:     func(text string) error { return a.runtime.copy(text) },
		Notify:   a.notify, Save: a.saveState,
	}
	moduleIDs := map[string]bool{}
	actionIDs := map[string]bool{}
	panelIDs := map[string]bool{"Activity": true}
	shortcutIDs := map[string]bool{}
	for _, spec := range shortcutSpecs {
		shortcutIDs[spec.id] = true
	}
	for _, item := range a.commandItems() {
		if strings.HasPrefix(item.ID, "action:") {
			actionIDs[strings.TrimPrefix(item.ID, "action:")] = true
		}
	}
	reject := func(module feature.Module, reason string) {
		if module != nil {
			module.Close()
		}
		for _, installed := range a.features {
			installed.Close()
		}
		a.runtime.stopWork()
		a.runtime.stopLifetime()
		panic(reason)
	}
	for _, factory := range factories {
		if factory == nil {
			reject(nil, "nil feature factory")
		}
		module := factory(host)
		if module == nil {
			reject(nil, "nil feature module")
		}
		if module.ID() == "" || moduleIDs[module.ID()] {
			reject(module, fmt.Sprintf("duplicate or empty feature identity %q", module.ID()))
		}
		moduleIDs[module.ID()] = true
		if provider, ok := module.(feature.Actions); ok {
			for _, action := range provider.Actions() {
				if action.ID == "" || action.Run == nil || actionIDs[action.ID] {
					reject(module, fmt.Sprintf("invalid or duplicate feature action %q", action.ID))
				}
				actionIDs[action.ID] = true
				a.registeredActions = append(a.registeredActions, action)
				if action.Slash != "" {
					run := action.Run
					a.registeredSlashes = append(a.registeredSlashes, slashCommand{action.Slash, action.Title, action.Description, func(*app) { run() }})
				}
				if action.Shortcut != "" {
					if shortcutIDs[action.Shortcut] {
						reject(module, fmt.Sprintf("duplicate shortcut %q", action.Shortcut))
					}
					shortcutIDs[action.Shortcut] = true
					a.registeredShortcuts = append(a.registeredShortcuts, shortcutSpec{action.Shortcut, action.Title, action.Mods, action.Key})
				}
			}
		}
		if provider, ok := module.(feature.Panels); ok {
			for _, panel := range provider.Panels() {
				if panel.ID == "" || panel.Render == nil || panelIDs[panel.ID] {
					reject(module, fmt.Sprintf("invalid or duplicate panel %q", panel.ID))
				}
				panelIDs[panel.ID] = true
				a.registeredPanels = append(a.registeredPanels, panel)
			}
		}
		a.features = append(a.features, module)
	}
	sort.Slice(a.registeredSlashes, func(i, j int) bool { return a.registeredSlashes[i].name < a.registeredSlashes[j].name })
}
func (a *app) featureContext() feature.Context {
	cfg := a.activeServerConn()
	config := cfg.transport()
	if !a.services.valid || a.services.config != config {
		a.services.close()
		a.services = featureServices{valid: true, config: config}
		client, err := a.backend.Client(config)
		a.services.err = err
		if err == nil {
			a.services.client = client
			a.services.files, _ = client.(workspace.Client)
		}
		if provider, ok := a.backend.(workspace.PolicyProvider); ok {
			policy, err := provider.Policy(config)
			if err == nil {
				a.services.policy = policy
			}
		}
	}

	return feature.Context{Attempt: a.connections.attempt, Closed: a.runtime.closed, Unavailable: a.services.err, Connection: a.connections.route, Policy: a.services.policy, Ready: a.connections.serverHealth[cfg.id].status == healthReady, IsDraft: a.conversation.target.isDraft(), Revision: a.featureRevision, Server: a.connections.activeServer, Directory: a.chatDir(), Home: a.preferences.home, Local: a.localActive(), Work: a.runtime.workCtx, Workspace: a.services.files}
}
func (a *app) featurePanel(id string) (feature.Panel, bool) { return a.panel(id) }
func (a *app) featureActions() []feature.Action             { return a.registeredActions }
func (a *app) runFeatureAction(id string) bool {
	for _, action := range a.featureActions() {
		if action.ID == id {
			action.Run()
			return true
		}
	}
	return false
}
func (a *app) bottom() feature.Bottom {
	for _, module := range a.features {
		if bottom, ok := module.(feature.Bottom); ok {
			return bottom
		}
	}
	return nil
}
func (a *app) bottomOpen() bool { return a.bottom() != nil && a.bottom().IsOpen() }
func (a *app) setBottomOpen(open bool) {
	if bottom := a.bottom(); bottom != nil {
		bottom.SetOpen(open)
	}
}
func (a *app) openWorkspaceFile(path string) {
	for _, module := range a.features {
		if opener, ok := module.(feature.FileOpener); ok {
			opener.OpenFile(path)
			return
		}
	}
}
func (a *app) moduleDialogs(c *ui.Context) {
	for _, module := range a.features {
		if dialogs, ok := module.(feature.Dialogs); ok {
			dialogs.Dialogs(c)
		}
	}
}
func (a *app) modulesModal() bool {
	for _, module := range a.features {
		if dialogs, ok := module.(feature.Dialogs); ok && dialogs.Modal() {
			return true
		}
	}
	return false
}
func (a *app) moduleSnapshot() map[string]json.RawMessage {
	state := map[string]json.RawMessage{}
	for id, raw := range a.savedModules {
		state[id] = append(json.RawMessage(nil), raw...)
	}
	for _, module := range a.features {
		if storage, ok := module.(feature.Storage); ok {
			state[module.ID()] = append(json.RawMessage(nil), storage.Snapshot()...)
		}
	}
	return state
}
func (a *app) restoreModules(state map[string]json.RawMessage) {
	a.savedModules = map[string]json.RawMessage{}
	for id, raw := range state {
		a.savedModules[id] = append(json.RawMessage(nil), raw...)
	}
	for _, module := range a.features {
		if storage, ok := module.(feature.Storage); ok {
			storage.Restore(append(json.RawMessage(nil), state[module.ID()]...))
		}
	}
}
func (a *app) openLocalPath(c *ui.Context, path string) { native.OpenLocalPath(c, path) }

func (a *app) dismissModuleDialogs() {
	for _, module := range a.features {
		if d, ok := module.(feature.Dismissible); ok {
			d.Dismiss()
		}
	}
}

func (a *app) syncComposerFeatures() {
	for _, module := range a.features {
		if composer, ok := module.(feature.Composer); ok {
			composer.SyncComposer()
		}
	}
}
func (a *app) composerOverlays(c *ui.Context) {
	for _, module := range a.features {
		if composer, ok := module.(feature.Composer); ok {
			composer.ComposerOverlay(c)
		}
	}
}
func (a *app) composerFeatureKey(key ui.TextKey) bool {
	for _, module := range a.features {
		if composer, ok := module.(feature.Composer); ok && composer.ComposerKey(key) {
			return true
		}
	}
	return false
}
func (a *app) beforeFeatureSend() bool {
	for _, module := range a.features {
		if composer, ok := module.(feature.Composer); ok && composer.BeforeSend() {
			return true
		}
	}
	return false
}
func (a *app) resetComposerFeatures() {
	for _, module := range a.features {
		if composer, ok := module.(feature.Composer); ok {
			composer.ResetComposer()
		}
	}
}

func (a *app) moduleHeaders(c *ui.Context) {
	for _, module := range a.features {
		if header, ok := module.(feature.Header); ok {
			header.Header(c)
		}
	}
}
func (a *app) modulesReady() {
	for _, module := range a.features {
		if ready, ok := module.(feature.Ready); ok {
			ready.Connected()
		}
	}
}

func (a *app) startModules() {
	for _, module := range a.features {
		if starter, ok := module.(feature.Starter); ok {
			starter.Start()
		}
	}
}
func (a *app) moduleComposerActions(c *ui.Context, compact bool) {
	for _, module := range a.features {
		if controls, ok := module.(feature.Accessory); ok {
			controls.ComposerActions(c, compact)
		}
	}
}

type featureServices struct {
	valid  bool
	config agent.Config
	client agent.Client
	files  workspace.Client
	policy workspace.Policy
	err    error
}

func (a *app) hasFeatureAction(id string) bool {
	for _, action := range a.featureActions() {
		if action.ID == id {
			return true
		}
	}
	return false
}
func (a *app) hasWorkspaceFeatures() bool { return a.hasFeatureAction("folder") }

func (a *app) visibleProjectDirectories() []string {
	if !a.hasWorkspaceFeatures() {
		return nil
	}
	return a.projectDirectories()
}

func (a *app) draftContent() feature.DraftContent {
	for _, module := range a.features {
		if screen, ok := module.(feature.DraftScreen); ok {
			return screen.DraftContent()
		}
	}
	return feature.DraftContent{Description: "Ask a question, explore an idea, or share a file.", Suggestions: []feature.Suggestion{
		{Title: "Explain an idea", Detail: "Make something complicated clearer", Prompt: "Help me understand an idea", Icon: icBook},
		{Title: "Help me write", Detail: "Find the right words", Prompt: "Help me draft something", Icon: icPen},
		{Title: "Think it through", Detail: "Explore options and tradeoffs", Prompt: "Help me think through a decision", Icon: icChat},
	}}
}

func (s *featureServices) close() {
	if s.client != nil {
		s.client.Stop()
	}
	if policy, ok := s.policy.(interface{ Stop() }); ok {
		policy.Stop()
	}
}
