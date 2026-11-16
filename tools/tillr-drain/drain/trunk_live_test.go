package drain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type historyLiveSteps struct {
	dirtyPathSteps
	started, release chan struct{}
}

func (s *historyLiveSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
		return run.Record{}, "", ctx.Err()
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: time.Now().UTC().Format(time.RFC3339), FinishedAt: time.Now().UTC().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}
	st, err := store.Open(ctx, s.store, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "", err
}
func TestRewriteHoldsLandingButLiveRunFinishes(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	steps := &historyLiveSteps{dirtyPathSteps: dirtyPathSteps{t: t, root: f.root, store: f.storePath}, started: make(chan struct{}), release: make(chan struct{})}
	l.cfg.Steps = steps
	base := runGit(t, f.root, "rev-parse", "HEAD")
	candidate := historyCandidate(t, f, l, base)
	// Hold the candidate gate while the sibling run starts. Observe a newer tip
	// before the reset, otherwise a reset to an already observed base is not a rewrite.
	writeForkFile(t, filepath.Join(f.root, "owner.txt"), "owner\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "owner")
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "reset", "--hard", base)
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("rewrite stopped pass: %s / %v", stop, err)
	}
	select {
	case <-steps.started:
	case <-time.After(5 * time.Second):
		t.Fatal("rewrite prevented sibling dispatch")
	}
	// Complete the examination, but leave the run waiting on its own release.
	for l.active[candidate.RunID] {
		select {
		case c := <-l.completions:
			if stop, err := l.takeCompletion(c, &result); err != nil || stop != "" {
				t.Fatalf("examination: %s / %v", stop, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("held examination did not finish")
		}
	}
	if steps.lands != 0 {
		t.Fatal("rewrite landed the candidate")
	}
	close(steps.release)
	drainOwned(t, l, &result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec, err := terminalRecord(tx.Reader(), "run-item-b-a1")
		if err != nil {
			return err
		}
		if rec == nil || rec.ExitCode == nil || *rec.ExitCode != 0 {
			t.Fatalf("live run was stopped: %+v", rec)
		}
		return nil
	})
}
