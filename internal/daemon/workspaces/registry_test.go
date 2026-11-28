package workspaces

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"wada-desktop-go/internal/daemon/store"
	"wada-desktop-go/internal/protocol"
)

func open(t *testing.T, dataDir string, events *[]protocol.Event) *Registry {
	t.Helper()
	st, err := store.Open(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := int64(0)
	r, err := Open(t.Context(), st, Options{
		Inbox: filepath.Join(dataDir, "workspaces", "inbox"),
		Emit: func(ev protocol.Event) {
			if events != nil {
				*events = append(*events, ev)
			}
		},
		Now: func() int64 { clock++; return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInboxIsCreatedOnceAndCantBeArchived(t *testing.T) {
	dir := t.TempDir()
	r := open(t, dir, nil)
	ps := r.Projects(false)
	if len(ps) != 1 || !ps[0].Inbox || ps[0].Name != "Inbox" {
		t.Fatalf("projects = %+v, want the inbox", ps)
	}
	inbox := r.Inbox()
	if inbox.ID == "" || inbox.Project != ps[0].ID || inbox.Isolation != protocol.IsolationProject {
		t.Fatalf("inbox workspace = %+v", inbox)
	}
	if err := r.ArchiveProject(t.Context(), ps[0].ID); err == nil {
		t.Fatal("archived the inbox")
	}
	again := open(t, dir, nil)
	if got := again.Projects(false); len(got) != 1 || got[0].ID != ps[0].ID {
		t.Fatalf("reopened projects = %+v, want the same inbox", got)
	}
	if again.Inbox().ID != inbox.ID {
		t.Fatal("reopening made another inbox workspace")
	}
}

func TestAddProjectIsIdempotentAndSaved(t *testing.T) {
	dir := t.TempDir()
	var events []protocol.Event
	r := open(t, dir, &events)
	events = nil
	root := filepath.Join(t.TempDir(), "app")
	p, w, err := r.AddProject(t.Context(), root+"/")
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != root || p.Name != "app" || w.Directory != root || w.Project != p.ID {
		t.Fatalf("added %+v %+v", p, w)
	}
	if len(events) != 2 || events[0].Project == nil || events[1].Workspace == nil {
		t.Fatalf("events = %+v, want the project then its workspace", events)
	}
	p2, w2, err := r.AddProject(t.Context(), root)
	if err != nil || p2.ID != p.ID || w2.ID != w.ID {
		t.Fatalf("adding again = %+v %+v %v, want the same", p2, w2, err)
	}
	if len(events) != 2 {
		t.Fatal("adding a known project announced a change")
	}
	if _, _, err := r.AddProject(t.Context(), "relative"); err == nil {
		t.Fatal("added a relative root")
	}

	again := open(t, dir, nil)
	if got, ok := again.Active(root); !ok || got.ID != w.ID {
		t.Fatalf("reopened workspace at root = %+v %v", got, ok)
	}
	if got := again.Projects(false); len(got) != 2 || got[0].ID != p.ID || !got[1].Inbox {
		t.Fatalf("reopened projects = %+v, want app then the inbox", got)
	}
}

func TestArchiveHidesAndAddRestores(t *testing.T) {
	r := open(t, t.TempDir(), nil)
	root := filepath.Join(t.TempDir(), "app")
	p, w, err := r.AddProject(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveProject(t.Context(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Active(root); ok {
		t.Fatal("an archived workspace is still active")
	}
	if got, ok := r.At(root); !ok || got.ID != w.ID || got.Archived == 0 {
		t.Fatalf("At(root) = %+v %v, want the archived workspace", got, ok)
	}
	// A conversation in an archived workspace stays there.
	if got, err := r.Adopt(t.Context(), root); err != nil || got.ID != w.ID {
		t.Fatalf("Adopt = %+v %v, want the archived workspace", got, err)
	}
	if len(r.Projects(false)) != 1 || len(r.Projects(true)) != 2 {
		t.Fatal("archived project listed as active, or missing from the archived list")
	}
	if err := r.RenameProject(t.Context(), p.ID, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renaming an archived project = %v", err)
	}
	p2, w2, err := r.AddProject(t.Context(), root)
	if err != nil || p2.ID != p.ID || w2.ID != w.ID || p2.Archived != 0 || w2.Archived != 0 {
		t.Fatalf("re-adding = %+v %+v %v, want the same project restored", p2, w2, err)
	}
}

func TestAdoptAddsAProjectForANewDirectory(t *testing.T) {
	r := open(t, t.TempDir(), nil)
	root := filepath.Join(t.TempDir(), "cli")
	w, err := r.Adopt(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Active(root); !ok || got.ID != w.ID {
		t.Fatalf("Active = %+v %v", got, ok)
	}
	if _, err := r.Adopt(t.Context(), ""); err == nil {
		t.Fatal("adopted an empty directory")
	}
}

func TestRenames(t *testing.T) {
	r := open(t, t.TempDir(), nil)
	root := filepath.Join(t.TempDir(), "app")
	p, w, _ := r.AddProject(t.Context(), root)
	if err := r.RenameProject(t.Context(), p.ID, "  Web app "); err != nil {
		t.Fatal(err)
	}
	if got := r.Projects(false)[0]; got.Name != "Web app" {
		t.Fatalf("name = %q", got.Name)
	}
	if err := r.RenameProject(t.Context(), p.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := r.Projects(false)[0]; got.Name != "app" {
		t.Fatalf("cleared name = %q, want the base name", got.Name)
	}
	if err := r.RenameWorkspace(t.Context(), w.ID, "main"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Workspace(w.ID); got.Title != "main" {
		t.Fatalf("title = %q", got.Title)
	}
	if err := r.RenameWorkspace(context.Background(), "wks_missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("renaming a missing workspace = %v", err)
	}
}
