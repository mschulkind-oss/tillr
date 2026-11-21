package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// verifyFinding is long, spaced oddly and punctuated on purpose: a return that truncates,
// trims or rewords the verifier's reason fails the word-for-word check below.
const verifyFinding = "uncovered: the second criterion needs  the  repair   in tools/item-a/repair.txt;\n\tsee \"c2\" — and keep this tail: " +
	"0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789END"

// verifyReturnSteps runs planned-build@v1 with a verifier that refuses the first candidate
// and passes the next, and records what each build run was told.
type verifyReturnSteps struct {
	*workflowSteps
	expanded       bool
	verdict        string // reason of the first verify's not-done finding
	always         bool   // every verify refuses with that finding
	varyFinding    bool   // each verifier refusal names a different defect
	refusedAt      []time.Time
	now            func() time.Time
	cannotMeet     string // when set, the first repair run ends with this report
	pieceObstacle  string // when set, the first ordinary piece-two run blocks
	pieceRuns      []run.Request
	widenedPath    string
	plans          int
	verifies       int
	repairs        []run.Request
	repairText     []string
	repairHead     []string
	verified       string
	verifiedRun    string
	pinnedAtRepair string
	candidate      string
	buildPieces    bool   // the last piece is a build piece too, so its stage is the one a repair returns to
	forkName       string // when set, every lane works in .forks/lanes/<lane>/<forkName>
	lands          int
}

func (s *verifyReturnSteps) lastSeat() string {
	if s.buildPieces {
		return "build"
	}
	return "edit"
}

func (s *verifyReturnSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	if s.forkName != "" {
		// A fork candidate's history is the fork's own: the landing moves the patch branch,
		// which the fixture records without a merge into the root repository.
		fixtureLanding(s.t, s.f.storePath, c)
		return LandOutcome{Result: LandLanded}, nil
	}
	return s.workflowSteps.Land(ctx, c)
}

func (s *verifyReturnSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(laneDir, WorktreeDir)
	if s.forkName != "" {
		target = filepath.Join(s.f.root, ".forks/lanes", req.LaneID, s.forkName)
	}
	brief, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	told := string(brief)
	if req.ResumePrompt != "" {
		told = req.ResumePrompt
	}
	report := "RESULT: done\n"
	commit := func(name string) error {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a", name), req.RunID)
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "build "+name)
		return nil
	}
	switch {
	case req.NodeID == "plan":
		s.plans++
		if s.firstHead == "" {
			s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
		}
		if s.expanded {
			one, two := piece("one", "build", "write"), piece("two", s.lastSeat(), "write")
			if s.pieceObstacle != "" {
				two["write_scope"] = []string{"tools/item-a/two.txt"}
			} else if s.widenedPath != "" {
				one["write_scope"] = []string{"tools/item-a/one.txt"}
				two["write_scope"] = []string{"tools/item-a/one.txt", "tools/item-a/two.txt"}
			}
			proposalArtifact(s.t, s.f.root, req.RunID, pieceProposal([]map[string]any{one, two}))
		}
	case req.NodeID == "verify":
		s.verifies++
		head := runGit(s.t, target, "rev-parse", "HEAD")
		verdict, reason := "done", "covered"
		if s.verifies == 1 || s.always {
			verdict, reason = "not-done", s.verdict
			s.verified, s.verifiedRun = head, req.RunID
			if s.varyFinding {
				defects := []string{"missing check", "wrong input", "stale output", "absent receipt", "wrong scope", "missing assertion"}
				reason = "uncovered: " + defects[(s.verifies-1)%len(defects)]
				s.refusedAt = append(s.refusedAt, s.now())
			}
		}
		if s.verifies == 1 && s.cannotMeet != "" && s.widenedPath != "" {
			// The return selects the last writer using the verifier's staged fence.
			// The new run can ask to use the permission added since staging.
			fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET allowed_paths='["tools/item-a/"]' WHERE item_id='item-a'`)
				return err
			})
		}
		producer := "node:work"
		if s.expanded {
			producer = "node:work/piece-one"
		}
		v := item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: runGit(s.t, target, "rev-parse", head+"^{tree}"), Outcome: "pass", Verdicts: []item.JudgeEntry{
			{CriterionID: "c1", CriterionHash: item.CriterionHash("the first criterion of item-a"), Verdict: "done", ClaimClass: item.ClaimVerification, Reason: "covered", Evidence: []string{producer}},
			{CriterionID: "c2", CriterionHash: item.CriterionHash("second criterion"), Verdict: verdict, ClaimClass: item.ClaimVerification, Reason: reason, Evidence: []string{producer}},
		}}
		if verdict == "not-done" {
			v.Outcome = "fail"
		}
		raw, _ := json.Marshal(v)
		writeForkFile(s.t, filepath.Join(run.DurableDir(laneDir, req.RunID), "eval.output"), string(raw))
	case strings.Contains(told, "read the candidate commit this run starts from"):
		// The build step, returned with the verifier's findings.
		s.repairs = append(s.repairs, req)
		if out, err := exec.Command("git", "-C", s.f.root, "rev-parse", "--verify", "-q", pinRef(s.verifiedRun)).Output(); err == nil {
			s.pinnedAtRepair = strings.TrimSpace(string(out))
		}
		s.repairText = append(s.repairText, told)
		s.repairHead = append(s.repairHead, runGit(s.t, target, "rev-parse", "HEAD"))
		if s.cannotMeet != "" && len(s.repairs) == 1 {
			report = s.cannotMeet
		} else if s.always && !s.varyFinding {
			// An identical repeat judges the same candidate: every repair rebuilds the
			// same commit from the candidate the first verifier read.
			s.t.Setenv("GIT_AUTHOR_DATE", "2026-11-15T00:00:00Z")
			s.t.Setenv("GIT_COMMITTER_DATE", "2026-11-15T00:00:00Z")
			if s.candidate == "" {
				s.candidate = runGit(s.t, target, "rev-parse", "HEAD")
			}
			runGit(s.t, target, "reset", "--hard", "-q", s.candidate)
			if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
				return run.Record{}, "", err
			}
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/repair.txt"), "repair")
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", "repair")
		} else {
			name := "repair.txt"
			if s.widenedPath != "" {
				name = s.widenedPath
			}
			if err := commit(name); err != nil {
				return run.Record{}, "", err
			}
		}
	case req.NodeID == "work" || strings.HasPrefix(req.NodeID, "work/piece-"):
		name := "work.txt"
		if strings.HasPrefix(req.NodeID, "work/piece-") {
			name = strings.TrimPrefix(req.NodeID, "work/piece-") + ".txt"
		}
		if req.NodeID == "work/piece-two" && s.pieceObstacle != "" {
			s.pieceRuns = append(s.pieceRuns, req)
			body, err := os.ReadFile(filepath.Join(laneDir, FenceFile))
			if err != nil {
				return run.Record{}, "", err
			}
			fence := fenceEntries(string(body))
			if got, want := insideFence("tools/item-a/other.txt", fence), len(s.pieceRuns) > 1; got != want {
				s.t.Fatalf("run %s admits requested path: got %v, want %v; fence=%v", req.RunID, got, want, fence)
			}
			if !insideFence("tools/item-a/two.txt", fence) || insideFence("tools/item-a/one.txt", fence) {
				s.t.Fatalf("run %s changed more than the requested scope: %v", req.RunID, fence)
			}
			if len(s.pieceRuns) == 1 {
				report = s.pieceObstacle
				break
			}
			name = s.widenedPath
		}
		if err := commit(name); err != nil {
			return run.Record{}, "", err
		}
	default:
		return run.Record{}, "", fmt.Errorf("unexpected node %s", req.NodeID)
	}
	path := filepath.Join(run.DurableDir(laneDir, req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, path, report)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func verifyReturnFixture(t *testing.T, resume bool, steps func(*workflowSteps) *verifyReturnSteps) (*Loop, *verifyReturnSteps, refusalLoopFixture) {
	t.Helper()
	f, l, base := plannedFixture(t)
	writeForkFile(t, filepath.Join(f.root, "config/tillr/plan-guidance.md"), "cheap models")
	if resume {
		declareWorkerResumeFixture(t, l)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('item-a','c2','second criterion','not-done','verification-only')`)
		return err
	})
	l.cfg.Policy.Seats["design"] = "fixture"
	s := steps(base)
	l.cfg.Steps = s
	return l, s, f
}

func runVerifyReturn(t *testing.T, f refusalLoopFixture, l *Loop, s *verifyReturnSteps, until func() bool) {
	t.Helper()
	result := &LoopResult{}
	for i := 0; i < 60 && !until(); i++ {
		graphPass(t, l, result)
	}
	if !until() {
		fixtureRead(t, f, func(r *store.Reader) error {
			rows, _ := r.Query(`SELECT object_id, payload FROM events WHERE kind='drain.refusal'`)
			defer rows.Close()
			for rows.Next() {
				var id, p string
				_ = rows.Scan(&id, &p)
				if len(p) > 600 {
					p = p[:600]
				}
				t.Logf("refusal %s %s", id, p)
			}
			return nil
		})
		for _, p := range mustPlan(t, l) {
			t.Logf("plan: %+v reason=%s", p.decision.Action, p.decision.Reason)
		}
		t.Fatalf("did not finish: plans=%d verifies=%d repairs=%d gates=%d lands=%d runs=%+v", s.plans, s.verifies, len(s.repairs), s.gates, s.lands, s.runs)
	}
}

func storeCount(t *testing.T, f refusalLoopFixture, query string, args ...any) int {
	t.Helper()
	var n int
	fixtureRead(t, f, func(r *store.Reader) error { return r.QueryRow(query, args...).Scan(&n) })
	return n
}

// A refused candidate goes back to the build step on the commit the verifier read, with the
// findings word for word, and lands with no second plan run. It holds for the unexpanded
// graph and for one the planner expanded into pieces, and for a build session that resumes
// and one that starts fresh on the candidate.
func TestVerifyNotDoneRepairedByBuildStep(t *testing.T) {
	for _, tc := range []struct {
		name             string
		expanded, resume bool
	}{
		{"unexpanded-fresh", false, false},
		{"unexpanded-resumed", false, true},
		{"expanded-fresh", true, false},
		{"expanded-resumed", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, s, f := verifyReturnFixture(t, tc.resume, func(w *workflowSteps) *verifyReturnSteps {
				return &verifyReturnSteps{workflowSteps: w, expanded: tc.expanded, verdict: verifyFinding}
			})
			runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
			if s.plans != 1 {
				t.Errorf("plans = %d, want 1", s.plans)
			}
			if s.verifies != 2 || s.gates != 1 || s.lands != 1 {
				t.Errorf("verifies=%d gates=%d lands=%d", s.verifies, s.gates, s.lands)
			}
			if len(s.repairs) != 1 {
				t.Fatalf("repair runs = %d, want 1", len(s.repairs))
			}
			if s.repairHead[0] != s.verified {
				t.Errorf("repair started at %s, not the verified candidate %s", s.repairHead[0], s.verified)
			}
			if !strings.Contains(s.repairText[0], "c2: "+verifyFinding) {
				t.Errorf("findings not word for word in the build brief: %q", s.repairText[0])
			}
			// An expanded graph has no single piece to resume: its inserted work stage
			// starts a session of its own, on the candidate.
			if got, want := s.repairs[0].SessionTurn, tc.resume && !tc.expanded; got != want {
				t.Errorf("SessionTurn = %v, want %v", got, want)
			}
			if got := s.repairs[0].NodeID; got != "work" {
				t.Errorf("repair node = %s, want work", got)
			}
			if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a'`); n != 0 {
				t.Errorf("conflicts = %d", n)
			}
		})
	}
}

// The first repair changes the candidate. The second rebuilds it identically;
// triage takes precedence when that repeat is also the third refusal.
func TestThirdVerifyNotDoneTakesPrecedenceOverIdenticalRepeat(t *testing.T) {
	l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w, verdict: "uncovered: still missing", always: true}
	})
	result := &LoopResult{}
	for i := 0; i < 24; i++ {
		graphPass(t, l, result)
	}
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a'`); n != 0 {
		t.Fatalf("triage should take precedence over a design conflict; got %d conflicts", n)
	}
	if s.verifies != 3 || s.plans != 1 || len(s.repairs) != 2 || s.gates != 0 {
		t.Fatalf("verifies=%d plans=%d repairs=%d gates=%d", s.verifies, s.plans, len(s.repairs), s.gates)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		holds, err := ReadItemFaults(r)
		if err == nil && holds["item-a"].Reason != reasonVerifyTriage {
			t.Fatalf("third refusal was not held for triage: %+v", holds)
		}
		return err
	})
}

// A real verifier hold releases the only lane, even though the refusal also
// records a session for the operator's eventual retry.
func TestVerifyTriageReleasesLaneForNextDispatch(t *testing.T) {
	l, s, f := verifyReturnFixture(t, true, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w, always: true, varyFinding: true}
	})
	s.now = l.cfg.Now
	l.cfg.Lanes = []string{"lane-1"}
	runVerifyReturn(t, f, l, s, func() bool { return s.verifies == 3 })
	thirdHead, thirdRun := s.verified, s.verifiedRun
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue', workflow='planned-build@v1' WHERE item_id='item-b'`)
		return err
	})
	result := &LoopResult{}
	for i := 0; i < 8; i++ {
		graphPass(t, l, result)
		var fault ItemFault
		fixtureRead(t, f, func(r *store.Reader) error {
			faults, err := ReadItemFaults(r)
			fault = faults["item-a"]
			return err
		})
		if fault.Reason == reasonVerifyTriage {
			break
		}
		if i == 7 {
			t.Fatal("third verifier refusal did not hold the item")
		}
	}
	if got := runGit(t, f.root, "rev-parse", pinRef(thirdRun)); got != thirdHead {
		t.Fatalf("pin = %s, want held head %s", got, thirdHead)
	}
	if got, want := runGit(t, l.laneTarget("lane-1"), "rev-parse", "HEAD"), runGit(t, f.root, "rev-parse", TrunkRef); got != want {
		t.Fatalf("hold left lane at %s, want trunk %s", got, want)
	}
	if s.runs[len(s.runs)-1].ItemID != "item-b" {
		graphPass(t, l, result)
	}
	if got := s.runs[len(s.runs)-1]; got.ItemID != "item-b" || got.LaneID != "lane-1" {
		t.Fatalf("next dispatch = %+v, want item-b on lane-1", got)
	}
}

// Three distinct findings hold the item. One operator retry releases it and starts
// a new window without dropping the earlier findings.
func TestVerifyNotDoneHoldsForOperatorRetry(t *testing.T) {
	l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w, always: true, varyFinding: true}
	})
	at := l.cfg.Now().UTC().Truncate(time.Second)
	l.cfg.Now = func() time.Time { return at }
	s.now = l.cfg.Now
	result := &LoopResult{}
	readHold := func() ItemFault {
		t.Helper()
		var fault ItemFault
		fixtureRead(t, f, func(r *store.Reader) error {
			faults, err := ReadItemFaults(r)
			fault = faults["item-a"]
			return err
		})
		return fault
	}
	pass := func() {
		before := s.verifies
		graphPass(t, l, result)
		if s.verifies != before {
			at = at.Add(time.Minute)
		}
	}
	for i := 0; i < 30; i++ {
		pass()
	}
	fault := readHold()
	if fault.Reason != "verify-triage" || s.verifies != 3 || s.plans != 1 || len(s.repairs) != 2 || s.gates != 0 {
		t.Fatalf("fault=%+v verifies=%d plans=%d repairs=%d gates=%d", fault, s.verifies, s.plans, len(s.repairs), s.gates)
	}
	checkFindings := func(fault ItemFault, start int, findings ...string) {
		t.Helper()
		var want []string
		for i, finding := range findings {
			want = append(want, s.refusedAt[start+i].Format(time.RFC3339)+": c2: uncovered: "+finding)
		}
		if !strings.Contains(fault.Detail, strings.Join(want, "; ")) {
			t.Fatalf("hold text %q does not list ordered findings %v", fault.Detail, want)
		}
	}
	checkFindings(fault, 0, "missing check", "wrong input", "stale output")
	heldHead := s.verified
	cmd := retryCommand("item-a", s.verifiedRun, "")
	if !strings.Contains(fault.Detail, cmd) || !strings.Contains(fault.Detail, "operator") {
		t.Fatalf("hold does not name the operator and accepted command %q: %+v", cmd, fault)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := AcquitRun(tx, fault.RunID, "reviewed the findings", l.cfg.Now()); err == nil || !strings.Contains(err.Error(), cmd) {
			t.Errorf("acquit should refuse and name %q, got %v", cmd, err)
		}
		return nil
	})
	if got := readHold(); got != fault {
		t.Fatalf("acquit changed the triage hold: got %+v, want %+v", got, fault)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := ClearItemFault(tx, "item-a", fault.Seq, l.cfg.Now()); err == nil || !strings.Contains(err.Error(), cmd) {
			t.Fatalf("clear-fault should refuse and name %q, got %v", cmd, err)
		}
		wrong := RetryRequest{ItemID: "item-a", RunID: s.repairs[0].RunID, By: "operator", Why: "wrong run", At: l.cfg.Now()}
		if _, err := AuthorizeRetry(context.Background(), tx, wrong); err == nil || !strings.Contains(err.Error(), cmd) {
			t.Fatalf("retry of earlier run should refuse and name %q, got %v", cmd, err)
		}
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: "item-a", RunID: fault.RunID, By: "operator", Why: "read all three findings", At: l.cfg.Now(),
		})
		return err
	})
	if f := readHold(); f.Reason != "" {
		t.Fatalf("operator retry did not release hold: %+v", f)
	}
	for i := 0; i < 30; i++ {
		pass()
		if f := readHold(); s.verifies < 6 && f.Reason != "" {
			t.Fatalf("early hold after retry: %+v (verifies=%d)", f, s.verifies)
		}
	}
	if f := readHold(); f.Reason != "verify-triage" || s.verifies != 6 {
		t.Fatalf("after retry fault=%+v, verifies=%d, want new hold after six total refusals", f, s.verifies)
	} else {
		checkFindings(f, 3, "absent receipt", "wrong scope", "missing assertion")
	}
	if len(s.repairHead) < 3 || s.repairHead[2] != heldHead {
		t.Fatalf("operator retry did not restore held head %s: repair heads %v", heldHead, s.repairHead)
	}
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='drain.refusal' AND json_extract(payload,'$.item_id')='item-a' AND json_extract(payload,'$.step')='verify-not-done'`); n != 6 {
		t.Fatalf("refusal history has %d events, want 6", n)
	}
}

// A finding that says the plan was wrong starts the plan step again, as today.
func TestVerifyPlanWrongFindingRunsPlanAgain(t *testing.T) {
	for _, reason := range []string{"plan-wrong: the pieces cannot cover c2", "  PLAN-Wrong: shout"} {
		t.Run(strings.TrimSpace(reason)[:10], func(t *testing.T) {
			l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
				return &verifyReturnSteps{workflowSteps: w, expanded: true, verdict: reason}
			})
			runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
			if s.plans != 2 || len(s.repairs) != 0 {
				t.Errorf("plans=%d repairs=%d, want 2 and 0", s.plans, len(s.repairs))
			}
		})
	}
}

// A builder that cannot meet the findings within the plan sends the item back to plan.
func TestBuilderCannotMeetFindingsRunsPlanAgain(t *testing.T) {
	for name, report := range map[string]string{
		"definition": "OBSTACLE: definition\nCRITERIA: c2\nEVIDENCE: Plan-Wrong: the piece cannot hold it\nRESULT: blocked\n",
		"line":       "PLAN-WRONG: the plan cut the work wrongly\nRESULT: blocked\n",
	} {
		t.Run(name, func(t *testing.T) {
			l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
				return &verifyReturnSteps{workflowSteps: w, verdict: verifyFinding, cannotMeet: report}
			})
			runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
			if s.plans != 2 || len(s.repairs) != 1 {
				t.Errorf("plans=%d repairs=%d, want 2 and 1", s.plans, len(s.repairs))
			}
			if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='drain.refusal' AND json_extract(payload,'$.step')=?`, RefusalPlanWrong); n != 1 {
				t.Errorf("plan-wrong refusals = %d", n)
			}
		})
	}
}

// offerColdLane runs passes until the first verify has refused the candidate, then offers
// only lane-2, which has no worktree of its own: the build step the refusal returns to has
// to be cut there from the pinned candidate.
func offerColdLane(t *testing.T, f refusalLoopFixture, l *Loop, s *verifyReturnSteps) {
	t.Helper()
	result := &LoopResult{}
	for i := 0; i < 40 && s.verifies < 1; i++ {
		graphPass(t, l, result)
	}
	if s.verifies != 1 || len(s.repairs) != 0 {
		t.Fatalf("verifies=%d repairs=%d before the lane change", s.verifies, len(s.repairs))
	}
	l.cfg.Lanes = []string{"lane-2"}
	cold := filepath.Join(f.laneRoot, "lane-2", WorktreeDir)
	_, _ = exec.Command("git", "-C", f.root, "worktree", "remove", "--force", cold).CombinedOutput()
	if err := os.RemoveAll(filepath.Join(f.laneRoot, "lane-2")); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "worktree", "prune")
}

// When the lane that held the candidate is unavailable, the returned build step runs on
// another lane that has never been staged, starts on the candidate the verifier read, keeps
// the graph (the inserted work stage included), and still lands with one plan run.
func TestVerifyReturnOnColdSecondLane(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		t.Run(fmt.Sprintf("expanded=%v", expanded), func(t *testing.T) {
			l, s, f := verifyReturnFixture(t, true, func(w *workflowSteps) *verifyReturnSteps {
				return &verifyReturnSteps{workflowSteps: w, expanded: expanded, verdict: verifyFinding}
			})
			offerColdLane(t, f, l, s)
			if _, err := os.Stat(filepath.Join(f.laneRoot, "lane-2", WorktreeDir)); err == nil {
				t.Fatal("lane-2 still has a worktree")
			}
			runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
			if s.plans != 1 || len(s.repairs) != 1 {
				t.Fatalf("plans=%d repairs=%d, want 1 and 1", s.plans, len(s.repairs))
			}
			if got := s.repairs[0].LaneID; got != "lane-2" {
				t.Errorf("repair lane = %s, want lane-2", got)
			}
			if s.repairs[0].SessionTurn {
				t.Errorf("a session on another lane was resumed")
			}
			if s.repairHead[0] != s.verified {
				t.Errorf("repair started at %s, not the verified candidate %s", s.repairHead[0], s.verified)
			}
			if !strings.Contains(s.repairText[0], "c2: "+verifyFinding) {
				t.Errorf("findings not word for word: %q", s.repairText[0])
			}
			if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='drain.started' AND json_extract(payload,'$.decision.worker_session_reason')='lane-unavailable'`); n < 1 {
				t.Errorf("no dispatch recorded worker_session_reason lane-unavailable")
			}
		})
	}
}

// A fork-series item runs the same route: the candidate is pinned out of the fork checkout,
// and the build step restages on another lane's fork checkout at the verified commit.
func TestVerifyReturnForkSeriesColdSecondLane(t *testing.T) {
	var fork2 string
	l, s, f := verifyReturnFixture(t, true, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w, verdict: verifyFinding, forkName: "pi"}
	})
	fork1 := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	fork2 = filepath.Join(f.root, ".forks/lanes/lane-2/pi")
	if err := os.MkdirAll(fork1, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork1, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(fork1, "base.txt"), "fork base\n")
	runGit(t, fork1, "add", ".")
	runGit(t, fork1, "commit", "-qm", "fork base")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "ignore fork checkout")
	runGit(t, f.root, "fetch", "--no-tags", fork1, "refs/heads/main:refs/heads/patch/pi")
	if err := os.MkdirAll(filepath.Dir(fork2), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "clone", "-q", "--no-tags", "-b", "main", fork1, fork2)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='[".forks/lanes/lane-1/pi"]'`)
		return err
	})
	offerColdLane(t, f, l, s)
	runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
	if s.plans != 1 || len(s.repairs) != 1 {
		t.Fatalf("plans=%d repairs=%d, want 1 and 1", s.plans, len(s.repairs))
	}
	if got := s.repairs[0].LaneID; got != "lane-2" {
		t.Errorf("repair lane = %s, want lane-2", got)
	}
	if s.repairHead[0] != s.verified {
		t.Errorf("repair started at %s, not the verified fork commit %s", s.repairHead[0], s.verified)
	}
	if s.pinnedAtRepair != s.verified {
		t.Errorf("pin %s = %q at the repair, want %s", pinRef(s.verifiedRun), s.pinnedAtRepair, s.verified)
	}
	if status := runGit(t, fork2, "status", "--porcelain"); status != "" {
		t.Errorf("lane-2 fork checkout is dirty: %s", status)
	}
}

// A narrowed build piece that reports the plan cut the work wrongly, with a complete fence
// obstacle whose paths are all inside the item's own fence, sends the item back to plan.
func TestReturnedPieceFenceObstacleRetriesPiece(t *testing.T) {
	report := "OBSTACLE: fence\nPATHS: tools/item-a/other.txt\nEVIDENCE: the piece's scope is narrower than the repair needs\nRESULT: blocked\n"
	l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w, expanded: true, buildPieces: true, verdict: verifyFinding, cannotMeet: report, widenedPath: "other.txt"}
	})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["tools/item-a/one.txt","tools/item-a/two.txt"]' WHERE item_id='item-a'`)
		return err
	})
	runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
	if s.plans != 1 || len(s.repairs) != 2 || s.repairs[0].NodeID != s.repairs[1].NodeID || s.repairs[0].RunID == s.repairs[1].RunID {
		t.Fatalf("expected a fresh run of the same piece without replanning: plans=%d repairs=%+v", s.plans, s.repairs)
	}
}

func TestOrdinaryPieceFenceObstacle(t *testing.T) {
	for _, outside := range []bool{false, true} {
		t.Run(fmt.Sprint(outside), func(t *testing.T) {
			paths := "tools/item-a/other.txt"
			if outside {
				paths += ", outside.txt"
			}
			report := "OBSTACLE: fence\nPATHS: " + paths + "\nEVIDENCE: need " + paths + "\nRESULT: blocked\n"
			l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
				return &verifyReturnSteps{workflowSteps: w, expanded: true, buildPieces: true, pieceObstacle: report, widenedPath: "other.txt"}
			})
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE items SET body_ref=NULL WHERE item_id='item-a'`); err != nil {
					return err
				}
				_, err := tx.Exec(`INSERT INTO request_words(item_id,words,words_hash,input_hash) VALUES('item-a',?,'words','input')`, []byte("Build both pieces"))
				return err
			})
			var reason, action string
			runVerifyReturn(t, f, l, s, func() bool {
				fixtureRead(t, f, func(r *store.Reader) error {
					holds, _, err := ReadObstacleHolds(r)
					reason, action = holds["item-a"].Reason, holds["item-a"].Action
					return err
				})
				return reason != "" || s.verifies > 0
			})
			if outside {
				if len(s.pieceRuns) != 1 || !strings.Contains(reason, "outside.txt") || !strings.Contains(action, "outside.txt") || strings.Contains(reason+action, "other.txt") {
					t.Fatalf("expected only the outside path in the hold with no retry: runs=%d reason=%q action=%q", len(s.pieceRuns), reason, action)
				}
			} else if reason != "" || s.plans != 1 || len(s.pieceRuns) != 2 || s.pieceRuns[0].RunID == s.pieceRuns[1].RunID {
				t.Fatalf("expected a fresh run of the same piece without a hold: plans=%d runs=%+v reason=%q", s.plans, s.pieceRuns, reason)
			}
		})
	}
}
