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
// before os.Exit, which skips defers. Subprocess entry points may also exit, but
// every explicit exit must be immediately preceded by the same flush. A child
// that fails and exits at once otherwise loses its own failing span, which is
// the record a lock investigation needs.
func TestEveryStoreProgramFlushesTheSQLiteTraceOnExit(t *testing.T) {
	mains, err := filepath.Glob(filepath.Join("..", "..", "tillr-*", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	checked := map[string]bool{}
	for _, path := range mains {
		module := filepath.Dir(path)
		imports, unflushed := scanModule(t, module)
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
		if unflushed != 0 {
			t.Errorf("%s: the program has %d os.Exit calls without an immediately preceding store.FlushSQLiteTraceAtExit", module, unflushed)
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
// and how many os.Exit calls its non-test files make without flushing first.
func scanModule(t *testing.T, module string) (imports bool, unflushed int) {
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
		unflushed += unflushedExits(file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return imports, unflushed
}

// Count every exit expression, then subtract only exits whose immediately
// preceding statement in the same block is the flush. An exit nested in an
// argument, assignment, defer, or goroutine cannot borrow a neighboring flush.
func unflushedExits(file *ast.File) int {
	exits, flushed := 0, 0
	ast.Inspect(file, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && isCall(call, "os", "Exit") {
			exits++
		}
		if block, ok := node.(*ast.BlockStmt); ok {
			for i := 1; i < len(block.List); i++ {
				exit, ok := block.List[i].(*ast.ExprStmt)
				call, _ := exprCall(exit, ok)
				if call == nil || !isCall(call, "os", "Exit") {
					continue
				}
				flush, ok := block.List[i-1].(*ast.ExprStmt)
				before, _ := exprCall(flush, ok)
				if before != nil && isCall(before, "store", "FlushSQLiteTraceAtExit") {
					flushed++
				}
			}
		}
		return true
	})
	return exits - flushed
}

func TestTraceExitScanChecksEveryExit(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"one flushed", `store.FlushSQLiteTraceAtExit(); os.Exit(0)`, 0},
		{"nested entry", `if helper { store.FlushSQLiteTraceAtExit(); os.Exit(1) }; store.FlushSQLiteTraceAtExit(); os.Exit(0)`, 0},
		{"missing helper flush", `if helper { os.Exit(1) }; store.FlushSQLiteTraceAtExit(); os.Exit(0)`, 1},
		{"wrong flush", `other.FlushSQLiteTraceAtExit(); os.Exit(0)`, 1},
		{"intervening work", `store.FlushSQLiteTraceAtExit(); work(); os.Exit(0)`, 1},
		{"different branch", `if helper { store.FlushSQLiteTraceAtExit() }; os.Exit(0)`, 1},
		{"deferred exit", `store.FlushSQLiteTraceAtExit(); defer os.Exit(0)`, 1},
		{"nested exit", `store.FlushSQLiteTraceAtExit(); wrapper(os.Exit(0))`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "entry.go", "package entry; func main(){"+tc.body+"}", 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := unflushedExits(file); got != tc.want {
				t.Fatalf("unflushed=%d want %d", got, tc.want)
			}
		})
	}
}
