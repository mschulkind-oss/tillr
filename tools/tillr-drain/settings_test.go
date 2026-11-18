package main_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestLoopSettingsRefusedBeforeStartingOrChecking(t *testing.T) {
	cases := []struct{ body, key, expected string }{
		{"[unknown]", "unknown", "table"},
		{"[sync]\nroadmaps = 'docs/roadmap.md'", "sync.roadmaps", "array of strings"},
		{"[sync]\nroadmaps = ['docs/roadmap.md', 1]", "sync.roadmaps", "array of strings"},
		{"[sync]\nroadmaps = false", "sync.roadmaps", "array of strings"},
		{"[roll]\ncommmand = 'x'", "roll.commmand", "command"},
		{"[push]\nafter_landing = 'yes'", "push.after_landing", "boolean"},
		{"[roll]\ncommand = 'x'", "roll.command", "roll.paths"},
	}
	for _, check := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(tc.key+stringBool(check), func(t *testing.T) {
				root := scratch(t, "settings-refusal")
				path := filepath.Join(root, ".tillr.toml")
				writeFile(t, path, tc.body)
				args := []string{"loop", "--root", root, "--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true"}
				if check {
					args = append(args, "--check")
				}
				code, out := invoke(t, args...)
				if code != 2 {
					t.Fatalf("exit %d, want usage 2: %s", code, out)
				}
				for _, word := range []string{path, tc.key, tc.expected, "expected"} {
					if !strings.Contains(string(out), word) {
						t.Errorf("output missing %q: %s", word, out)
					}
				}
				if _, err := os.Stat(filepath.Join(root, ".tillr", "state.db")); !os.IsNotExist(err) {
					t.Fatalf("invalid settings started the loop: %v", err)
				}
			})
		}
	}
}

func TestLoopCheckRefusesIncompatibleStoreWithoutChangingIt(t *testing.T) {
	for _, ddl := range []string{
		`CREATE TABLE schema_migrations(wrong INTEGER)`,
		`CREATE TABLE schema_migrations(version INTEGER); INSERT INTO schema_migrations VALUES (0)`,
		`CREATE TABLE schema_migrations(version INTEGER); INSERT INTO schema_migrations VALUES (999)`,
	} {
		t.Run(ddl, func(t *testing.T) {
			root := scratch(t, "check-schema")
			path := filepath.Join(root, "store.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			policy := filepath.Join(root, "policy.json")
			writeFile(t, policy, `{"default_backend":"fixture","preference":["fixture"],"backends":{"fixture":{"adapter":"/bin/true","model":"fixture"}}}`)
			code, out := invoke(t, "loop", "--check", "--root", root, "--store", path, "--policy", policy,
				"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")
			if code != 3 {
				t.Errorf("check exit %d, want incompatible 3: %s", code, out)
			}
			if !strings.Contains(string(out), "schema") || !strings.Contains(string(out), path) {
				t.Errorf("refusal does not name schema and store: %s", out)
			}
			if strings.Count(strings.TrimSpace(string(out)), "\n") != 0 {
				t.Errorf("refusal must be one line: %s", out)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Error("check changed the incompatible database")
			}
			if _, err := os.Stat(filepath.Join(root, ".tillr")); !os.IsNotExist(err) {
				t.Errorf("check wrote loop state: %v", err)
			}
		})
	}
}

func stringBool(b bool) string {
	if b {
		return "-check"
	}
	return "-loop"
}

func TestLoopSettingsStartupAndCheck(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, check := range []bool{false, true} {
			t.Run(stringBool(configured)+stringBool(check), func(t *testing.T) {
				f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
				policy := filepath.Join(scratch(t, "settings-policy"), "policy.json")
				raw, err := json.Marshal(f.policy)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, policy, string(raw))
				s, err := store.Open(context.Background(), f.store, true)
				if err != nil {
					t.Fatal(err)
				}
				s.Close()
				if configured {
					writeFile(t, filepath.Join(f.root, ".tillr.toml"), `[roll]
paths = ["tools/tillr*/"]
command = "false"
pause_timeout_seconds = 30
[push]
after_landing = true
remote = "origin"
ref = "refs/heads/main"
`)
				}
				args := []string{"loop", "--root", f.root, "--store", f.store, "--policy", policy,
					"--lane-root", f.laneRoot, "--item-bin", sibling(t, "item"),
					"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true", "--trace-bin=", "--rolling=", "--exit-when-drained"}
				if check {
					args = append(args, "--check")
				}
				code, out := invoke(t, args...)
				if code != 0 {
					t.Fatalf("exit %d: %s", code, out)
				}
				var lines []string
				for _, line := range strings.Split(string(out), "\n") {
					if strings.HasPrefix(line, "settings:") {
						lines = append(lines, line)
					}
				}
				if len(lines) != 1 {
					t.Fatalf("want one settings line: %s", out)
				}
				expected := []string{`sync.roadmaps=[]`, `roll.command=none`, `roll.paths=[]`, "roll.pause_timeout_seconds=120", "push.after_landing=false", "push.remote=upstream", "push.ref=upstream"}
				if configured {
					expected = []string{`sync.roadmaps=[]`, `roll.command="false"`, `roll.paths=["tools/tillr*/"]`, "roll.pause_timeout_seconds=30", "push.after_landing=true", `push.remote="origin"`, `push.ref="refs/heads/main"`}
				}
				for _, word := range expected {
					if !strings.Contains(lines[0], word) {
						t.Errorf("settings line missing %q: %s", word, lines[0])
					}
				}
				if check {
					if strings.Contains(string(out), "tick(s)") {
						t.Fatalf("check started loop: %s", out)
					}
					if entries, err := os.ReadDir(f.laneRoot); err != nil || len(entries) != 0 {
						t.Fatalf("check staged lanes: %v, %v", entries, err)
					}
				} else if !strings.Contains(string(out), "queue-drained") {
					t.Fatalf("loop did not finish a pass: %s", out)
				}
			})
		}
	}
}
