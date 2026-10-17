package main

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

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
