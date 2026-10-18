package main

import "wada-desktop-go/internal/opencode"

import "wada-desktop-go/internal/chat"

import "testing"

func TestDraftRejectsUnrelatedSessionEvents(t *testing.T) {
	a := newTestApp(t)
	a.startNewChat()
	a.conversation.draft = "my question"
	a.send()
	for _, ev := range []opencode.Event{
		{Type: "session.text.started", Data: []byte(`{"sessionID":"other","assistantMessageID":"msg","ordinal":0}`)},
		{Type: "session.execution.started", Data: []byte(`{"sessionID":"other"}`)},
		{Type: "permission.asked", Data: []byte(`{"sessionID":"other","id":"ask","action":"shell"}`)},
	} {
		a.apply(ev)
	}
	if a.conversation.transcript.Len() != 1 || a.conversation.transcript.Row(0).Kind != chat.KindUser || len(a.conversation.transcript.Pending()) != 0 || a.conversation.transcript.Busy() != "" {
		t.Fatalf("unrelated events entered the pending turn: %+v", a.conversation.transcript.Snapshot())
	}
	a.conversation.target.selectSession("mine")
	a.apply(opencode.Event{Type: "session.text.started", Data: []byte(`{"assistantMessageID":"anonymous","ordinal":0}`)})
	if a.conversation.transcript.Len() != 1 {
		t.Fatal("an event without a session ID entered the selected conversation")
	}
	a.apply(opencode.Event{Type: "session.text.started", Data: []byte(`{"sessionID":"mine","assistantMessageID":"mine-msg","ordinal":0}`)})
	if a.conversation.transcript.Len() != 2 || a.conversation.transcript.Row(1).Kind != chat.KindStream {
		t.Fatal("the adopted conversation did not accept its own stream")
	}
}
