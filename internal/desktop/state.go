package desktop

import (
	"context"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/runner"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"github.com/oneo-me/onevra/source/components/command"
)

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
	post          func(func())
	closed        bool
	shutdown      bool
	win           *mygo.Window
	pickFolder    func(done func(path string))
	pickExport    func(done func(path string))
	pickAttach    func(done func(paths []string))
	probe         func(cfg serverConn)
	pair          func(link string, done func(addr, token string, err error))
	copy          func(text string) error
	// notify shows one desktop banner; clearNotices takes them down when
	// the app comes forward or changes server. Both are nil under tests
	// and where the platform cannot show them.
	notify       func(desktopNotice)
	clearNotices func()
	// menu runs one menu bar choice on the UI thread; a test sets it to
	// run the work at once, with no window.
	menu     func(func())
	setTitle func(string)
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
	serverFacts      map[string]agent.Facts
	modelOff         map[string]bool
	route            uint64
	attempt          uint64
}

// conversationState owns the conversation fields coordinated on the UI thread.
type conversationState struct {
	transcript   chat.State
	commandError string
	selected     string
	draft        string
	draftProject string
	draftModel   agent.ModelRef
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
// preferencesState owns the preferences fields coordinated on the UI thread.
type preferencesState struct {
	home                string
	appearance          string
	chatText            string
	keymap              map[string]chord
	projects            []string
	projectPins         map[string]bool
	projectLinks        map[string][]string
	notifyFinished      bool
	notifyApprovals     bool
	commandRecents      []string
	metadata            *metadataWriter
	metadataVersion     uint64
	metadataError       string
	unavailableProjects map[string]bool
}

// searchState owns the search fields coordinated on the UI thread.
type searchState struct {
	searchOpen     bool
	resultsList    ui.ListState
	searchText     string
	searchResults  []agent.Session
	searchSelected int
	searchLoading  bool
	searchVersion  uint64
	searches       chan searchRequest
	focusSearch    bool
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
	browserTool       string
	// paneTabs are the right pane's open tools and pages, in strip order.
	paneTabs          []paneTab
	expanded          map[string]bool
	projectPickerOpen bool
	projectFilter     string
	projectSelected   int
	projectFocus      bool
	projectList       ui.ListState
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
	modelSelected      int
	modelScrollAt      int
	modelFocus         bool
	settingsOpen       bool
	settingsTab        string
	historyOpen        bool
	historyFocus       bool
	historyQuery       string
	historySelected    int
	historyList        ui.ListState
	serverEditor       *serverEditor
	serverConfirm      *confirmState
	modelsQuery        string
	modelsList         ui.ListState
	composerFocus      bool
	toasts             []ui.Toast
	commandOpen        bool
	commandScope       string
	commands           command.State
	slashHelp          bool
	slashSelected      int
	slashSeen          string
	bellOpen           bool
	queueOpen          bool
	shortcutOpen       bool
	shortcutRecord     string
	// fullView hides the side panes for a conversation-only page, keeping
	// the states it found so leaving it restores the workspace.
	fullView         bool
	fullSidebar      bool
	fullBrowser      bool
	fullTerminalOpen bool
}
