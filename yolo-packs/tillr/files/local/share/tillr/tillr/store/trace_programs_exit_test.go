package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// This test reads the programs' main.go files from disk, so it judges the tree on
// disk rather than a build overlay.
// Every tillr program that opens the store flushes the trace on its way out: main
// defers FlushSQLiteTraceAtExit (a panic unwinding main) and calls it immediately
// before its only os.Exit, which skips defers, and nothing else in the program calls
// os.Exit. A child that fails and exits at once otherwise loses its own failing
// span, which is the record a lock investigation needs.
func TestEveryStoreProgramFlushesTheSQLiteTraceOnExit(t *testing.T) {
	mains, err := filepath.Glob(filepath.Join("..", "..", "tillr-*", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	checked := map[string]bool{}
	for _, path := range mains {
		module := filepath.Dir(path)
		imports, exits := scanModule(t, module)
		if !imports {
			continue
		}
		checked[filepath.Base(module)] = true
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var body []ast.Stmt
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "main" && fn.Recv == nil {
				body = fn.Body.List
			}
		}
		if len(body) < 3 {
			t.Errorf("%s: main is not the flush-then-exit shape", path)
			continue
		}
		deferred, first := body[0].(*ast.DeferStmt)
		if !first || !isCall(deferred.Call, "store", "FlushSQLiteTraceAtExit") {
			t.Errorf("%s: main does not begin by deferring store.FlushSQLiteTraceAtExit", path)
		}
		last, ok := body[len(body)-1].(*ast.ExprStmt)
		exitCall, _ := exprCall(last, ok)
		flush, ok := body[len(body)-2].(*ast.ExprStmt)
		flushCall, _ := exprCall(flush, ok)
		if exitCall == nil || !isCall(exitCall, "os", "Exit") || flushCall == nil || !isCall(flushCall, "store", "FlushSQLiteTraceAtExit") {
			t.Errorf("%s: main does not call store.FlushSQLiteTraceAtExit immediately before os.Exit", path)
		}
		if exits != 1 {
			t.Errorf("%s: the program calls os.Exit %d times outside tests; only main's flushed exit may", module, exits)
		}
	}
	for _, want := range []string{"tillr-drain", "tillr-item", "tillr-land", "tillr-run", "tillr-store", "tillr-view"} {
		if !checked[want] {
			t.Errorf("%s was not checked: the scan no longer finds a store program it must", want)
		}
	}
}

func exprCall(stmt *ast.ExprStmt, ok bool) (*ast.CallExpr, bool) {
	if !ok {
		return nil, false
	}
	call, ok := stmt.X.(*ast.CallExpr)
	return call, ok
}

func isCall(call *ast.CallExpr, pkg, name string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	return ok && ident.Name == pkg && selector.Sel.Name == name
}

// scanModule reports whether any non-test package in the module imports the store,
// and how many os.Exit calls its non-test files make.
func scanModule(t *testing.T, module string) (imports bool, exits int) {
	t.Helper()
	err := filepath.WalkDir(module, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) && path != module {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			if spec.Path.Value == `"github.com/mschulkind-oss/tillr/store"` {
				imports = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok && isCall(call, "os", "Exit") {
				exits++
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return imports, exits
}
