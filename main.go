package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// app is the state the window shows.
type app struct {
	selected string
	sidebarW float32

	// expanded holds collapsible lines ("activity:…").
	expanded map[string]bool

	draft string

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

	// The file-attach dialog, quoting a workspace file into the draft.
	attachOpen  bool
	attachDraft string
	attachNote  string

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

	// The chats search: what the field shows, the results it last got
	// and whether one is on.
	searchText    string
	searchResults []agentSession
	searchLoading bool
	// focusSearch hands the field the keyboard once, from the header's
	// search shape.
	focusSearch bool

	// The model catalog and the picker's popover.
	models       []agentModel
	modelsLoaded bool
	pickerOpen   bool
	modelFilter  string

	// The agent runner feeds the jobs; the rest is its bookkeeping of
	// what is streaming where.
	jobs       chan agentJob
	live       map[string]int    // streaming row by message id and ordinal
	toolNames  map[string]string // tool call id -> tool name
	toolCmds   map[string]string // tool call id -> command line
	toolBlocks map[string]int    // tool call id -> activity row index
}

func newApp() *app {
	a := &app{
		sidebarW: 340,
		expanded: map[string]bool{},
		blocks:   []block{},

		jobs:       make(chan agentJob, 16),
		live:       map[string]int{},
		toolNames:  map[string]string{},
		toolCmds:   map[string]string{},
		toolBlocks: map[string]int{},
		pending:    map[string]*agentPerm{},
	}
	return a
}

// reviseSearch follows the search field: a fresh query queues a lookup
// in the store, and an empty one drops back to the ordinary list at
// once.
func (a *app) reviseSearch() {
	if a.searchText == "" {
		a.searchResults = nil
		a.searchLoading = false
		return
	}
	select {
	case a.jobs <- agentJob{kind: "search", text: a.searchText}:
		a.searchLoading = true
	default:
	}
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
	a.attachModal(c, p)
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
		// a way to see the interface without clicking through it. The wait
		// lets the boot settle onto the newest conversation first.
		if out := os.Getenv("WADA_CAPTURE"); out != "" {
			win.OnReadyToShow(func() {
				time.AfterFunc(3*time.Second, func() {
					win.Update(func() {
						if png, err := win.CapturePage(); err == nil {
							os.WriteFile(out, png, 0o644)
						}
						mygo.App.Exit(0)
					})
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
	// A turn starting on nothing real opens the conversation: its first
	// words must not land under whatever the mock left on display.
	if a.selectedSession == "" {
		a.freshTranscript()
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

// openAttachDialog readies the file-attach dialog for the chat's folder.
func (a *app) openAttachDialog() {
	a.attachDraft = ""
	a.attachNote = ""
	a.attachOpen = true
}

// attachFile quotes a workspace file into the draft, as a fenced block
// the agent reads with the message; typing it again does not repeat it.
func (a *app) attachFile() {
	rel := strings.TrimSpace(a.attachDraft)
	if rel == "" {
		a.attachNote = "Name a file first."
		return
	}
	pth := rel
	if !strings.HasPrefix(rel, "~") && !filepath.IsAbs(rel) {
		pth = filepath.Join(a.chatDir(), rel)
	}
	abs, data, err := expandFile(pth)
	if err != nil {
		a.attachNote = err.Error()
		return
	}
	mark := "(file: " + abs + ")"
	if strings.Contains(a.draft, mark) {
		a.attachNote = "That file is already in the draft."
		return
	}
	a.draft += "\n\n" + mark + "\n```" + codeLang(abs) + "\n" + string(data) + "\n```\n"
	a.attachOpen = false
	a.attachDraft = ""
	a.attachNote = ""
}

// selectedRow is the Recents row the transcript currently looks at.
func (a *app) selectedRow() (agentSession, bool) {
	for _, s := range a.sessions {
		if s.id == a.selectedSession {
			return s, true
		}
	}
	return agentSession{}, false
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

// sessionModelOf is the model one conversation points at; the app's free
// default stands in before anything was picked.
func (a *app) sessionModelOf(sid string) agentModelRef {
	for _, s := range a.sessions {
		if s.id == sid {
			if s.model.id != "" {
				return s.model
			}
			break
		}
	}
	prov, name := sessionModel()
	return agentModelRef{providerID: prov, id: name}
}

// modelLabel is the fancy name of a model ref, from the catalog; the ref
// ids read fine while the catalog has not loaded.
func (a *app) modelLabel(ref agentModelRef) (name string, variants []string) {
	name = ref.id
	for _, m := range a.models {
		if m.providerID == ref.providerID && m.id == ref.id {
			name = m.name
			variants = m.variants
		}
	}
	return name, variants
}

// chooseModel queues a switch of what the open conversation runs on.
func (a *app) chooseModel(m agentModel, variant string) {
	a.pickerOpen = false
	select {
	case a.jobs <- agentJob{kind: "model", sid: a.selectedSession,
		provider: m.providerID, model: m.id, variant: variant}:
	default:
	}
}

// pickVariant keeps the conversation's model and changes its reasoning
// effort.
func (a *app) pickVariant(variant string) {
	ref := a.sessionModelOf(a.selectedSession)
	for _, m := range a.models {
		if m.providerID == ref.providerID && m.id == ref.id {
			a.chooseModel(m, variant)
			return
		}
	}
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

// adoptSession settles the view onto one conversation: what a Recents
// click does, and what the boot does with the newest conversation.
func (a *app) adoptSession(s agentSession) {
	label := s.title
	if label == "" {
		label = "New chat"
	}
	a.selected = label
	a.selectedSession = s.id
	a.freshTranscript()
}

// settleBoot finishes the boot: the conversation list lands and the view
// settles onto the newest conversation, as its click would have; with
// none, the transcript starts clean instead of showing the mock.
func (a *app) settleBoot(ls []agentSession, next string, lerr error) {
	if lerr == nil {
		a.sessions = ls
		a.nextCursor = next
	}
	if lerr == nil && len(ls) > 0 {
		a.adoptSession(ls[0])
		select {
		case a.jobs <- agentJob{kind: "open", sid: ls[0].id}:
		default:
		}
	} else {
		a.freshTranscript()
	}
	a.sessionsLoaded = true
}

// startNewChat clears the view; the conversation itself comes into being
// with the first words, so a new chat is not one until it is spoken in.
func (a *app) startNewChat() {
	a.selected = ""
	a.selectedSession = ""
	a.draft = ""
	a.freshTranscript()
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
