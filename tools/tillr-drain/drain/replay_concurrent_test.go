package drain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Two concurrent base replays of the same commit must each get their own
// worktree under <lane root>/.replay so neither removes the other's tree
// while checks are running or when the first finishes and cleans up.
func TestConcurrentReplaysOfSameCommitUseDistinctWorktrees(t *testing.T) {
	root, tip := heavyRepo(t)
	laneRoot := filepath.Join(scratchUnderDurable(t), "lanes")

	type enteredCall struct {
		id  string
		cwd string
	}
	entered := make(chan enteredCall, 2)
	release := map[string]chan struct{}{
		"check-1": make(chan struct{}),
		"check-2": make(chan struct{}),
	}
	releaseAll := func() {
		for _, ch := range release {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}
	t.Cleanup(releaseAll)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		cwd := r.URL.Query().Get("cwd")
		entered <- enteredCall{id: id, cwd: cwd}
		select {
		case <-release[id]:
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)

	program := executable(t, `exec python3 - "$1" <<'PY'
import os, pathlib, sys, urllib.parse, urllib.request

check_id = sys.argv[1]
cwd = os.getcwd()
if pathlib.Path("value").read_text() != "good":
    sys.exit(1)
qs = urllib.parse.urlencode({"id": check_id, "cwd": cwd})
with urllib.request.urlopen("`+server.URL+`/enter?" + qs) as resp:
    if resp.status != 200:
        sys.exit(1)
if os.getcwd() != cwd:
    sys.exit(1)
if pathlib.Path("value").read_text() != "good":
    sys.exit(1)
PY`)

	steps := &ProcessSteps{
		Root:          root,
		LaneRoot:      laneRoot,
		ReplayProgram: program + " {only}",
	}

	type replayResult struct {
		id  string
		out ReplayOutcome
		err error
	}
	done := make(chan replayResult, 2)
	for _, checkID := range []string{"check-1", "check-2"} {
		go func(id string) {
			out, err := steps.Replay(context.Background(), Candidate{}, tip, []string{id})
			done <- replayResult{id: id, out: out, err: err}
		}(checkID)
	}

	var calls []enteredCall
	for len(calls) < 2 {
		select {
		case c := <-entered:
			calls = append(calls, c)
		case res := <-done:
			releaseAll()
			t.Fatalf("replay %s finished before both entered: out=%+v err=%v", res.id, res.out, res.err)
		}
	}

	replayRoot := filepath.Join(laneRoot, ".replay")
	for _, c := range calls {
		if filepath.Dir(c.cwd) != replayRoot {
			releaseAll()
			t.Fatalf("replay %s ran in %q, want a worktree directly under %q", c.id, c.cwd, replayRoot)
		}
	}
	if calls[0].cwd == calls[1].cwd {
		releaseAll()
		t.Fatalf("concurrent replays of %s shared worktree %q", shortSHA(tip), calls[0].cwd)
	}

	// Let the second replay finish and run its worktree cleanup while the first
	// replay is still inside its own worktree, then release the first replay.
	close(release[calls[1].id])
	firstDone := <-done
	if firstDone.id != calls[1].id {
		releaseAll()
		t.Fatalf("expected %s to finish first, got %s", calls[1].id, firstDone.id)
	}
	if firstDone.err != nil || !firstDone.out.Known || len(firstDone.out.Red) != 0 {
		releaseAll()
		t.Fatalf("replay %s failed: out=%+v err=%v", firstDone.id, firstDone.out, firstDone.err)
	}

	close(release[calls[0].id])
	secondDone := <-done
	if secondDone.err != nil || !secondDone.out.Known || len(secondDone.out.Red) != 0 {
		t.Fatalf("replay %s failed after peer cleanup: out=%+v err=%v", secondDone.id, secondDone.out, secondDone.err)
	}

	entries, err := os.ReadDir(replayRoot)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("concurrent replays left worktrees behind: %v", entries)
	}
}
