package explore

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
)

var parent = chat.Conversation{Session: "parent", Directory: "/project", Title: "Original"}

// start opens the original conversation after stored is in the backend.
func start(t *testing.T, store *kerneltest.Store, stored ...map[agent.SessionID][]agent.Message) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Chat, *kittest.Host) {
	t.Helper()
	a, ctl, host := plugintest.NewAgent(), &plugintest.Chat{}, &kittest.Host{}
	for _, m := range stored {
		maps.Copy(a.Stored, m)
	}
	ws := &plugintest.Workspace{Convs: []workspace.Conversation{{ID: "parent", Folder: "/project", Title: "Original"}}}
	var p *Plugin
	h := kerneltest.Start(t, kerneltest.Config{Store: store}, a.New, ctl.New, host.New, ws.New,
		kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	ctl.Open(ws.Convs[0].Session())
	return h, p, a, ctl, host
}

// page draws the drawer beside a stand-in for the conversation, as the chat
// page does.
func page(p *Plugin) *ui.Tester {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			ui.Box(c).Grow(1).MinWidth(0)
			p.drawer(c)
		})
	}, 1000, 700)
	tt.Frame()
	return tt
}

func TestASelectionOpensTheDrawerWithTheQuoteInItsComposer(t *testing.T) {
	_, p, a, _, _ := start(t, nil)
	tt := page(p)
	if tt.HasText("Side chat") {
		t.Fatal("the drawer shows before a side chat starts")
	}
	p.start(chat.Selection{Conversation: parent, Text: "SQLite is an option\nfor now"})
	tt.Frame()
	for _, s := range []string{"Side chat", "No messages yet", "Beside “Original” · read-only"} {
		if !tt.HasText(s) {
			t.Fatalf("no %q in %q", s, tt.Texts())
		}
	}
	if p.side.draft != "> SQLite is an option\n> for now\n\n" || !tt.Focused("Side chat message") {
		t.Fatalf("draft %q", p.side.draft)
	}
	if len(a.Calls()) != 0 {
		t.Fatalf("starting a side chat reached the backend: %v", a.Calls())
	}
	if err := tt.Click("Close side chat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Side chat") {
		t.Fatal("the drawer did not close")
	}
}

func TestSendingCreatesAReadOnlyChildOnceAndAnswersComeBackAsADraft(t *testing.T) {
	h, p, a, ctl, _ := start(t, nil)
	var started []chat.Conversation
	kernel.On(h, chat.Started, func(c chat.Conversation) { started = append(started, c) })
	tt := page(p)
	p.start(chat.Selection{Conversation: parent, Text: "SQLite is an option"})
	tt.Frame()
	tt.Type("Could we avoid a database?")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	p.send() // a second send while the first is on its way does nothing
	h.Flush()
	requests := a.SpawnRequests()
	if len(requests) != 1 || requests[0].Parent != "parent" || requests[0].Directory != "/project" ||
		requests[0].Name != "Side chat: Could we avoid a database?" {
		t.Fatalf("spawn requests %+v", requests)
	}
	for _, kind := range []agent.ToolKind{agent.ToolEdit, agent.ToolShell, agent.ToolFetch, agent.ToolOther} {
		if !slices.Contains(requests[0].Policy.Deny, kind) {
			t.Fatalf("tool %v was not denied", kind)
		}
	}
	want := "send child-1 " + preamble + "> SQLite is an option\n\nCould we avoid a database?"
	if calls := a.Calls(); !slices.Contains(calls, want) || a.Called("send ") != 1 {
		t.Fatalf("calls %q", calls)
	}
	if len(started) != 0 || ctl.Conv.Session != "parent" {
		t.Fatalf("the side chat joined the main record or took the page: %+v, %+v", started, ctl.Conv)
	}
	kernel.Emit(h, agent.Events, []agent.Event{
		{Kind: agent.TurnStarted, Session: "child-1"},
		{Kind: agent.TextDelta, Session: "child-1", Text: &agent.TextEvent{Message: "m", Delta: "Use a plain "}},
	})
	tt.Frame()
	if !tt.HasText("Use") || tt.HasText("Add to main chat") {
		t.Fatalf("streaming answer: %q", tt.Texts())
	}
	a.Respond("child-1", "Use a plain file")
	h.Flush()
	tt.Frame()
	ctl.SetDraft("Keep this unsent thought")
	if err := tt.Click("Add to main chat"); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	if ctl.Draft != "Keep this unsent thought\n\nFrom a side chat:\n\nUse a plain file" {
		t.Fatalf("draft %q", ctl.Draft)
	}
	p.side.draft = "And for tests?"
	p.send()
	h.Flush()
	if a.Called("spawn ") != 1 || !slices.Contains(a.Calls(), "send child-1 And for tests?") {
		t.Fatalf("the second message did not go to the same side chat: %v", a.Calls())
	}
	if a.Called("send parent") != 0 {
		t.Fatal("the main conversation was sent a message")
	}
}

func TestTheDrawerStaysWithItsConversationAndComesBackAfterRestart(t *testing.T) {
	store := kerneltest.NewStore()
	h, p, _, ctl, _ := start(t, store)
	p.start(chat.Selection{Conversation: parent, Text: "quote"})
	p.side.draft = "Why?"
	p.send()
	h.Flush()
	ctl.Open(agent.Session{ID: "elsewhere"})
	if p.shown() {
		t.Fatal("the drawer followed to another conversation")
	}
	ctl.Open(agent.Session{ID: "parent", Directory: "/project"})
	if !p.shown() {
		t.Fatal("the drawer did not come back with its conversation")
	}
	h.Close()

	h, p, a, ctl, _ := start(t, store, map[agent.SessionID][]agent.Message{"child-1": {
		{ID: "u", Role: agent.RoleUser, Text: preamble + "> quote\n\nWhy?"},
		{ID: "a", Role: agent.RoleAssistant, Parts: []agent.Part{{Kind: agent.PartText, Text: "Because."}}},
	}})
	h.Flush()
	if !p.shown() || p.side.focus {
		t.Fatal("the open side chat did not come back beside its conversation, or took the focus")
	}
	if len(p.side.entries) != 2 || p.side.entries[0].text != "> quote\n\nWhy?" || p.side.entries[1].text != "Because." {
		t.Fatalf("entries %+v", p.side.entries)
	}
	p.side.draft = "More?"
	p.send()
	h.Flush()
	if !slices.Contains(a.Calls(), "send child-1 More?") || a.Called("spawn ") != 0 {
		t.Fatalf("calls %v", a.Calls())
	}
	ctl.Open(agent.Session{ID: "elsewhere"})
	if p.canShow() {
		t.Fatal("another conversation offers this side chat")
	}
	ctl.Open(agent.Session{ID: "parent"})
	p.close()
	p.replace(nil)
	ctl.Open(agent.Session{ID: "elsewhere"})
	ctl.Open(agent.Session{ID: "parent"})
	if p.shown() || !p.canShow() {
		t.Fatal("a closed side chat came back by itself, or can't be shown")
	}
	p.reveal(true)
	h.Flush()
	if !p.shown() || !p.side.focus {
		t.Fatal("Show Side Chat did not open it")
	}
	a.State.Origin = "another backend"
	kernel.Emit(h, agent.StatusChanged, a.State)
	if p.side != nil || p.canShow() {
		t.Fatal("a side chat crossed backend origins")
	}
}

func TestSpawnFailuresAreNotRetriedAndPartialSessionsAreRemembered(t *testing.T) {
	h, p, a, _, _ := start(t, nil)
	a.OnSpawn = func(agent.Spawn) (agent.Session, error) {
		return agent.Session{ID: "partial"}, errors.New("model setup failed")
	}
	p.start(chat.Selection{Conversation: parent, Text: "quote"})
	p.side.draft = "Why?"
	p.send()
	h.Flush()
	last := p.side.entries[len(p.side.entries)-1]
	if a.Called("spawn ") != 1 || a.Called("send ") != 0 || len(p.links) != 1 || p.links[0].Child != "partial" ||
		last.kind != entryNote || !strings.Contains(last.text, "model setup failed") || p.side.running() {
		t.Fatalf("calls %v, links %+v, entries %+v", a.Calls(), p.links, p.side.entries)
	}
}

func TestANewChatCannotStartASideChat(t *testing.T) {
	_, p, _, _, host := start(t, nil)
	p.start(chat.Selection{Text: "quote"})
	if p.side != nil || len(host.Toasts) != 1 {
		t.Fatalf("side %+v toasts %+v", p.side, host.Toasts)
	}
}

func TestSideChatsOfRemovedConversationsAreForgotten(t *testing.T) {
	h, p, _, _, _ := start(t, nil)
	p.links = []link{
		{Origin: "fake", Parent: "parent", Child: "kept"},
		{Origin: "fake", Parent: "removed", Child: "dropped"},
		{Origin: "other", Parent: "elsewhere", Child: "other"},
	}
	kernel.Emit(h, workspace.Changed, struct{}{})
	if len(p.links) != 2 || p.links[0].Child != "kept" || p.links[1].Child != "other" {
		t.Fatalf("links %+v", p.links)
	}
}

// created starts a side chat beside parent, child-1, and answers its first
// message.
func created(t *testing.T, p *Plugin, h *kerneltest.Harness, a *plugintest.Agent) *side {
	t.Helper()
	p.start(chat.Selection{Conversation: parent, Text: "quote"})
	p.side.draft = "Why?"
	p.send()
	h.Flush()
	a.Respond("child-1", "Because.")
	h.Flush()
	if p.side.child != "child-1" || p.side.running() {
		t.Fatalf("side %+v", p.side)
	}
	return p.side
}

func TestASideChatLeftWhileItIsCreatedStillSendsItsFirstMessage(t *testing.T) {
	h, p, a, _, _ := start(t, nil)
	p.start(chat.Selection{Conversation: parent, Text: "quote"})
	p.side.draft = "Why?"
	p.send()
	p.start(chat.Selection{Conversation: parent, Text: "another"}) // before the creation returns
	h.Flush()
	if !slices.Contains(a.Calls(), "send child-1 "+preamble+"Why?") {
		t.Fatalf("the first message was dropped: %v", a.Calls())
	}
	a.Respond("child-1", "Because.")
	h.Flush()
	p.replace(nil)
	p.reveal(true)
	if p.side.child != "child-1" || len(p.side.entries) != 2 || p.side.entries[1].text != "Because." {
		t.Fatalf("entries %+v", p.side.entries)
	}
}

func TestASendOnItsWayOutlivesLeavingTheDrawer(t *testing.T) {
	h, p, a, _, _ := start(t, nil)
	created(t, p, h, a)
	a.Fail["Send"] = errors.New("timeout")
	p.side.draft = "Again?"
	p.send()
	p.start(chat.Selection{Conversation: parent, Text: "another"}) // before the send returns
	h.Flush()
	p.replace(nil)
	p.reveal(true)
	last := p.side.entries[len(p.side.entries)-1]
	if last.kind != entryNote || !strings.Contains(last.text, "may not have reached the agent") || p.side.running() {
		t.Fatalf("the send's outcome was lost: %+v", p.side.entries)
	}
}

func TestATurnIsFollowedWhileAnotherConversationShowsItsSideChat(t *testing.T) {
	h, p, a, ctl, _ := start(t, nil)
	created(t, p, h, a)
	p.links = append(p.links, link{Origin: "fake", Parent: "other", Child: "other-side", Open: true})
	a.Emit(agent.Event{Kind: agent.TurnStarted, Session: "child-1"},
		agent.Event{Kind: agent.TextDelta, Session: "child-1", Text: &agent.TextEvent{Message: "m", Delta: "Half"}})
	h.Flush()
	ctl.Open(agent.Session{ID: "other"})
	h.Flush()
	if p.side.child != "other-side" {
		t.Fatalf("the other conversation's side chat did not open: %+v", p.side)
	}
	a.Emit(agent.Event{Kind: agent.TextDelta, Session: "child-1", Text: &agent.TextEvent{Message: "m", Delta: " done"}})
	h.Flush()
	if slices.ContainsFunc(p.side.entries, func(e *entry) bool { return strings.Contains(e.text, "done") }) {
		t.Fatal("one side chat's words landed in another")
	}
	ctl.Open(agent.Session{ID: "parent", Directory: "/project"})
	h.Flush()
	s := p.side
	if s.child != "child-1" || !s.running() || s.entries[len(s.entries)-1].text != "Half done" {
		t.Fatalf("the running turn was not followed: %+v", s.entries)
	}
}

func TestOtherSessionsEventsStayOutOfTheSideChat(t *testing.T) {
	h, p, a, _, _ := start(t, nil)
	s := created(t, p, h, a)
	n := len(s.entries)
	a.Respond("parent", "The main chat's answer")
	h.Flush()
	if len(s.entries) != n || s.running() {
		t.Fatalf("the main conversation's turn reached the side chat: %+v", s.entries)
	}
}

func TestAnAnswerJoinsOnlyTheDraftOfItsOwnConversation(t *testing.T) {
	h, p, a, ctl, _ := start(t, nil)
	created(t, p, h, a)
	ctl.Open(agent.Session{ID: "elsewhere"})
	ctl.SetDraft("Elsewhere's words")
	p.addToChat("Because.")
	if ctl.Draft != "Elsewhere's words" {
		t.Fatalf("an answer joined another conversation's draft: %q", ctl.Draft)
	}
}

func TestADroppedConnectionEndsTheTurn(t *testing.T) {
	h, p, a, ctl, _ := start(t, nil)
	s := created(t, p, h, a)
	a.Emit(agent.Event{Kind: agent.TurnStarted, Session: "child-1"})
	h.Flush()
	ctl.Open(agent.Session{ID: "elsewhere"}) // also while the drawer is not on show
	a.State.State = agent.Down
	kernel.Emit(h, agent.StatusChanged, a.State)
	if last := s.entries[len(s.entries)-1]; s.running() || !strings.Contains(last.text, "connection dropped") {
		t.Fatalf("running %v, entries %+v", s.running(), s.entries)
	}
}
