package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func buildUtility(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, "tillr-"+name)
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = filepath.Join("..", "tillr-"+name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v: %s", name, err, out)
	}
	return path
}

func invokeUtility(t *testing.T, binary string, want int, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+t.TempDir())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != want || strings.Contains(stderr.String(), "no such table") {
		t.Fatalf("%s %v: exit %d, want %d; stdout: %s; stderr: %s", binary, args, code, want, out, stderr.String())
	}
	return out
}

func assertEmptyArray(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var rows []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &rows) != nil || len(rows) != 0 {
		t.Fatalf("want an empty answer, got %s", raw)
	}
}

// Build and run the real read faces, not package calls whose imports might accidentally
// register the missing tables. Their logical fingerprints must not change either.
func TestInitAloneSupportsEveryReadFace(t *testing.T) {
	dir := t.TempDir()
	binaries := map[string]string{}
	for _, name := range []string{"drain", "view", "run"} {
		binaries[name] = buildUtility(t, dir, name)
	}
	path := filepath.Join(dir, "store.db")
	invokeUtility(t, bin, 0, "init", "--store", path)
	before, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, utility string
		args          []string
		code          int
	}{
		{"why", "drain", []string{"why", "--json"}, 0},
		{"waiting", "view", []string{"waiting", "--json"}, 0},
		{"status", "view", []string{"status"}, 0},
		{"lanes", "run", []string{"lanes"}, 0},
		{"absent-run", "run", []string{"status", "--run", "absent"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(append([]string{}, tc.args...), "--store", path)
			out := invokeUtility(t, binaries[tc.utility], tc.code, args...)
			if tc.name == "waiting" {
				assertEmptyArray(t, out)
			} else {
				var answer map[string]json.RawMessage
				if err := json.Unmarshal(out, &answer); err != nil {
					t.Fatalf("decode answer: %v: %s", err, out)
				}
				if tc.name == "why" {
					assertEmptyArray(t, answer["rows"])
				} else {
					var payload map[string]json.RawMessage
					if err := json.Unmarshal(answer["payload"], &payload); err != nil {
						t.Fatal(err)
					}
					switch tc.name {
					case "status":
						if string(answer["kind"]) != `"view.status"` {
							t.Fatalf("wrong record: %s", out)
						}
						for _, key := range []string{"items", "candidates", "runs"} {
							assertEmptyArray(t, payload[key])
						}
					case "lanes":
						if string(answer["kind"]) != `"run.lanes"` {
							t.Fatalf("wrong record: %s", out)
						}
						assertEmptyArray(t, payload["lanes"])
					case "absent-run":
						if string(answer["kind"]) != `"run.status"` || string(payload["run_id"]) != `"absent"` || string(payload["found"]) != "false" || string(payload["run"]) != "null" {
							t.Fatalf("want absent-run record: %s", out)
						}
					}
				}
			}
			after, err := store.Fingerprint(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("read face changed the schema or stored rows")
			}
		})
	}
}

// schemaColumns includes every pragma field (name, type, default, nullability and key),
// not just a table count that could hide a missing registered column.
func schemaColumns(t *testing.T, path string) map[string][]string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result := map[string][]string{}
	for _, table := range tables {
		columns, err := db.Query(`SELECT cid, name, type, "notnull", dflt_value, pk FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		for columns.Next() {
			var cid, notnull, pk int
			var name, kind string
			var def sql.NullString
			if err := columns.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal([]any{cid, name, kind, notnull, def, pk})
			result[table] = append(result[table], string(raw))
		}
		if err := columns.Err(); err != nil {
			t.Fatal(err)
		}
		columns.Close()
	}
	return result
}

// Build a separate process with exactly the requested owners. The core-only form
// reproduces a store made by the old store binary; the all-owner form provides an
// independent schema reference without relying on the imports of the binary under test.
func createWithOwners(t *testing.T, dir, path string, all bool) {
	t.Helper()
	imports := ""
	if all {
		for _, owner := range []string{"drain", "item", "land", "run"} {
			imports += `_ "github.com/mschulkind-oss/tillr-` + owner + `/` + owner + `"` + "\n"
		}
	}
	source := `package main
import (
 "context"
 "os"
 "github.com/mschulkind-oss/tillr/store"
` + imports + `)
func main() {
 s, err := store.Open(context.Background(), os.Args[1], true)
 if err != nil { panic(err) }
 if err := s.Close(); err != nil { panic(err) }
}
`
	file := filepath.Join(dir, "create.go")
	if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "create")
	cmd := exec.Command("go", "build", "-o", binary, file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build owner fixture: %v: %s", err, out)
	}
	invokeUtility(t, binary, 0, path)
}

// The real empty loop opens and drains a fixture repository, using freshly built
// siblings. No installed tool or real board state participates in these tests.
func drainEmptyStore(t *testing.T, dir, path string) {
	t.Helper()
	root := filepath.Join(dir, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"add", ".gitignore"}, {"commit", "-m", "fixture"}} {
		if args[0] == "add" {
			if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".tillr/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git %v: %v: %s", args, err, out)
		}
	}
	policy := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policy, []byte(`{"default_backend":"fixture","preference":["fixture"],"backends":{"fixture":{"adapter":"/bin/true","model":"fixture"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	drain := buildUtility(t, dir, "drain")
	item := buildUtility(t, dir, "item")
	run := buildUtility(t, dir, "run")
	out := invokeUtility(t, drain, 0, "loop", "--root", root, "--store", path,
		"--lane-root", filepath.Join(dir, "lanes"), "--lanes", "1", "--policy", policy,
		"--item-bin", item, "--run-bin", run, "--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true",
		"--rolling=", "--replay=", "--exit-when-drained")
	if !strings.Contains(string(out), "queue-drained") || !strings.Contains(string(out), "dispatched 0") {
		t.Fatalf("empty store did not drain: %s", out)
	}
}

func TestInitSchemaMatchesEveryOwnerAndTheLoop(t *testing.T) {
	dir := t.TempDir()
	fresh, reference := filepath.Join(dir, "init.db"), filepath.Join(dir, "loop.db")
	invokeUtility(t, bin, 0, "init", "--store", fresh)
	createWithOwners(t, dir, reference, true)
	drainEmptyStore(t, dir, reference)
	assertSameSchema(t, fresh, reference)
}

func TestAnOldCoreOnlyStoreStillOpensAndDrains(t *testing.T) {
	dir := t.TempDir()
	legacy, fresh := filepath.Join(dir, "old.db"), filepath.Join(dir, "init.db")
	createWithOwners(t, dir, legacy, false)
	before := schemaColumns(t, legacy)
	if len(before) != 4 || before["meta"] == nil || before["schema_migrations"] == nil || before["events"] == nil || before["sqlite_sequence"] == nil {
		t.Fatalf("old fixture must hold only meta, schema_migrations, events and SQLite's sequence table: %v", before)
	}
	invokeUtility(t, bin, 0, "check", "--store", legacy)
	drainEmptyStore(t, dir, legacy)
	invokeUtility(t, bin, 0, "init", "--store", legacy)
	invokeUtility(t, bin, 0, "init", "--store", fresh)
	assertSameSchema(t, legacy, fresh)
}

func assertSameSchema(t *testing.T, a, b string) {
	t.Helper()
	got, want := schemaColumns(t, a), schemaColumns(t, b)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema differs: init %v; owners/loop %v", got, want)
	}
}
