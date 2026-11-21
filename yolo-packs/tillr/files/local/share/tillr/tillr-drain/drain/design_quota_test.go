package drain

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func designQuotaFixture(t *testing.T) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.Backends["designer-b"] = Backend{Adapter: "fixture"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-a", ItemID: "item-a", RunID: "blocked-run", BodyRef: "briefs/item-a.md", Text: "requires design", Status: "raised", WaitingOn: "design", BlockedCriteria: []string{"c1"}}, l.cfg.Now())
	})
	return f, l
}

func TestConflictDesignHoldsOnItsShutBackend(t *testing.T) {
	f, l := designQuotaFixture(t)
	reset := l.cfg.Now().Add(time.Hour)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_windows VALUES ('fixture', 'weekly', 1, 'rejected', ?, 'refused')`, reset.UnixMilli())
		return err
	})
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionHold || d.Reason != ReasonBackendWindow || d.RunRequest != nil {
		t.Fatalf("shut design seat: %+v", d)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		row := answer.Rows[0]
		if row.Decision.Seat != "design" || row.Decision.DeferredBy != "fixture" {
			t.Errorf("hold lost the design seat or selected backend: %+v", row.Decision)
		}
		if !strings.Contains(row.Why, "fixture: weekly quota refusal recorded by run refused") ||
			!strings.Contains(row.Mover, reset.Format(time.RFC3339)) {
			t.Errorf("hold has the wrong quota evidence or mover: %+v", row)
		}
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("held conflict dispatched %d runs", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	l.cfg.Now = func() time.Time { return reset }
	reopened := mustPlan(t, l)[0].decision
	if reopened.RunRequest == nil || reopened.RunRequest.Backend != "fixture" {
		t.Fatalf("window did not reopen: %+v", reopened)
	}
}

func TestDesignQuotaRefusalHoldsConflictOnSameSeat(t *testing.T) {
	f, l := designQuotaFixture(t)
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("apply: %v / %v", ok, err)
	}
	reset := l.cfg.Now().Add(time.Hour)
	report := filepath.Join(f.root, "quota.report")
	body := "# run report\nrate limit kind: weekly\nfraction: 1\nstatus: rejected\nreset: " + strconv.FormatInt(reset.Unix(), 10) + "\n---\nYou've hit your weekly limit\n"
	if err := os.WriteFile(report, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	d := p.decision
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(1), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	outcome := l.settle(context.Background(), d, rec, body, base, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, true)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settle: %+v", outcome)
	}
	if err := l.releaseExamination(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		cs, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		if c := cs[d.ItemID]; c.Status != "raised" || c.WaitingOn != "design" {
			t.Errorf("quota refusal stranded conflict: %+v", c)
		}
		var backend string
		var at int64
		if err := tx.QueryRow(`SELECT backend, reset_at FROM backend_windows WHERE backend = 'fixture'`).Scan(&backend, &at); err != nil {
			return err
		}
		if at != reset.UnixMilli() {
			t.Errorf("wrong reset: %d", at)
		}
		return nil
	})
	next := mustPlan(t, l)[0].decision
	if next.Action != ActionHold || next.Reason != ReasonBackendWindow || next.DeferredBy != "fixture" {
		t.Fatalf("next pass did not hold on fixture: %+v", next)
	}
}

// The owner ruled on 2026-11-17 that a design run that commits, but not to the item's
// block, did what it judged right: its gated commit is the answer, and the item goes back
// to its worker. Before the ruling the settlement called it "did not amend" and handed
// the conflict to the owner without a gate.
func TestADesignCommitThatLeavesTheBlockUnchangedIsGatedAsTheAnswer(t *testing.T) {
	f, l := designQuotaFixture(t)
	doc := "# Doc\n\n### Build item a\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/item-a/\" -->\n- **c1:** the first criterion\n"
	if err := os.MkdirAll(filepath.Join(f.root, "briefs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "briefs/item-a.md"), doc)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "item definition")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	target := l.laneTarget("lane-1")
	runGit(t, target, "reset", "--hard", base)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(target, "tools/item-a/answer.txt"), "unrelated design answer\n")
	runGit(t, target, "add", ".")
	runGit(t, target, "commit", "-qm", "unrelated answer")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "tools/item-a/\n")
	d := mustPlan(t, l)[0].decision
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	outcome := l.settle(context.Background(), d, rec, "RESULT: done\n", base, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, false)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settle normal design answer: %v %q", outcome.err, outcome.stop)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		cs, err := item.ReadConflicts(tx.Reader())
		if c := cs[d.ItemID]; c.Status != "answered" || c.WaitingOn != "build" || strings.Contains(c.Text, "did not amend") {
			t.Errorf("the gated design commit was not taken as the answer: %+v", c)
		}
		return err
	})
}
