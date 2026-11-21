package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type obstacleRequestSteps struct {
	recordingSteps
	t      *testing.T
	f      refusalLoopFixture
	report func(req run.Request) (string, *landingOffer)
	runs   []string
}

func (s *obstacleRequestSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	runGit(s.t, s.f.root, "merge", "--squash", c.HeadSHA)
	runGit(s.t, s.f.root, "commit", "-qm", "land request")
	return LandOutcome{Result: LandLanded}, nil
}

func (s *obstacleRequestSteps) ItemState(ctx context.Context, id, _, state string, waiting ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		if state == StateLanded {
			_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, state, id)
			return err
		}
		_, err := item.SetState(ctx, tx, id, state, time.Date(2026, 11, 19, 0, 5, 0, 0, time.UTC), waiting...)
		return err
	})
}

func (s *obstacleRequestSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req.RunID)
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	durable := run.DurableDir(laneDir, req.RunID)
	if err := os.MkdirAll(durable, 0o755); err != nil {
		return run.Record{}, "", err
	}
	report, offer := s.report(req)
	if offer != nil {
		logPath := filepath.Join(durable, "run.log")
		if err := os.WriteFile(logPath, []byte("run log\n"), 0o644); err != nil {
			return run.Record{}, "", err
		}
		raw, err := json.Marshal(offer)
		if err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(filepath.Join(durable, landingOfferFile), raw, 0o644); err != nil {
			return run.Record{}, "", err
		}
	}
	reportPath := filepath.Join(durable, "report.md")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		return run.Record{}, "", err
	}
	started := time.Date(2026, 11, 19, 0, 0, len(s.runs), 0, time.UTC).Format(time.RFC3339)
	finished := time.Date(2026, 11, 19, 0, 0, len(s.runs)+1, 0, time.UTC).Format(time.RFC3339)
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: countPtr(0),
		StartedAt:  started,
		FinishedAt: finished,
		ArtifactRefs: []run.ArtifactRef{
			{Kind: "lane-root", Ref: laneDir},
			{Kind: "scratch", Ref: durable},
			{Kind: "log", Ref: filepath.Join(durable, "run.log")},
			{Kind: "report", Ref: reportPath},
		},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	return rec, report, st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
}

func wrapExtractedReport(runID, body string) string {
	return "# run report\n\n- run: `" + runID + "`\n- backend: `pi`\n\n---\n\n" + body
}

// TestRequestObstacleHoldsRecordKindAndFields covers every obstacle kind on a filed
// request whose extracted report begins with `# run report`: only `owner` sets waiter
// `owner` (with the QUESTION text), `fence` names PATHS with mover `operator`, `trunk`
// names CHECKS with mover `drain`, and `tooling`, `seat`, and `definition` name
// `operator` with the evidence line. None ever shows `# run report` or missing-document
// triage wording.
func TestRequestObstacleHoldsRecordKindAndFields(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		kind          string
		workflow      string
		useOffer      bool
		body          string
		offerFields   map[string]string
		wantReason    string
		wantWhySub    []string
		wantMoverPref string
		wantMoverSub  string
		wantClass     string
		wantWherePref string
		wantWaiter    string
		wantState     string
		wantOwnerWait int
		wantDrainNext int
		wantOpNext    int
	}{
		{
			kind:          ObstacleOwner,
			workflow:      "scout-then-build@v1",
			body:          "OBSTACLE: owner\nQUESTION: Should the CLI print JSON or plain text by default?\nRESULT: blocked\n",
			wantReason:    ReasonObstacleOwner,
			wantWhySub:    []string{"Should the CLI print JSON or plain text by default?"},
			wantMoverPref: "owner; ",
			wantMoverSub:  "answer the question",
			wantClass:     "needs you",
			wantWherePref: "queue",
			wantWaiter:    "owner",
			wantState:     StateBlocked,
			wantOwnerWait: 1,
		},
		{
			kind:          ObstacleTrunk,
			workflow:      "scout-then-build@v1",
			body:          "OBSTACLE: trunk\nCHECKS: go-tool/unit, tillr-docs\nBASE: 0123456\nRESULT: blocked\n",
			wantReason:    ReasonObstacleTrunk,
			wantWhySub:    []string{"go-tool/unit, tillr-docs"},
			wantMoverPref: "drain; ",
			wantMoverSub:  "go-tool/unit, tillr-docs",
			wantClass:     "waits on work",
			wantWherePref: "trunk",
			wantState:     StateInQueue,
			wantDrainNext: 1,
		},
		{
			kind:          ObstacleTooling,
			workflow:      item.SingleWorkerWorkflow,
			body:          "OBSTACLE: tooling\nCOMMAND: go test ./drain/...\nEVIDENCE:\npanic: test timed out after 10m0s\ngoroutine 1 [running]:\nPATHS: tools/tillr-drain/drain/slow_test.go\nRESULT: blocked\n",
			wantReason:    ReasonObstacleTooling,
			wantWhySub:    []string{"go test ./drain/...", "panic: test timed out after 10m0s"},
			wantMoverPref: "operator; ",
			wantMoverSub:  "panic: test timed out after 10m0s",
			wantClass:     "needs you",
			wantWherePref: "queue",
			wantState:     StateInQueue,
			wantOpNext:    1,
		},
		{
			kind:          ObstacleSeat,
			workflow:      "scout-then-build@v1",
			body:          "OBSTACLE: seat\nTRIED: two lock-order rewrites\nMISSING: deadlock-free proof across writer and reader\nRESULT: blocked\n",
			wantReason:    ReasonObstacleSeat,
			wantWhySub:    []string{"tried: two lock-order rewrites; missing: deadlock-free proof across writer and reader"},
			wantMoverPref: "operator; ",
			wantMoverSub:  "tried: two lock-order rewrites; missing: deadlock-free proof across writer and reader",
			wantClass:     "needs you",
			wantWherePref: "queue",
			wantState:     StateInQueue,
			wantOpNext:    1,
		},
		{
			kind:          ObstacleDefinition,
			workflow:      item.SingleWorkerWorkflow,
			body:          "OBSTACLE: definition\nCRITERIA: request\nEVIDENCE: the requested --legacy flag was removed\nRESULT: blocked\n",
			wantReason:    ReasonObstacleDefinition,
			wantWhySub:    []string{"the requested --legacy flag was removed"},
			wantMoverPref: "operator; ",
			wantMoverSub:  "the requested --legacy flag was removed",
			wantClass:     "needs you",
			wantWherePref: "queue",
			wantState:     StateInQueue,
			wantOpNext:    1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			if tc.kind != ObstacleDefinition {
				l.cfg.Policy.Seats["design"] = "fixture"
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed'`)
				return err
			})
			id := fileFixtureRequestWith(t, f, "Request for "+tc.kind+"\n", "build", tc.workflow, requestFence, false)
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			steps := &obstacleRequestSteps{
				t: t, f: f,
				report: func(req run.Request) (string, *landingOffer) {
					if tc.useOffer {
						return wrapExtractedReport(req.RunID, "Inspecting files outside the fence.\n"), &landingOffer{
							CallID:   "call-" + req.RunID,
							Tool:     "obstacle",
							Base:     base,
							Obstacle: &landingOfferObstacle{Kind: tc.kind, Fields: tc.offerFields},
						}
					}
					return wrapExtractedReport(req.RunID, tc.body), nil
				},
			}
			l.cfg.Steps = steps
			result := &LoopResult{}
			for i := 0; i < 4 && len(steps.runs) == 0; i++ {
				if _, err := l.pass(ctx, result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if _, err := l.pass(ctx, result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)

			fixtureRead(t, f, func(r *store.Reader) error {
				var state string
				if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
					return err
				}
				if state != tc.wantState {
					t.Fatalf("state = %q, want %q", state, tc.wantState)
				}
				waiting, _, err := item.ReadWaitingOn(r, id)
				if err != nil {
					return err
				}
				if tc.wantWaiter == "" {
					if waiting != nil {
						t.Fatalf("non-owner obstacle set waiter %+v, want none", waiting)
					}
				} else {
					if waiting == nil || waiting.Waiter != tc.wantWaiter || waiting.Reason != tc.wantWhySub[0] {
						t.Fatalf("waiting_on = %+v, want waiter=%q reason=%q", waiting, tc.wantWaiter, tc.wantWhySub[0])
					}
				}

				answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
				if err != nil {
					return err
				}
				if answer.Summary.OwnerWaiting != tc.wantOwnerWait || answer.Summary.DrainNext != tc.wantDrainNext || answer.Summary.OperatorNext != tc.wantOpNext {
					t.Fatalf("summary = %+v, want owner=%d drain=%d operator=%d", answer.Summary, tc.wantOwnerWait, tc.wantDrainNext, tc.wantOpNext)
				}
				for _, row := range answer.Rows {
					if row.ItemID != id {
						continue
					}
					if row.Reason != tc.wantReason || row.Class != tc.wantClass || !strings.HasPrefix(row.Where, tc.wantWherePref) {
						t.Fatalf("row = %+v, want reason=%s class=%s where~=%s", row, tc.wantReason, tc.wantClass, tc.wantWherePref)
					}
					for _, bad := range []string{"# run report", "no document exists", "triage"} {
						if strings.Contains(row.Why, bad) || strings.Contains(row.Mover, bad) {
							t.Fatalf("row leaked %q: why=%q mover=%q", bad, row.Why, row.Mover)
						}
					}
					for _, sub := range tc.wantWhySub {
						if !strings.Contains(row.Why, sub) {
							t.Fatalf("why %q lacks %q", row.Why, sub)
						}
					}
					if !strings.HasPrefix(row.Mover, tc.wantMoverPref) || !strings.Contains(row.Mover, tc.wantMoverSub) {
						t.Fatalf("mover = %q, want prefix %q and %q", row.Mover, tc.wantMoverPref, tc.wantMoverSub)
					}
					return nil
				}
				t.Fatalf("no why row for %s: %+v", id, answer.Rows)
				return nil
			})
		})
	}
}

// TestTrunkObstacleHoldReturnsByItselfWhenChecksAreGreen verifies that a request held on
// OBSTACLE: trunk with staged, in-fence work remains held on stale or incomplete
// proofs, then dispatches onto the repaired trunk with the saved work in its brief.
func TestTrunkObstacleHoldReturnsByItselfWhenChecksAreGreen(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequestWith(t, f, "Build after trunk is green\n", "build", "scout-then-build@v1", requestFence, false)
	redBase := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, redBase)

	const work = "tools/item-a/repaired.txt"
	trunkBlocked := true
	var savedRef string
	steps := &obstacleRequestSteps{
		t: t, f: f,
		report: func(req run.Request) (string, *landingOffer) {
			if req.NodeID == "work" {
				lane := filepath.Join(f.laneRoot, req.LaneID)
				target := filepath.Join(lane, WorktreeDir)
				if err := os.MkdirAll(filepath.Dir(filepath.Join(target, work)), 0o755); err != nil {
					t.Error(err)
					return "", nil
				}
				if trunkBlocked {
					writeForkFile(t, filepath.Join(target, work), "staged work\n")
					runGit(t, target, "add", "--", work)
					writeForkFile(t, filepath.Join(target, work), "working copy\n")
					savedRef = preservedPrefix + req.RunID
					return wrapExtractedReport(req.RunID, "OBSTACLE: trunk\nCHECKS: go-tool/unit, tillr-docs\nBASE: "+redBase+"\nRESULT: blocked\n"), nil
				}
				brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
				if err != nil || !strings.Contains(string(brief), "Earlier work exists at "+savedRef) {
					t.Errorf("worker brief did not offer preserved work %s (read error: %v)", savedRef, err)
				}
				writeForkFile(t, filepath.Join(target, work), "done\n")
				runGit(t, target, "add", "--", work)
				runGit(t, target, "commit", "-qm", "complete after trunk green")
			}
			return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
		},
	}
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 6 && savedRef == ""; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if savedRef == "" {
		t.Fatalf("writing node did not dispatch: runs=%v", steps.runs)
	}
	if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleTrunk {
		t.Fatalf("expected trunk obstacle hold, got %+v", d)
	}
	if got := runGit(t, f.root, "show", savedRef+":"+work); got != "working copy" {
		t.Fatalf("preserved worktree: %q", got)
	}
	if got := runGit(t, f.root, "show", savedRef+"^2:"+work); got != "staged work" {
		t.Fatalf("preserved index: %q", got)
	}

	runGit(t, f.root, "commit", "--allow-empty", "-qm", "trunk fix")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)
	later := l.cfg.Now().UTC().Add(time.Minute)

	// Older proof does not release.
	writeGreenProof(t, l, repaired, l.cfg.Now().UTC().Add(-time.Minute), []string{"go-tool/unit", "tillr-docs/face"})
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleTrunk {
		t.Fatalf("stale proof released trunk hold: %+v", d)
	}

	// Partial check list missing tillr-docs does not release.
	writeGreenProof(t, l, repaired, later, []string{"go-tool/unit"})
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleTrunk {
		t.Fatalf("incomplete check proof released trunk hold: %+v", d)
	}

	// Green proof covering both named checks releases the item without operator retry,
	// and the workflow runs from node 0 to landing.
	trunkBlocked = false
	writeGreenProof(t, l, repaired, later, []string{"go-tool/unit", "tillr-docs/every-face"})
	for i := 0; i < 12 && result.Landed == 0; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if result.Landed != 1 {
		t.Fatalf("item did not return and land after green checks: landed=%d runs=%v", result.Landed, steps.runs)
	}
}

// TestOwnerObstacleHoldRedispatchesWhenCleared verifies that answering an owner obstacle
// via AuthorizeRetry unblocks the request and dispatches it without run-link-missing.
func TestOwnerObstacleHoldRedispatchesWhenCleared(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["design"] = "fixture"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequestWith(t, f, "Clarify output format\n", "build", "scout-then-build@v1", requestFence, false)
	steps := &obstacleRequestSteps{
		t: t, f: f,
		report: func(req run.Request) (string, *landingOffer) {
			return wrapExtractedReport(req.RunID, "OBSTACLE: owner\nQUESTION: Use tabwriter or JSON?\nRESULT: blocked\n"), nil
		},
	}
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 4 && len(steps.runs) == 0; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(ctx, tx, RetryRequest{
			ItemID: id,
			RunID:  steps.runs[0],
			By:     "owner",
			Why:    "Use JSON by default.",
			At:     l.cfg.Now().UTC().Add(time.Minute),
		})
		return err
	})
	if d := planFor(t, l, id); d.Action != ActionRetry {
		t.Fatalf("cleared owner obstacle did not retry: %+v", d)
	}
}
