package voice

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
	"github.com/xleotranx/wada-kit/kernel/kerneltest"
	"github.com/xleotranx/wada-kit/kittest"
	"wada-desktop-go/internal/contract/chat"
	"wada-desktop-go/internal/plugintest"
)

// recognizer scripts the dictation page: what it hears, one state per look.
type recognizer struct {
	mu      sync.Mutex
	states  []map[string]any
	started int
	stopped int
	on      bool // listening, between a start and a stop
	start   string // what voice.start() returns
}

func (r *recognizer) eval(_ context.Context, code string) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case strings.Contains(code, "voice.start()"):
		r.started++
		r.on = r.start == ""
		return r.start, nil
	case strings.Contains(code, "voice.stop()"):
		r.stopped++
		r.on = false
		return nil, nil
	case strings.Contains(code, "voice.state()"):
		if len(r.states) == 0 {
			// Listening until asked to stop, as a recognizer is.
			return map[string]any{"text": "", "listening": r.on, "error": ""}, nil
		}
		st := r.states[0]
		r.states = r.states[1:]
		return st, nil
	}
	return nil, nil
}

func waitCond(t *testing.T, h *kerneltest.Harness, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		h.Flush()
		time.Sleep(time.Millisecond)
	}
}

func setup(t *testing.T, rec *recognizer) (*kerneltest.Harness, *Plugin, *plugintest.Chat, *kittest.Host, *kittest.WebView) {
	view := &kittest.WebView{EvalFunc: rec.eval}
	host := &kittest.Host{Data: t.TempDir(), Web: func(int) *kittest.WebView { return view }}
	ch := &plugintest.Chat{Conv: chat.Conversation{Session: "s1"}, Draft: "Fix the"}
	var p *Plugin
	h := kerneltest.New(t, host.New, ch.New, kerneltest.Keep(&p, func() *Plugin { return &Plugin{} }))
	return h, p, ch, host, view
}

func TestDictationFillsTheDraft(t *testing.T) {
	rec := &recognizer{states: []map[string]any{
		{"text": "login", "listening": true, "error": ""},
		{"text": "login bug please", "listening": true, "error": ""},
		{"text": "login bug please", "listening": false, "error": ""},
	}}
	h, p, ch, _, view := setup(t, rec)
	p.toggle()
	if !p.listening {
		t.Fatal("not listening")
	}
	tt := ui.NewTester(p.button, 40, 40)
	tt.Frame()
	if !tt.HasText("Stop dictating") {
		t.Fatalf("button = %q", tt.Texts())
	}
	waitCond(t, h, "dictation ends", func() bool { return !p.listening })
	if ch.Draft != "Fix the login bug please" {
		t.Fatalf("draft = %q", ch.Draft)
	}
	if len(view.Loaded) != 1 || !strings.HasSuffix(view.Loaded[0], "voice/dictation.html") || rec.started != 1 {
		t.Fatalf("loaded %q, started %d", view.Loaded, rec.started)
	}

	// Again, stopped by the user: the page is loaded already.
	p.toggle()
	p.toggle()
	waitCond(t, h, "dictation stops", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.started == 2 && rec.stopped == 1 && !p.listening
	})
	if len(view.Loaded) != 1 {
		t.Fatalf("loaded again: %q", view.Loaded)
	}
}

func TestDictationReportsProblems(t *testing.T) {
	rec := &recognizer{start: "unsupported"}
	h, p, _, host, _ := setup(t, rec)
	p.toggle()
	waitCond(t, h, "the failure", func() bool { return len(host.Toasts) == 1 })
	if p.listening || host.Toasts[0] != "Dictation stopped" {
		t.Fatalf("listening %v, toasts %+v", p.listening, host.Toasts)
	}
	rec.start = ""
	rec.states = []map[string]any{{"text": "", "listening": false, "error": "not-allowed"}}
	p.toggle()
	waitCond(t, h, "the refusal", func() bool { return len(host.Toasts) == 2 })
	if p.listening {
		t.Fatal("still listening")
	}
	if !strings.Contains(problemText("not-allowed"), "Privacy & Security") || !strings.Contains(problemText("unsupported"), "isn't available") {
		t.Fatal("problem texts")
	}
}

func TestLeavingStopsDictation(t *testing.T) {
	rec := &recognizer{}
	h, p, _, _, _ := setup(t, rec)
	p.toggle()
	kernel.Emit(h, chat.Changed, chat.Conversation{Session: "s2"})
	waitCond(t, h, "dictation stops", func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return rec.stopped == 1 && !p.listening
	})
}

func TestJoin(t *testing.T) {
	for _, c := range [][3]string{{"", "hi", "hi"}, {"a", "b", "a b"}, {"a ", "b", "a b"}, {"a\n", "b", "a\nb"}, {"a", "", "a"}} {
		if got := join(c[0], c[1]); got != c[2] {
			t.Errorf("join(%q, %q) = %q", c[0], c[1], got)
		}
	}
}
