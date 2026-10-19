package main

import (
	"strings"
	"testing"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestModePillBirthsPlanChats keeps the persona pick honest: the pill is
// there when the server names personas and gone when it names none, the
// pick lands on what is born, and a persona the server no longer offers
// stands down.
func TestModePillBirthsPlanChats(t *testing.T) {
	a := newTestApp(t)
	a.scope().agents = []opencode.Agent{
		{ID: "build", Name: "Build", Description: "does the work"},
		{ID: "plan", Name: "Plan", Description: "read-only"},
	}
	tt := ui.NewTester(a.view, 1400, 920)

	// The pill opens with the server's opening persona and the picker
	// carries every chat pick in the server's order.
	if err := tt.Click("Mode Build"); err != nil {
		t.Fatalf("the mode pill did not open: %v", err)
	}
	menu := tt.Menu()
	if len(menu) != 2 || !strings.Contains(menu[0], "Build") || !strings.Contains(menu[1], "Plan") {
		t.Fatalf("the mode menu ordered the picks wrong: %v", menu)
	}

	// Picking Plan births the chat under it: the send admit carries the
	// persona the draft holds.
	if err := tt.ChooseMenuItem("Plan"); err != nil {
		t.Fatalf("choosing the persona: %v", err)
	}
	tt.Frame()
	a.conversation.target.selectSession("ses-mode")
	a.conversation.selected = "Mode pick"
	seedTranscript(a, []chat.Block{})
	a.conversation.draft = "Say nothing, plan everything"
	tt.Frame()
	if err := tt.Click("Send message"); err != nil {
		t.Fatalf("the plan pick could not send: %v", err)
	}
	job := <-commandsOf(a)
	if job.agent != "plan" || job.text != "Say nothing, plan everything" {
		t.Fatalf("the plan pick did not ride the send: %+v", job)
	}

	// A persona the server dropped stands down to the default.
	a.scope().agents = []opencode.Agent{{ID: "build", Name: "Build"}}
	a.settleAgents()
	if a.conversation.agent != "" {
		t.Fatalf("a dropped persona survived: %q", a.conversation.agent)
	}

	// A server that names no personas shows no pill.
	a.scope().agents = nil
	tt.Frame()
	if tt.HasText("Mode Build") || tt.HasText("Mode Plan") {
		t.Fatalf("the mode pill stayed without a catalog: %v", tt.Texts())
	}
}

// TestModePickKeepsDroppedPersonaNames keeps the pill's title true to
// what the catalog names, defaulting to the server's opening persona.
func TestModePickKeepsDroppedPersonaNames(t *testing.T) {
	agents := []opencode.Agent{
		{ID: "build", Name: "Build"},
		{ID: "plan", Name: "Plan"},
	}
	if got := agentTitle(agents, "plan"); got != "Plan" {
		t.Fatalf("agentTitle(plan) = %q", got)
	}
	if got := agentTitle(agents, ""); got != "Build" {
		t.Fatalf("agentTitle(default) = %q", got)
	}
	if got := agentTitle([]opencode.Agent{{ID: "help", Name: "Help"}}, ""); got != "Help" {
		t.Fatalf("agentTitle(no build) = %q", got)
	}
}
