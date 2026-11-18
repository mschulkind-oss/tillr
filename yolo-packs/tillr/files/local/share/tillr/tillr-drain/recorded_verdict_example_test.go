package main_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/internal/testinputs"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRecordedVerdictExampleREADME(t *testing.T) {
	if err := testinputs.Track("."); err != nil {
		t.Fatal(err)
	}
	// Use the module-local alias so edits to example assets invalidate Go's test cache.
	source, err := filepath.Abs(".sibling-inputs/tillr/examples/recorded-verdict")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join(source, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(string(readme), "```sh\n")
	if len(blocks) != 2 {
		t.Fatal("README must have exactly one executable sh sequence")
	}
	sequence := strings.SplitN(blocks[1], "```", 2)[0]
	// The example is copied, not run from its source tree. Git discovery is bounded
	// at the scratch parent, so even repository-contained CI scratch is not a repo.
	parent, err := filepath.Abs(scratch(t, "recorded-verdict"))
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(parent, "example")
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(copied, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "empty")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	// Build all real faces before installing the Git sentinel, including observers.
	sibling(t, "store")
	sibling(t, "item")
	sibling(t, "run")
	sibling(t, "view")
	cli, err := filepath.Abs(sibling(t, "cli"))
	if err != nil {
		t.Fatal(err)
	}
	binaries, err := filepath.Abs(binDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(cli, filepath.Join(binaries, "tillr")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	sentinelDir := filepath.Join(parent, "sentinel")
	if err := os.Mkdir(sentinelDir, 0755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(parent, "git-called")
	if err := os.WriteFile(filepath.Join(sentinelDir, "git"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$GIT_CALLS\"\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", sentinelDir+":"+binaries+":"+os.Getenv("PATH"))
	t.Cleanup(func() {
		if raw, err := os.ReadFile(calls); !os.IsNotExist(err) {
			t.Errorf("Git ran: %s (%v)", raw, err)
		}
	})
	t.Setenv("TILLR_EXAMPLE", copied)
	t.Setenv("GIT_CALLS", calls)
	t.Setenv("GIT_CEILING_DIRECTORIES", parent)
	t.Setenv("BOARD_DB", "")
	t.Setenv("TILLR_SQLITE_TRACE", "")
	t.Setenv("TILLR_TRACE_DIR", "")
	t.Setenv("TILLR_PAUSE_REQUEST", "")
	t.Setenv("TILLR_SESSION_MODE", "new")
	t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
	cmd := exec.Command("sh", "-c", sequence)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("README sequence: %v\n%s", err, out)
	}
	if raw, err := os.ReadFile(calls); !os.IsNotExist(err) {
		t.Fatalf("Git ran: %s (%v)", raw, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !os.IsNotExist(err) {
		t.Fatalf("example became a repository: %v", err)
	}
	db := filepath.Join(root, ".tillr/state.db")
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	expected := map[string]string{"finding-confirmed": "confirmed", "finding-rejected": "rejected", "finding-inconclusive": "inconclusive"}
	verdicts := map[string]string{"confirmed": "done", "rejected": "not-done", "inconclusive": "blocked"}
	for id, state := range expected {
		raw := driveOK(t, sibling(t, "item"), "show", "--store", db, "--item", id)
		var shown struct {
			Payload struct {
				State    string `json:"state"`
				Reason   string `json:"lifecycle_reason"`
				By       string `json:"lifecycle_by"`
				Event    int    `json:"event_seq"`
				Criteria []struct {
					ID    string `json:"criterion_id"`
					State string `json:"state"`
				} `json:"criteria"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &shown); err != nil {
			t.Fatal(err)
		}
		if shown.Payload.State != state || shown.Payload.Reason == "" || shown.Payload.By != "definition:recorded-verdict" || shown.Payload.Event == 0 || len(shown.Payload.Criteria) != 2 {
			t.Fatalf("outcome %s: %s", id, raw)
		}
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			for _, criterion := range shown.Payload.Criteria {
				var count int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='item.verdict' AND json_extract(payload,'$.item_id')=? AND json_extract(payload,'$.criterion_id')=? AND json_extract(payload,'$.verdict')=? AND length(json_extract(payload,'$.reason'))>0`, id, criterion.ID, verdicts[state]).Scan(&count); err != nil {
					return err
				}
				if count != 1 {
					t.Errorf("%s/%s: %d verdict events", id, criterion.ID, count)
				}
			}
			var count int
			if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='item.update' AND json_extract(payload,'$.item_id')=? AND json_extract(payload,'$.state')=? AND json_extract(payload,'$.lifecycle_by')='definition:recorded-verdict'`, id, state).Scan(&count); err != nil {
				return err
			}
			if count != 1 {
				t.Errorf("%s: %d completion events", id, count)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range []string{"confirmed", "rejected", "inconclusive", "repeat-of", "tooling-fault"} {
		raw := driveOK(t, sibling(t, "item"), "list", "--store", db, "--state", state)
		var listed struct {
			Payload struct {
				Items []struct {
					State string `json:"state"`
				} `json:"items"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &listed); err != nil {
			t.Fatal(err)
		}
		want := 1
		if state == "repeat-of" || state == "tooling-fault" {
			want = 0
		}
		if len(listed.Payload.Items) != want {
			t.Fatalf("%s count: %s", state, raw)
		}
		for _, item := range listed.Payload.Items {
			if item.State != state {
				t.Fatalf("state filter: %s", raw)
			}
		}
	}
	// Both workflow nodes must have run once for each finding, in the plain lane.
	raw, err := os.ReadFile(filepath.Join(root, "stage-calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 6 {
		t.Fatalf("want six workflow calls: %s", raw)
	}
	seen := map[string]int{}
	for i, line := range lines {
		var call struct {
			Item string `json:"item"`
			Node string `json:"node"`
			Cwd  string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		want := "reproduce"
		if i%2 == 1 {
			want = "investigate"
		}
		if call.Node != want || expected[call.Item] == "" || call.Cwd != filepath.Join(root, ".tillr/lanes/lane-1/work") {
			t.Fatalf("workflow call: %s", line)
		}
		seen[call.Item+":"+call.Node]++
	}
	for id := range expected {
		for _, node := range []string{"reproduce", "investigate"} {
			if seen[id+":"+node] != 1 {
				t.Errorf("%s/%s did not run exactly once", id, node)
			}
		}
	}
}
