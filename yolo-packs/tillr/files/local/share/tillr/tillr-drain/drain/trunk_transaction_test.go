package drain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Startup's history observation must never run Git or access files while the
// SQLite writer is held. Inspect the callback, not the enclosing function:
// observation deliberately still pins refs before committing its row.
func TestTrunkObservationWriteCallbackContainsOnlyStoreWork(t *testing.T) {
	source, err := parser.ParseFile(token.NewFileSet(), "trunk_history.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	ast.Inspect(source, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "observeTrunk" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "WriteTx" {
				return true
			}
			callbacks++
			callback, ok := call.Args[1].(*ast.FuncLit)
			if !ok {
				t.Fatal("expected an inspectable write callback")
			}
			ast.Inspect(callback.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch f := c.Fun.(type) {
				case *ast.Ident:
					if f.Name != "readTrunkHistory" {
						t.Errorf("unexpected call inside writer: %s", f.Name)
					}
				case *ast.SelectorExpr:
					// record is rewriteAbsorption.record: Exec and Append on the transaction,
					// no Git. Now reads the loop's clock.
					if f.Sel.Name != "Reader" && f.Sel.Name != "Exec" && f.Sel.Name != "record" && f.Sel.Name != "Now" {
						t.Errorf("unexpected method inside writer: %s", f.Sel.Name)
					}
				default:
					t.Errorf("unexpected call inside writer: %T", c.Fun)
				}
				return true
			})
			return false
		})
		return false
	})
	if callbacks != 1 {
		t.Fatalf("inspected %d write callbacks, want 1", callbacks)
	}
}
