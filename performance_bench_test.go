package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func benchmarkApp(b *testing.B) *app {
	a := newApp()
	a.scope().sessionsLoaded = true
	a.selectedSession = "review"
	a.selected = "Performance review"
	a.scope().sessions = []agentSession{{id: "review", title: a.selected, dir: b.TempDir()}}
	return a
}

// These force warm headless frames, including build, layout and CPU
// painting. They do not measure native GPU presentation or idle CPU.
func BenchmarkTranscript(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := benchmarkApp(b)
			for i := range n {
				a.blocks = append(a.blocks, block{kind: "text", text: fmt.Sprintf("Answer %d: **review** the render path and keep `work` off the UI thread.\n\n- CPU time\n- Memory use", i)})
			}
			tt := ui.NewTester(a.view, 1400, 920)
			tt.Frame()
			b.ReportAllocs()
			for b.Loop() {
				tt.Frame()
			}
		})
	}
}

func BenchmarkSidebar(b *testing.B) {
	for _, n := range []int{50, 500, 2000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := benchmarkApp(b)
			for i := range n {
				a.scope().sessions = append(a.scope().sessions, agentSession{id: fmt.Sprint(i), title: fmt.Sprintf("Conversation %d", i)})
			}
			a.searchOpen = true
			a.searchText = "Conversation"
			a.searchResults = a.scope().sessions
			tt := ui.NewTester(a.view, 1400, 920)
			tt.Frame()
			b.ReportAllocs()
			for b.Loop() {
				tt.Frame()
			}
		})
	}
}

func BenchmarkStream(b *testing.B) {
	for _, n := range []int{256, 1024, 4096} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := benchmarkApp(b)
			start := agentEvent{Type: "session.text.started", Data: []byte(`{"sessionID":"review","assistantMessageID":"m","ordinal":0}`)}
			delta := agentEvent{Type: "session.text.delta", Data: []byte(`{"sessionID":"review","assistantMessageID":"m","ordinal":0,"delta":"01234567890123456789012345678901"}`)}
			b.SetBytes(int64(n * 32))
			b.ReportAllocs()
			for b.Loop() {
				a.freshTranscript()
				a.apply(start)
				for range n {
					a.apply(delta)
				}
			}
		})
	}
}
