package main

import (
	"context"
	"encoding/json"

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

func TestModelsTabBuildsOnlyVisibleRows(t *testing.T) {
	a := newTestApp(t)
	for i := range 400 {
		a.scope().models = append(a.scope().models, opencode.Model{ProviderID: "opencode-go",
			ID: fmt.Sprintf("m-%03d", i), Name: fmt.Sprintf("Model %03d", i)})
	}
	a.scope().modelsLoaded = true
	a.layout.settingsOpen = true
	a.layout.settingsTab = tabModels
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Model 000") || tt.HasText("Model 399") {
		t.Fatal("the catalog did not limit construction to visible rows")
	}
	a.layout.modelsList.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("Model 399") {
		t.Fatal("scrolling did not reach the tail of the catalog")
	}
}

func TestSidebarBuildsOnlyVisibleRows(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	for i := range 2000 {
		a.scope().sessions = append(a.scope().sessions, opencode.Session{ID: fmt.Sprint(i), Title: fmt.Sprintf("Chat %d", i)})
	}
	a.search.searchOpen = true
	a.search.searchText = "Chat"
	a.search.searchResults = a.scope().sessions
	tt := ui.NewTester(a.view, 1400, 920)
	if !tt.HasText("Chat 0") || tt.HasText("Chat 1999") {
		t.Fatal("sidebar did not limit construction to visible rows")
	}
	a.search.resultsList.ScrollToEnd()
	tt.Frame()
	if err := tt.Click("Chat 1999"); err != nil {
		t.Fatal(err)
	}
	if a.conversation.target.session != "1999" {
		t.Fatal("the scrolled row opened the wrong conversation")
	}
}

func TestActivitySurvivesVirtualScrolling(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessionsLoaded = true
	a.conversation.target.selectSession("large")
	for i := range 1000 {
		a.conversation.transcript.Append(chat.Block{Kind: "text", Text: fmt.Sprintf("Answer %d", i)})
	}
	a.conversation.transcript.Append(chat.Block{Kind: "activity", Text: "Ran command", Key: "activity:tail", Icon: "shell",
		Detail: []string{"$ echo retained", "retained"}})
	tt := ui.NewTester(a.view, 1400, 920)
	if err := tt.Click("Ran command"); err != nil {
		t.Fatal(err)
	}
	a.layout.feed.ScrollTo(0, ui.Start)
	tt.Frame()
	// A focused row may stay built for keyboard identity, but it must
	// be clipped while outside the viewport.
	if r, ok := tt.Find("Ran command"); ok && r.W > 0 && r.H > 0 {
		t.Fatalf("the tool row was not clipped: %+v", r)
	}
	a.layout.feed.ScrollToEnd()
	tt.Frame()
	if !tt.HasText("$ echo retained") {
		t.Fatal("tool expansion was lost while its row was outside the viewport")
	}
}

func TestApprovalRemovalPreservesLiveRows(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("s")
	a.apply(opencode.Event{Type: "permission.asked", Data: []byte(`{"id":"ask","sessionID":"s","action":"shell"}`)})
	a.apply(opencode.Event{Type: "session.text.started", Data: []byte(`{"sessionID":"s","assistantMessageID":"m","ordinal":0}`)})
	id := a.blockIdentity(1)
	a.dropPerm("ask")
	a.apply(opencode.Event{Type: "session.text.delta", Data: []byte(`{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"kept"}`)})
	a.apply(opencode.Event{Type: "session.text.ended", Data: []byte(`{"sessionID":"s","assistantMessageID":"m","ordinal":0,"text":"kept"}`)})
	if a.blockIdentity(0) != id || a.conversation.transcript.Rows()[0].Text != "kept" || a.conversation.transcript.Rows()[0].Kind != "text" {
		t.Fatal("removing an approval changed the live row's identity or content")
	}
}

func TestPolicyRefreshesOutsideEdits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dirs := make(chan policyTarget, 1)
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
	dirs <- policyTarget{dir: dir}
	want("full")
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(`{"permission":{"bash":{"*":"ask"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	want("ask")
	// Returning to the same directory still needs to fill a reset cache.
	dirs <- policyTarget{dir: dir}
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
	go searchChats(ctx, queries, func(ctx context.Context, query searchRequest) ([]opencode.Session, error) {
		text := query.text
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
	for range cap(commandsOf(a)) {
		commandsOf(a) <- commandRecord{kind: "stop"}
	}
	for i := range 20 {
		a.search.searchText = fmt.Sprintf("query-%d", i)
		a.reviseSearch()
	}
	query := <-a.search.searches
	if query.text != "query-19" {
		t.Fatalf("last lookup: %s", query.text)
	}
	a.acceptSearch(query, []opencode.Session{{Title: "Found"}}, nil)
	if a.search.searchLoading || len(a.search.searchResults) != 1 {
		t.Fatal("the latest query did not finish")
	}
	a.search.searchText = "other"
	a.reviseSearch()
	a.acceptSearch(query, nil, nil)
	if !a.search.searchLoading {
		t.Fatal("a stale result settled the newer search")
	}
	a.search.searchText = ""
	a.reviseSearch()
	if latest := <-a.search.searches; latest.text != "" || a.search.searchLoading {
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
	a.conversation.target.selectSession("s")
	a.apply(opencode.Event{Type: "session.text.started", Data: json.RawMessage(`{"sessionID":"s","assistantMessageID":"m","ordinal":0}`)})
	ev := opencode.Event{Type: "session.text.delta", Data: json.RawMessage(`{"sessionID":"s","assistantMessageID":"m","ordinal":0,"delta":"01234567890123456789012345678901"}`)}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 4096 {
		a.apply(ev)
	}
	runtime.ReadMemStats(&after)
	if len(a.conversation.transcript.Rows()[0].Text) != 128<<10 {
		t.Fatalf("answer has %d bytes", len(a.conversation.transcript.Rows()[0].Text))
	}
	// The race detector deliberately drops pooled JSON decoders. Allow
	// their extra allocations; prefix copying would exceed 250 MiB.
	if n := after.TotalAlloc - before.TotalAlloc; n > 8<<20 {
		t.Fatalf("assembling 128 KiB allocated %d bytes, budget 8 MiB", n)
	}
}
