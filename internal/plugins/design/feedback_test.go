package design

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/contract/workspace"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

func TestSavedFeedbackSurvivesPluginRestartAndDeletion(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.addNote(20, 30)
	f.p.text = "Try a quieter header"
	f.p.saveCard()
	if err := f.h.Disable("design"); err != nil {
		t.Fatal(err)
	}
	if err := f.h.Enable("design"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	if len(f.p.marks) != 1 {
		t.Fatalf("saved feedback lost: %+v", f.p.marks)
	}
	f.p.deleteMark(f.p.marks[0].ID)
	if err := f.h.Disable("design"); err != nil {
		t.Fatal(err)
	}
	if err := f.h.Enable("design"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	if len(f.p.marks) != 0 {
		t.Fatal("deleted feedback returned")
	}
}

func TestAddingFeedbackToDraftKeepsItsPinWithoutDuplicatingIt(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.addNote(20, 30)
	f.p.text = "More space"
	f.p.saveCard()
	f.p.sendMarks()
	draft := f.chat.Draft
	if len(f.p.marks) != 1 {
		t.Fatal("adding feedback to draft removed its pin")
	}
	f.p.sendMarks()
	if f.chat.Draft != draft {
		t.Fatal("feedback was added twice")
	}
	f.chat.Open(agent.Session{ID: "plain", Directory: f.dir})
	f.chat.Open(agent.Session{ID: "s", Directory: f.dir})
	if len(f.p.marks) != 1 {
		t.Fatal("feedback lost on navigation")
	}
}

func TestResolvedFeedbackIsNotAddedAndCanBeReopened(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.addNote(20, 30)
	f.p.text = "More space"
	f.p.saveCard()
	id := f.p.marks[0].ID
	f.p.resolveMark(id)
	f.p.sendMarks()
	if f.chat.Draft != "" || !f.p.marks[0].Resolved {
		t.Fatal("resolved feedback was added")
	}
	f.p.resolveMark(id)
	f.p.sendMarks()
	if f.chat.Draft == "" || !f.p.marks[0].Added {
		t.Fatal("reopened feedback was not added")
	}
}

func TestFeedbackIsIsolatedByBackendOrigin(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.addNote(20, 30)
	f.p.text = "Original feedback"
	f.p.saveCard()
	f.p.remember(design{Origin: "other", Session: "s", Dir: f.dir})
	f.agent.State.Origin = "other"
	kernel.Emit(f.h, agent.StatusChanged, f.agent.State)
	if len(f.p.marks) != 0 {
		t.Fatal("feedback crossed backend origins")
	}
	f.p.addNote(20, 30)
	f.p.text = "Other feedback"
	f.p.saveCard()
	f.agent.State.Origin = "fake"
	kernel.Emit(f.h, agent.StatusChanged, f.agent.State)
	if len(f.p.marks) != 1 || f.p.marks[0].Words != "Original feedback" {
		t.Fatal("original feedback was replaced")
	}
}

func TestFeedbackCanBeAddedAgainAfterDiscardingTheDraft(t *testing.T) {
	f := start(t, "a.html")
	f.open(t)
	f.p.addNote(20, 30)
	f.p.text = "More space"
	f.p.saveCard()
	f.p.sendMarks()
	draft := f.chat.Draft
	f.chat.SetDraft("")
	f.p.edit(f.p.marks[0].ID)
	tt := ui.NewTester(f.p.drawer, 1000, 800)
	tt.Frame()
	if err := tt.Click("Add to message again"); err != nil {
		t.Fatal(err)
	}
	if f.chat.Draft != draft {
		t.Fatalf("feedback not recovered: %q", f.chat.Draft)
	}
}

func TestPendingDesignKeepsFeedbackWhileItsFirstMessageStarts(t *testing.T) {
	f := start(t)
	f.p.newDesign()
	f.h.Flush()
	conv := f.chat.Conv
	folder := f.p.folder
	f.p.addNote(20, 30)
	f.p.text = "Keep this direction"
	f.p.saveCard()
	f.send(chat.Draft{DraftID: conv.DraftID, Directory: f.dir, Text: "Start"})
	f.chat.Conv.Starting = true
	f.chat.NewChat(f.dir)
	kernel.Emit(f.h, workspace.Changed, struct{}{})
	if len(f.p.feedback) != 1 || f.p.feedback[0].Folder != folder {
		t.Fatal("pending design feedback was forgotten")
	}
}
