package chat_test

import (
	"reflect"
	"testing"

	"wada-desktop-go/internal/chat"
)

func TestRemovingPermissionsKeepsStreamAndToolTargets(t *testing.T) {
	var s chat.State
	s.Start("session")
	s.Ask(&chat.Permission{ID: "first", Session: "session"})
	s.StartText("message", 0)
	s.Delta("message", 0, "First")
	s.ToolStarted("tool", "shell")
	s.ToolCalled("tool", "go test ./...")
	s.Ask(&chat.Permission{ID: "second", Session: "session"})
	s.StartText("message", 1)
	s.Delta("message", 1, "Second")
	firstID, toolID, secondID := s.Identity(1), s.Identity(2), s.Identity(4)

	s.DropPermission("first")
	s.DropPermission("second")
	s.Delta("message", 0, " answer")
	s.EndText("message", 0, "First answer")
	s.Delta("message", 1, " answer")
	s.EndText("message", 1, "Second answer")
	s.ToolFinished("tool", []string{"ok: all tests passed"})

	rows := s.Snapshot()
	if len(rows) != 3 || len(s.Pending()) != 0 {
		t.Fatalf("approval removal left rows=%+v pending=%+v", rows, s.Pending())
	}
	if rows[0].Kind != chat.KindText || rows[0].Text != "First answer" || s.Identity(0) != firstID {
		t.Fatalf("first stream lost its target after approval removal: %+v", rows[0])
	}
	if rows[1].Kind != chat.KindActivity || !reflect.DeepEqual(rows[1].Detail, []string{"ok: all tests passed"}) || s.Identity(1) != toolID {
		t.Fatalf("tool lost its target after approval removal: %+v", rows[1])
	}
	if rows[2].Kind != chat.KindText || rows[2].Text != "Second answer" || s.Identity(2) != secondID {
		t.Fatalf("second stream lost its target after approval removal: %+v", rows[2])
	}
}

func TestCompletionTriggersFollowupOncePerTurn(t *testing.T) {
	var s chat.State
	s.Start("session")
	if s.Busy() != "session" {
		t.Fatal("starting a turn did not mark its session busy")
	}
	followups := 0
	for range 3 {
		if s.Finish() {
			followups++
		}
	}
	if followups != 1 || s.Busy() != "" {
		t.Fatalf("repeated completion produced %d followups, busy=%q", followups, s.Busy())
	}
	s.Start("session")
	if !s.Finish() || s.Finish() {
		t.Fatal("a later turn did not have its own single completion")
	}
}

func TestResetClearsTurnStateWithoutReusingRowIdentities(t *testing.T) {
	var s chat.State
	s.Start("session")
	s.StartText("message", 0)
	s.Delta("message", 0, "old text")
	s.ToolStarted("tool", "shell")
	s.ToolCalled("tool", "old command")
	s.Ask(&chat.Permission{ID: "approval", Session: "session"})
	oldIDs := map[uint64]bool{}
	for i := range s.All() {
		oldIDs[s.Identity(i)] = true
	}
	s.Reset()
	if s.Len() != 0 || len(s.Pending()) != 0 || len(s.Streaming()) != 0 || s.Busy() != "" || s.ToolCommand("tool") != "" {
		t.Fatal("reset retained transcript or turn state")
	}

	// Reusing protocol identities must create fresh rows after a reset.
	s.Start("session")
	s.StartText("message", 0)
	s.Delta("message", 0, "new text")
	s.ToolStarted("tool", "read")
	s.ToolCalled("tool", "new command")
	s.Ask(&chat.Permission{ID: "approval", Session: "session"})
	if s.Len() != 3 || s.Row(0).Text != "new text" || s.ToolCommand("tool") != "new command" || len(s.Pending()) != 1 {
		t.Fatalf("reset left stale indexes or approvals: %+v", s.Snapshot())
	}
	for i := range s.All() {
		id := s.Identity(i)
		if id == 0 || oldIDs[id] || s.Identity(i) != id {
			t.Fatalf("row %d reused or changed its identity: %d", i, id)
		}
	}
	if !s.Finish() {
		t.Fatal("reset left completion suppressed")
	}
}

func TestInterruptedStreamsKeepPartialTextAndReleaseLiveTargets(t *testing.T) {
	var s chat.State
	s.Start("session")
	s.StartText("partial", 0)
	s.Delta("partial", 0, "Some answer")
	s.StartText("empty", 0)
	s.Ask(&chat.Permission{ID: "approval", Session: "session"})
	firstID := s.Identity(0)
	s.Cut()
	if s.Busy() != "" || len(s.Pending()) != 0 || len(s.Streaming()) != 0 {
		t.Fatal("interruption retained active turn state")
	}
	rows := s.Snapshot()
	if rows[0].Kind != chat.KindText || rows[0].Text != "Some answer" || s.Identity(0) != firstID {
		t.Fatalf("interruption lost partial text or row identity: %+v", rows[0])
	}
	if rows[1].Kind != chat.KindText || rows[1].Text == "" {
		t.Fatalf("interruption left an empty streaming row: %+v", rows[1])
	}
	s.Delta("partial", 0, " stale delta")
	s.EndText("partial", 0, "stale final answer")
	if s.Row(0).Text != "Some answer" {
		t.Fatal("a late stream result changed an interrupted answer")
	}
}

func TestUsageBelongsToLastRowOfItsMessage(t *testing.T) {
	var s chat.State
	s.Append(
		chat.Block{Kind: chat.KindText, Message: "message", Text: "First paragraph"},
		chat.Block{Kind: chat.KindActivity, Message: "message", Text: "Tool output"},
		chat.Block{Kind: chat.KindText, Message: "other", Text: "Other answer"},
		chat.Block{Kind: chat.KindText, Message: "message", Text: "Last paragraph"},
		chat.Block{Kind: chat.KindText, Message: "later", Text: "Later answer"},
	)
	s.Usage("message", 0.25, 123)
	for i, row := range s.All() {
		if i == 3 {
			if row.Cost != 0.25 || row.Tokens != 123 {
				t.Fatalf("last message row has no usage: %+v", row)
			}
		} else if row.Cost != 0 || row.Tokens != 0 {
			t.Fatalf("usage leaked to row %d: %+v", i, row)
		}
	}
	s.Usage("unknown", 1, 999)
	if s.Row(4).Cost != 0 || s.Row(4).Tokens != 0 {
		t.Fatal("unknown usage was attached to the latest unrelated answer")
	}
}

func TestReadViewsCannotMutateOwnedRowsOrPermissions(t *testing.T) {
	var s chat.State
	s.Append(chat.Block{Kind: chat.KindText, Text: "kept"})
	s.Ask(&chat.Permission{ID: "approval", Session: "session", Resources: []string{"keep"}})
	rows := s.Snapshot()
	rows[0].Text = "changed"
	rows[1].Request.Resources[0] = "changed"
	pending := s.Pending()
	delete(pending, "approval")
	if s.Row(0).Text != "kept" || s.Pending()["approval"].Resources[0] != "keep" {
		t.Fatal("read views leaked mutable state")
	}
}

func TestReplacingTranscriptInvalidatesLiveIndexes(t *testing.T) {
	var s chat.State
	s.StartText("old", 0)
	s.Delta("old", 0, "old")
	s.Replace([]chat.Block{{Kind: chat.KindText, Text: "replacement"}})
	s.Delta("old", 0, " stale")
	s.EndText("old", 0, "stale")
	if s.Row(0).Text != "replacement" {
		t.Fatal("replaced transcript accepted an old stream")
	}
}
