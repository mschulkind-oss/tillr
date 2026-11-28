package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPassRecordsSharedCoreWorktreeWithoutChangingConfig(t *testing.T) {
	f, l := passFixture(t)
	ctx := context.Background()
	config := filepath.Join(f.root, ".git", "config")
	readEvents := func() []map[string]string {
		t.Helper()
		s := openFixture(t, f.storePath)
		defer s.Close()
		var seen []map[string]string
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT payload FROM events WHERE kind = 'drain.trunk-core-worktree' ORDER BY seq`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var body []byte
				var payload map[string]string
				if err := rows.Scan(&body); err != nil {
					return err
				}
				if err := json.Unmarshal(body, &payload); err != nil {
					return err
				}
				seen = append(seen, payload)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return seen
	}
	if _, err := l.pass(ctx, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if seen := readEvents(); len(seen) != 0 {
		t.Fatalf("unset core.worktree produced events: %+v", seen)
	}

	// A run directory may have been deleted before the drain sees its path.
	worktree := filepath.Join(f.root, "missing run directory")
	runGit(t, f.root, "config", "--file", config, "core.worktree", worktree)
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	// Later Git operations may fail with the bad setting; the trunk-observation
	// event must already have been written regardless of that outcome.
	_, passErr := l.pass(ctx, &LoopResult{})
	seen := readEvents()
	if len(seen) != 1 || seen[0]["config_path"] != config || seen[0]["worktree"] != worktree {
		t.Fatalf("want one event naming %q and %q; got %+v (pass error: %v)", config, worktree, seen, passErr)
	}
	after, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("the observation changed the shared Git config")
	}

	// An empty value is still set. An ordinary Git read may refuse to chdir
	// to it, but the observation must still work.
	runGit(t, f.root, "config", "--file", config, "core.worktree", "")
	_, passErr = l.pass(ctx, &LoopResult{})
	seen = readEvents()
	if len(seen) != 2 || seen[1]["config_path"] != config || seen[1]["worktree"] != "" {
		t.Fatalf("want an observation of the empty value in %q; got %+v (pass error: %v)", config, seen, passErr)
	}
}

func TestObserveTrunkCoreWorktreeSkipsNonLanding(t *testing.T) {
	f, l := passFixture(t)
	l.nonLanding = true
	calls := filepath.Join(t.TempDir(), "git-calls")
	binDir := t.TempDir()
	stub := filepath.Join(binDir, "git")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$*\" >> '"+calls+"'\nexit 99\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	if err := l.observeTrunkCoreWorktree(context.Background()); err != nil {
		t.Fatalf("non-landing observeTrunkCoreWorktree: %v", err)
	}
	if raw, err := os.ReadFile(calls); !os.IsNotExist(err) {
		t.Fatalf("git ran in non-landing mode: %s (%v)", raw, err)
	}
	_ = f
}
