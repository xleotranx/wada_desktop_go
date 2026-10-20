package main

import (
	"context"

	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components/command"
)

// terminalState owns the bottom panel's shells and its geometry. Tabs keep
// running while the panel is folded away, so opening it again shows the
// same scrollback; each shell's generation separates a restart from the
// title and exit notices of the one it replaced.
type terminalState struct {
	open      bool
	height    float32
	maximized bool
	savedH    float32
	tabs      []*terminalTab
	active    int
	nextID    uint64
}

// terminalTab is one shell in the bottom panel.
type terminalTab struct {
	id         uint64
	term       *terminal.Terminal
	title      string
	starting   bool
	exited     bool
	exitCode   int
	err        string
	generation uint64
	focus      bool
}

// runtimeState owns the runtime fields coordinated on the UI thread.
type runtimeState struct {
	submitCommand func(runner.Generation, runner.Command) runner.Admission
	connectTo     func(runner.Generation, runner.Connection)
	lifetime      context.Context
	stopLifetime  context.CancelFunc
	// localPassword is the secret this run's own server takes: named once
	// here rather than inside the spawn, so tools dialing on their own
	// speak the same password the pipe started the server with.
	localPassword string
	workCtx       context.Context
	stopWork      context.CancelFunc
	background    func(work func() func())
	closed        bool
	win           *mygo.Window
	pickFolder    func(done func(path string))
	pickExport    func(done func(path string))
	pickAttach    func(done func(paths []string))
	newTerminal   func(terminal.Options) (*terminal.Terminal, error)
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
	// draftLoaded names the draft key the composer holds, so clearing the
	// words removes only that key instead of an unloaded new-chat draft.
	draftLoaded string
	// agent names the persona the next conversation is born under ("plan"
	// answers read-only). The picker holds it; a session's own agent is
	// the server's word and stays what it was born with.
	agent string

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
	appearance          string
	chatText            string
	keymap              map[string]chord
	recent              map[string][]recentFile
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
	sidebarW          float32
	browserW          float32
	sidebarCollapsed  bool
	browserCollapsed  bool
	sidebarFilter     string
	selectedRail      string
	projectsExpanded  bool
	projectCollapsed  map[string]bool
	projectChatsShown map[string]bool
	browserAddress    string
	browserPage       string
	browserTool       string
	// paneTabs are the right pane's open tools and pages, in strip order.
	paneTabs          []paneTab
	browserHistory    []string
	browserHistoryAt  int
	expanded          map[string]bool
	projectPickerOpen bool
	projectFilter     string
	feed              ui.ListState
	md                markdown.Options
	mdTheme           markdown.Theme
	// The transcript's find bar: its open state, query, the match it
	// shows, and the match and query it last scrolled to, so a reader can
	// still scroll away between searches.
	findOpen           bool
	findQuery          string
	findMatch          int
	findShownMatch     int
	findShownQuery     string
	findQuerySeen      string
	selectAddedProject bool
	attach             *attachState
	quoteRequest       requestState
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
	composerFocus      bool
	toasts             []ui.Toast
	commandOpen        bool
	commandScope       string
	commands           command.State
	quickOpen          bool
	quickFocus         bool
	quickSelected      int
	quickList          ui.ListState
	slashHelp          bool
	slashSelected      int
	slashSeen          string
	shortcutOpen       bool
	shortcutRecord     string
	// fullView hides the side panes for a conversation-only page, keeping
	// the states it found so leaving it restores the workspace.
	fullView         bool
	fullSidebar      bool
	fullBrowser      bool
	fullTerminalOpen bool
}
