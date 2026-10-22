package chat

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/shell"
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

func TestFolderOfANewChatChangesWithoutLosingTheDraftAndIsAnnounced(t *testing.T) {
	h, p, _, _ := start(t)
	var started []chat.Conversation
	kernel.On(h, chat.Started, func(c chat.Conversation) { started = append(started, c) })
	p.NewChat("/work")
	p.draft = "hello"
	p.SetDirectory("/other")
	if p.draft != "hello" || p.conv.Directory != "/other" {
		t.Fatalf("draft %q conversation %+v", p.draft, p.conv)
	}
	p.send()
	h.Flush()
	if len(started) != 1 || started[0].Session != "new" || started[0].Directory != "/other" {
		t.Fatalf("started %+v", started)
	}
	p.SetDirectory("/elsewhere") // a stored conversation keeps its folder
	if p.conv.Directory != "/other" {
		t.Fatalf("conversation %+v", p.conv)
	}
}

func TestHandledInputReleasesComposerWithoutSettlingAnIndependentRun(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			h, p, f, _ := start(t)
			f.Disposition = agent.SendHandled
			p.draft = "/extension-command"
			p.send()
			h.RunWork(0) // Create the conversation.
			h.RunPosts() // Queue Send.
			if running {
				kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "new"}})
			}
			h.Flush()
			if p.sending || p.t.busy != running {
				t.Fatalf("handled input: sending=%v busy=%v; independent run=%v", p.sending, p.t.busy, running)
			}
		})
	}
}

func TestLateHandledReceiptDoesNotReleaseTheNextSend(t *testing.T) {
	h, p, f, _ := start(t)
	f.Disposition = agent.SendHandled
	p.draft = "/command"
	p.send()
	h.RunWork(0)
	h.RunPosts() // Create, then queue the first send.
	h.RunWork(0) // The receipt is waiting on the UI queue.
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "new"}, {Kind: agent.TurnSucceeded, Session: "new"}})
	f.Disposition = agent.SendAccepted
	p.draft = "next prompt"
	p.send()
	h.RunPosts() // Apply the first send's handled receipt.
	if !p.sending {
		t.Fatal("an older receipt released the next send")
	}
	h.Flush()
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

func TestATakenMessageGoesNoFurther(t *testing.T) {
	var took []string
	taker := &extension{setup: func(c *kernel.Context) error {
		kernel.Transform(c, chat.Outgoing, 0, func(d chat.Draft) (chat.Draft, error) {
			if strings.HasPrefix(d.Text, "take") {
				took = append(took, d.Text)
				return d, chat.ErrTaken
			}
			return d, nil
		})
		return nil
	}}
	h, p, f, host := start(t, taker)
	p.NewChat("/work")
	p.draft = "take this"
	p.send()
	h.Flush()
	if p.draft != "" || len(f.Calls()) != 0 || len(host.Toasts) != 0 || len(p.t.rows) != 0 || p.sending || p.conv.Starting {
		t.Fatalf("draft %q, calls %q, toasts %q, rows %d, sending %v", p.draft, f.Calls(), host.Toasts, len(p.t.rows), p.sending)
	}
	if !slices.Equal(took, []string{"take this"}) {
		t.Fatalf("took %q", took)
	}
	p.draft = "send this"
	p.send()
	h.Flush()
	if got := f.Calls(); !slices.Equal(got, []string{"create /work", "send new send this"}) {
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
		{Kind: agent.ToolCalled, Session: "s1", Tool: &agent.Tool{ID: "t1", Name: "run_terminal_cmd", Kind: agent.ToolShell, Command: "ls"}},
		{Kind: agent.ToolFinished, Session: "s1", Tool: &agent.Tool{ID: "t1", Output: []string{"a\nb\n"}, Exit: 1}},
		{Kind: agent.ApprovalRequested, Session: "s1", Approval: &agent.Approval{ID: "ap", Kind: agent.ToolShell, Action: "run_terminal_cmd"}},
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
	approval := agent.Approval{ID: "ap", Kind: agent.ToolEdit, Action: "str_replace"}
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
		{Kind: agent.ApprovalRequested, Session: "s1", Approval: &agent.Approval{ID: "ap", Kind: agent.ToolShell, Action: "run_terminal_cmd", Resources: []string{"rm -rf build"}}},
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
			{Kind: agent.PartTool, Tool: &agent.Tool{Name: "fs_read_v2", Kind: agent.ToolRead, Output: []string{"1\n2\n3\n4\n5\n6\n7\n"}, Truncated: true}},
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

func TestFilesGoOutAndShowWithTheMessage(t *testing.T) {
	attach := &extension{setup: func(c *kernel.Context) error {
		kernel.Transform(c, chat.Outgoing, 0, func(d chat.Draft) (chat.Draft, error) {
			d.Files = append(d.Files, agent.File{Name: "shot.png", MIME: "image/png", Data: []byte{1}})
			return d, nil
		})
		return kernel.Contribute(c, chat.ComposerAttachments, "extension.chips", 0,
			shell.View{Render: func(c *ui.Context) { ui.Text(c, "pending chip") }})
	}}
	h, p, f, _ := start(t, attach)
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	tt.Frame()
	if !tt.HasText("pending chip") {
		t.Fatalf("attachments slot missing: %q", tt.Texts())
	}
	p.draft = "look"
	p.send()
	h.Flush()
	tt.Frame()
	if !slices.Contains(f.Calls(), "send s1 look +shot.png") || !tt.HasText("shot.png") {
		t.Fatalf("calls %q texts %q", f.Calls(), tt.Texts())
	}
	f.Fail["Send"] = fmt.Errorf("%w: Pi takes images and text files, not a.zip", agent.ErrNotSent)
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnSucceeded, Session: "s1"}})
	p.draft = "again"
	p.send()
	h.Flush()
	if last := p.t.rows[len(p.t.rows)-1]; last.text != "The message was not sent: Pi takes images and text files, not a.zip." {
		t.Fatalf("note %q", last.text)
	}
}

func TestPasteHandlersTakePastes(t *testing.T) {
	var seen []string
	taker := &extension{setup: func(c *kernel.Context) error {
		return kernel.Contribute(c, chat.Paste, "extension.paste", 0, func(text string) bool {
			seen = append(seen, text)
			return text == "take me"
		})
	}}
	h, p, _, _ := start(t, taker)
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	p.focus = true
	tt.Frame()
	for _, clip := range []string{"take me", "keep me"} {
		tt.SetClipboard(clip)
		tt.Key(ui.Cmd, ui.KeyV)
		tt.Frame()
	}
	if !slices.Equal(seen, []string{"take me", "keep me"}) || p.draft != "keep me" {
		t.Fatalf("seen %q draft %q", seen, p.draft)
	}
}

func TestHistoryLoadDoesNotReplaceLiveTurn(t *testing.T) {
	for _, finishBeforeLoad := range []bool{false, true} {
		t.Run(fmt.Sprint("finished=", finishBeforeLoad), func(t *testing.T) {
			h, p, f, _ := start(t)
			f.Stored["s1"] = []agent.Message{{Role: agent.RoleUser, Text: "history"}}
			p.Open(agent.Session{ID: "s1"})
			h.RunWork(0) // an older snapshot is waiting for the UI
			kernel.Emit(h, agent.Events, []agent.Event{
				{Kind: agent.TurnStarted, Session: "s1"},
				textEvent(agent.TextDelta, "s1", "live answer", ""),
			})
			finish := func() {
				f.Stored["s1"] = append(f.Stored["s1"], agent.Message{ID: "m", Role: agent.RoleAssistant,
					Parts: []agent.Part{{Kind: agent.PartText, Text: "live answer"}}})
				kernel.Emit(h, agent.Events, []agent.Event{
					textEvent(agent.TextEnded, "s1", "", "live answer"),
					{Kind: agent.TurnFailed, Session: "s1", Error: "turn error"},
				})
			}
			if finishBeforeLoad {
				finish()
			}
			h.RunPosts()
			if !finishBeforeLoad {
				if !p.running() || len(p.t.rows) != 1 || p.t.rows[0].text != "live answer" {
					t.Fatalf("load erased live turn: running=%v rows=%v", p.running(), kinds(p))
				}
				finish()
			}
			h.Flush()
			if p.running() || len(p.t.rows) != 3 || p.t.rows[0].text != "history" ||
				p.t.rows[1].text != "live answer" || p.t.rows[2].text != "The agent stopped with an error: turn error" {
				t.Fatalf("history did not reconcile after turn: running=%v rows=%v", p.running(), kinds(p))
			}
		})
	}
}

func TestCannotSendWhileHistoryIsLoading(t *testing.T) {
	h, p, f, _ := start(t)
	p.Open(agent.Session{ID: "s1"})
	p.draft = "wait for history"
	p.send()
	h.Flush()
	if f.Called("send") != 0 || p.draft != "wait for history" {
		t.Fatalf("sent during load: calls=%q draft=%q", f.Calls(), p.draft)
	}
}

func TestAChatLeftWhileStartingIsStillAnnouncedAndSent(t *testing.T) {
	h, p, f, _ := start(t)
	var started []chat.Conversation
	kernel.On(h, chat.Started, func(c chat.Conversation) { started = append(started, c) })
	p.NewChat("/work")
	p.draft = "hello"
	p.send()
	p.SetDirectory("/other") // the folder is fixed once the message is on its way
	if !p.conv.Starting || p.conv.Directory != "/work" {
		t.Fatalf("conversation %+v", p.conv)
	}
	p.NewChat("/next")
	h.Flush()
	if len(started) != 1 || started[0].Session != "new" || started[0].Directory != "/work" {
		t.Fatalf("started %+v", started)
	}
	if got := f.Calls(); !slices.Equal(got, []string{"create /work", "send new hello"}) {
		t.Fatalf("calls %q", got)
	}
	if p.conv.Session != "" || p.conv.Directory != "/next" || len(p.t.rows) != 0 {
		t.Fatalf("the left chat landed in the next one: %+v rows %v", p.conv, kinds(p))
	}
}

// openTurn opens a stored conversation of one turn: "hi", then an answer.
func openTurn(t *testing.T, h *kerneltest.Harness, p *Plugin, f *plugintest.Agent) {
	t.Helper()
	f.Stored["s1"] = []agent.Message{
		{ID: "u1", Role: agent.RoleUser, Text: "hi"},
		{ID: "a1", Role: agent.RoleAssistant, Parts: []agent.Part{{Kind: agent.PartText, Text: "Hello there"}}},
	}
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
}

func TestRetryTakesTheLastTurnBackAndSendsAgain(t *testing.T) {
	h, p, f, _ := start(t)
	openTurn(t, h, p, f)
	p.draft = "half typed"
	p.retry("hi")
	if !p.running() || !slices.Equal(kinds(p), []rowKind{rowUser}) || p.draft != "half typed" {
		t.Fatalf("after retry: running=%v rows=%v draft=%q", p.running(), kinds(p), p.draft)
	}
	h.Flush()
	if got := f.Calls()[1:]; !slices.Equal(got, []string{"messages s1", "rewind s1 u1", "send s1 hi"}) {
		t.Fatalf("calls %q", got)
	}
}

func TestEditReplacesTheLastMessage(t *testing.T) {
	h, p, f, _ := start(t)
	openTurn(t, h, p, f)
	tt := ui.NewTester(p.view, 800, 600)
	tt.Frame()
	hover(t, tt, "hi")
	for _, label := range []string{"Retry message", "Edit message"} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("no %q on the last message", label)
		}
	}
	p.edit(p.t.rows[p.t.lastUser()])
	tt.Frame()
	if p.draft != "hi" || !tt.HasText("Editing your last message. Sending replaces it and everything after it.") {
		t.Fatalf("draft %q, texts %q", p.draft, tt.Texts())
	}
	p.draft = "hello instead"
	p.send()
	h.Flush()
	if got := f.Calls()[1:]; !slices.Equal(got, []string{"messages s1", "rewind s1 u1", "send s1 hello instead"}) {
		t.Fatalf("calls %q", got)
	}
	if p.replacing || len(p.t.rows) != 1 || p.t.rows[0].text != "hello instead" {
		t.Fatalf("replacing=%v rows=%+v", p.replacing, p.t.rows)
	}
}

func TestCancelledEditSendsAnOrdinaryMessage(t *testing.T) {
	h, p, f, _ := start(t)
	openTurn(t, h, p, f)
	p.edit(p.t.rows[p.t.lastUser()])
	p.cancelEdit()
	p.draft = "more"
	p.send()
	h.Flush()
	if got := f.Calls()[1:]; !slices.Equal(got, []string{"send s1 more"}) {
		t.Fatalf("calls %q", got)
	}
}

func TestBackendThatForksGoesOnInTheNewConversation(t *testing.T) {
	h, p, f, host := start(t)
	f.Forks = true
	var started []chat.Conversation
	kernel.On(h, chat.Started, func(c chat.Conversation) { started = append(started, c) })
	openTurn(t, h, p, f)
	p.retry("hi")
	h.Flush()
	if p.conv.Session != "s1-fork" || len(started) != 1 || started[0].Session != "s1-fork" {
		t.Fatalf("conversation %+v, started %+v", p.conv, started)
	}
	if got := f.Calls()[1:]; !slices.Equal(got, []string{"messages s1", "rewind s1 u1", "send s1-fork hi"}) {
		t.Fatalf("calls %q", got)
	}
	if len(host.Toasts) == 0 || host.Toasts[0] != "Continued in a new conversation" {
		t.Fatalf("toasts %+v", host.Toasts)
	}
}

func TestFailedRewindSendsNothingAndReadsTheConversationAgain(t *testing.T) {
	h, p, f, _ := start(t)
	openTurn(t, h, p, f)
	f.Fail["Rewind"] = errors.New("server said no")
	p.retry("hi")
	h.Flush()
	if got := f.Calls()[1:]; !slices.Equal(got, []string{"messages s1", "rewind s1 u1", "messages s1"}) {
		t.Fatalf("calls %q", got)
	}
	if p.running() || !slices.Equal(kinds(p), []rowKind{rowUser, rowText, rowNote}) ||
		!strings.Contains(p.t.rows[2].text, "was not replaced: server said no") {
		t.Fatalf("running=%v rows=%v", p.running(), kinds(p))
	}
}

func TestNoRetryWhileATurnRuns(t *testing.T) {
	h, p, f, _ := start(t)
	openTurn(t, h, p, f)
	kernel.Emit(h, agent.Events, []agent.Event{{Kind: agent.TurnStarted, Session: "s1"}})
	p.retry("hi")
	h.Flush()
	if got := f.Calls()[1:]; len(got) != 0 {
		t.Fatalf("calls %q", got)
	}
	tt := ui.NewTester(p.view, 800, 600)
	tt.Frame()
	hover(t, tt, "hi")
	if _, ok := tt.Find("Copy message"); !ok {
		t.Fatal("the hover did not reveal the message's actions")
	}
	if _, ok := tt.Find("Retry message"); ok {
		t.Fatal("retry offered while a turn runs")
	}
}

// hover moves the pointer over the text, revealing the actions of its message.
func hover(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	b, ok := tt.Find(text)
	if !ok {
		t.Fatalf("no %q in %q", text, tt.Texts())
	}
	tt.Move(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame()
}

func TestToolKindFromItsStartCarriesToTheCall(t *testing.T) {
	var tr transcript
	tr.reset()
	tr.apply(agent.Event{Kind: agent.ToolStarted, Tool: &agent.Tool{ID: "t", Name: "str_replace", Kind: agent.ToolEdit}})
	tr.apply(agent.Event{Kind: agent.ToolCalled, Tool: &agent.Tool{ID: "t"}})
	if got := tr.rows[0].tool; got.label != "Edited files" || got.kind != agent.ToolEdit {
		t.Fatalf("called %+v", got)
	}
	tr.apply(agent.Event{Kind: agent.ToolFinished, Tool: &agent.Tool{ID: "t", Output: []string{"ok"}}})
	if got := tr.rows[0].tool; got.label != "Edited files" || got.kind != agent.ToolEdit {
		t.Fatalf("finished %+v", got)
	}
	if tr.apply(agent.Event{Kind: agent.ToolCalled, Tool: &agent.Tool{ID: "u", Name: "summon_llama"}}); tr.rows[1].tool.label != "Used summon_llama" {
		t.Fatalf("other %+v", tr.rows[1].tool)
	}
}

func TestFinishedTurnFoldsItsWorkAboveTheAnswer(t *testing.T) {
	var tr transcript
	tr.reset()
	tool := func(id string) *agent.Tool { return &agent.Tool{ID: id, Kind: agent.ToolRead} }
	tr.addUser("question", nil)
	tr.apply(agent.Event{Kind: agent.TurnStarted, Session: "s"})
	tr.apply(textEvent(agent.TextEnded, "s", "", "Let me look."))
	tr.apply(agent.Event{Kind: agent.ToolFinished, Session: "s", Tool: tool("a")})
	tr.apply(agent.Event{Kind: agent.ToolFinished, Session: "s", Tool: tool("b")})
	if items := tr.items(); len(items) != 4 || slices.ContainsFunc(items, func(it item) bool { return it.work != nil || it.answer }) {
		t.Fatalf("turn in flight folded: %+v", items)
	}
	tr.apply(agent.Event{Kind: agent.TextEnded, Session: "s", Text: &agent.TextEvent{Message: "m", Ordinal: 1, Text: "The answer."}})
	tr.apply(agent.Event{Kind: agent.TurnSucceeded, Session: "s"})
	items := tr.items()
	if len(items) != 3 || items[0].row.kind != rowUser || items[0].answer || len(items[1].work) != 3 ||
		items[2].row.text != "The answer." || !items[2].answer {
		t.Fatalf("items %+v", items)
	}
	if label := workLabel(items[1].work); label != "Worked · 2 steps" {
		t.Fatalf("label %q", label)
	}
}
