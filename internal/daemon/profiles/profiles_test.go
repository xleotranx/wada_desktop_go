package profiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wada-desktop-go/internal/protocol"
)

func TestLoad(t *testing.T) {
	data, dir := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(data, UserFile), `{"reviewer":{"provider":"codex","instructions":"mine"},"planner":{"instructions":"Plan."}}`)
	write(filepath.Join(dir, "wada.json"), `{"setup":"make","agents":{"reviewer":{"provider":"claude","mode":"ask","instructions":"Review."}}}`)

	list, err := Load(data, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "planner" || list[0].Source != protocol.ProfileUser ||
		list[1].Name != "reviewer" || list[1].Provider != "claude" || list[1].Source != protocol.ProfileProject {
		t.Fatalf("profiles = %+v", list)
	}
	if user, _ := Load(data, ""); len(user) != 2 || user[1].Provider != "codex" {
		t.Fatalf("the user's only = %+v", user)
	}
	if _, err := Find(data, dir, "nobody"); err == nil {
		t.Fatal("found a missing profile")
	}
	if got := Prompt(list[1], "Look at main.go"); got != "Review.\n\n---\n\nLook at main.go" {
		t.Fatalf("prompt = %q", got)
	}
	if got := Prompt(protocol.AgentProfile{}, "as is"); got != "as is" {
		t.Fatalf("prompt without instructions = %q", got)
	}

	write(filepath.Join(dir, "wada.json"), `{"agents":{"x":{"mode":"yolo"}}}`)
	if _, err := Load(data, dir); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("a bad mode loaded: %v", err)
	}
	if list, err := Load(t.TempDir(), t.TempDir()); err != nil || len(list) != 0 {
		t.Fatalf("no files = %+v, %v", list, err)
	}
}
