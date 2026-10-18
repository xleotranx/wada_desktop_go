package main

import (
	"testing"
	"wada-desktop-go/internal/chat"
	"wada-desktop-go/internal/opencode"
	"wada-desktop-go/internal/runner"
)

func TestOpenedTranscriptRejectsEarlierVisitToSameSession(t *testing.T) {
	a := newTestApp(t)
	first := opencode.Session{ID: "A", Title: "A"}
	a.openConversation(first)
	old := a.conversation.target.revision
	a.openConversation(opencode.Session{ID: "B", Title: "B"})
	a.openConversation(first)
	latest := a.conversation.target.revision
	a.conversation.transcript.Append(chat.Block{Kind: chat.KindNote, Text: "current view"})
	messages := []opencode.Message{{Type: "user", Text: "restored"}}
	sink := runnerSink{a}
	sink.Opened(runner.Opened{Session: "A", Revision: old, Messages: messages})
	if a.conversation.transcript.Row(0).Text != "current view" {
		t.Fatal("an earlier visit replaced the current transcript")
	}
	sink.Opened(runner.Opened{Session: "A", Revision: latest, Messages: messages})
	if a.conversation.transcript.Row(0).Text != "restored" {
		t.Fatal("the current visit failed to restore")
	}
}
func TestRejectedOpenKeepsTheCurrentConversation(t *testing.T) {
	a := newTestApp(t)
	a.adoptSession(opencode.Session{ID: "kept", Title: "Kept"})
	a.conversation.transcript.Append(chat.Block{Kind: chat.KindText, Text: "keep"})
	before := a.conversation.target
	for range cap(commandsOf(a)) {
		commandsOf(a) <- commandRecord{kind: "refresh"}
	}
	if a.openConversation(opencode.Session{ID: "other"}) {
		t.Fatal("full queue accepted navigation")
	}
	if a.conversation.target != before || a.conversation.transcript.Row(0).Text != "keep" {
		t.Fatal("rejected read changed navigation")
	}
}
func TestLateCreatedSessionCannotAdoptAReplacementDraft(t *testing.T) {
	a := newTestApp(t)
	a.startDraft("/one")
	old := a.conversation.target.revision
	a.startDraft("/two")
	runnerSink{a}.Created(runner.Created{Draft: old, Session: opencode.Session{ID: "old", Directory: "/one"}})
	if !a.conversation.target.isDraft() || a.conversation.draftProject != "/two" {
		t.Fatal("an old creation adopted the replacement draft")
	}
}

func TestBootstrapCannotReplaceAnExplicitDraft(t *testing.T) {
	a := newTestApp(t)
	a.startDraft("/chosen")
	a.conversation.draft = "Keep this draft"
	runnerSink{a}.Sessions(runner.Page{Sessions: []opencode.Session{{ID: "old", Title: "Old conversation"}}})
	if !a.conversation.target.isDraft() || a.conversation.draftProject != "/chosen" || a.conversation.draft != "Keep this draft" {
		t.Fatal("bootstrap replaced explicit draft navigation")
	}
}

func TestFailureFromAnotherConversationDoesNotEnterThisTranscript(t *testing.T) {
	a := newTestApp(t)
	a.adoptSession(opencode.Session{ID: "current"})
	runnerSink{a}.Failed(runner.Failure{Origin: runner.Origin{Session: "other"}, Text: "The other action failed"})
	if a.conversation.transcript.Len() != 0 || a.conversation.commandError == "" {
		t.Fatal("another conversation's failure entered the transcript or disappeared")
	}
}
