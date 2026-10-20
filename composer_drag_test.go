package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestFileRowDragsAMentionIntoTheComposer drags a file from the Files
// tree onto the composer and expects its path as an @mention.
func TestFileRowDragsAMentionIntoTheComposer(t *testing.T) {
	a := newTestApp(t)
	a.openTool("Files")
	a.workspace.files = &filesState{
		location: t.TempDir(),
		open:     map[string]bool{"src": true},
		entries: map[string][]opencode.FileEntry{
			".":   {{Path: "src", Name: "src", Directory: true}},
			"src": {{Path: "src/app.go", Name: "app.go"}},
		},
		loading: map[string]bool{},
	}
	a.conversation.draft = "check this:"
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	src, ok := tt.Find("Read app.go")
	if !ok {
		t.Fatal("the file row is not drawn")
	}
	dst, ok := tt.Find("Compose")
	if !ok {
		t.Fatal("the composer is not drawn")
	}
	tt.Press(src.X+src.W/2, src.Y+src.H/2)
	tt.Move(src.X+src.W/2+12, src.Y+src.H/2)
	tt.Move(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Release(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Frame()
	if a.conversation.draft != "check this: @src/app.go " {
		t.Fatalf("the drop wrote %q", a.conversation.draft)
	}
	if a.mentionOpen() {
		t.Fatal("the completed mention reopened its menu")
	}
}

// TestInsertFileMentionStartsItsWord keeps a mention off the end of a
// word and on a fresh line as it finds it.
func TestInsertFileMentionStartsItsWord(t *testing.T) {
	a := newTestApp(t)
	a.conversation.draft = "line\n"
	a.insertFileMention("./src/app.go")
	if a.conversation.draft != "line\n@src/app.go " {
		t.Fatalf("a mention after a line reads %q", a.conversation.draft)
	}
	a.conversation.draft = ""
	a.insertFileMention("src/app.go")
	if a.conversation.draft != "@src/app.go " {
		t.Fatalf("a mention in an empty draft reads %q", a.conversation.draft)
	}
}
