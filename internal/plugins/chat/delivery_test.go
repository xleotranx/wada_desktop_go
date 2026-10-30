package chat

import (
	"errors"
	"testing"

	"github.com/xleotranx/wada-kit/kernel"
	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
)

type testDelivery struct {
	err    error
	drafts []chat.Draft
	busy   bool
}

func (d *testDelivery) New() kernel.Plugin { return d }
func (*testDelivery) Manifest() kernel.Manifest {
	return kernel.Manifest{ID: "test-delivery", Provides: []kernel.Named{chat.DeliveryKey}}
}
func (d *testDelivery) Setup(c *kernel.Context) error {
	return kernel.Provide[chat.Delivery](c, chat.DeliveryKey, d)
}
func (d *testDelivery) Validate(chat.Draft) error    { return d.err }
func (d *testDelivery) Admit(draft chat.Draft) error { d.drafts = append(d.drafts, draft); return nil }
func (d *testDelivery) Busy(agent.SessionID) bool    { return d.busy }
func (*testDelivery) Stop(agent.SessionID)           {}

func TestDeliveryValidationPreservesDraftAndPreparation(t *testing.T) {
	delivery := &testDelivery{err: errors.New("storage loading")}
	committed := false
	preparation := &extension{setup: func(c *kernel.Context) error {
		kernel.Transform(c, chat.Outgoing, 0, func(d chat.Draft) (chat.Draft, func(), error) {
			d.Workflow = "selected"
			return d, func() { committed = true }, nil
		})
		return nil
	}}
	h, p, a, _ := start(t, delivery.New, preparation.New)
	p.draft = "implement"
	p.send()
	h.Flush()
	if p.draft != "implement" || committed || len(p.t.rows) > 0 || a.Called("create") > 0 {
		t.Fatalf("failed admission consumed draft: %q committed=%v", p.draft, committed)
	}
}
func TestQueuedMessagesKeepStableAnchorsAndCanBeRemoved(t *testing.T) {
	delivery := &testDelivery{}
	h, p, a, _ := start(t, delivery.New)
	a.Stored["main"] = []agent.Message{{ID: "first", Role: agent.RoleUser, Text: "initial task"}}
	p.Open(agent.Session{ID: "main"})
	h.Flush()
	delivery.busy = true
	p.draft = "why?"
	p.send()
	if len(delivery.drafts) != 1 || delivery.drafts[0].Anchor != 2 {
		t.Fatalf("admissions %+v", delivery.drafts)
	}
	d := delivery.drafts[0]
	kernel.Emit(h, chat.UserAdmitted, d)
	if len(p.t.rows) != 2 {
		t.Fatalf("user message duplicated: %+v", p.t.rows)
	}
	kernel.Emit(h, chat.QueueRemoved, d)
	if len(p.t.rows) != 1 || p.t.rows[0].text != "initial task" {
		t.Fatal("removing queue did not reconcile transcript")
	}
}
func TestExternalReviewerRequestsSurviveHistoryReload(t *testing.T) {
	h, p, _, _ := start(t)
	p.Open(agent.Session{ID: "main"})
	h.Flush()
	request := agent.Interaction{ID: "review-question", Session: "child", Kind: agent.Confirm, Title: "Reviewer decision"}
	kernel.Emit(h, chat.Incoming, []agent.Event{{Kind: agent.InteractionRequested, Session: "main", Interaction: &request}})
	approval := agent.Approval{ID: "review-approval", Session: "child", Kind: agent.ToolShell}
	kernel.Emit(h, chat.Incoming, []agent.Event{{Kind: agent.ApprovalRequested, Session: "main", Approval: &approval}})
	p.load()
	h.Flush()
	var questions, approvals int
	for _, row := range p.t.rows {
		if row.interaction != nil && row.interaction.request.Session == "child" {
			questions++
		}
		if row.approval != nil && row.approval.Session == "child" {
			approvals++
		}
	}
	if questions != 1 || approvals != 1 {
		t.Fatalf("external requests lost: questions=%d approvals=%d", questions, approvals)
	}
}

func TestInitialReadRecoversWhenSameConnectionBecomesReady(t *testing.T) {
	h, p, a, _ := start(t)
	a.Fail["Messages"] = agent.ErrNotConnected
	p.Open(agent.Session{ID: "main"})
	h.Flush()
	if p.t.loaded {
		t.Fatal("failed read marked loaded")
	}
	delete(a.Fail, "Messages")
	a.Stored["main"] = []agent.Message{{ID: "first", Role: agent.RoleUser, Text: "restored task"}}
	kernel.Emit(h, agent.StatusChanged, a.State)
	h.Flush()
	if !p.t.loaded || len(p.t.rows) != 1 || p.t.rows[0].text != "restored task" {
		t.Fatal("first ready event did not restore transcript")
	}
}

func TestReviewerApprovalReplySurvivesNavigation(t *testing.T) {
	h, p, a, _ := start(t)
	p.Open(agent.Session{ID: "main"})
	h.Flush()
	approval := agent.Approval{Session: "reviewer", ID: "approval", Kind: agent.ToolShell}
	kernel.Emit(h, chat.Incoming, []agent.Event{{Session: "main", Kind: agent.ApprovalRequested, Approval: &approval}})
	var answered chat.Answered
	kernel.On(h, chat.RequestAnswered, func(value chat.Answered) { answered = value })
	p.reply(approval, agent.Once)
	p.NewChat("/elsewhere")
	h.Flush()
	replies := a.Replies()
	if len(replies) != 1 || replies[0].Session != "reviewer" || answered.Session != "reviewer" {
		t.Fatalf("reply=%+v answered=%+v", replies, answered)
	}
	if kept := p.kept.find("main"); kept != nil {
		for _, r := range kept.t.rows {
			if r.approval != nil {
				t.Fatal("answered reviewer approval replayed after navigation")
			}
		}
	}
}
