package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func commitTestFiles(t *testing.T, root string, files int, linesPerFile int, prefix string) (base, head string) {
	t.Helper()
	base = runGit(t, root, "rev-parse", "HEAD")
	var lineBuf strings.Builder
	for i := 0; i < linesPerFile; i++ {
		lineBuf.WriteString("line\n")
	}
	content := lineBuf.String()
	var paths []string
	for i := 0; i < files; i++ {
		name := fmt.Sprintf("%s-%02d.txt", prefix, i)
		p := filepath.Join(root, name)
		writeForkFile(t, p, content)
		paths = append(paths, name)
	}
	runGit(t, root, append([]string{"add", "--"}, paths...)...)
	runGit(t, root, "commit", "-qm", "candidate "+prefix)
	head = runGit(t, root, "rev-parse", "HEAD")
	return base, head
}

func TestVerifierSplitCallOnS1ExceedingLimits(t *testing.T) {
	for _, tc := range []struct {
		name         string
		files        int
		linesPerFile int
		replan       bool
	}{
		{name: "exceeds-file-limit", files: 3, linesPerFile: 1, replan: false},
		{name: "exceeds-file-limit-replan", files: 3, linesPerFile: 1, replan: true},
		{name: "exceeds-added-line-limit", files: 1, linesPerFile: 10, replan: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, parent := splitFixture(t)
			l.cfg.CandidateSizeLimits = &settings.CandidateSizeLimits{FileLimit: 2, AddedLineLimit: 5}
			base, head := commitTestFiles(t, f.root, tc.files, tc.linesPerFile, tc.name)

			ctx := context.Background()
			runID := "run-" + tc.name
			c := Candidate{
				ItemID:       parent,
				LaneID:       "lane-1",
				RunID:        runID,
				Worktree:     f.root,
				BaseSHA:      base,
				HeadSHA:      head,
				VerifyReplan: tc.replan,
			}
			if err := l.recordRefusal(ctx, c, RefusalVerifyNotDone); err != nil {
				t.Fatal(err)
			}

			// Donor ref must be pinned even when VerifyReplan is true.
			gotSHA := runGit(t, f.root, "rev-parse", pinRef(runID))
			if gotSHA != head {
				t.Fatalf("donor pin = %s, want %s", gotSHA, head)
			}

			// Pending split call must be recorded with S1 reading and donor metadata.
			var call *item.SplitCall
			if err := l.readStore(ctx, func(r *store.Reader) error {
				var err error
				call, err = item.PendingSplitCall(r, parent)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if call == nil {
				t.Fatal("expected pending split call, got nil")
			}
			if call.Caller != item.SplitCallerVerifier || call.RunID != runID || call.DonorRef != pinRef(runID) || call.DonorSHA != head || call.BaseSHA != base {
				t.Fatalf("unexpected split call: %+v", call)
			}
			if len(call.Signals) != 1 || call.Signals[0].Signal != item.SplitSignalS1 ||
				call.Signals[0].Files != int64(tc.files) || call.Signals[0].AddedLines != int64(tc.files*tc.linesPerFile) ||
				call.Signals[0].FileLimit != 2 || call.Signals[0].AddedLineLimit != 5 {
				t.Fatalf("unexpected S1 signal: %+v", call.Signals)
			}

			// WorkerSession must be nil on drain.refusal so abandon releases the lane.
			p := candidateSizeEvent(t, l, runID)
			if p["worker_session"] != nil {
				t.Fatalf("expected worker_session to be nil on split call refusal, got %s", p["worker_session"])
			}
		})
	}
}

func TestVerifierSplitCallAtOrBelowLimitAndNonRequestRecordsNoCall(t *testing.T) {
	ctx := context.Background()
	f, l, parent := splitFixture(t)
	l.cfg.CandidateSizeLimits = &settings.CandidateSizeLimits{FileLimit: 2, AddedLineLimit: 5}
	base, head := commitTestFiles(t, f.root, 2, 2, "at-limit") // 2 files <= 2, 4 lines <= 5

	// At-or-below limit on a request item records no split call.
	if err := l.recordRefusal(ctx, Candidate{
		ItemID: parent, LaneID: "lane-1", RunID: "run-at-limit",
		Worktree: f.root, BaseSHA: base, HeadSHA: head,
	}, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}
	// Missing base (CandidateSize unavailable) records no split call.
	if err := l.recordRefusal(ctx, Candidate{
		ItemID: parent, LaneID: "lane-1", RunID: "run-unavailable",
		Worktree: f.root, BaseSHA: "missing-base", HeadSHA: head,
	}, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}
	// Non-request item (no saved request words) records no split call even above limits.
	_, bigHead := commitTestFiles(t, f.root, 4, 3, "non-request-big")
	if err := l.recordRefusal(ctx, Candidate{
		ItemID: "queue-before", LaneID: "lane-1", RunID: "run-non-request",
		Worktree: f.root, BaseSHA: base, HeadSHA: bigHead,
	}, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}

	if err := l.readStore(ctx, func(r *store.Reader) error {
		for _, id := range []string{parent, "queue-before"} {
			calls, err := item.ReadSplitCalls(r, id)
			if err != nil {
				return err
			}
			if len(calls) != 0 {
				t.Fatalf("item %s unexpectedly recorded split calls: %+v", id, calls)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifierSplitCallDoneVerdictNeverCallsSplit(t *testing.T) {
	l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w, verdict: "done"}
	})
	// Configure tiny limits so the candidate exceeds both file_limit and added_line_limit.
	writeForkFile(t, filepath.Join(f.root, ".tillr.toml"), "[candidate_size]\nfile_limit = 0\nadded_line_limit = 0\n")
	cfg, err := settings.Read(f.root)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.CandidateSizeLimits = &cfg.CandidateSize
	runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
	if s.lands != 1 {
		t.Fatalf("lands = %d, want 1", s.lands)
	}
	if err := l.readStore(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM split_calls`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("split_calls = %d, want 0 on done verdict", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifierSplitCallAlreadyAnsweredNoSplitDoesNotReRaise(t *testing.T) {
	ctx := context.Background()
	f, l, parent := splitFixture(t)
	l.cfg.CandidateSizeLimits = &settings.CandidateSizeLimits{FileLimit: 2, AddedLineLimit: 5}
	base, head := commitTestFiles(t, f.root, 3, 1, "first-oversized")

	if err := l.recordRefusal(ctx, Candidate{
		ItemID: parent, LaneID: "lane-1", RunID: "run-1",
		Worktree: f.root, BaseSHA: base, HeadSHA: head,
	}, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.AnswerSplitNoSplit(ctx, tx, parent, "design-1", "shared invariant", l.cfg.Now())
		return err
	})

	// Equal size after no-split does not re-raise.
	if err := l.recordRefusal(ctx, Candidate{
		ItemID: parent, LaneID: "lane-1", RunID: "run-2",
		Worktree: f.root, BaseSHA: base, HeadSHA: head,
	}, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}
	if err := l.readStore(ctx, func(r *store.Reader) error {
		p, err := item.PendingSplitCall(r, parent)
		if err != nil || p != nil {
			t.Fatalf("expected no pending split call without new evidence, got %+v, %v", p, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Larger candidate than what no-split saw DOES raise a new call.
	_, biggerHead := commitTestFiles(t, f.root, 4, 1, "bigger-oversized")
	if err := l.recordRefusal(ctx, Candidate{
		ItemID: parent, LaneID: "lane-1", RunID: "run-3",
		Worktree: f.root, BaseSHA: base, HeadSHA: biggerHead,
	}, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}
	if err := l.readStore(ctx, func(r *store.Reader) error {
		p, err := item.PendingSplitCall(r, parent)
		if err != nil || p == nil || p.RunID != "run-3" {
			t.Fatalf("expected new pending split call for larger candidate, got %+v, %v", p, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSweepRefsProtectsSplitDonorPins(t *testing.T) {
	ctx := context.Background()
	f, l, parent := splitFixture(t)
	l.cfg.CandidateSizeLimits = &settings.CandidateSizeLimits{FileLimit: 2, AddedLineLimit: 5}
	base, head := commitTestFiles(t, f.root, 3, 1, "donor")

	donorRun := "run-donor"
	if err := l.recordRefusal(ctx, Candidate{
		ItemID: parent, LaneID: "lane-1", RunID: donorRun,
		Worktree: f.root, BaseSHA: base, HeadSHA: head,
	}, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}
	// Mark donorRun as a known, examined run so only split-donor protection keeps it.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, "run.allocate", donorRun, map[string]string{"run_id": donorRun, "item_id": parent}, time.Now().UTC()); err != nil {
			return err
		}
		return appendEffect(tx, examinationDoneKind, donorRun, struct{}{}, time.Now().UTC())
	})

	checkPinExists := func(want bool, phase string) {
		t.Helper()
		_, err := os.Stat(filepath.Join(f.root, ".git"))
		_ = err
		out := runGit(t, f.root, "for-each-ref", "--format=%(refname)", pinRef(donorRun))
		got := strings.TrimSpace(out) == pinRef(donorRun)
		if got != want {
			t.Fatalf("%s: donor pin exists=%v, want %v", phase, got, want)
		}
	}

	// 1. Pending split call protects donor pin.
	if _, err := SweepRefs(ctx, RefSweepRequest{Root: f.root, Store: f.storePath, At: l.cfg.Now()}); err != nil {
		t.Fatal(err)
	}
	checkPinExists(true, "pending split call")

	// 2. Open split parent (blocked) still protects donor pin.
	if _, err := FileSplit(ctx, f.root, f.storePath, parent, strings.NewReader(splitExample), l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := SweepRefs(ctx, RefSweepRequest{Root: f.root, Store: f.storePath, At: l.cfg.Now()}); err != nil {
		t.Fatal(err)
	}
	checkPinExists(true, "open split parent")

	// 3. Once parent finishes (landed), SweepRefs prunes the donor pin.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, StateLanded, parent)
		return err
	})
	if _, err := SweepRefs(ctx, RefSweepRequest{Root: f.root, Store: f.storePath, At: l.cfg.Now()}); err != nil {
		t.Fatal(err)
	}
	checkPinExists(false, "finished split parent")
}
