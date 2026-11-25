package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
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

func putTrunkObstacleChecks(t *testing.T, root string) {
	t.Helper()
	checks := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checks, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checks, "gate-budget-test.json"), `{"id":"gate-budget-test","scope":"tree","tier":"heavy"}`)
	writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), `print('{"declarations": [{"id":"go-tool/unit","tier":"rolling"},{"id":"tillr-docs","tier":"landing"}]}')`)
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
			body:          "OBSTACLE: trunk\nCHECKS: go-tool/unit, gate-budget-test\nBASE: 0123456\nRESULT: blocked\n",
			wantReason:    ReasonObstacleTrunk,
			wantWhySub:    []string{"go-tool/unit, gate-budget-test"},
			wantMoverPref: "drain; ",
			wantMoverSub:  "go-tool/unit, gate-budget-test",
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
			if tc.kind == ObstacleTrunk {
				putTrunkObstacleChecks(t, f.root)
			}
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

func TestTrunkObstacleChecksMustAllBeListed(t *testing.T) {
	root := t.TempDir()
	putTrunkObstacleChecks(t, root)
	l := &Loop{cfg: LoopConfig{Root: root}}
	for _, tc := range []struct {
		field string
		want  bool
	}{
		{"`go-tool/unit`,\n`gate-budget-test`", true},
		{"go-tool/unit, test/integration/workflow-auto-resume.test.ts", false},
		{"tillr-docs", false}, // Listed by the gate, but never run in a trunk proof.
		{"go-tool/unit gate-budget-test", false},
		{"go-tool/unit passes now, gate-budget-test", false},
	} {
		o := Obstacle{Fields: map[string]string{"CHECKS": tc.field}}
		if got, err := l.trunkProofLists(context.Background(), o.trunkChecks()); err != nil || got != tc.want {
			t.Errorf("CHECKS %q: listed = %v, error = %v, want %v", tc.field, got, err, tc.want)
		}
	}
}

func TestTrunkObstacleProseReturnsToWorker(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequestWith(t, f, "Repair the fork test\n", "build", "scout-then-build@v1", requestFence, false)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	const claim = "test/integration/workflow-auto-resume.test.ts — “resumes a run whose Pi exited once” fails with session start claimed the run on both the base and the candidate."
	var reported bool
	steps := &obstacleRequestSteps{t: t, f: f, report: func(req run.Request) (string, *landingOffer) {
		if req.NodeID == "work" {
			reported = true
			return wrapExtractedReport(req.RunID, "OBSTACLE: trunk\nCHECKS: "+claim+"\nBASE: "+base+"\nRESULT: blocked\n"), nil
		}
		return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
	}}
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 6 && !reported; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if !reported {
		t.Fatalf("writing node did not dispatch: runs=%v", steps.runs)
	}
	d := planFor(t, l, id)
	if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Returned == nil {
		t.Fatalf("expected worker continuation, got %+v", d)
	}
	brief := returnedBrief(*d.Returned)
	for _, want := range []string{claim, "did not name only trunk check IDs", "fork checkout", "Continue the work"} {
		if !strings.Contains(brief, want) {
			t.Errorf("returned brief lacks %q: %s", want, brief)
		}
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
	putTrunkObstacleChecks(t, f.root)
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
					return wrapExtractedReport(req.RunID, "OBSTACLE: trunk\nCHECKS: go-tool/unit, gate-budget-test\nBASE: "+redBase+"\nRESULT: blocked\n"), nil
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
	writeGreenProof(t, l, repaired, l.cfg.Now().UTC().Add(-time.Minute), []string{"go-tool/unit", "gate-budget-test"})
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleTrunk {
		t.Fatalf("stale proof released trunk hold: %+v", d)
	}

	// Partial check list missing gate-budget-test does not release.
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
	writeGreenProof(t, l, repaired, later, []string{"go-tool/unit", "gate-budget-test"})
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

// TestTrunkObstacleGateCheckUnknownIDAndPlainCommand covers the three trunk obstacle
// shapes: a named gate check holds on the drain, an unknown check id returns to the
// worker with a loop line naming the checks and rejection reasons, and a plain command
// outside the gate's checks records an operator-alerting obstacle hold without sending
// the worker back.
func TestTrunkObstacleGateCheckUnknownIDAndPlainCommand(t *testing.T) {
	ctx := context.Background()

	runCase := func(t *testing.T, checksField string) (refusalLoopFixture, *Loop, string, string) {
		t.Helper()
		f, l, _ := workflowLoopFixture(t, "")
		var logBuf bytes.Buffer
		l.cfg.Report = &logBuf
		putTrunkObstacleChecks(t, f.root)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state='closed'`)
			return err
		})
		id := fileFixtureRequestWith(t, f, "Run criterion check\n", "build", "scout-then-build@v1", requestFence, false)
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		var reported bool
		steps := &obstacleRequestSteps{
			t: t, f: f,
			report: func(req run.Request) (string, *landingOffer) {
				if req.NodeID == "work" {
					reported = true
					return wrapExtractedReport(req.RunID, "OBSTACLE: trunk\nCHECKS: "+checksField+"\nBASE: "+base+"\nRESULT: blocked\n"), nil
				}
				return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
			},
		}
		l.cfg.Steps = steps
		result := &LoopResult{}
		for i := 0; i < 6 && !reported; i++ {
			if _, err := l.pass(ctx, result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)
		}
		if !reported {
			t.Fatalf("work node did not report: runs=%v", steps.runs)
		}
		return f, l, id, logBuf.String()
	}

	t.Run("named-gate-check", func(t *testing.T) {
		f, l, id, _ := runCase(t, "go-tool/unit, gate-budget-test")
		if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleTrunk {
			t.Fatalf("expected trunk obstacle hold, got %+v", d)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			active, _, err := obstacles.Read(r)
			if err != nil {
				return err
			}
			h, ok := active[id]
			if !ok {
				t.Fatalf("missing obstacle hold for %s", id)
			}
			if h.Mover != "drain" || len(h.Checks) != 2 {
				t.Fatalf("hold = %+v, want mover=drain and 2 checks", h)
			}
			answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			if answer.Summary.DrainNext != 1 || answer.Summary.OperatorNext != 0 {
				t.Fatalf("summary = %+v, want drain=1 operator=0", answer.Summary)
			}
			return nil
		})
	})

	t.Run("unknown-check-id", func(t *testing.T) {
		_, l, id, logs := runCase(t, "go-tool/unit, no-such-check, tillr-docs")
		d := planFor(t, l, id)
		if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Returned == nil {
			t.Fatalf("expected worker return for unknown check id, got %+v", d)
		}
		var invalidLine string
		for _, line := range strings.Split(logs, "\n") {
			if strings.Contains(line, "named an invalid trunk obstacle") {
				invalidLine = line
				break
			}
		}
		if invalidLine == "" {
			t.Fatalf("missing invalid trunk obstacle line in logs:\n%s", logs)
		}
		for _, want := range []string{
			"go-tool/unit",
			"no-such-check",
			"unknown check id",
			"tillr-docs",
			"not a trunk proof check",
		} {
			if !strings.Contains(invalidLine, want) {
				t.Errorf("invalid trunk obstacle line %q lacks %q", invalidLine, want)
			}
		}
	})

	t.Run("plain-command", func(t *testing.T) {
		const cmd = "pytest tests/test_api.py -k smoke"
		f, l, id, logs := runCase(t, cmd)
		if strings.Contains(logs, "named an invalid trunk obstacle") {
			t.Fatalf("plain command was rejected as invalid:\n%s", logs)
		}
		if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleTrunk {
			t.Fatalf("plain command should hold on trunk obstacle, not return to worker: %+v", d)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			active, _, err := obstacles.Read(r)
			if err != nil {
				return err
			}
			h, ok := active[id]
			if !ok {
				t.Fatalf("missing obstacle hold for %s", id)
			}
			if h.Kind != ObstacleTrunk || h.Mover != "operator" || len(h.Checks) != 0 {
				t.Fatalf("hold = %+v, want kind=trunk mover=operator checks=nil", h)
			}
			if !strings.Contains(h.Reason, cmd) {
				t.Fatalf("hold reason %q lacks command %q", h.Reason, cmd)
			}
			answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			if answer.Summary.OperatorNext != 1 || answer.Summary.DrainNext != 0 {
				t.Fatalf("summary = %+v, want operator=1 drain=0", answer.Summary)
			}
			for _, row := range answer.Rows {
				if row.ItemID != id {
					continue
				}
				if row.Class != "needs you" || !strings.HasPrefix(row.Mover, "operator; ") || !strings.Contains(row.Why, cmd) {
					t.Fatalf("why row = %+v, want needs you / operator naming %q", row, cmd)
				}
			}
			return nil
		})
		// An unrelated trunk proof pass must not auto-release an operator-held plain command.
		runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated trunk fix")
		repaired := runGit(t, f.root, "rev-parse", TrunkRef)
		writeGreenProof(t, l, repaired, l.cfg.Now().UTC().Add(time.Minute), []string{"go-tool/unit", "gate-budget-test"})
		if err := l.releaseTrunkAcquittals(ctx); err != nil {
			t.Fatal(err)
		}
		if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleTrunk {
			t.Fatalf("unrelated trunk proof released operator-held plain command: %+v", d)
		}
	})
}
