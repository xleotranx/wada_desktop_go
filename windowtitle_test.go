package main

import (
	"testing"
	"wada-desktop-go/internal/opencode"
)

// TestWindowTitleNamesTheChat follows the native window title from a chat
// to the settings page, back, and to a fresh draft.
func TestWindowTitleNamesTheChat(t *testing.T) {
	a := newTestApp(t)
	var last string
	a.runtime.setTitle = func(title string) { last = title }
	a.scope().sessions = []opencode.Session{{ID: "ses_a", Title: "Retry the upload"}}
	a.scope().sessionsLoaded = true
	a.adoptSession(a.scope().sessions[0])
	if last != "Retry the upload — Codex" {
		t.Fatalf("the title after opening reads %q", last)
	}
	a.openSettings("")
	if last != "Settings — Codex" {
		t.Fatalf("the title on settings reads %q", last)
	}
	a.closeSettings()
	if last != "Retry the upload — Codex" {
		t.Fatalf("the title after settings reads %q", last)
	}
	a.resetDraft("")
	if last != "Codex" {
		t.Fatalf("the title on a draft reads %q", last)
	}
}
