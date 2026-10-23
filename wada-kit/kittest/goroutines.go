package kittest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// PluginsSpawnTheirGoroutines keeps the plugins under dir, an app's plugins
// folder, to goroutines the kernel runs: a go statement in any of their Go
// files, tests aside, is a goroutine whose panic ends the app instead of
// failing its plugin, and that Close does not wait for. Plugins start
// workers with kernel.Scope.Spawn or kernel.Go.
func PluginsSpawnTheirGoroutines(t testing.TB, dir string) {
	t.Helper()
	fset := token.NewFileSet()
	scanned := 0
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		scanned++
		ast.Inspect(f, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				t.Errorf("%s: a plugin starts goroutines with kernel.Scope.Spawn or kernel.Go", fset.Position(g.Pos()))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatalf("goroutine guard did not scan any Go source under %s", dir)
	}
}
