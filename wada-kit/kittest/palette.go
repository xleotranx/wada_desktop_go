package kittest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// kitModule is the kit's module path: only its native/palette.go defines colors.
const kitModule = "github.com/xleotranx/wada-kit"

// ColorsStayInThePalette keeps every color of a module's interface in
// native.PaletteFor: a ui.Hex or ui.RGBA in any Go file under root, the
// module's root, means a pane has grown its own theme. Folders of other
// modules, hidden folders and build are skipped; tests may name a literal
// color to check a pixel, and the kit's own native/palette.go defines them.
func ColorsStayInThePalette(t testing.TB, root string) {
	t.Helper()
	// The palette's own file is skipped only when root is the kit; an app's
	// native/palette.go would be a second theme.
	palette := ""
	if mod, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil &&
		strings.HasPrefix(string(mod), "module "+kitModule+"\n") {
		palette = filepath.Join(root, "native", "palette.go")
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "build" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir // another module checks its own
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && filepath.Clean(path) != palette {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("palette guard did not scan any Go source")
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "ui" && (sel.Sel.Name == "Hex" || sel.Sel.Name == "RGBA") {
				t.Errorf("%s: ui.%s belongs in native.PaletteFor", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}
