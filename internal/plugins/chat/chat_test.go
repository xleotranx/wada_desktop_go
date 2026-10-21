package chat

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/kernel"
	"wada-desktop-go/internal/kernel/kerneltest"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
)

func start(t *testing.T, extra ...kernel.Plugin) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *plugintest.Host) {
	t.Helper()
	f := plugintest.NewAgent()
	host := &plugintest.Host{}
	p := &Plugin{}
	h := kerneltest.New(t, append([]kernel.Plugin{f, host, p}, extra...)...)
	return h, p, f, host
}

func kinds(p *Plugin) []rowKind {
	var out []rowKind
	for _, r := range p.t.rows {
		out = append(out, r.kind)
	}
	return out
}

func textEvent(kind agent.EventKind, session agent.SessionID, delta, text string) agent.Event {
	return agent.Event{Kind: kind, Session: session, Text: &agent.TextEvent{Message: "m", Delta: delta, Text: text}}
}

func TestFirstMessageCreatesTheConversationThenSends(t *testing.T) {
	h, p, f, _ := start(t)
	var changed []chat.Conversation
	kernel.On(h, chat.Changed, func(c chat.Conversation) { changed = append(changed, c) })
	p.NewChat("/work")
	p.draft = "  hello  "
	p.send()
	if p.draft != "" || !p.running() || !slices.Equal(kinds(p), []rowKind{rowUser}) || p.t.rows[0].text != "hello" {
		t.Fatalf("after send: draft=%q running=%v rows=%v", p.draft, p.running(), kinds(p))
	}
	p.send() // a second send while one is on its way does nothing
	h.Flush()
	if got := f.Calls(); !slices.Equal(got, []string{"create /work", "send new hello"}) {
		t.Fatalf("calls %q", got)
	}
	if p.conv.Session != "new" || changed[len(changed)-1].Session != "new" {
		t.Fatalf("conversation %+v", p.conv)
	}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "new"}})
	if p.sending || !p.t.busy {
		t.Fatal("the turn did not take over from the send")
	}
}

func TestOutgoingPipelineRewritesAndRejects(t *testing.T) {
	picker := &extension{setup: func(c *kernel.Context) error {
		kernel.Transform(c, chat.Outgoing, 0, func(d chat.Draft) (chat.Draft, error) {
			if strings.Contains(d.Text, "forbidden") {
				return d, errors.New("not allowed")
			}
			d.Model = agent.ModelRef{Provider: "p", ID: "chosen"}
			d.Text = strings.ToUpper(d.Text)
			return d, nil
		})
		return nil
	}}
	h, p, f, host := start(t, picker)
	p.draft = "forbidden words"
	p.send()
	h.Flush()
	if len(f.Calls()) != 0 || p.draft != "forbidden words" || len(host.Toasts) != 1 {
		t.Fatalf("rejected draft went out: calls=%q draft=%q toasts=%q", f.Calls(), p.draft, host.Toasts)
	}
	p.draft = "fine"
	p.send()
	h.Flush()
	if got := f.Calls(); !slices.Equal(got, []string{"create  chosen", "send new FINE"}) {
		t.Fatalf("calls %q", got)
	}
}

type extension struct{ setup func(*kernel.Context) error }

func (*extension) Manifest() kernel.Manifest       { return kernel.Manifest{ID: "extension"} }
func (e *extension) Setup(c *kernel.Context) error { return e.setup(c) }

func TestEventsBuildTheOpenConversation(t *testing.T) {
	h, p, _, _ := start(t)
	p.Open(agent.Session{ID: "s1", Title: "One"})
	h.Flush()
	kernel.Emit(h, agent.Events, []agent.Event{
		{Kind: agent.TurnStarted, Session: "s1"},
		textEvent(agent.TextDelta, "s1", "Hel", ""),
		textEvent(agent.TextDelta, "other", "not ours", ""),
		textEvent(agent.TextDelta, "s1", "lo", ""),
		{Kind: agent.ToolCalled, Session: "s1", Tool: &agent.Tool{ID: "t1", Name: "shell", Command: "ls"}},
		{Kind: agent.ToolFinished, Session: "s1", Tool: &agent.Tool{ID: "t1", Output: []string{"a\nb\n"}, Exit: 1}},
		{Kind: agent.ApprovalRequested, Session: "s1", Approval: &agent.Approval{ID: "ap", Action: "shell"}},
		{Kind: agent.SessionRenamed, Session: "s1", Title: "Renamed"},
	})
	if !slices.Equal(kinds(p), []rowKind{rowStream, rowTool, rowApproval}) || p.t.rows[0].text != "Hello" {
		t.Fatalf("rows %v, first %q", kinds(p), p.t.rows[0].text)
	}
	if got := p.t.rows[1].tool.detail; !slices.Equal(got, []string{"$ ls", "a", "b", "exit 1"}) {
		t.Fatalf("tool detail %q", got)
	}
	if p.title() != "Renamed" || p.working() {
		t.Fatalf("title %q working %v", p.title(), p.working())
	}
	kernel.Emit(h, agent.Events, []agent.Event{
		textEvent(agent.TextEnded, "s1", "", "Hello there"),
		{Kind: agent.TurnSucceeded, Session: "s1"},
	})
	if !slices.Equal(kinds(p), []rowKind{rowText, rowTool}) || p.t.rows[0].text != "Hello there" || p.running() {
		t.Fatalf("after the turn: rows %v running %v", kinds(p), p.running())
	}
}

func TestReplyRemovesTheApproval(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	approval := agent.Approval{ID: "ap", Action: "edit"}
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.ApprovalRequested, Session: "s1", Approval: &approval}})
	p.reply(approval, agent.Always)
	h.Flush()
	if !slices.Contains(f.Calls(), "reply ap") || p.t.waitingApproval() {
		t.Fatalf("calls %q rows %v", f.Calls(), kinds(p))
	}
}

func TestSwitchingDropsTheEarlierConversationsLoad(t *testing.T) {
	h, p, f, _ := start(t)
	f.Stored["s1"] = []agent.Message{{Role: agent.RoleUser, Text: "from one"}}
	f.Stored["s2"] = []agent.Message{{Role: agent.RoleUser, Text: "from two"}}
	p.Open(agent.Session{ID: "s1"})
	p.Open(agent.Session{ID: "s2"})
	h.RunWork(0) // s1 finishes first
	h.RunPosts()
	h.Flush()
	if len(p.t.rows) != 1 || p.t.rows[0].text != "from two" {
		t.Fatalf("rows %+v", p.t.rows)
	}
}

func TestFailedSendIsReportedNotRetried(t *testing.T) {
	h, p, f, _ := start(t)
	f.Fail["Send"] = errors.New("timeout")
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	p.draft = "hi"
	p.send()
	h.Flush()
	sends := 0
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, "send") {
			sends++
		}
	}
	if sends != 1 || p.running() || p.t.rows[len(p.t.rows)-1].kind != rowNote {
		t.Fatalf("sends=%d running=%v rows=%v", sends, p.running(), kinds(p))
	}
}

func TestReconnectSettlesTheTurnAndReloads(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "s1"}, textEvent(agent.TextDelta, "s1", "par", "")})
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Down, Attempt: 1})
	if p.running() || kinds(p)[len(kinds(p))-1] != rowNote {
		t.Fatalf("drop: running=%v rows=%v", p.running(), kinds(p))
	}
	f.Stored["s1"] = []agent.Message{{Role: agent.RoleUser, Text: "reloaded"}}
	kernel.Emit(h, agent.StatusChanged, agent.Status{State: agent.Ready, Attempt: 2})
	h.Flush()
	if got := f.Calls(); !slices.Equal(got, []string{"messages s1", "messages s1"}) || p.t.rows[0].text != "reloaded" {
		t.Fatalf("calls %q rows %+v", got, p.t.rows)
	}
}

func TestComposerSendsAndApprovalButtonsAnswer(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	p.focus = true
	tt.Frame()
	tt.Type("ship it")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	h.Flush()
	tt.Frame()
	if !tt.HasText("ship it") || !slices.Contains(f.Calls(), "send s1 ship it") {
		t.Fatalf("composer send: calls %q texts %q", f.Calls(), tt.Texts())
	}
	kernel.Emit(h, agent.Events, []agent.Event{
		{Kind: agent.TurnStarted, Session: "s1"},
		{Kind: agent.ApprovalRequested, Session: "s1", Approval: &agent.Approval{ID: "ap", Action: "shell", Resources: []string{"rm -rf build"}}},
	})
	tt.Frame()
	if !tt.HasText("Approval needed — run a shell command") || !tt.HasText("rm -rf build") {
		t.Fatalf("approval card missing: %q", tt.Texts())
	}
	if err := tt.Click("Allow once"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	tt.Frame()
	if !slices.Contains(f.Calls(), "reply ap") || tt.HasText("Allow once") {
		t.Fatalf("approval not answered: %q", f.Calls())
	}
}

func TestStoredConversationLoads(t *testing.T) {
	var tr transcript
	tr.reset()
	tr.load([]agent.Message{
		{Role: agent.RoleUser, Text: " question "},
		{Role: agent.RoleAssistant, Parts: []agent.Part{
			{Kind: agent.PartReasoning, Text: "hidden"},
			{Kind: agent.PartText, Text: "answer"},
			{Kind: agent.PartTool, Tool: &agent.Tool{Name: "read", Output: []string{"1\n2\n3\n4\n5\n6\n7\n"}, Truncated: true}},
		}},
		{Role: agent.RoleOther, Text: "idle"},
	})
	if len(tr.rows) != 3 || tr.rows[0].text != "question" || tr.rows[1].text != "answer" {
		t.Fatalf("rows %+v", tr.rows)
	}
	if tool := tr.rows[2].tool; tool.label != "Read files" || !slices.Equal(tool.detail, []string{"2", "3", "4", "5", "6", "7", "…"}) {
		t.Fatalf("tool %+v", tool)
	}
	tr.apply(agent.Event{Kind: agent.TextStarted, Session: "s", Text: &agent.TextEvent{Message: "m"}})
	tr.apply(agent.Event{Kind: agent.TurnInterrupted, Session: "s"})
	if last := tr.rows[len(tr.rows)-1]; last.kind != rowText || last.text != "(the answer was cut short)" {
		t.Fatalf("cut row %+v", last)
	}
}

func TestFailedTurnShowsTheReason(t *testing.T) {
	var tr transcript
	tr.reset()
	tr.apply(agent.Event{Kind: agent.TurnStarted, Session: "s"})
	tr.apply(agent.Event{Kind: agent.TurnFailed, Session: "s", Error: "401: Invalid credential"})
	if last := tr.rows[len(tr.rows)-1]; last.kind != rowNote || last.text != "The agent stopped with an error: 401: Invalid credential" || tr.busy {
		t.Fatalf("note %+v busy %v", last, tr.busy)
	}
}
