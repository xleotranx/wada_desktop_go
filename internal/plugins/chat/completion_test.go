package chat

import (
	"fmt"
	"slices"
	"testing"

	"wada-desktop-go/internal/contract/agent"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
)

// words is a contribution that offers a fixed list after "@", as a plugin
// would with the answers of its lookups.
type words struct {
	list     []chat.Candidate
	queries  []string
	accepted []string
}

func (*words) Manifest() kernel.Manifest { return kernel.Manifest{ID: "words"} }

func (w *words) Setup(c *kernel.Context) error {
	return kernel.Contribute(c, chat.Completions, "words.files", 0, chat.Completion{
		Trigger: '@',
		Query: func(typed string, active bool) {
			w.queries = append(w.queries, fmt.Sprintf("%s/%v", typed, active))
		},
		Candidates: func() []chat.Candidate { return w.list },
		Accept: func(c chat.Candidate) string {
			w.accepted = append(w.accepted, c.ID)
			return "@" + c.ID + " "
		},
	})
}

func startWords(t *testing.T) (*kerneltest.Harness, *Plugin, *plugintest.Agent, *words, *ui.Tester) {
	t.Helper()
	w := &words{list: []chat.Candidate{
		{ID: "src/main.go", Label: "main.go", Detail: "src"},
		{ID: "src/mail.go", Label: "mail.go", Detail: "src"},
		{ID: "docs/map.md", Label: "map.md", Detail: "docs"},
	}}
	h, p, f, _ := start(t, w)
	p.Open(agent.Session{ID: "s1"})
	h.Flush()
	tt := ui.NewTester(p.view, 1000, 800)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	p.focus = true
	tt.Frame()
	return h, p, f, w, tt
}

// typeAndSettle types, lets the frame tell the contribution, and draws again.
func typeAndSettle(h *kerneltest.Harness, tt *ui.Tester, s string) {
	tt.Type(s)
	tt.Frame()
	h.Flush()
	tt.Frame()
}

func TestTypingATriggerWordListsWhatTheContributionOffers(t *testing.T) {
	h, p, _, w, tt := startWords(t)
	typeAndSettle(h, tt, "fix @ma")
	if !slices.Equal(w.queries, []string{"ma/true"}) {
		t.Fatalf("queries %q", w.queries)
	}
	for _, want := range []string{"main.go", "mail.go", "map.md", "docs"} {
		if !tt.HasText(want) {
			t.Fatalf("%q missing from the list: %q", want, tt.Texts())
		}
	}
	if p.draft != "fix @ma" {
		t.Fatalf("draft %q", p.draft)
	}
	typeAndSettle(h, tt, "i")
	if !slices.Equal(w.queries, []string{"ma/true", "mai/true"}) {
		t.Fatalf("queries %q", w.queries)
	}
	typeAndSettle(h, tt, " ")
	if tt.HasText("mail.go") || w.queries[len(w.queries)-1] != "/false" {
		t.Fatalf("the list outlived the word: queries %q texts %q", w.queries, tt.Texts())
	}
}

func TestWordsThatDoNotStartWithTheTriggerOfferNothing(t *testing.T) {
	h, _, _, w, tt := startWords(t)
	typeAndSettle(h, tt, "mail me@exa")
	if tt.HasText("main.go") || len(w.queries) != 0 {
		t.Fatalf("an address opened the list: queries %q", w.queries)
	}
}

func TestArrowsMoveAndTabChooses(t *testing.T) {
	h, p, _, w, tt := startWords(t)
	typeAndSettle(h, tt, "fix @ma")
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if p.comp.active != 1 {
		t.Fatalf("active %d, want 1", p.comp.active)
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	h.Flush()
	tt.Frame()
	if p.draft != "fix @src/mail.go " || !slices.Equal(w.accepted, []string{"src/mail.go"}) {
		t.Fatalf("draft %q accepted %q", p.draft, w.accepted)
	}
	if tt.HasText("map.md") {
		t.Fatal("the list stays after a choice")
	}
	typeAndSettle(h, tt, "now")
	if p.draft != "fix @src/mail.go now" {
		t.Fatalf("the caret did not land after the choice: %q", p.draft)
	}
}

func TestEnterChoosesWhileTheListIsUpAndSendsOtherwise(t *testing.T) {
	h, p, f, _, tt := startWords(t)
	typeAndSettle(h, tt, "look @ma")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	h.Flush()
	tt.Frame()
	if p.draft != "look @src/main.go " || f.Called("send") != 0 || p.running() {
		t.Fatalf("Enter with a list: draft %q calls %q", p.draft, f.Calls())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	h.Flush()
	if !slices.Contains(f.Calls(), "send s1 look @src/main.go") {
		t.Fatalf("the second Enter did not send: %q", f.Calls())
	}
}

func TestEscapeHidesTheListUntilTheWordChanges(t *testing.T) {
	h, p, f, _, tt := startWords(t)
	typeAndSettle(h, tt, "@ma")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("mail.go") {
		t.Fatal("Escape left the list up")
	}
	typeAndSettle(h, tt, "i")
	if !tt.HasText("mail.go") {
		t.Fatal("typing did not bring the list back")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Key(0, ui.KeyEnter) // hidden: Enter sends what was typed
	tt.Frame()
	h.Flush()
	if !slices.Contains(f.Calls(), "send s1 @mai") || p.draft != "" {
		t.Fatalf("calls %q draft %q", f.Calls(), p.draft)
	}
}

func TestClickingASuggestionChoosesItAndKeepsTheEditorFocused(t *testing.T) {
	h, p, _, w, tt := startWords(t)
	typeAndSettle(h, tt, "see @ma")
	if err := tt.Click("map.md"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	tt.Frame()
	if p.draft != "see @docs/map.md " || !slices.Equal(w.accepted, []string{"docs/map.md"}) {
		t.Fatalf("draft %q accepted %q", p.draft, w.accepted)
	}
	typeAndSettle(h, tt, "ok")
	if p.draft != "see @docs/map.md ok" {
		t.Fatalf("the editor lost the focus or the caret: %q", p.draft)
	}
}

func TestAChoiceReplacesTheWholeWordAroundTheCaret(t *testing.T) {
	h, p, _, _, tt := startWords(t)
	typeAndSettle(h, tt, "x @ma tail")
	for range 5 {
		tt.Key(0, ui.KeyLeft)
	}
	tt.Frame()
	h.Flush()
	tt.Frame()
	if p.comp.typed != "ma" {
		t.Fatalf("typed %q, want the part before the caret", p.comp.typed)
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	h.Flush()
	tt.Frame()
	if p.draft != "x @src/main.go tail" {
		t.Fatalf("draft %q", p.draft)
	}
	typeAndSettle(h, tt, "|")
	if p.draft != "x @src/main.go |tail" {
		t.Fatalf("the caret is not after the inserted word: %q", p.draft)
	}
}

func TestTheSendButtonSendsEvenWithTheListUp(t *testing.T) {
	h, _, f, _, tt := startWords(t)
	typeAndSettle(h, tt, "go @ma")
	if err := tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	h.Flush()
	if !slices.Contains(f.Calls(), "send s1 go @ma") {
		t.Fatalf("calls %q", f.Calls())
	}
}

func TestAnEmptyListOffersNothingAndEnterSends(t *testing.T) {
	h, _, f, w, tt := startWords(t)
	w.list = nil
	typeAndSettle(h, tt, "ping @zz")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	h.Flush()
	if !slices.Contains(f.Calls(), "send s1 ping @zz") {
		t.Fatalf("calls %q", f.Calls())
	}
}

func TestAChoiceMadeAgainstAChangedDraftIsDropped(t *testing.T) {
	h, p, _, w, tt := startWords(t)
	typeAndSettle(h, tt, "a @ma")
	p.draft = "a plain" // the draft changed before the posted choice ran
	p.choose(w.list[0])
	if p.draft != "a plain" || len(w.accepted) != 0 {
		t.Fatalf("draft %q accepted %q", p.draft, w.accepted)
	}
}

func TestAChoiceIsTheCandidateChosenNotItsPlaceInTheList(t *testing.T) {
	h, p, _, w, tt := startWords(t)
	typeAndSettle(h, tt, "see @ma")
	p.chooseSoon(w.list[1]) // src/mail.go
	// A lookup lands before the choice is applied and reorders the list.
	w.list = []chat.Candidate{w.list[2], w.list[0], w.list[1]}
	h.Flush()
	if p.draft != "see @src/mail.go " || !slices.Equal(w.accepted, []string{"src/mail.go"}) {
		t.Fatalf("draft %q accepted %q", p.draft, w.accepted)
	}
}

func TestWordAt(t *testing.T) {
	for _, tc := range []struct {
		text        string
		caret       int
		start, end  int
		description string
	}{
		{"fix @ma", 7, 4, 7, "caret at the end"},
		{"fix @main now", 7, 4, 9, "caret inside a word: the word runs on"},
		{"fix ", 4, 4, 4, "after a space"},
		{"", 0, 0, 0, "empty"},
		{"a\n@b", 4, 2, 4, "a new line starts a word"},
	} {
		s, e := wordAt([]rune(tc.text), tc.caret)
		if s != tc.start || e != tc.end {
			t.Errorf("%s: %q at %d is [%d,%d), want [%d,%d)", tc.description, tc.text, tc.caret, s, e, tc.start, tc.end)
		}
	}
}
