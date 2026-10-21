package desktop

import (
	"testing"
	"time"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// TestBlockKindsRender mounts one row of every kind: blockRow's switch has
// no compiler exhaustiveness check, so this table is the spec that keeps a
// new kind from quietly falling through to the plain-text default. Text
// the row must show raw catches a kind routed through the markdown
// package, and vice versa.
func TestBlockKindsRender(t *testing.T) {
	cases := []struct {
		name     string
		block    chat.Block
		drawn    string   // a text the row must show
		notDrawn []string // texts that would mean the row went its wrong way
	}{
		{name: "text", block: chat.Block{Kind: chat.KindText, Text: "a **bold** answer"},
			drawn: "a bold answer", notDrawn: []string{"**"}},
		{name: "user", block: chat.Block{Kind: chat.KindUser, Text: "**ship it**"},
			drawn: "**ship it**"},
		{name: "stream", block: chat.Block{Kind: chat.KindStream, Text: "half a **thought**", Key: "stream:m1:0"},
			drawn: "half a thought", notDrawn: []string{"**"}},
		{name: "note", block: chat.Block{Kind: chat.KindNote, Text: "# engine trouble"},
			drawn: "# engine trouble"},
		{name: "working", block: chat.Block{Kind: chat.KindWorking, Text: "Working…"},
			drawn: "Working…"},
		{name: "activity", block: chat.Block{Kind: chat.KindActivity, Text: "Ran command", Icon: "shell",
			Key: "activity:call_1", Detail: []string{"$ echo hi", "hi"}},
			drawn: "Ran command"},
		{name: "permission", block: chat.Block{Kind: chat.KindPermission, Text: "Approval needed",
			Key: "permission:per_1", Request: &chat.Permission{ID: "per_1", Session: "ses_z", Action: "shell"}},
			drawn: "run a shell command"},
	}
	if len(cases) != 7 {
		t.Fatalf("the table covers every block kind; a new kind joins it: %d of 7", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			a.conversation.target.selectSession("ses_z")
			seedTranscript(a, []chat.Block{tc.block})
			tt := ui.NewTester(a.view, 1400, 920)
			tt.Advance(time.Second) // streaming words pace in
			if !tt.HasText(tc.drawn) {
				t.Fatalf("a %s row draws %q nowhere: %v", tc.name, tc.drawn, tt.Texts())
			}
			for _, no := range tc.notDrawn {
				if tt.HasText(no) {
					t.Fatalf("a %s row drew %q", tc.name, no)
				}
			}
		})
	}
}
