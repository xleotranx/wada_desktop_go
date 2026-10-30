package design

import (
	"os"
	"path/filepath"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

func TestOverlappingDesignStartsKeepTheirOwnFolders(t *testing.T) {
	for _, firstFinishesFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "in order", false: "out of order"}[firstFinishesFirst], func(t *testing.T) {
			f := start(t)
			f.p.newDesign()
			f.h.Flush()
			first, firstDir := f.chat.Conv, f.p.folder
			f.send(chat.Draft{DraftID: first.DraftID, Directory: f.dir, Text: "First design"})
			f.chat.Conv.Starting = true
			f.p.newDesign()
			f.h.Flush()
			second, secondDir := f.chat.Conv, f.p.folder
			f.send(chat.Draft{DraftID: second.DraftID, Directory: f.dir, Text: "Second design"})
			first.Session, second.Session = "first", "second"
			finishFirst := func() { kernel.Emit(f.h, chat.Started, first) }
			finishSecond := func() {
				f.chat.Conv = second
				kernel.Emit(f.h, chat.Changed, second)
				kernel.Emit(f.h, chat.Started, second)
			}
			if firstFinishesFirst {
				finishFirst()
				finishSecond()
			} else {
				finishSecond()
				finishFirst()
			}
			for session, dir := range map[agent.SessionID]string{"first": firstDir, "second": secondDir} {
				i := f.p.designIndex(session)
				if i < 0 || f.p.designs[i].Dir != dir {
					t.Fatalf("%s should own %s; designs %+v", session, dir, f.p.designs)
				}
			}
		})
	}
}

func TestAnotherChatsCompletionDoesNotTakeAPendingDesign(t *testing.T) {
	f := start(t)
	f.p.newDesign()
	f.h.Flush()
	first, dir := f.chat.Conv, f.p.folder
	f.send(chat.Draft{DraftID: first.DraftID, Directory: f.dir, Text: "A design"})
	f.chat.Conv.Starting = true
	f.chat.NewChat(f.dir)
	plain := f.chat.Conv
	plain.Session = "plain"
	f.chat.Conv = plain
	kernel.Emit(f.h, chat.Changed, plain)
	kernel.Emit(f.h, chat.Started, plain)
	if f.p.designIndex("plain") >= 0 {
		t.Fatal("an unrelated chat became a design")
	}
	first.Session = "design"
	kernel.Emit(f.h, chat.Started, first)
	if i := f.p.designIndex("design"); i < 0 || f.p.designs[i].Dir != dir {
		t.Fatalf("the original design was lost: %+v", f.p.designs)
	}
}

func TestARefreshDoesNotDiscardNewArtboardCreation(t *testing.T) {
	f := start(t)
	f.open(t)
	f.p.newArtboard()
	f.p.scan() // Reload or an agent event arrives before creation finishes.
	f.h.Flush()
	path := filepath.Join(f.dir, "artboard-1.html")
	if f.p.selected != path || f.chat.Draft != "Design artboard-1.html: \n\n" || f.p.boards[path] == nil {
		t.Fatalf("selected %q, draft %q, boards %v", f.p.selected, f.chat.Draft, f.p.boards)
	}
}

func TestNewArtboardCompletionDoesNotChangeAnotherConversation(t *testing.T) {
	f := start(t)
	f.open(t)
	f.p.newArtboard()
	f.chat.Open(agent.Session{ID: "plain", Directory: f.dir})
	f.chat.SetDraft("Keep this draft")
	f.h.Flush()
	if f.chat.Draft != "Keep this draft" || f.p.open || f.p.selected != "" {
		t.Fatalf("draft %q, canvas %v, selected %q", f.chat.Draft, f.p.open, f.p.selected)
	}
}

func TestTheEmptyCanvasCanCreateItsFirstArtboard(t *testing.T) {
	f := start(t)
	f.open(t)
	tt := ui.NewTester(f.p.drawer, 1000, 800)
	tt.Frame()
	if err := tt.Click("New artboard"); err != nil {
		t.Fatal(err)
	}
	f.h.Flush()
	tt.Frame()
	if _, err := os.Stat(filepath.Join(f.dir, "artboard-1.html")); err != nil {
		t.Fatal(err)
	}
	if _, ok := tt.Find("Select artboard-1"); !ok {
		t.Fatal("the first artboard did not appear")
	}
}
