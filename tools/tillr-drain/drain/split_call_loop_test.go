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
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func intPtr(v int) *int { return &v }

func recordTestSplitCallFromRefusal(t *testing.T, f refusalLoopFixture, l *Loop, parent, runID string, files, linesPerFile int, replan bool, feedback *verifyFeedback) (base, head string, call item.SplitCall) {
	t.Helper()
	ctx := context.Background()
	l.cfg.CandidateSizeLimits = &settings.CandidateSizeLimits{FileLimit: 2, AddedLineLimit: 5}
	base, head = commitTestFiles(t, f.root, files, linesPerFile, runID)
	c := Candidate{
		ItemID:         parent,
		LaneID:         "lane-1",
		RunID:          runID,
		Worktree:       f.root,
		BaseSHA:        base,
		HeadSHA:        head,
		VerifyReplan:   replan,
		verifyFeedback: feedback,
	}
	if err := l.recordRefusal(ctx, c, RefusalVerifyNotDone); err != nil {
		t.Fatal(err)
	}
	if err := l.readStore(ctx, func(r *store.Reader) error {
		p, err := item.PendingSplitCall(r, parent)
		if err != nil {
			return err
		}
		if p == nil {
			t.Fatalf("expected pending split call for %s", parent)
		}
		call = *p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return base, head, call
}

func splitTestDesignPolicy(t *testing.T, designBackend string) *Policy {
	t.Helper()
	return &Policy{
		DefaultBackend: "builder",
		Seats: map[string]string{
			"build":  "builder",
			"plan":   "builder",
			"verify": "builder",
			"design": designBackend,
		},
		Backends: map[string]Backend{
			"builder":     {Adapter: "fixture"},
			designBackend: {Adapter: "fixture"},
		},
	}
}

func TestSplitCallPlanningRoutesToDesignSeatAndHoldsWhenUnavailable(t *testing.T) {
	f, l, parent := splitFixture(t)
	_, _, call := recordTestSplitCallFromRefusal(t, f, l, parent, "run-plan-route", 3, 2, false, nil)

	// 1. No policy -> hold with ReasonSplitCallDesign.
	d := func() Decision {
		ctx := context.Background()
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		def, defErr := definition.GitLanding()
		if defErr != nil {
			t.Fatal(defErr)
		}
		var out Decision
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			envs, err := planWithDetails(r, Pass{
				Now:            l.cfg.Now().Format(time.RFC3339Nano),
				AvailableLanes: &[]string{"lane-1"},
			}, nil, nil, def, nil)
			if err != nil {
				return err
			}
			for _, env := range envs {
				var dec Decision
				if err := json.Unmarshal(env.Payload, &dec); err != nil {
					return err
				}
				if dec.ItemID == parent {
					out = dec
					return nil
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}()
	if d.Action != ActionHold || d.Reason != ReasonSplitCallDesign || d.Seat != "design" || d.SplitCall == nil || d.SplitCall.CallID != call.CallID {
		t.Fatalf("no-policy decision = %+v, want hold %s on design", d, ReasonSplitCallDesign)
	}

	// 2. Policy without design seat -> hold with ReasonSplitCallDesign.
	l.cfg.Policy = &Policy{
		DefaultBackend: "builder",
		Seats:          map[string]string{"build": "builder"},
		Backends:       map[string]Backend{"builder": {Adapter: "fixture"}},
	}
	d = decisionFor(t, l, parent).decision
	if d.Action != ActionHold || d.Reason != ReasonSplitCallDesign || d.Seat != "design" {
		t.Fatalf("no-design-seat decision = %+v, want hold %s", d, ReasonSplitCallDesign)
	}

	// 3. No lane / MaxDispatches=0 -> hold with ReasonSplitCallDesign.
	zero := 0
	l.cfg.Policy = splitTestDesignPolicy(t, "designer")
	d = func() Decision {
		ctx := context.Background()
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var out Decision
		def, defErr := definition.GitLanding()
		if defErr != nil {
			t.Fatal(defErr)
		}
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			envs, err := planWithDetails(r, Pass{
				Now:            l.cfg.Now().Format(time.RFC3339Nano),
				MaxDispatches:  &zero,
				AvailableLanes: &[]string{"lane-1"},
			}, l.cfg.Policy, nil, def, nil)
			if err != nil {
				return err
			}
			for _, env := range envs {
				var dec Decision
				if err := json.Unmarshal(env.Payload, &dec); err != nil {
					return err
				}
				if dec.ItemID == parent {
					out = dec
					return nil
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}()
	if d.Action != ActionHold || d.Reason != ReasonSplitCallDesign || d.Seat != "design" {
		t.Fatalf("no-lane/cap decision = %+v, want hold %s", d, ReasonSplitCallDesign)
	}

	// 4. Available lane + policy -> dispatch to design seat, no workflow attached.
	d = decisionFor(t, l, parent).decision
	if d.Action != ActionDispatch || d.Seat != "design" || d.Reason != ReasonSplitCallDesign || d.Workflow != nil || d.SplitCall == nil {
		t.Fatalf("dispatch decision = %+v, want design dispatch with SplitCall and nil Workflow", d)
	}
	if d.RunRequest == nil || d.RunRequest.Backend != "designer" {
		t.Fatalf("unexpected RunRequest: %+v", d.RunRequest)
	}
}

func TestSplitCallDesignBriefIncludesRequiredContextAndAnswerContract(t *testing.T) {
	f, l, parent := splitFixture(t)
	feedback := &verifyFeedback{
		Findings: []verifyFindingRecord{
			{
				Criterion: "c5",
				Reason:    "second behavior is broken",
				Findings: item.VerdictFindings{
					{Kind: "gap", Case: "handles second behavior", Paths: []string{"file:pkg/b.go"}},
				},
			},
		},
	}
	base, head, call := recordTestSplitCallFromRefusal(t, f, l, parent, "run-brief-1", 3, 2, false, feedback)

	// Record a previous malformed-statement rejection reason to verify §7.6 feedback in brief.
	ctx := context.Background()
	if err := l.recordSplitCallRejection(ctx, parent, "run-design-prev", "invalid split statement: a split needs at least two parts"); err != nil {
		t.Fatal(err)
	}
	if err := l.readStore(ctx, func(r *store.Reader) error {
		p, err := item.PendingSplitCall(r, parent)
		if err != nil {
			return err
		}
		call = *p
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	tmpl := filepath.Join(t.TempDir(), "brief-template.md")
	writeForkFile(t, tmpl, "# {{ITEM_ID}} {{TITLE}}\n## Task\n{{TASK}}\n## Criteria\n{{CRITERION_1}}\n## Facts\n{{FACTS}}\n## Settled\n{{SETTLED}}\n")
	stager := &LaneStager{Root: l.cfg.Root, LaneRoot: l.cfg.LaneRoot, Store: l.cfg.Store, Template: tmpl, Now: l.cfg.Now}
	runID := "run-split-brief-test"
	spec := StageSpec{
		LaneID:     "lane-1",
		ItemID:     parent,
		RunID:      runID,
		Backend:    "designer",
		BaseSHA:    base,
		WorkingDir: WorktreeDir,
		SplitCall:  &call,
	}
	if err := stager.Stage(ctx, spec); err != nil {
		t.Fatal(err)
	}
	briefBytes, err := os.ReadFile(filepath.Join(l.cfg.LaneRoot, "lane-1", BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(briefBytes)
	durableOut := splitCallOutputPath(run.DurableDir(filepath.Join(l.cfg.LaneRoot, "lane-1"), runID))

	for _, want := range []string{
		"Owner's words",
		"c5:",
		"caller=verifier",
		"files=3 (limit 2)",
		"added_lines=6 (limit 5)",
		pinRef("run-brief-1"),
		head,
		"[gap]",
		"handles second behavior",
		"pkg/b.go",
		"invalid split statement: a split needs at least two parts",
		durableOut,
		"SPLIT-ANSWER: no-split",
		"REASON:",
		"No repository edits or commits are required",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

func TestSplitCallSettleValidStatementFilesPartsAndBlocksParent(t *testing.T) {
	f, l, parent := splitFixture(t)
	_, _, call := recordTestSplitCallFromRefusal(t, f, l, parent, "run-valid-1", 3, 2, false, nil)
	l.cfg.Policy = splitTestDesignPolicy(t, "designer")

	d := decisionFor(t, l, parent).decision
	if d.Action != ActionDispatch || d.SplitCall == nil {
		t.Fatalf("expected split-call dispatch, got %+v", d)
	}
	lane := d.LaneID
	runID := d.RunRequest.RunID
	durableDir := run.DurableDir(filepath.Join(l.cfg.LaneRoot, lane), runID)
	if err := os.MkdirAll(durableDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statement := `{
  "parts": [
    {"label": "a", "title": "Part A", "share": "First half", "leaves": {"b": "Second half"}, "required": true},
    {"label": "b", "title": "Part B", "share": "Second half", "leaves": {"a": "First half"}, "follows": ["a"], "required": true}
  ],
  "coverage": {
    "c5": [{"part": "a"}],
    "c6": [{"part": "b"}]
  }
}`
	if err := os.WriteFile(splitCallOutputPath(durableDir), []byte(statement), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rec := run.Record{RunID: runID, ItemID: parent, LaneID: lane, Backend: "designer", State: run.StateExited, ExitCode: intPtr(0)}
	outcome := l.settleSplitCallRun(ctx, d, *d.RunRequest, rec, lane, "RESULT: done\n", l.cfg.Now())
	if outcome.err != nil {
		t.Fatalf("settleSplitCallRun error = %v", outcome.err)
	}

	if err := l.readStore(ctx, func(r *store.Reader) error {
		pending, err := item.PendingSplitCall(r, parent)
		if err != nil {
			return err
		}
		if pending != nil {
			t.Fatalf("expected no pending split call after filing, got %+v", pending)
		}
		calls, err := item.ReadSplitCalls(r, parent)
		if err != nil {
			return err
		}
		if len(calls) != 1 || calls[0].CallID != call.CallID || calls[0].Status != item.SplitCallStatusSplit {
			t.Fatalf("unexpected calls after split: %+v", calls)
		}
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, parent).Scan(&state); err != nil {
			return err
		}
		if state != StateBlocked {
			t.Fatalf("parent state = %q, want %q", state, StateBlocked)
		}
		var partCount int
		if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'item-part-%'`).Scan(&partCount); err != nil {
			return err
		}
		if partCount != 2 {
			t.Fatalf("filed part count = %d, want 2", partCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSplitCallSettleMalformedStatementRollsBackAndReturnsToDesignSeat(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statement string
	}{
		{
			name:      "one-part",
			statement: `{"parts":[{"label":"a","title":"Only","share":"Everything","required":true}],"coverage":{"c5":[{"part":"a"}]}}`,
		},
		{
			name:      "unowned-criterion",
			statement: `{"parts":[{"label":"a","title":"A","share":"A","required":true},{"label":"b","title":"B","share":"B","required":true}],"coverage":{}}`,
		},
		{
			name:      "unknown-field",
			statement: `{"parts":[{"label":"a","title":"A","share":"A","required":true,"extra":1},{"label":"b","title":"B","share":"B","required":true}],"coverage":{"c5":[{"part":"a"}]}}`,
		},
		{
			name:      "invalid-json",
			statement: `{not-json`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, parent := splitFixture(t)
			recordTestSplitCallFromRefusal(t, f, l, parent, "run-malformed-"+tc.name, 3, 2, false, nil)
			l.cfg.Policy = splitTestDesignPolicy(t, "designer")

			d := decisionFor(t, l, parent).decision
			lane := d.LaneID
			runID := d.RunRequest.RunID
			durableDir := run.DurableDir(filepath.Join(l.cfg.LaneRoot, lane), runID)
			if err := os.MkdirAll(durableDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(splitCallOutputPath(durableDir), []byte(tc.statement), 0o644); err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			rec := run.Record{RunID: runID, ItemID: parent, LaneID: lane, Backend: "designer", State: run.StateExited, ExitCode: intPtr(0)}
			outcome := l.settleSplitCallRun(ctx, d, *d.RunRequest, rec, lane, "RESULT: done\n", l.cfg.Now())
			if outcome.err != nil {
				t.Fatalf("settleSplitCallRun error = %v", outcome.err)
			}

			if err := l.readStore(ctx, func(r *store.Reader) error {
				pending, err := item.PendingSplitCall(r, parent)
				if err != nil {
					return err
				}
				if pending == nil || pending.Status != item.SplitCallStatusPending || pending.LastRejectionReason == "" {
					t.Fatalf("expected pending split call with rejection reason, got %+v", pending)
				}
				var partCount int
				if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'item-part-%'`).Scan(&partCount); err != nil {
					return err
				}
				if partCount != 0 {
					t.Fatalf("expected 0 filed parts on malformed statement, got %d", partCount)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			// Next planner pass still routes to design seat.
			next := decisionFor(t, l, parent).decision
			if next.Action != ActionDispatch || next.Seat != "design" || next.SplitCall == nil || next.SplitCall.LastRejectionReason == "" {
				t.Fatalf("next decision = %+v, want design dispatch with LastRejectionReason", next)
			}
		})
	}
}

func TestSplitCallSettleNoSplitReturnsItemToBuildOrPlanWithoutReRaisingSameS1(t *testing.T) {
	for _, replan := range []bool{false, true} {
		name := "build-return"
		if replan {
			name = "plan-return"
		}
		t.Run(name, func(t *testing.T) {
			f, l, parent := splitFixture(t)
			ctx := context.Background()
			l.cfg.Policy = splitTestDesignPolicy(t, "designer")

			// Record an initial workflow run for the item so reconstructSplitCallWorkerSession
			// can recover the build stage when replan=false.
			refusedRunID := "run-nosplit-verify"
			wfJSON := `{"name":"planned-build","version":1,"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write"},{"id":"verify","kind":"agent","seat":"verify","access":"read_only","judge_classes":["verification-only"]}],"edges":[{"from_node":"plan","to_node":"work"},{"from_node":"work","to_node":"verify"}],"exit":{"requires_nodes":["plan","work","verify"],"requires_candidate":"commit_required","evaluator_slot":"verify"}}`
			var wf Workflow
			if err := json.Unmarshal([]byte(wfJSON), &wf); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/planned-build@v1.json"), wfJSON)
			s, err := store.Open(ctx, l.cfg.Store, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				pw := pendingWork{
					BaseSHA: runGit(t, f.root, "rev-parse", "HEAD"),
					Decision: Decision{
						ItemID:   parent,
						LaneID:   "lane-1",
						Workflow: &wf,
						RunRequest: &run.Request{
							RunID:   refusedRunID,
							ItemID:  parent,
							LaneID:  "lane-1",
							Backend: "builder",
						},
					},
				}
				if err := appendEffect(tx, dispatchStartedKind, refusedRunID, pw, l.cfg.Now()); err != nil {
					return err
				}
				return appendEffect(tx, examinationDoneKind, refusedRunID, Candidate{ItemID: parent, RunID: refusedRunID, LaneID: "lane-1"}, l.cfg.Now())
			}); err != nil {
				t.Fatal(err)
			}
			s.Close()

			feedback := &verifyFeedback{
				Findings: []verifyFindingRecord{
					{Criterion: "c5", Reason: "edge case missing"},
				},
			}
			base, head, _ := recordTestSplitCallFromRefusal(t, f, l, parent, refusedRunID, 3, 2, replan, feedback)

			// Mark the refused run in `runs` as terminal so priors/latest reflect it.
			s, err = store.Open(ctx, l.cfg.Store, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES (?,'free')`, "lane-1"); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, artifact_refs, event_seq) VALUES (?,?,?,?,?,?,?,?,?,(SELECT MAX(seq) FROM events))`,
					refusedRunID, parent, "lane-1", "builder", run.StateExited, 0, 1000, 2000, "[]"); err != nil {
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			s.Close()

			d := decisionFor(t, l, parent).decision
			if d.Action != ActionDispatch || d.Seat != "design" || d.SplitCall == nil {
				t.Fatalf("expected split-call design dispatch, got %+v", d)
			}
			designRunID := d.RunRequest.RunID
			rec := run.Record{RunID: designRunID, ItemID: parent, LaneID: d.LaneID, Backend: "designer", State: run.StateExited, ExitCode: intPtr(0)}
			// Record the design run in `runs` as terminal (just as run exec + abandon does).
			s, err = store.Open(ctx, l.cfg.Store, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES (?,'free')`, d.LaneID); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, artifact_refs, event_seq) VALUES (?,?,?,?,?,?,?,?,?,(SELECT MAX(seq) FROM events))`,
					designRunID, parent, d.LaneID, "designer", run.StateExited, 0, 3000, 4000, "[]")
				return err
			}); err != nil {
				t.Fatal(err)
			}
			s.Close()

			log := "SPLIT-ANSWER: no-split\nREASON: one shared invariant prevents splitting\nRESULT: done\n"
			outcome := l.settleSplitCallRun(ctx, d, *d.RunRequest, rec, d.LaneID, log, l.cfg.Now())
			if outcome.err != nil {
				t.Fatalf("settleSplitCallRun error = %v", outcome.err)
			}

			if err := l.readStore(ctx, func(r *store.Reader) error {
				pending, err := item.PendingSplitCall(r, parent)
				if err != nil {
					return err
				}
				if pending != nil {
					t.Fatalf("expected no pending split call after no-split, got %+v", pending)
				}
				calls, err := item.ReadSplitCalls(r, parent)
				if err != nil {
					return err
				}
				if len(calls) != 1 || calls[0].Status != item.SplitCallStatusNoSplit || calls[0].NoSplit == nil || calls[0].NoSplit.Reason != "one shared invariant prevents splitting" {
					t.Fatalf("unexpected calls after no-split: %+v", calls)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			// Next planner decision returns item to build (or plan if replan) with verifier findings.
			next := decisionFor(t, l, parent).decision
			if next.Action != ActionDispatch || next.SplitCall != nil {
				t.Fatalf("expected non-split-call dispatch after no-split, got %+v", next)
			}
			if next.Returned == nil || next.Returned.Cause != RefusalVerifyNotDone || !strings.Contains(next.Returned.Detail, "edge case missing") {
				t.Fatalf("expected Returned with verifier findings, got %+v", next.Returned)
			}
			if replan {
				if next.Returned.Replan != true {
					t.Fatalf("expected Returned.Replan=true, got %+v", next.Returned)
				}
				if next.Seat != "plan" {
					t.Fatalf("expected Seat=plan when replan=true, got %q", next.Seat)
				}
			} else {
				if next.Seat != "build" || next.Returned.WorkerSession == nil || next.Returned.WorkerSession.HeadSHA != head {
					t.Fatalf("expected Seat=build with kept candidate %s, got seat=%q ws=%+v", head, next.Seat, next.Returned.WorkerSession)
				}
			}

			// Same-size refusal must NOT re-raise S1.
			if err := l.recordRefusal(ctx, Candidate{
				ItemID: parent, LaneID: "lane-1", RunID: "run-after-nosplit",
				Worktree: f.root, BaseSHA: base, HeadSHA: head,
			}, RefusalVerifyNotDone); err != nil {
				t.Fatal(err)
			}
			if err := l.readStore(ctx, func(r *store.Reader) error {
				p, err := item.PendingSplitCall(r, parent)
				if err != nil {
					return err
				}
				if p != nil {
					t.Fatalf("expected no re-raised split call on same S1 size, got %+v", p)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSplitCallUnansweredAndRepeatRoutesToOwnerWithDonorPinned(t *testing.T) {
	f, l, parent := splitFixture(t)
	wfJSON := `{"name":"planned-build","version":1,"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write"},{"id":"verify","kind":"agent","seat":"verify","access":"read_only","judge_classes":["verification-only"]}],"edges":[{"from_node":"plan","to_node":"work"},{"from_node":"work","to_node":"verify"}],"exit":{"requires_nodes":["plan","work","verify"],"requires_candidate":"commit_required","evaluator_slot":"verify"}}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/planned-build@v1.json"), wfJSON)
	_, head, _ := recordTestSplitCallFromRefusal(t, f, l, parent, "run-unans-1", 3, 2, false, nil)
	l.cfg.Policy = splitTestDesignPolicy(t, "designer")
	ctx := context.Background()

	// 1. First unanswered/failed design run keeps call on design seat and keeps donor pinned.
	d1 := decisionFor(t, l, parent).decision
	rec1 := run.Record{RunID: d1.RunRequest.RunID, ItemID: parent, LaneID: d1.LaneID, Backend: "designer", State: run.StateExited, ExitCode: intPtr(1)}
	if outcome := l.settleSplitCallRun(ctx, d1, *d1.RunRequest, rec1, d1.LaneID, "failed", l.cfg.Now()); outcome.err != nil {
		t.Fatal(outcome.err)
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id, state) VALUES (?,'free')`, d1.LaneID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, artifact_refs, event_seq) VALUES (?,?,?,?,?,?,?,?,?,(SELECT MAX(seq) FROM events))`,
			d1.RunRequest.RunID, parent, d1.LaneID, "designer", run.StateExited, 1, 3000, 4000, "[]")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := l.readStore(ctx, func(r *store.Reader) error {
		p, err := item.PendingSplitCall(r, parent)
		if err != nil || p == nil {
			t.Fatalf("expected pending split call after first failure, err=%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if gotSHA := runGit(t, f.root, "rev-parse", pinRef("run-unans-1")); gotSHA != head {
		t.Fatalf("donor pin lost after first failure: %s != %s", gotSHA, head)
	}

	// 2. Second identical unanswered design run applies design-repeat route (holds for owner, keeps donor pinned).
	d2 := decisionFor(t, l, parent).decision
	if d2.Action != ActionDispatch || d2.Seat != "design" || d2.SplitCall == nil {
		t.Fatalf("expected second design dispatch, got %+v", d2)
	}
	rec2 := run.Record{RunID: d2.RunRequest.RunID, ItemID: parent, LaneID: d2.LaneID, Backend: "designer", State: run.StateExited, ExitCode: intPtr(0)}
	if outcome := l.settleSplitCallRun(ctx, d2, *d2.RunRequest, rec2, d2.LaneID, "no answer here\n", l.cfg.Now()); outcome.err != nil {
		t.Fatal(outcome.err)
	}

	d3 := decisionFor(t, l, parent).decision
	if d3.Action != ActionHold || d3.Reason != ReasonConflictOwner || d3.Seat != "owner" {
		t.Fatalf("expected owner hold on repeated unanswered design run, got %+v", d3)
	}
	if gotSHA := runGit(t, f.root, "rev-parse", pinRef("run-unans-1")); gotSHA != head {
		t.Fatalf("donor pin lost after repeat: %s != %s", gotSHA, head)
	}
}
