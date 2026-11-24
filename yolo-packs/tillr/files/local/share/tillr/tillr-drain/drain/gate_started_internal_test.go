package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestGateStartedNotDuplicatedOnAttach(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "repo")
	worktree := filepath.Join(dir, "worktree")
	for _, p := range []string{root, worktree} {
		if err := os.MkdirAll(filepath.Join(p, ".git"), 0o755); err != nil {
			t.Fatalf("creating %s failed: %v", p, err)
		}
	}
	storePath := filepath.Join(dir, "board.sqlite3")
	enteredFile := filepath.Join(dir, "gate.entered")
	releaseFile := filepath.Join(dir, "gate.release")
	gateProg := executable(t, `#!/bin/sh
set -eu
printf 'entered\n' > "`+enteredFile+`"
while [ ! -f "`+releaseFile+`" ]; do
  sleep 0.02
done
exit 0
`)
	steps := &ProcessSteps{
		Root:        root,
		Store:       storePath,
		Now:         func() time.Time { return time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC) },
		GateProgram: gateProg,
	}
	candidate := Candidate{
		ItemID: "item-attach", LaneID: "lane-1", RunID: "run-attach-1",
		TickID: "tick-1", Worktree: worktree, BaseSHA: "aaaa1111", HeadSHA: "bbbb1111",
	}

	h1, err := steps.startOrAttachDetachedGate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("first startOrAttachDetachedGate failed: %v", err)
	}
	defer steps.stopDetachedGate(h1)

	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(enteredFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached gate never entered")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Attaching to the still-live detached gate must not append a second gate.started event.
	h2, err := steps.startOrAttachDetachedGate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("second startOrAttachDetachedGate (attach) failed: %v", err)
	}
	if h2.face.PID != h1.face.PID {
		t.Fatalf("attach saw pid %d, want %d", h2.face.PID, h1.face.PID)
	}

	if err := os.WriteFile(releaseFile, []byte("go\n"), 0o600); err != nil {
		t.Fatalf("writing release file: %v", err)
	}
	done, err := steps.awaitDetachedGate(context.Background(), nil, h1)
	if err != nil {
		t.Fatalf("awaitDetachedGate failed: %v", err)
	}
	if _, err := steps.finishDetachedGate(context.Background(), candidate, h1, done); err != nil {
		t.Fatalf("finishDetachedGate failed: %v", err)
	}

	st, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer st.Close()
	events, err := st.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading events: %v", err)
	}
	started := 0
	for _, e := range events {
		if e.Kind == GateStartedKind {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("gate.started count after start + attach = %d, want 1", started)
	}
}
