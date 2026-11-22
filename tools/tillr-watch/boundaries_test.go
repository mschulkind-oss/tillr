package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchExplicitRootWinsOverGitEnvironment(t *testing.T) {
	s, dir := fixture(t, false)
	const lag = `[[rule]]
id = "lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_commits = 1
[rule.guidance]
summary = "unproved trunk"
check = ["inspect"]
do = ["consider"]
[rule.delivery]
receiver = "foreground"
`
	seedStatusOverride(t, s, "lag", lag)
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"status", "--store", s.Path(), "--rules", dir}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "--root") {
		t.Fatalf("missing root: %d, stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	git := func(root string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	root, other := t.TempDir(), t.TempDir()
	for _, repo := range []string{root, other} {
		git(repo, "init", "--quiet", "-b", "main")
		git(repo, "commit", "--quiet", "--allow-empty", "-m", "base")
	}
	git(root, "update-ref", "refs/tillr/green", "HEAD")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	args := []string{"--store", s.Path(), "--rules", dir, "--root", root}
	stdout.Reset()
	stderr.Reset()
	if code := run(t.Context(), append([]string{"status"}, args...), &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "rule lag skipped=0\n") || strings.Contains(stdout.String(), "alert ") {
		t.Fatalf("explicit root: %d, stdout=%s stderr=%s", code, &stdout, &stderr)
	}
}

func TestWatchStatusCheckpointedWALLeavesStoreUntouched(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "0")
	s, rules := fixture(t, false)
	seed := s.Path()
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	var stderr bytes.Buffer
	if code := run(t.Context(), []string{"eval", "--once", "--store", seed, "--rules", rules}, &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("seed eval: exit %d: %s", code, &stderr)
	}
	seedStatusOverride(t, s, "refusals", testRule)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// A checkpointed copy starts with the full store in the main file and no
	// WAL of its own.
	db, err := sql.Open("sqlite", seed)
	if err != nil {
		t.Fatal(err)
	}
	var busy, frames, checkpointed int
	err = db.QueryRowContext(t.Context(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed)
	if closeErr := db.Close(); err != nil || closeErr != nil || busy != 0 || frames != 0 || checkpointed != 0 {
		t.Fatalf("checkpoint: %d %d %d, query: %v, close: %v", busy, frames, checkpointed, err, closeErr)
	}
	before, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 20 || before[18] != 2 || before[19] != 2 {
		t.Fatal("fixture is not in WAL mode")
	}
	wantRows := watchStoreRows(t, seed)
	path := filepath.Join(t.TempDir(), "state?#.db")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if code := run(t.Context(), []string{"status", "--json", "--rules", rules, "--store", path}, &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"subject":"item-a"`) {
		t.Fatalf("status: exit %d, stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("main database bytes changed: %v", err)
	}
	// Check before the inspection itself opens SQLite, which can create sidecars.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			t.Errorf("status created %s", entry.Name())
		}
	}
	if got := watchStoreRows(t, path); !reflect.DeepEqual(wantRows, got) {
		t.Error("logical database contents changed")
	}
	t.Run("writer starts during read", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.db")
		if err := os.WriteFile(path, before, 0600); err != nil {
			t.Fatal(err)
		}
		err := store.ReadWithoutCreating(t.Context(), path, func(*store.Store) error {
			writer, err := store.Open(t.Context(), path, false)
			if err != nil {
				return err
			}
			t.Cleanup(func() { writer.Close() })
			fact(t, writer, `{"item_id":"newly-committed"}`)
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "changed during checkpointed read") {
			t.Fatalf("accepted checkpoint-only report after a writer committed: %v", err)
		}
	})
}

func TestWatchStatusReadsCommittedLiveWAL(t *testing.T) {
	s, rules := fixture(t, false)
	path := s.Path()
	read := func(path string) []byte {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	mainBefore := read(path)
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	var stdout, stderr bytes.Buffer
	common := []string{"--store", path, "--rules", rules}
	if code := run(t.Context(), append([]string{"eval", "--once"}, common...), &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("eval: exit %d, stdout=%s stderr=%s", code, &stdout, &stderr)
	}
	if !bytes.Equal(mainBefore, read(path)) {
		t.Fatal("fixture checkpointed the alert; it must be visible only through the WAL")
	}
	walBefore, shmBefore := read(path+"-wal"), read(path+"-shm")
	child := startCLIChild(t, append([]string{"status", "--json"}, common...)...)
	if err := child.wait(t); err != nil || child.stderr.Len() != 0 || !strings.Contains(child.out.String(), `"subject":"item-a"`) {
		t.Fatalf("status missed the committed WAL alert: %v, stdout=%s stderr=%s", err, &child.out, &child.stderr)
	}
	if !bytes.Equal(mainBefore, read(path)) || !bytes.Equal(walBefore, read(path+"-wal")) || !bytes.Equal(shmBefore, read(path+"-shm")) {
		t.Error("status modified the main database, WAL, or shared index")
	}
	t.Run("WAL without its index", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.db")
		for name, data := range map[string][]byte{path: mainBefore, path + "-wal": walBefore} {
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		tmp := t.TempDir()
		t.Setenv("TMPDIR", tmp)
		stdout.Reset()
		stderr.Reset()
		if code := run(t.Context(), []string{"status", "--json", "--store", path, "--rules", rules}, &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"subject":"item-a"`) {
			t.Errorf("status missed the committed WAL alert: exit %d, stdout=%s stderr=%s", code, &stdout, &stderr)
		}
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 || entries[0].Name() != "state.db" || entries[1].Name() != "state.db-wal" {
			t.Errorf("status changed the store directory: %v", entries)
		}
		if !bytes.Equal(mainBefore, read(path)) || !bytes.Equal(walBefore, read(path+"-wal")) {
			t.Error("status modified the main database or WAL")
		}
		if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
			t.Errorf("status left a private copy behind: %v, %v", entries, err)
		}
	})
}

func watchStoreRows(t *testing.T, path string) map[string][]string {
	t.Helper()
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	read := func(query string, accept func(*sql.Rows)) {
		t.Helper()
		rows, err := db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		accept(rows)
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	var tables []string
	read("SELECT name FROM sqlite_schema WHERE type = 'table'", func(rows *sql.Rows) {
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			tables = append(tables, name)
		}
	})
	contents := make(map[string][]string)
	for _, table := range tables {
		var records []string
		read(`SELECT * FROM "`+strings.ReplaceAll(table, `"`, `""`)+`"`, func(rows *sql.Rows) {
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			values, dest := make([]any, len(cols)), make([]any, len(cols))
			for i := range dest {
				dest[i] = &values[i]
			}
			for rows.Next() {
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				records = append(records, fmt.Sprintf("%#v", values))
			}
		})
		slices.Sort(records)
		contents[table] = records
	}
	return contents
}

func TestBoundaries(t *testing.T) {
	watchPkgDir := filepath.Clean(filepath.Join("..", "tillr", "watch"))
	fset := token.NewFileSet()

	checkDir := func(dir string, allowCommandContext map[string]bool, checkSiblingImports bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("readdir %s: %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			name := entry.Name()
			path := filepath.Join(dir, name)
			parsed, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			execAliases := map[string]bool{}
			for _, imp := range parsed.Imports {
				impPath, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("unquote import in %s: %v", path, err)
				}
				if checkSiblingImports && strings.HasPrefix(impPath, "github.com/mschulkind-oss/tillr-") {
					if imp.Name == nil || imp.Name.Name != "_" {
						t.Errorf("%s imports sibling %s without blank identifier _", path, impPath)
					}
				}
				if impPath == "os/exec" {
					alias := "exec"
					if imp.Name != nil {
						alias = imp.Name.Name
					}
					if alias != "_" && alias != "." {
						execAliases[alias] = true
					}
				}
			}
			if len(execAliases) == 0 {
				continue
			}
			ast.Inspect(parsed, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || !execAliases[id.Name] {
					return true
				}
				switch sel.Sel.Name {
				case "Command":
					t.Errorf("%s uses forbidden os/exec.Command", path)
				case "CommandContext":
					if !allowCommandContext[name] {
						t.Errorf("%s uses forbidden os/exec.CommandContext", path)
					}
				}
				return true
			})
		}
	}

	checkDir(".", nil, true)
	checkDir(watchPkgDir, map[string]bool{"git.go": true, "lint_charter.go": true}, false)
}
