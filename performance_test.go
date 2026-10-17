package main

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestSearchCancelsObsoleteLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queries := make(chan searchRequest, 1)
	started := make(chan string, 2)
	stopped := make(chan struct{}, 1)
	accepted := make(chan searchRequest, 2)
	go searchChats(ctx, queries, func(ctx context.Context, text string) ([]agentSession, error) {
		started <- text
		if text == "old" {
			<-ctx.Done()
			stopped <- struct{}{}
			return nil, ctx.Err()
		}
		return []agentSession{{title: text}}, nil
	}, func(fn func()) { fn() }, func(q searchRequest, _ []agentSession, _ error) { accepted <- q })
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
	a := newApp()
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
	a.acceptSearch(query, []agentSession{{title: "Found"}}, nil)
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
	lines := toolDetailLines(toolRun{out: textOf([]toolOut{{Type: "text", Text: output}})})
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
	a := newApp()
	a.apply(agentEvent{Type: "session.text.started", Data: json.RawMessage(`{"assistantMessageID":"m","ordinal":0}`)})
	ev := agentEvent{Type: "session.text.delta", Data: json.RawMessage(`{"assistantMessageID":"m","ordinal":0,"delta":"01234567890123456789012345678901"}`)}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 4096 {
		a.apply(ev)
	}
	runtime.ReadMemStats(&after)
	if len(a.blocks[0].text) != 128<<10 {
		t.Fatalf("answer has %d bytes", len(a.blocks[0].text))
	}
	if n := after.TotalAlloc - before.TotalAlloc; n > 4<<20 {
		t.Fatalf("assembling 128 KiB allocated %d bytes, budget 4 MiB", n)
	}
}
