package main

import (
	"strings"
	"testing"
	"wada-desktop-go/internal/chat"

	"github.com/egoist/mygo/ui"
)

// TestMessageActionsCopyAndQuote hovers a transcript row and drives the
// pair of actions it reveals: copy puts the raw words on the clipboard,
// and quote appends them to the composer after the current draft.
func TestMessageActionsCopyAndQuote(t *testing.T) {
	a := newTestApp(t)
	a.conversation.target.selectSession("ses_actions")
	a.conversation.selected = "Actions"
	seedTranscript(a, []chat.Block{
		{Kind: chat.KindText, Text: "The answer is forty-two."},
		{Kind: chat.KindUser, Text: "What is the answer?"},
	})
	copied := ""
	a.runtime.copy = func(text string) error { copied = text; return nil }
	tt := ui.NewTester(a.view, 1400, 920)

	answer, ok := tt.Find("The answer is forty-two.")
	if !ok {
		t.Fatalf("the answer is missing: %v", tt.Texts())
	}
	tt.Move(answer.X+4, answer.Y+4)
	tt.Frame()
	if _, ok := tt.Find("Copy message"); !ok {
		t.Fatalf("hovering the answer revealed no actions: %v", tt.Texts())
	}
	if err := tt.Click("Copy message"); err != nil {
		t.Fatal(err)
	}
	if copied != "The answer is forty-two." {
		t.Fatalf("copied %q", copied)
	}

	a.conversation.draft = "draft words"
	answer, ok = tt.Find("The answer is forty-two.")
	if !ok {
		t.Fatal("the answer left after copying")
	}
	tt.Move(answer.X+4, answer.Y+4)
	tt.Frame()
	if err := tt.Click("Quote in reply"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.conversation.draft, "draft words\n\n> The answer is forty-two.") {
		t.Fatalf("quote did not follow the draft: %q", a.conversation.draft)
	}

	// The user's own row carries the same pair, on its side of the chat.
	question, ok := tt.Find("What is the answer?")
	if !ok {
		t.Fatalf("the question is missing: %v", tt.Texts())
	}
	tt.Move(question.X+4, question.Y+4)
	tt.Frame()
	if _, ok := tt.Find("Quote in reply"); !ok {
		t.Fatalf("hovering the question revealed no actions: %v", tt.Texts())
	}
}
