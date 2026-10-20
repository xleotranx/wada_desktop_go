package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// projectDirs makes three fresh project folders and adds them in order.
func projectDirs(t *testing.T, a *app, names ...string) []string {
	t.Helper()
	dirs := make([]string, 0, len(names))
	for _, name := range names {
		dir := filepath.Join(t.TempDir(), name)
		a.addProject(dir)
		dirs = append(dirs, canonDir(dir))
	}
	return dirs
}

// TestProjectDragReorders drags the first local project over the last and
// expects it to take that slot.
func TestProjectDragReorders(t *testing.T) {
	a := newTestApp(t)
	projectDirs(t, a, "one", "two", "three")
	tt := ui.NewTester(a.view, 1400, 920)
	tt.Frame()
	src, ok := tt.Find("Hide chats in one")
	if !ok {
		t.Fatal("the first project is not drawn")
	}
	dst, ok := tt.Find("Hide chats in three")
	if !ok {
		t.Fatal("the last project is not drawn")
	}
	tt.Press(src.X+src.W/2, src.Y+src.H/2)
	tt.Move(src.X+src.W/2+12, src.Y+src.H/2)
	tt.Move(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Release(dst.X+dst.W/2, dst.Y+dst.H/2)
	tt.Frame()
	got := make([]string, 0, len(a.preferences.projects))
	for _, dir := range a.preferences.projects {
		got = append(got, filepath.Base(dir))
	}
	if !slices.Equal(got, []string{"two", "three", "one"}) {
		t.Fatalf("the dragged projects read %v", got)
	}
}

// TestMoveProjectBackwards puts a project back over its neighbour.
func TestMoveProjectBackwards(t *testing.T) {
	a := newTestApp(t)
	dirs := projectDirs(t, a, "one", "two", "three")
	a.moveProjectTo(dirs[2], dirs[0])
	got := make([]string, 0, len(a.preferences.projects))
	for _, dir := range a.preferences.projects {
		got = append(got, filepath.Base(dir))
	}
	if !slices.Equal(got, []string{"three", "one", "two"}) {
		t.Fatalf("backwards drag reads %v", got)
	}
}

// TestMoveProjectStaysInItsPinGroup refuses to cross the pinned line and
// says why.
func TestMoveProjectStaysInItsPinGroup(t *testing.T) {
	a := newTestApp(t)
	dirs := projectDirs(t, a, "one", "two", "three")
	a.preferences.projectPins[dirs[0]] = true
	a.preferences.projectPins[dirs[2]] = true
	before := slices.Clone(a.preferences.projects)
	a.moveProjectTo(dirs[1], dirs[0])
	if !slices.Equal(a.preferences.projects, before) {
		t.Fatalf("a pinned line was crossed: %v", a.preferences.projects)
	}
	if len(a.layout.toasts) == 0 {
		t.Fatal("the refusal said nothing")
	}
	a.moveProjectTo(dirs[0], dirs[2])
	got := make([]string, 0, len(a.preferences.projects))
	for _, dir := range a.preferences.projects {
		got = append(got, filepath.Base(dir))
	}
	if !slices.Equal(got, []string{"two", "three", "one"}) {
		t.Fatalf("a move inside the pinned group reads %v", got)
	}
}

// TestMoveProjectLeavesRemoteListsAlone keeps a server's own project order.
func TestMoveProjectLeavesRemoteListsAlone(t *testing.T) {
	a := newTestApp(t)
	dirs := projectDirs(t, a, "one", "two")
	a.connections.servers = append(a.connections.servers, serverConn{id: "srv-box", url: "http://10.0.0.12:4096"})
	a.connections.activeServer = "srv-box"
	before := slices.Clone(a.preferences.projects)
	a.moveProjectTo(dirs[0], dirs[1])
	if !slices.Equal(a.preferences.projects, before) {
		t.Fatalf("a remote list moved: %v", a.preferences.projects)
	}
}
