package main

import (
	"log"
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// block is one paragraph, bubble or activity line of the transcript.
type block struct {
	kind   string // "text", "user", "stream", "activity" or "permission"
	text   string
	icon   *ui.SVG
	key    string                         // key into app.expanded for collapsible lines
	detail func(c *ui.Context, p palette) // drawn below the line once expanded
	req    *agentPerm                     // the approval request a permission card carries
}

// sideFolder is a section entry of the sidebar with its chats.
type sideFolder struct {
	name  string
	tasks []string
}

// app is the state the window shows.
type app struct {
	selected string
	sidebarW float32

	// expanded holds collapsible lines ("activity:…"), folder rows
	// ("folder:…") and section titles ("pinned", "projects", "recents").
	expanded map[string]bool

	draft string

	pinned  string
	folders []sideFolder

	feed   ui.ScrollState
	blocks []block

	// Real conversations from the agent: what the Recents list shows and
	// what the composer talks to.
	sessions        []agentSession
	sessionsLoaded  bool
	selectedSession string
	busySession     string // the conversation whose turn is running
	pending         map[string]*agentPerm

	// The folder dialog for starting a chat in another directory.
	dirOpen  bool
	dirDraft string
	dirNote  string

	// The rename and delete dialogs of the Recents list, and where the
	// older conversations pick up once the first page is through.
	renameOpen  bool
	renameSid   string
	renameDraft string
	confirmOpen bool
	confirmSid  string
	confirmWhat string
	nextCursor  string
	moreLoading bool

	// The agent runner feeds the jobs; the rest is its bookkeeping of
	// what is streaming where.
	jobs       chan agentJob
	live       map[string]int    // streaming row by message id and ordinal
	toolNames  map[string]string // tool call id -> tool name
	toolCmds   map[string]string // tool call id -> command line
	toolBlocks map[string]int    // tool call id -> activity row index
}

func newApp() *app {
	wadaPi := sideFolder{"wada_pi_webapp", []string{
		"Implement PD-09 outcomes and retries",
		"Start PD-08",
		"Build trusted parallel agent flows",
		"Brainstorm Wada app improvements",
		"Review code changes",
	}}
	a := &app{
		sidebarW: 340,
		expanded: map[string]bool{
			"folder:wada_pi_webapp": true,
			"activity:viewed-image": true,
		},
		selected: "Implement PD-09 outcomes and retries",
		pinned:   "Compare OpenCode dev bot options",
		folders: []sideFolder{
			wadaPi,
			{"wada_automation", []string{"Nightly digest job", "Watch releases of mygo"}},
			{"wada-starter-hono", []string{"Scaffold the starter", "Wire the OAuth flow"}},
			{"wada_social", []string{"Draft the first post"}},
		},
		blocks: memoTranscript(),

		jobs:       make(chan agentJob, 16),
		live:       map[string]int{},
		toolNames:  map[string]string{},
		toolCmds:   map[string]string{},
		toolBlocks: map[string]int{},
		pending:    map[string]*agentPerm{},
	}
	return a
}

// view lays the window out: a sidebar, a hairline beside it and the
// transcript of the chosen chat, with the composer at its foot.
func (a *app) view(c *ui.Context) {
	p := paletteFor(c)
	bar := c.TitleBar()
	// The window controls are centered in a strip of bar.Height, so the
	// top rows take the same height: traffic lights, nav buttons and
	// the chat title share one center line.
	headerH := bar.Height
	if headerH < 48 {
		headerH = 48
	}

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.sidebarPane(c, p, bar, headerH)
		ui.Box(c).Width(1).FillHeight().Background(p.hairline)
		a.contentPane(c, p, headerH)
	})
	a.folderModal(c, p)
	a.sessionDialogs(c, p)
}

func main() {
	a := newApp()
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "Wada",
			Width:     1400,
			Height:    920,
			MinWidth:  980,
			MinHeight: 640,
			// The window draws its own title bar under the traffic lights.
			TitleBarStyle: mygo.TitleBarHidden,
			// Center the traffic lights in the header: the close button
			// is 14 DIP tall, so Y = header/2 - 7 puts the lights on the
			// same center line the top rows use (TitleBar().Height is
			// button height + 2*Y).
			TrafficLightPosition: &mygo.Point{X: 14, Y: 25},
			StateKey:             "main",
			Content:              ui.View(a.view),
		})

		// The agent pipe lives in the background; it publishes what it
		// streams back into the view through Window.Update.
		go a.connect(win, a.jobs)

		// WADA_CAPTURE=/path/out.png writes a PNG of the window and quits:
		// a way to see the interface without clicking through it.
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			win.OnReadyToShow(func() {
				win.Update(func() {
					if png, err := win.CapturePage(); err == nil {
						os.WriteFile(out, png, 0o644)
					}
					mygo.App.Exit(0)
				})
			})
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// send appends the draft to the transcript as the user's own words and
// brings the latest block in view.
func (a *app) send() {
	if strings.TrimSpace(a.draft) == "" {
		return
	}
	a.blocks = append(a.blocks, block{kind: "user", text: a.draft})
	text := strings.TrimSpace(a.draft)
	a.draft = ""
	// math.MaxFloat32 scrolls the tracked container to its end.
	a.feed.Y = 1e9
	// Hand the turn to the agent runner; where none runs, the transcript
	// keeps acting as the design mock it is.
	a.agentSend(text)
}

// agentSend queues one user turn for the runner, addressed to the open
// conversation or to a fresh one when none is open.
func (a *app) agentSend(text string) {
	select {
	case a.jobs <- agentJob{kind: "prompt", sid: a.selectedSession, text: text}:
	default:
	}
}

// stopTurn asks the runner to interrupt the running conversation.
func (a *app) stopTurn() {
	select {
	case a.jobs <- agentJob{kind: "stop", sid: a.busySession}:
	default:
	}
}

// answerPerm sends the decision for one approval request; the runner
// replies to the server and the card leaves the transcript once it lands.
func (a *app) answerPerm(perm *agentPerm, decision string) {
	if perm == nil {
		return
	}
	select {
	case a.jobs <- agentJob{kind: "answer", sid: perm.session, request: perm.id, decision: decision}:
	default:
	}
}

// chatDir is the directory the open conversation works in.
func (a *app) chatDir() string {
	for _, s := range a.sessions {
		if s.id == a.selectedSession {
			return s.dir
		}
	}
	return ""
}

// openFolderDialog readies the folder dialog with the directory the next
// chat would sensibly reuse.
func (a *app) openFolderDialog() {
	a.dirDraft = a.chatDir()
	if a.dirDraft == "" {
		a.dirDraft = workdir()
	}
	a.dirNote = ""
	a.dirOpen = true
}

// startChatIn checks a typed folder and queues a fresh chat for it.
func (a *app) startChatIn(path string) {
	clean, err := expandPath(path)
	if err != nil {
		a.dirNote = err.Error()
		return
	}
	a.dirOpen = false
	select {
	case a.jobs <- agentJob{kind: "new", dir: clean}:
	default:
	}
}

// requestRename opens the rename dialog for one conversation.
func (a *app) requestRename(s agentSession) {
	a.renameSid = s.id
	a.renameDraft = s.title
	a.renameOpen = true
}

// saveRename queues the new title; the sidebar row itself updates when
// the rename event comes back over the stream.
func (a *app) saveRename() {
	a.renameOpen = false
	title := strings.TrimSpace(a.renameDraft)
	if a.renameSid == "" || title == "" || title == a.titleOf(a.renameSid) {
		return
	}
	select {
	case a.jobs <- agentJob{kind: "rename", sid: a.renameSid, text: title}:
	default:
	}
}

// titleOf is one conversation's title as the list shows it now.
func (a *app) titleOf(sid string) string {
	for _, s := range a.sessions {
		if s.id == sid {
			return s.title
		}
	}
	return ""
}

// confirmDelete shows the dialog that stands between a click and a
// conversation gone for good.
func (a *app) confirmDelete(s agentSession) {
	a.confirmSid = s.id
	a.confirmWhat = s.title
	a.confirmOpen = true
}

// deleteConfirmed queues the deletion after the dialog's say-so.
func (a *app) deleteConfirmed() {
	a.confirmOpen = false
	if a.confirmSid == "" {
		return
	}
	select {
	case a.jobs <- agentJob{kind: "delete", sid: a.confirmSid}:
	default:
	}
}

// startNewChat empties the transcript and asks the runner for a fresh
// conversation once the server is up.
func (a *app) startNewChat() {
	a.selected = ""
	a.draft = ""
	a.freshTranscript()
	select {
	case a.jobs <- agentJob{kind: "new"}:
	default:
	}
}

// freshTranscript empties the transcript for another conversation,
// forgetting what was streaming and what tools ran.
func (a *app) freshTranscript() {
	a.blocks = nil
	a.live = map[string]int{}
	a.toolNames = map[string]string{}
	a.toolCmds = map[string]string{}
	a.toolBlocks = map[string]int{}
	a.pending = map[string]*agentPerm{}
	a.busySession = ""
	a.feed.Y = 1e9
}
