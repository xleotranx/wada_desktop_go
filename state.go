package main

import (
	"context"

	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// runtimeState owns the runtime fields coordinated on the UI thread.
type runtimeState struct {
	submitCommand func(runner.Generation, runner.Command) runner.Admission
	connectTo     func(runner.Generation, runner.Connection)
	lifetime      context.Context
	stopLifetime  context.CancelFunc
	workCtx       context.Context
	stopWork      context.CancelFunc
	background    func(work func() func())
	closed        bool
	win           *mygo.Window
	pickFolder    func(done func(path string))
	probe         func(cfg serverConn)
	pair          func(link string, done func(addr, token string, err error))
	copy          func(text string) error
}

// connectionsState owns the connections fields coordinated on the UI thread.
type connectionsState struct {
	booted bool

	registryRequests map[string]*requestState
	pairLinks        map[string]mintedLink
	scopes           map[string]*serverScope
	servers          []serverConn
	activeServer     string
	serverHealth     map[string]serverHealth
	serverFacts      map[string]opencode.Facts
	modelOff         map[string]bool
	route            uint64
}

// conversationState owns the conversation fields coordinated on the UI thread.
type conversationState struct {
	transcript   chat.State
	commandError string
	selected     string
	draft        string
	draftProject string
	draftModel   opencode.ModelRef

	target conversationTarget
}

// workspaceState owns the workspace fields coordinated on the UI thread.
type workspaceState struct {
	policyRequest                                   requestState
	policyVersion, policyRoute, policyWriteRevision uint64
	policyRemote                                    bool
	policyTail                                      <-chan struct{}
	changes                                         *changesState
	files                                           *filesState
	policyDir, policyLevel                          string
	policyDirs                                      chan policyTarget
	policyWrites                                    chan policyChange
}

// preferencesState owns the preferences fields coordinated on the UI thread.
type preferencesState struct {
	home                string
	projects            []string
	projectPins         map[string]bool
	metadata            *metadataWriter
	metadataVersion     uint64
	metadataSaving      bool
	metadataError       string
	unavailableProjects map[string]bool
}

// searchState owns the search fields coordinated on the UI thread.
type searchState struct {
	searchOpen    bool
	resultsList   ui.ListState
	searchText    string
	searchResults []opencode.Session
	searchLoading bool
	searchVersion uint64
	searches      chan searchRequest
	focusSearch   bool
}

// viewState owns the view fields coordinated on the UI thread.
type viewState struct {
	sidebarW           float32
	sidebarCollapsed   bool
	browserCollapsed   bool
	selectedRail       string
	projectsExpanded   bool
	projectCollapsed   map[string]bool
	projectChatsShown  map[string]bool
	browserAddress     string
	browserPage        string
	browserTool        string
	browserHistory     []string
	browserHistoryAt   int
	expanded           map[string]bool
	projectPickerOpen  bool
	projectFilter      string
	feed               ui.ListState
	md                 markdown.Options
	mdTheme            markdown.Theme
	selectAddedProject bool
	attach             *attachState
	rename             *renameState
	confirm            *confirmState
	browse             *browseState
	pickerOpen         bool
	modelFilter        string
	settingsOpen       bool
	settingsTab        string
	serverEditor       *serverEditor
	serverConfirm      *confirmState
	modelsQuery        string
	modelsList         ui.ListState
}
