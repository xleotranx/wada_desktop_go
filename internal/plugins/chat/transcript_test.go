package chat

import (
	"testing"

	"wada-desktop-go/internal/contract/agent"
)

// The folded items are built again only when what folding reads changes:
// words streaming into a row reuse them, a row added, a row changing kind,
// a row removed or the turn settling builds them anew.
func TestItemsAreReusedUntilTheShapeChanges(t *testing.T) {
	var tr transcript
	tr.reset()
	tr.addUser("go", nil)
	tr.apply(agent.Event{Kind: agent.TurnStarted})
	tr.apply(agent.Event{Kind: agent.ToolCalled, Tool: &agent.Tool{ID: "t1", Kind: agent.ToolShell, Command: "ls"}})
	tr.apply(textEvent(agent.TextDelta, "", "Here", ""))
	first := tr.items()
	same := func(a, b []item) bool { return len(a) == len(b) && len(a) > 0 && &a[0] == &b[0] }

	tr.apply(textEvent(agent.TextDelta, "", " it is", ""))
	if got := tr.items(); !same(got, first) || got[len(got)-1].row.text != "Here it is" {
		t.Fatal("a streamed word built the items again, or was not seen")
	}
	if n := testing.AllocsPerRun(50, func() { tr.items() }); n != 0 {
		t.Fatalf("reading unchanged items allocates %v times", n)
	}
	steps := []struct {
		name   string
		change func()
		folded bool // the turn's work is folded after it
	}{
		{"the words end", func() { tr.apply(textEvent(agent.TextEnded, "", "", "Here it is")) }, false},
		{"the turn settles", func() { tr.apply(agent.Event{Kind: agent.TurnSucceeded}) }, true},
		{"a note is added", func() { tr.note("noted") }, true},
		{"the turn is dropped", func() { tr.dropLastTurn() }, false},
	}
	prev := tr.items()
	for _, s := range steps {
		s.change()
		got := tr.items()
		if same(got, prev) && len(got) == len(prev) {
			t.Fatalf("%s: the items were not built again", s.name)
		}
		folded := false
		for _, it := range got {
			folded = folded || it.work != nil
		}
		if folded != s.folded {
			t.Fatalf("%s: folded %v, want %v", s.name, folded, s.folded)
		}
		fresh := tr.fold()
		if len(fresh) != len(got) {
			t.Fatalf("%s: %d items kept, %d built afresh", s.name, len(got), len(fresh))
		}
		for i := range fresh {
			if fresh[i].key != got[i].key || fresh[i].row != got[i].row || len(fresh[i].work) != len(got[i].work) {
				t.Fatalf("%s: item %d differs from one built afresh", s.name, i)
			}
		}
		prev = got
	}
}
