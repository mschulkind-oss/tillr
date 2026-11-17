package run_test

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// This structural test proves the correction's entire call boundary, rather than
// sampling processes before/after and missing a short-lived child. All calls are
// SQL, pure in-memory operations or the store's transaction boundary. In particular,
// no process starter, artifact reader, Git helper or injected callback can be reached
// from either write transaction. The store's own SQLite I/O is not artifact work.
func TestCopiedUsageTransactionStartsNoProcess(t *testing.T) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "copied_usage.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowedImports := map[string]bool{
		"context": true, "database/sql": true, "encoding/json": true, "strings": true, "time": true,
		"github.com/mschulkind-oss/tillr/event": true, "github.com/mschulkind-oss/tillr/store": true,
	}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if !allowedImports[path] || imp.Name != nil {
			t.Fatalf("correction can reach an unaudited dependency: %s", path)
		}
	}
	allowedCalls := map[string]bool{
		"append": true, "detectCopiedUsage": true, "markCopiedUsage": true,
		"Internalf": true, "Usagef": true,
		"strings.TrimSpace": true, "json.Marshal": true,
		"s.ReadTx": true, "s.WriteTx": true,
		"r.Query": true, "tx.Reader": true, "tx.QueryRow": true, "tx.Append": true, "tx.Exec": true,
		"rows.Close": true, "rows.Next": true, "rows.Scan": true, "rows.Err": true,
		"tx.QueryRow(`SELECT started_at,usage_tokens,usage_cost_usd FROM runs WHERE run_id=?`, row.id).Scan": true,
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var out bytes.Buffer
		if err := format.Node(&out, fs, call.Fun); err != nil {
			t.Fatal(err)
		}
		if !allowedCalls[out.String()] {
			t.Errorf("call inside correction can escape the no-process/no-artifact boundary: %s", out.String())
		}
		return true
	})
}
