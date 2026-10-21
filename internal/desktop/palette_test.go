package desktop

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestColorsStayInThePalette keeps the convention true: every surface's
// color comes from paletteFor, so a ui.Hex or ui.RGBA anywhere but
// palette.go means a pane has grown its own theme. Tests may name a
// literal color to check a pixel; the app's surfaces may not.
func TestColorsStayInThePalette(t *testing.T) {
	sourceRoot := filepath.Join("..", "..")
	var files []string
	err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != sourceRoot && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
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
		if filepath.Clean(name) == filepath.Join("..", "..", "internal", "native", "palette.go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, 0)
		if perr != nil {
			t.Fatalf("%s: %v", name, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && pkg.Name == "ui" && (sel.Sel.Name == "Hex" || sel.Sel.Name == "RGBA") {
				t.Errorf("%s: ui.%s belongs in paletteFor", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}
