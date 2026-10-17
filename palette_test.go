package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestColorsStayInThePalette keeps the convention true: every surface's
// color comes from paletteFor, so a ui.Hex or ui.RGBA anywhere but
// palette.go means a pane has grown its own theme. Tests may name a
// literal color to check a pixel; the app's surfaces may not.
func TestColorsStayInThePalette(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if name == "palette.go" || strings.HasSuffix(name, "_test.go") {
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
