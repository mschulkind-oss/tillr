package main_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func scaleFiles(t *testing.T, root string) map[string]bool {
	t.Helper()
	files := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			files[path] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// Compare every check's declared writes with every other gate's reads and
// writes, resolving relative paths in the gate's own worktree. The gate also
// inventories created files at execution, so an undeclared output fails rather
// than making this a comparison of declarations alone.
func assertScaleIsolation(t *testing.T, f loopFixture, runs map[string][]scaleEvent) {
	t.Helper()
	type access struct {
		run           string
		reads, writes []string
	}
	accesses := []access{}
	for run := range runs {
		raw, err := os.ReadFile(filepath.Join(f.root, ".tillr", "loop", run, "gate.plan.json"))
		if err != nil {
			t.Fatal(err)
		}
		var plan struct {
			Payload struct {
				Checks []struct{ Reads, Writes []string }
			}
		}
		if err = json.Unmarshal(raw, &plan); err != nil {
			t.Fatal(err)
		}
		if len(plan.Payload.Checks) == 0 {
			t.Fatal("gate declared no checks")
		}
		for _, check := range plan.Payload.Checks {
			if len(check.Reads) == 0 || len(check.Writes) == 0 {
				t.Fatal("empty declaration")
			}
			lane := strings.Split(check.Reads[0], "/")[1]
			root := filepath.Join(f.laneRoot, lane, "worktree")
			a := access{run: run}
			for _, p := range check.Reads {
				a.reads = append(a.reads, filepath.Join(root, p))
			}
			for _, p := range check.Writes {
				a.writes = append(a.writes, filepath.Join(root, p))
			}
			accesses = append(accesses, a)
		}
	}
	overlap := func(a, b string) bool {
		return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
	}
	for _, a := range accesses {
		for _, b := range accesses {
			if a.run == b.run {
				continue
			}
			for _, w := range a.writes {
				for _, p := range append(append([]string{}, b.reads...), b.writes...) {
					if overlap(w, p) {
						t.Fatalf("gate %s writes %s, shared by %s at %s", a.run, w, b.run, p)
					}
				}
			}
		}
	}
}
