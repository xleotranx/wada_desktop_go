package main

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

// TestSlashCommandRunsLocally keeps a command out of the transcript and
// the runner, and runs the action it names.
func TestSlashCommandRunsLocally(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_a")
	a.conversation.draft = "/files"
	if !a.send() {
		t.Fatal("the slash command was not accepted")
	}
	if !a.layout.quickOpen {
		t.Fatal("/files did not open the quick picker")
	}
	if a.conversation.draft != "" || a.conversation.transcript.Len() != 0 {
		t.Fatalf("command left draft=%q blocks=%d",
			a.conversation.draft, a.conversation.transcript.Len())
	}
	if jobs := commandsOf(a); len(jobs) != 0 {
		t.Fatalf("command sent %d runner jobs", len(jobs))
	}
}

// TestUnknownSlashStillPrompts leaves a slash word that is not a command
// to the agent.
func TestUnknownSlashStillPrompts(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_a")
	a.conversation.draft = "/not-a-command"
	if !a.send() {
		t.Fatal("the prompt was not accepted")
	}
	if a.layout.quickOpen {
		t.Fatal("an unknown command ran something")
	}
	if a.conversation.transcript.Len() != 1 || a.conversation.transcript.Row(0).Text != "/not-a-command" {
		t.Fatalf("unknown slash transcript %+v", a.conversation.transcript.Snapshot())
	}
	jobs := commandsOf(a)
	if len(jobs) != 1 {
		t.Fatalf("unknown slash sent %d jobs", len(jobs))
	}
	if job := <-jobs; job.kind != "prompt" {
		t.Fatalf("unknown slash job %+v", job)
	}
}

// TestSlashSuggestionsAndHelp lists prefix matches and opens /help.
func TestSlashSuggestionsAndHelp(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_a")
	a.conversation.draft = "/f"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Run /files") || !tt.HasText("Run /full") {
		t.Fatalf("the slash suggestions are missing: %v", tt.Texts())
	}
	if err := tt.Click("Run /files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.layout.quickOpen || a.conversation.draft != "" {
		t.Fatalf("choosing /files: quick=%v draft=%q",
			a.layout.quickOpen, a.conversation.draft)
	}

	a.layout.quickOpen = false
	a.conversation.draft = "/help"
	if !a.send() {
		t.Fatal("/help was not accepted")
	}
	tt.Frame()
	if !a.layout.slashHelp || !tt.HasText("/terminal") {
		t.Fatalf("the help sheet is missing: open=%v texts=%v", a.layout.slashHelp, tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.layout.slashHelp {
		t.Fatal("Escape did not close the slash help")
	}
}

// TestSlashCommandWithWordsGoesToTheAgent only treats an exact command as
// local.
func TestSlashCommandWithWordsGoesToTheAgent(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_a")
	a.conversation.draft = "/files explain this"
	if !a.send() {
		t.Fatal("the prompt was not accepted")
	}
	if a.layout.quickOpen {
		t.Fatal("a command with arguments ran locally")
	}
	jobs := commandsOf(a)
	if len(jobs) != 1 {
		t.Fatalf("arguments sent %d jobs", len(jobs))
	}
	if job := <-jobs; job.kind != "prompt" {
		t.Fatalf("arguments job %+v", job)
	}
}
