package main

import (
	"context"
	"path/filepath"
	"testing"
	"wada-desktop-go/internal/opencode"

	"github.com/egoist/mygo/ui"
)

// TestDraftsFollowTheirChats keeps unsent words with the chat they were
// typed in instead of carrying them into the next conversation.
func TestDraftsFollowTheirChats(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha"},
		{ID: "ses_b", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.conversation.draft = "alpha words"
	a.adoptSession(a.scope().sessions[1])
	if a.conversation.draft != "" {
		t.Fatalf("Beta inherited %q", a.conversation.draft)
	}
	a.conversation.draft = "beta words"
	a.adoptSession(a.scope().sessions[0])
	if a.conversation.draft != "alpha words" {
		t.Fatalf("Alpha came back as %q", a.conversation.draft)
	}
	a.adoptSession(a.scope().sessions[1])
	if a.conversation.draft != "beta words" {
		t.Fatalf("Beta came back as %q", a.conversation.draft)
	}
}

// TestNewChatDraftReturns keeps the new-chat composer's words across a
// visit to a conversation.
func TestNewChatDraftReturns(t *testing.T) {
	a := newTestApp(t)
	a.startDraft("")
	a.conversation.draft = "a new idea"
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Alpha"}}
	a.adoptSession(a.scope().sessions[0])
	if a.conversation.draft != "" {
		t.Fatalf("the chat inherited the draft %q", a.conversation.draft)
	}
	a.startDraft("")
	if a.conversation.draft != "a new idea" {
		t.Fatalf("the new-chat draft came back as %q", a.conversation.draft)
	}
}

// TestSendForgetsTheDraft clears the stored words once the prompt lands.
func TestSendForgetsTheDraft(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha"},
		{ID: "ses_b", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.conversation.draft = "send this"
	if !a.send() {
		t.Fatal("the prompt was not accepted")
	}
	if _, ok := a.scope().drafts["ses_a"]; ok {
		t.Fatal("the sent draft stayed stored")
	}
	a.conversation.draft = "unsent"
	a.adoptSession(a.scope().sessions[1])
	a.adoptSession(a.scope().sessions[0])
	if a.conversation.draft != "unsent" {
		t.Fatalf("the new unsent draft came back as %q", a.conversation.draft)
	}
}

// TestDraftSnapshotAndRestore carries the live composer and stored drafts
// through the metadata state.
func TestDraftSnapshotAndRestore(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Alpha"}}
	a.adoptSession(a.scope().sessions[0])
	a.conversation.draft = "still typing"
	snap := a.stateSnapshot()
	if got := snap.drafts[localServerID]["ses_a"]; got != "still typing" {
		t.Fatalf("snapshot draft %q", got)
	}

	b := newTestApp(t)
	b.restoreState(appMetadata{drafts: map[string]map[string]string{
		localServerID: {"ses_a": "restored words", "": "new idea"},
	}})
	b.adoptSession(opencode.Session{ID: "ses_a", Title: "Alpha"})
	if b.conversation.draft != "restored words" {
		t.Fatalf("restored draft %q", b.conversation.draft)
	}
}

// TestBootRestoresNewChatDraft shows the stored new-chat words when the
// server has no conversations to land on.
func TestBootRestoresNewChatDraft(t *testing.T) {
	a := newTestApp(t)
	a.scope().drafts[""] = "restored at boot"
	a.settleBoot(nil, "", nil)
	if a.conversation.draft != "restored at boot" {
		t.Fatalf("boot draft %q", a.conversation.draft)
	}
}

// TestCloseWorkFlushesTheDraft writes the composer on the way out, even
// when no navigation happened after the last keystroke.
func TestCloseWorkFlushesTheDraft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.db")
	a := newTestApp(t)
	db, err := a.loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	a.preferences.metadata = startMetadataWriter(db, func(_ uint64, err error) {
		if err != nil {
			t.Error(err)
		}
	})
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Alpha"}}
	a.adoptSession(a.scope().sessions[0])
	a.conversation.draft = "flushed on quit"
	a.closeWork()

	saved, err := openMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	state, err := saved.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.drafts[localServerID]["ses_a"]; got != "flushed on quit" {
		t.Fatalf("saved draft %q", got)
	}
}

// TestBootKeepsNewChatDraftWhileOpeningChat makes sure adopting the first
// conversation cannot erase new-chat words that were never loaded.
func TestBootKeepsNewChatDraftWhileOpeningChat(t *testing.T) {
	a := newTestApp(t)
	a.scope().drafts[""] = "new idea"
	a.scope().drafts["ses_a"] = "chat words"
	a.settleBoot([]opencode.Session{{ID: "ses_a", Title: "Alpha"}}, "", nil)
	if a.conversation.draft != "chat words" {
		t.Fatalf("opened chat draft %q", a.conversation.draft)
	}
	if a.scope().drafts[""] != "new idea" {
		t.Fatalf("new-chat draft was erased: %v", a.scope().drafts)
	}
	a.startDraft("")
	if a.conversation.draft != "new idea" {
		t.Fatalf("new-chat draft came back as %q", a.conversation.draft)
	}
}

// TestClearingADraftForgetsIt removes the stored words when the user
// empties a draft they had loaded, rather than bringing it back later.
func TestClearingADraftForgetsIt(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{
		{ID: "ses_a", Title: "Alpha"},
		{ID: "ses_b", Title: "Beta"},
	}
	a.adoptSession(a.scope().sessions[0])
	a.conversation.draft = "clear me"
	a.adoptSession(a.scope().sessions[1])
	a.adoptSession(a.scope().sessions[0])
	if a.conversation.draft != "clear me" {
		t.Fatalf("loaded draft %q", a.conversation.draft)
	}
	a.conversation.draft = ""
	a.adoptSession(a.scope().sessions[1])
	a.adoptSession(a.scope().sessions[0])
	if a.conversation.draft != "" {
		t.Fatalf("cleared draft returned as %q", a.conversation.draft)
	}
}

// TestDraftMarkers names the live draft over a stale stored one and clears
// the marker when the words are sent.
func TestDraftMarkers(t *testing.T) {
	a := newTestApp(t)
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Alpha"}}
	a.conversation.target.selectSession("ses_a")
	a.conversation.draft = "live words"
	a.scope().drafts["ses_a"] = "old words"
	if got := a.draftFor("ses_a"); got != "live words" {
		t.Fatalf("the marker read %q", got)
	}
	if !a.hasDraft("ses_a") {
		t.Fatal("the live draft has no marker")
	}
	if got := draftSnippet("one\n two   three"); got != "one two three" {
		t.Fatalf("snippet %q", got)
	}
	if !a.send() {
		t.Fatal("the draft was not sent")
	}
	if a.hasDraft("ses_a") {
		t.Fatal("the marker outlived the sent draft")
	}
}

// TestNewChatDraftMarker shows the badge beside New chat for the new-chat
// composer, and only for it.
func TestNewChatDraftMarker(t *testing.T) {
	a := newTestApp(t)
	a.scope().drafts[""] = "a new idea"
	a.restoreDraft("")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	if !tt.HasText("Draft") {
		t.Fatalf("the New chat row has no draft badge: %v", tt.Texts())
	}
	if err := tt.Click("New chat"); err != nil {
		t.Fatal(err)
	}
	if a.conversation.draft != "a new idea" {
		t.Fatalf("New chat opened %q", a.conversation.draft)
	}
}
