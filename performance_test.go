package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

func TestAgentUpdatesBatchAndFilter(t *testing.T) {
	for _, n := range []int{0, 80} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			posted, applied := 0, 0
			ended := errors.New("stream ended")
			err := agentUpdates(context.Background(), func(ctx context.Context, emit func(opencode.Event)) error {
				for range 100 {
					emit(opencode.Event{Type: "session.tool.input.delta"})
				}
				for i := range n {
					emit(opencode.Event{Type: "session.text.delta", Data: []byte(fmt.Sprint(i))})
				}
				return ended
			}, func(fn func()) { posted++; fn() }, func(ev opencode.Event) {
				if string(ev.Data) != fmt.Sprint(applied) {
					t.Fatalf("event %d: %s", applied, ev.Data)
				}
				applied++
			})
			if err != ended || applied != n || (n == 0 && posted != 0) || (n > 0 && posted >= n) {
				t.Fatalf("err %v, %d events, %d updates", err, applied, posted)
			}
		})
	}
}

func TestAgentUpdatesCancelWhileUIIsBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	posts := make(chan func(), 1)
	done := make(chan error, 1)
	sourceDone := make(chan struct{})
	applied := false
	go func() {
		done <- agentUpdates(ctx, func(ctx context.Context, emit func(opencode.Event)) error {
			defer close(sourceDone)
			for range 10000 {
				emit(opencode.Event{Type: "session.text.delta"})
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
			return nil
		}, func(fn func()) { posts <- fn }, func(opencode.Event) { applied = true })
	}()
	var pending func()
	select {
	case pending = <-posts:
	case <-time.After(2 * time.Second):
		t.Fatal("no batch was posted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a blocked UI prevented shutdown")
	}
	select {
	case <-sourceDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the source remained blocked")
	}
	pending()
	if applied || len(posts) != 0 {
		t.Fatal("events were applied after shutdown or multiple UI batches accumulated")
	}
}

func TestTranscriptBuildsOnlyVisibleRows(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.selectedSession = "large"
	for i := range 1000 {
		a.transcript.Append(chat.Block{Kind: "text", Text: fmt.Sprintf("Answer %d", i)})
	}
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Answer 999") {
		t.Fatal("a loaded conversation did not open at its end")
	}
	if tt.HasText("Answer 0") {
		t.Fatal("an offscreen message was still built")
	}
}

// TestModelsTabBuildsOnlyVisibleRows keeps the settings' catalog on the
// same virtual list the transcript and the sidebar use: a server with
// hundreds of models builds the rows in view, not the whole catalog.
func TestModelsTabBuildsOnlyVisibleRows(t *testing.T) {
	a := newTestApp(t)
	for i := range 400 {
		a.scope().models = append(a.scope().models, opencode.Model{ProviderID: "opencode-go",
			ID: fmt.Sprintf("m-%03d", i), Name: fmt.Sprintf("opencode.Model %03d", i)})
	}
	a.scope().modelsLoaded = true
	a.settingsOpen = true
	a.settingsTab = tabModels
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("opencode.Model 000") || tt.HasText("opencode.Model 399") {
		t.Fatal("the catalog did not limit construction to visible rows")
	}
	a.modelsList.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("opencode.Model 399") {
		t.Fatal("scrolling did not reach the tail of the catalog")
	}
}

func TestSidebarBuildsOnlyVisibleRows(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	for i := range 2000 {
		a.scope().sessions = append(a.scope().sessions, opencode.Session{ID: fmt.Sprint(i), Title: fmt.Sprintf("Chat %d", i)})
	}
	a.searchOpen = true
	a.searchText = "Chat"
	a.searchResults = a.scope().sessions
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Chat 0") || tt.HasText("Chat 1999") {
		t.Fatal("sidebar did not limit construction to visible rows")
	}
	a.resultsList.ScrollToEnd()
	tt.Frame()
	if err := tt.Click("Chat 1999"); err != nil {
		t.Fatal(err)
	}
	if a.selectedSession != "1999" {
		t.Fatal("the scrolled row opened the wrong conversation")
	}
}

func TestActivitySurvivesVirtualScrolling(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.selectedSession = "large"
	for i := range 1000 {
		a.transcript.Append(chat.Block{Kind: "text", Text: fmt.Sprintf("Answer %d", i)})
	}
	a.transcript.Append(chat.Block{Kind: "activity", Text: "Ran command", Key: "activity:tail", Icon: "shell",
		Detail: []string{"$ echo retained", "retained"}})
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Ran command"); err != nil {
		t.Fatal(err)
	}
	a.feed.ScrollTo(0, ui.Start)
	tt.Frame()
	// A focused row may stay built for keyboard identity, but it must
	// be clipped while outside the viewport.
	if r, ok := tt.Find("Ran command"); ok && r.W > 0 && r.H > 0 {
		t.Fatalf("the tool row was not clipped: %+v", r)
	}
	a.feed.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("$ echo retained") {
		t.Fatal("tool expansion was lost while its row was outside the viewport")
	}
}

func TestApprovalRemovalPreservesLiveRows(t *testing.T) {
	a := newTestApp(t)
	a.selectedSession = "s"
	a.apply(opencode.Event{Type: "permission.asked", Data: []byte(`{"id":"ask","sessionID":"s","action":"shell"}`)})
	a.apply(opencode.Event{Type: "session.text.started", Data: []byte(`{"sessionID":"s","assistantMessageID":"m","ordinal":0}`)})
	id := a.blockIdentity(1)
	a.dropPerm("ask")
	a.apply(opencode.Event{Type: "session.text.delta", Data: []byte(`{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"kept"}`)})
	a.apply(opencode.Event{Type: "session.text.ended", Data: []byte(`{"sessionID":"s","assistantMessageID":"m","ordinal":0,"text":"kept"}`)})
	if a.blockIdentity(0) != id || a.transcript.Rows()[0].Text != "kept" || a.transcript.Rows()[0].Kind != "text" {
		t.Fatal("removing an approval changed the live row's identity or content")
	}
}

func TestPolicyRefreshesOutsideEdits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dirs := make(chan string, 1)
	writes := make(chan policyChange, 1)
	updates := make(chan policyResult, 4)
	go watchPolicy(ctx, dirs, writes, func(result policyResult) { updates <- result })
	dir := t.TempDir()
	want := func(level string) {
		t.Helper()
		select {
		case result := <-updates:
			if result.dir != dir || result.level != level || result.err != nil {
				t.Fatalf("policy: %+v, want %s", result, level)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("policy refresh did not arrive")
		}
	}
	dirs <- dir
	want("full")
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(`{"permission":{"bash":{"*":"ask"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	want("ask")
	// Returning to the same directory still needs to fill a reset cache.
	dirs <- dir
	want("ask")
	if err := os.Remove(filepath.Join(dir, "opencode.json")); err != nil {
		t.Fatal(err)
	}
	want("full")
}

func TestSearchCancelsObsoleteLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queries := make(chan searchRequest, 1)
	started := make(chan string, 2)
	stopped := make(chan struct{}, 1)
	accepted := make(chan searchRequest, 2)
	go searchChats(ctx, queries, func(ctx context.Context, text string) ([]opencode.Session, error) {
		started <- text
		if text == "old" {
			<-ctx.Done()
			stopped <- struct{}{}
			return nil, ctx.Err()
		}
		return []opencode.Session{{Title: text}}, nil
	}, func(fn func()) { fn() }, func(q searchRequest, _ []opencode.Session, _ error) { accepted <- q })
	queries <- searchRequest{text: "old", version: 1}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("search did not start")
	}
	queries <- searchRequest{text: "new", version: 2}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("obsolete lookup was not canceled")
	}
	select {
	case q := <-accepted:
		if q.text != "new" {
			t.Fatalf("accepted stale search: %+v", q)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("new search did not finish")
	}
}

func TestSearchKeepsNewestQuery(t *testing.T) {
	a := newTestApp(t)
	for range cap(a.jobs) {
		a.jobs <- agentJob{kind: "stop"}
	}
	for i := range 20 {
		a.searchText = fmt.Sprintf("query-%d", i)
		a.reviseSearch()
	}
	query := <-a.searches
	if query.text != "query-19" {
		t.Fatalf("last lookup: %s", query.text)
	}
	a.acceptSearch(query, []opencode.Session{{Title: "Found"}}, nil)
	if a.searchLoading || len(a.searchResults) != 1 {
		t.Fatal("the latest query did not finish")
	}
	a.searchText = "other"
	a.reviseSearch()
	a.acceptSearch(query, nil, nil)
	if !a.searchLoading {
		t.Fatal("a stale result settled the newer search")
	}
	a.searchText = ""
	a.reviseSearch()
	if latest := <-a.searches; latest.text != "" || a.searchLoading {
		t.Fatal("clearing the field did not cancel the lookup")
	}
}

func TestToolTailReleasesOutput(t *testing.T) {
	output := strings.Repeat("x", 4<<20) + "\none\ntwo\nthree\nfour\nfive\nsix\n"
	lines := toolDetailLines(toolRun{out: textOf([]opencode.ToolOutput{{Type: "text", Text: output}})})
	if strings.Join(lines, "|") != "one|two|three|four|five|six" {
		t.Fatalf("tail: %q", lines)
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(output)))
	for _, line := range lines {
		p := uintptr(unsafe.Pointer(unsafe.StringData(line)))
		if p >= start && p < start+uintptr(len(output)) {
			t.Fatal("a short tail line retains the whole tool output")
		}
	}
	runtime.KeepAlive(output)
}

func TestStreamAllocationBudget(t *testing.T) {
	a := newTestApp(t)
	a.selectedSession = "s"
	a.apply(opencode.Event{Type: "session.text.started", Data: json.RawMessage(`{"sessionID":"s","assistantMessageID":"m","ordinal":0}`)})
	ev := opencode.Event{Type: "session.text.delta", Data: json.RawMessage(`{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"01234567890123456789012345678901"}`)}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 4096 {
		a.apply(ev)
	}
	runtime.ReadMemStats(&after)
	if len(a.transcript.Rows()[0].Text) != 128<<10 {
		t.Fatalf("answer has %d bytes", len(a.transcript.Rows()[0].Text))
	}
	// The race detector deliberately drops pooled JSON decoders. Allow
	// their extra allocations; prefix copying would exceed 250 MiB.
	if n := after.TotalAlloc - before.TotalAlloc; n > 8<<20 {
		t.Fatalf("assembling 128 KiB allocated %d bytes, budget 8 MiB", n)
	}
}
