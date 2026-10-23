package kittest

import (
	"os"
	"path/filepath"
	"testing"
)

// recorder counts the guard's complaints without failing the test.
type recorder struct {
	testing.TB
	errors int
}

func (r *recorder) Errorf(string, ...any) { r.errors++ }
func (r *recorder) Helper()               {}

func TestAnAppsOwnPaletteFileIsChecked(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":            "module example.com/app\n",
		"native/palette.go": "package native\n\nimport \"github.com/egoist/mygo/ui\"\n\nvar red = ui.Hex(\"#f00\")\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &recorder{TB: t}
	ColorsStayInThePalette(r, root)
	if r.errors != 1 {
		t.Fatalf("an app's native/palette.go raised %d complaints, want 1", r.errors)
	}
}

func TestAPluginsGoStatementIsCaught(t *testing.T) {
	dir := t.TempDir()
	body := "package p\n\nfunc f() { go func() {}() }\n"
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recorder{TB: t}
	PluginsSpawnTheirGoroutines(r, dir)
	if r.errors != 1 {
		t.Fatalf("a go statement raised %d complaints, want 1", r.errors)
	}
}

// TestKitPluginsSpawnTheirGoroutines holds the kit's own plugins to it.
func TestKitPluginsSpawnTheirGoroutines(t *testing.T) {
	PluginsSpawnTheirGoroutines(t, "../plugins")
}
