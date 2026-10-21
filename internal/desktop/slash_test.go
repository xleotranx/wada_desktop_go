package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
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
	if !codingModule(a).QuickOpen {
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
	if codingModule(a).QuickOpen {
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
	if !codingModule(a).QuickOpen || a.conversation.draft != "" {
		t.Fatalf("choosing /files: quick=%v draft=%q",
			codingModule(a).QuickOpen, a.conversation.draft)
	}

	codingModule(a).QuickOpen = false
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
	if codingModule(a).QuickOpen {
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

// TestSlashMenuArrowsChoose picks the second prefix match with Down and
// runs it with Enter.
func TestSlashMenuArrowsChoose(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if err := tt.Click("Compose"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/f")
	tt.Frame()
	if a.layout.slashSelected != 0 {
		t.Fatalf("typing /f selected %d", a.layout.slashSelected)
	}
	tt.Key(0, ui.KeyDown)
	if a.layout.slashSelected != 1 {
		t.Fatalf("Down selected %d", a.layout.slashSelected)
	}
	tt.Key(0, ui.KeyEnter)
	if a.conversation.draft != "" || !a.layout.fullView {
		t.Fatalf("Enter on /full: draft=%q full=%v", a.conversation.draft, a.layout.fullView)
	}
	if len(a.conversation.transcript.Snapshot()) != 0 {
		t.Fatal("a slash command entered the transcript")
	}
}

// TestSlashPrefixEnterRunsFirst lets Enter run the first prefix match
// instead of sending a bare "/f" prompt.
func TestSlashPrefixEnterRunsFirst(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_a")
	a.conversation.draft = "/f"
	if !a.send() {
		t.Fatal("the prefix was not accepted")
	}
	if !codingModule(a).QuickOpen {
		t.Fatal("Enter on /f did not run /files")
	}
	if a.conversation.transcript.Len() != 0 {
		t.Fatal("the prefix entered the transcript")
	}
}
