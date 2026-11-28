package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/splitclosing"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

type splitClosingSteps struct {
	*recordingSteps
	t            *testing.T
	f            refusalLoopFixture
	l            *Loop
	mu           sync.Mutex
	runs         []run.Request
	briefs       map[string]string
	onReview     func(req run.Request, brief, evalOut string) error
	onDesign     func(req run.Request, brief, extOut string) error
	onClosingCtx func(ctx context.Context, req run.Request, brief, evalOut string) (run.Record, string, error)
	onWorker     func(ctx context.Context, req run.Request) (run.Record, string, error)
}

func newSplitClosingSteps(t *testing.T, f refusalLoopFixture, l *Loop) *splitClosingSteps {
	t.Helper()
	return &splitClosingSteps{
		recordingSteps: &recordingSteps{},
		t:              t,
		f:              f,
		l:              l,
		briefs:         map[string]string{},
	}
}

func (s *splitClosingSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	before := runGit(s.t, s.f.root, "rev-parse", TrunkRef)
	runGit(s.t, s.f.root, "cherry-pick", c.HeadSHA)
	after := runGit(s.t, s.f.root, "rev-parse", TrunkRef)
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return LandOutcome{}, err
	}
	defer st.Close()
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'verdict','landed',?,?,?,?,999)`,
			"land-"+c.ItemID, c.ItemID, c.RunID, c.BaseSHA, c.HeadSHA, before, after,
		)
		return err
	}); err != nil {
		return LandOutcome{}, err
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: after}, nil
}

func (s *splitClosingSteps) Verdict(ctx context.Context, c Candidate, outcome string) error {
	if outcome != "pass" {
		return nil
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET state = 'done' WHERE item_id = ?`, c.ItemID)
		return err
	})
}

func (s *splitClosingSteps) ItemState(ctx context.Context, id, _ string, state string, waiting ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, id, state, s.l.cfg.Now().UTC(), waiting...)
		return err
	})
}

func (s *splitClosingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.Workflow != item.SplitClosingWorkflow {
		s.mu.Lock()
		s.runs = append(s.runs, req)
		s.mu.Unlock()
		if s.onWorker != nil {
			return s.onWorker(ctx, req)
		}
		return s.recordingSteps.Run(ctx, req)
	}
	_, _, durableDir := splitclosing.Dirs(s.l.cfg.Root, s.l.cfg.LaneRoot, s.l.laneDirPath, req.RunID)
	rawBrief, err := os.ReadFile(filepath.Join(durableDir, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	brief := string(rawBrief)

	s.mu.Lock()
	s.runs = append(s.runs, req)
	s.briefs[req.RunID] = brief
	s.mu.Unlock()

	if s.onClosingCtx != nil {
		return s.onClosingCtx(ctx, req, brief, filepath.Join(durableDir, "eval.output"))
	}

	switch req.Seat {
	case "review":
		if s.onReview != nil {
			if err := s.onReview(req, brief, filepath.Join(durableDir, "eval.output")); err != nil {
				return run.Record{}, "", err
			}
		}
	case "design":
		if s.onDesign != nil {
			if err := s.onDesign(req, brief, filepath.Join(durableDir, "split-extension.json")); err != nil {
				return run.Record{}, "", err
			}
		}
	}

	now := s.l.cfg.Now().UTC().Format(time.RFC3339)
	code := 0
	return run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		Seat:         req.Seat,
		Workflow:     req.Workflow,
		State:        run.StateExited,
		ExitCode:     &code,
		StartedAt:    now,
		FinishedAt:   now,
		ArtifactRefs: []run.ArtifactRef{},
	}, "RESULT: done\n", nil
}

func advanceSplitClosingAndSettle(t *testing.T, ctx context.Context, l *Loop, mainSHA string, result *LoopResult) (bool, error) {
	t.Helper()
	worked, err := l.advanceSplitClosingChecks(ctx, mainSHA, result)
	if err != nil || !worked {
		return worked, err
	}
	deadline := time.Now().Add(hangGuard)
	for {
		l.mu.Lock()
		active := len(l.splitClosing.active)
		l.mu.Unlock()
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for async split closing run to finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return l.advanceSplitClosingChecks(ctx, mainSHA, result)
}

func splitClosingFixture(t *testing.T) (refusalLoopFixture, *Loop, *splitClosingSteps, string, map[string]string) {
	t.Helper()
	f, l, parent := splitFixture(t)
	if l.cfg.Policy.Seats == nil {
		l.cfg.Policy.Seats = map[string]string{}
	}
	l.cfg.Policy.Seats["build"] = "fixture"
	l.cfg.Policy.Seats["review"] = "reviewer"
	l.cfg.Policy.Seats["design"] = "designer"
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture", Model: "review-model"}
	l.cfg.Policy.Backends["designer"] = Backend{Adapter: "fixture", Model: "design-model"}
	steps := newSplitClosingSteps(t, f, l)
	l.cfg.Steps = steps

	parts, err := FileSplit(context.Background(), f.root, f.storePath, parent, strings.NewReader(splitExample), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	return f, l, steps, parent, parts
}

func landPartOnTrunk(t *testing.T, f refusalLoopFixture, itemID, runID, filename, content string, seq int64) string {
	t.Helper()
	before := runGit(t, f.root, "rev-parse", TrunkRef)
	writeForkFile(t, filepath.Join(f.root, filename), content)
	runGit(t, f.root, "add", filename)
	runGit(t, f.root, "commit", "-qm", "land "+itemID)
	after := runGit(t, f.root, "rev-parse", TrunkRef)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, itemID); err != nil {
			return err
		}
		_, err := tx.Exec(
			`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'verdict','landed',?,?,?,?,?)`,
			"land-"+itemID, itemID, runID, before, after, before, after, seq,
		)
		return err
	})
	return after
}

func writeClosingEvalOutput(t *testing.T, f refusalLoopFixture, parent, evalOut, head, tree, outcome string, overrides map[string]item.JudgeEntry) {
	t.Helper()
	ctx := context.Background()
	var entries []item.JudgeEntry
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, parent)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, text, class string
			if err := rows.Scan(&id, &text, &class); err != nil {
				return err
			}
			if ov, ok := overrides[id]; ok {
				ov.CriterionID = id
				ov.CriterionHash = item.CriterionHash(text)
				ov.ClaimClass = class
				entries = append(entries, ov)
				continue
			}
			entries = append(entries, item.JudgeEntry{
				CriterionID:   id,
				CriterionHash: item.CriterionHash(text),
				Verdict:       item.VerdictDone,
				ClaimClass:    class,
				Reason:        "verified on trunk",
				Evidence:      []string{"cmd:closing-check"},
			})
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	body := item.JudgeOutput{
		EvaluatedHead: head,
		EvaluatedTree: tree,
		Outcome:       outcome,
		Verdicts:      entries,
	}
	if err := splitclosing.WriteAtomicJSON(evalOut, body); err != nil {
		t.Fatal(err)
	}
}

func parentWhyRow(t *testing.T, ctx context.Context, l *Loop, storePath, itemID string) WhyRow {
	t.Helper()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got WhyRow
	var found bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		a, err := WhyForItem(r, Pass{Now: l.cfg.Now().Format(time.RFC3339)}, l.cfg.Policy, nil, itemID)
		if err != nil {
			return err
		}
		for _, row := range a.Rows {
			if row.ItemID == itemID {
				got = row
				found = true
				return nil
			}
		}
		return errors.New("missing why row for " + itemID)
	}); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("missing why row for %s", itemID)
	}
	return got
}

func TestSplitClosingCheckPassesWhenLastRequiredPartLands(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()

	commitFirst := landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
	commitSecond := landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)
	trunkBefore := runGit(t, f.root, "rev-parse", TrunkRef)
	treeBefore := runGit(t, f.root, "rev-parse", trunkBefore+"^{tree}")

	var seenBrief string
	var runID string
	steps.onReview = func(req run.Request, brief, evalOut string) error {
		seenBrief = brief
		runID = req.RunID
		writeClosingEvalOutput(t, f, parent, evalOut, trunkBefore, treeBefore, "pass", nil)
		return nil
	}

	worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{})
	if err != nil || !worked {
		t.Fatalf("advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}
	if len(steps.runs) != 1 {
		t.Fatalf("expected 1 review run, got %d", len(steps.runs))
	}
	if steps.runs[0].Seat != "review" || steps.runs[0].Workflow != item.SplitClosingWorkflow || steps.runs[0].NodeID != item.SplitClosingNode {
		t.Fatalf("unexpected review request: %+v", steps.runs[0])
	}

	for _, want := range []string{
		"Owner's words",
		"Keep this whitespace.",
		"c5",
		"A works； B works.",
		"c6",
		"Together they work",
		`"closing_check": true`,
		commitFirst,
		commitSecond,
		parts["extra"] + "): required=false state=in-queue commit=",
	} {
		if !strings.Contains(seenBrief, want) {
			t.Errorf("closing check brief missing %q:\n%s", want, seenBrief)
		}
	}

	if trunkAfter := runGit(t, f.root, "rev-parse", TrunkRef); trunkAfter != trunkBefore {
		t.Fatalf("closing check moved trunk from %s to %s", trunkBefore, trunkAfter)
	}

	recordPath := filepath.Join(RunDir(f.root, runID), "closing-check.json")
	rawRecord, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("reading closing-check.json: %v", err)
	}
	var rec splitclosing.Record
	if err := json.Unmarshal(rawRecord, &rec); err != nil {
		t.Fatalf("unmarshaling closing-check.json: %v", err)
	}
	if rec.ItemID != parent || rec.Landed || rec.TrunkSHA != trunkBefore || len(rec.Parts) != 3 {
		t.Fatalf("unexpected closing-check.json: %+v", rec)
	}
	wantCommits := map[string]string{
		"first":  commitFirst,
		"second": commitSecond,
		"extra":  "",
	}
	for _, p := range rec.Parts {
		if p.Commit != wantCommits[p.Label] {
			t.Errorf("closing-check.json part %s commit=%q want %q", p.Label, p.Commit, wantCommits[p.Label])
		}
	}

	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, parent).Scan(&state); err != nil {
			return err
		}
		if state != StateLanded {
			t.Fatalf("parent state=%q, want %q", state, StateLanded)
		}

		rows, err := r.Query(`SELECT payload FROM events WHERE kind='item.verdict' AND object_id=? ORDER BY seq`, parent)
		if err != nil {
			return err
		}
		defer rows.Close()
		verdicts := map[string]string{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var v struct {
				CriterionID string `json:"criterion_id"`
				Verdict     string `json:"verdict"`
			}
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			verdicts[v.CriterionID] = v.Verdict
		}
		if err := rows.Err(); err != nil {
			return err
		}
		wantVerdicts := map[string]string{}
		cRows, err := r.Query(`SELECT criterion_id FROM item_criteria WHERE item_id=?`, parent)
		if err != nil {
			return err
		}
		for cRows.Next() {
			var cid string
			if err := cRows.Scan(&cid); err != nil {
				cRows.Close()
				return err
			}
			wantVerdicts[cid] = item.VerdictDone
		}
		if err := cRows.Err(); err != nil {
			cRows.Close()
			return err
		}
		cRows.Close()
		if !reflect.DeepEqual(verdicts, wantVerdicts) {
			t.Fatalf("parent verdicts=%v, want %v", verdicts, wantVerdicts)
		}

		var evRaw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind='drain.graph-completed' AND object_id=?`, runID).Scan(&evRaw); err != nil {
			return err
		}
		var ev struct {
			ItemID string                  `json:"item_id"`
			Landed bool                    `json:"landed"`
			Parts  []item.SplitPartLanding `json:"parts"`
		}
		if err := json.Unmarshal(evRaw, &ev); err != nil {
			return err
		}
		if ev.ItemID != parent || ev.Landed || len(ev.Parts) != 3 {
			t.Fatalf("unexpected drain.graph-completed payload: %s", evRaw)
		}
		for _, p := range ev.Parts {
			if p.Commit != wantCommits[p.Label] {
				t.Errorf("event part %s commit=%q want %q", p.Label, p.Commit, wantCommits[p.Label])
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	downstream := parentWhyRow(t, ctx, l, f.storePath, "queue-after")
	if downstream.State != StateInQueue || downstream.Decision.Reason == ReasonBlocked {
		t.Fatalf("downstream item still blocked after parent landed: %+v", downstream)
	}
	for _, w := range downstream.WaitingOn {
		if w == parent {
			t.Fatalf("downstream item still waiting on parent %s: %+v", parent, downstream)
		}
	}
}

func TestSplitClosingCheckFindingExtendsSplitAndPasses(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()

	commitFirst := landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
	commitSecond := landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)

	reviewCall := 0
	var designBrief string
	var secondReviewRunID string
	steps.onReview = func(req run.Request, _ string, evalOut string) error {
		reviewCall++
		head := runGit(t, f.root, "rev-parse", TrunkRef)
		tree := runGit(t, f.root, "rev-parse", head+"^{tree}")
		if reviewCall == 1 {
			writeClosingEvalOutput(t, f, parent, evalOut, head, tree, "fail", map[string]item.JudgeEntry{
				"c6": {
					Verdict:  item.VerdictNotDone,
					Reason:   "missing seam between A and B",
					Evidence: []string{"cmd:closing-check"},
					Findings: item.VerdictFindings{{Kind: "gap", Case: "A and B are not wired", Paths: []string{"file:second.txt"}}},
				},
			})
			return nil
		}
		secondReviewRunID = req.RunID
		writeClosingEvalOutput(t, f, parent, evalOut, head, tree, "pass", nil)
		return nil
	}
	steps.onDesign = func(req run.Request, brief, extOut string) error {
		designBrief = brief
		ext := `{
  "parts": [
    {
      "label": "third",
      "title": "Third part",
      "share": "Wire A and B together",
      "leaves": {"first": "Deliver A", "second": "Deliver B"},
      "criteria": ["A and B work together"],
      "follows": ["second"],
      "required": true
    }
  ]
}`
		return os.WriteFile(extOut, []byte(ext), 0o644)
	}

	// Step 1: First closing check finds missing seam.
	if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
		t.Fatalf("step 1 advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}
	whyFinding := parentWhyRow(t, ctx, l, f.storePath, parent)
	if whyFinding.State != StateBlocked || whyFinding.Class != "waits on work" || !strings.HasPrefix(whyFinding.Mover, "design") || !strings.Contains(whyFinding.Why, "missing seam between A and B") {
		t.Fatalf("parent why after finding: %+v", whyFinding)
	}

	// Step 2: Design seat extends the split.
	if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
		t.Fatalf("step 2 advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}
	if !strings.Contains(designBrief, "c6: missing seam between A and B") {
		t.Fatalf("design brief missing finding:\n%s", designBrief)
	}

	var thirdID string
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		stmt, ids, err := item.ReadSplit(r, parent)
		if err != nil {
			return err
		}
		if len(stmt.Parts) != 4 {
			t.Fatalf("expected 4 parts after extension, got %d", len(stmt.Parts))
		}
		for _, lbl := range []string{"first", "second", "extra", "third"} {
			if ids[lbl] == "" {
				t.Fatalf("missing part %q in ids: %v", lbl, ids)
			}
		}
		thirdID = ids["third"]
		edges, err := readEdges(r)
		if err != nil {
			return err
		}
		if len(edges[parent]) != 3 {
			t.Fatalf("expected 3 parent blocked_by edges, got %v", edges[parent])
		}
		if !reflect.DeepEqual(edges[thirdID], []itemEdge{{Target: parts["second"], Kind: item.EdgeKindBlockedBy}}) {
			t.Fatalf("unexpected edges for third part: %v", edges[thirdID])
		}
		return nil
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	whyExtended := parentWhyRow(t, ctx, l, f.storePath, parent)
	if whyExtended.State != StateBlocked || !reflect.DeepEqual(whyExtended.WaitingOn, []string{thirdID}) {
		t.Fatalf("parent why after extension: %+v", whyExtended)
	}

	// While third is unlanded, closing check must not run again.
	if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || worked {
		t.Fatalf("closing check ran before new part landed: worked=%v err=%v", worked, err)
	}

	// Step 3: Land the new part.
	commitThird := landPartOnTrunk(t, f, thirdID, "run-third", "third.txt", "A and B wired\n", 103)

	// Step 4: Second closing check passes and finishes parent.
	if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
		t.Fatalf("step 4 advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}
	if reviewCall != 2 {
		t.Fatalf("expected 2 review runs, got %d", reviewCall)
	}

	rawRec, err := os.ReadFile(filepath.Join(RunDir(f.root, secondReviewRunID), "closing-check.json"))
	if err != nil {
		t.Fatalf("reading second closing-check.json: %v", err)
	}
	var rec splitclosing.Record
	if err := json.Unmarshal(rawRec, &rec); err != nil {
		t.Fatalf("unmarshaling second closing-check.json: %v", err)
	}
	if len(rec.Parts) != 4 {
		t.Fatalf("expected 4 parts in closing-check.json, got %+v", rec.Parts)
	}
	wantCommits := map[string]string{
		"first":  commitFirst,
		"second": commitSecond,
		"extra":  "",
		"third":  commitThird,
	}
	for _, p := range rec.Parts {
		if p.Commit != wantCommits[p.Label] {
			t.Errorf("part %s commit=%q want %q", p.Label, p.Commit, wantCommits[p.Label])
		}
	}

	s2, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.ReadTx(ctx, func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, parent).Scan(&state); err != nil {
			return err
		}
		if state != StateLanded {
			t.Fatalf("parent state=%q want %q", state, StateLanded)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSplitClosingExtensionRefusalAndMissingOutput(t *testing.T) {
	t.Run("malformed extension refused atomically and repeated refusal holds on design", func(t *testing.T) {
		f, l, steps, parent, parts := splitClosingFixture(t)
		ctx := context.Background()

		landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
		landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)

		steps.onReview = func(req run.Request, _ string, evalOut string) error {
			head := runGit(t, f.root, "rev-parse", TrunkRef)
			tree := runGit(t, f.root, "rev-parse", head+"^{tree}")
			writeClosingEvalOutput(t, f, parent, evalOut, head, tree, "fail", map[string]item.JudgeEntry{
				"c6": {
					Verdict:  item.VerdictNotDone,
					Reason:   "missing seam",
					Evidence: []string{"cmd:closing-check"},
					Findings: item.VerdictFindings{{Kind: "gap", Case: "seam missing", Paths: []string{"file:a.txt"}}},
				},
			})
			return nil
		}

		var designBriefs []string
		steps.onDesign = func(_ run.Request, brief, extOut string) error {
			designBriefs = append(designBriefs, brief)
			// Malformed: unknown field "extra_bad_field"
			return os.WriteFile(extOut, []byte(`{"parts":[{"label":"third","title":"Third","share":"Wire","required":true,"extra_bad_field":1}]}`), 0o644)
		}

		// Closing check produces finding.
		if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("finding pass: worked=%v err=%v", worked, err)
		}

		snapshotCounts := func() (items, edges, splits int, stmt string) {
			s, err := store.OpenRead(ctx, f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				if err := r.QueryRow(`SELECT count(*) FROM items`).Scan(&items); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT count(*) FROM item_edges`).Scan(&edges); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT count(*) FROM item_splits`).Scan(&splits); err != nil {
					return err
				}
				return r.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&stmt)
			}); err != nil {
				t.Fatal(err)
			}
			return items, edges, splits, stmt
		}

		beforeItems, beforeEdges, beforeSplits, beforeStmt := snapshotCounts()

		// First design run: malformed extension is refused atomically.
		if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("first design pass: worked=%v err=%v", worked, err)
		}
		afterItems, afterEdges, afterSplits, afterStmt := snapshotCounts()
		if afterItems != beforeItems || afterEdges != beforeEdges || afterSplits != beforeSplits || afterStmt != beforeStmt {
			t.Fatalf("refused extension mutated store: items %d->%d edges %d->%d splits %d->%d", beforeItems, afterItems, beforeEdges, afterEdges, beforeSplits, afterSplits)
		}

		whyRefused := parentWhyRow(t, ctx, l, f.storePath, parent)
		if whyRefused.Class != "waits on work" || !strings.HasPrefix(whyRefused.Mover, "design") || !strings.Contains(whyRefused.Why, "prior extension refused:") || !strings.Contains(whyRefused.Why, "unknown field") {
			t.Fatalf("parent why after first refusal: %+v", whyRefused)
		}

		// Second design run: receives prior refusal reason; repeating same invalid output holds on design.
		if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("second design pass: worked=%v err=%v", worked, err)
		}
		if len(designBriefs) != 2 || !strings.Contains(designBriefs[1], "## Prior Extension Refusal") || !strings.Contains(designBriefs[1], "unknown field") {
			t.Fatalf("second design brief missing prior refusal:\n%v", designBriefs)
		}

		whyHeld := parentWhyRow(t, ctx, l, f.storePath, parent)
		if whyHeld.State != StateBlocked || whyHeld.Class != "needs you" || !strings.HasPrefix(whyHeld.Mover, "operator;") {
			t.Fatalf("parent why after repeated refusal: %+v", whyHeld)
		}

		// Held item must not dispatch another design run.
		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || worked {
			t.Fatalf("held parent still advanced: worked=%v err=%v", worked, err)
		}
		if len(designBriefs) != 2 {
			t.Fatalf("expected no further design runs after hold, got %d", len(designBriefs))
		}
	})

	t.Run("missing design output holds on design seat", func(t *testing.T) {
		f, l, steps, parent, parts := splitClosingFixture(t)
		ctx := context.Background()

		landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
		landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)

		steps.onReview = func(req run.Request, _ string, evalOut string) error {
			head := runGit(t, f.root, "rev-parse", TrunkRef)
			tree := runGit(t, f.root, "rev-parse", head+"^{tree}")
			writeClosingEvalOutput(t, f, parent, evalOut, head, tree, "fail", map[string]item.JudgeEntry{
				"c6": {
					Verdict:  item.VerdictNotDone,
					Reason:   "missing seam",
					Evidence: []string{"cmd:closing-check"},
					Findings: item.VerdictFindings{{Kind: "gap", Case: "seam missing", Paths: []string{"file:a.txt"}}},
				},
			})
			return nil
		}
		designCalls := 0
		steps.onDesign = func(_ run.Request, _, _ string) error {
			designCalls++
			// Intentionally do not write split-extension.json.
			return nil
		}

		if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("finding pass: worked=%v err=%v", worked, err)
		}
		if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("missing output pass: worked=%v err=%v", worked, err)
		}
		if designCalls != 1 {
			t.Fatalf("expected 1 design call, got %d", designCalls)
		}

		whyHeld := parentWhyRow(t, ctx, l, f.storePath, parent)
		if whyHeld.State != StateBlocked || whyHeld.Class != "needs you" || !strings.HasPrefix(whyHeld.Mover, "operator;") || !strings.Contains(whyHeld.Why, "missing split-extension.json") {
			t.Fatalf("parent why after missing design output: %+v", whyHeld)
		}

		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || worked {
			t.Fatalf("held parent still advanced: worked=%v err=%v", worked, err)
		}
		if designCalls != 1 {
			t.Fatalf("expected no further design calls after hold, got %d", designCalls)
		}
	})
}

func TestSplitClosingRequiredPartClosedWithoutLanding(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()

	landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetStateWithReceipt(ctx, tx, parts["second"], StateResolvedElsewhere, l.cfg.Now(), item.MoveReceipt{
			By: "operator", Reason: "closed without landing", ResolvesVia: "item:" + parts["first"],
		})
		return err
	})

	why := parentWhyRow(t, ctx, l, f.storePath, parent)
	if why.State != StateBlocked || why.Decision.Action != ActionHold || why.Class != "needs you" ||
		!strings.HasPrefix(why.Mover, "owner;") || !strings.Contains(why.Why, "required part did not land") {
		t.Fatalf("unexpected parent why when required part closed without landing: %+v", why)
	}

	worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{})
	if err != nil || worked {
		t.Fatalf("closing check should not run when required part closed without landing: worked=%v err=%v", worked, err)
	}
	if len(steps.runs) != 0 {
		t.Fatalf("expected 0 closing/design runs, got %d", len(steps.runs))
	}
}

func TestSplitClosingBriefsPassPreflight(t *testing.T) {
	preflightPath, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(preflightPath); err != nil {
		t.Fatalf("preflight.sh missing at %s: %v", preflightPath, err)
	}

	_, l, _, parent, parts := splitClosingFixture(t)
	criteria := []splitclosing.Criterion{
		{ID: "c1", Text: "First criterion", ClaimClass: "verification", Hash: item.CriterionHash("First criterion")},
	}
	reqTrue := true
	statement := &item.SplitStatement{
		Parts: []item.SplitPart{
			{Label: "first", Title: "First", Share: "Part A", Required: &reqTrue},
		},
		Coverage: map[string][]item.SplitCoverage{
			"c1": {{Part: "first", ClosingCheck: true}},
		},
	}
	landings := []item.SplitPartLanding{
		{Label: "first", ItemID: parts["first"], Required: true, State: StateLanded, Commit: "1234567890abcdef1234567890abcdef12345678"},
	}
	words := []byte("Request words containing literal {{braces}} to verify brace-literals.")

	checkPreflight := func(name, runID, brief string) {
		t.Helper()
		_, laneDir, durableDir := splitclosing.Dirs(l.cfg.Root, l.cfg.LaneRoot, l.laneDirPath, runID)
		if err := splitclosing.StageBriefFiles(l.cfg.Root, runID, laneDir, durableDir, brief, braceLiteralCount(brief)); err != nil {
			t.Fatalf("%s StageBriefFiles: %v", name, err)
		}
		for _, briefPath := range []string{filepath.Join(durableDir, BriefFile), filepath.Join(laneDir, BriefFile)} {
			cmd := exec.Command("sh", "-c", `PROG=run-pi; . "$1"; preflight_brief "$2"`, "_", preflightPath, briefPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s preflight_brief(%s) failed: %v\n%s", name, briefPath, err, out)
			}
		}
	}

	closingBrief := splitclosing.FormatClosingBrief(
		parent,
		"closing-preflight-1",
		l.cfg.Root,
		"1234567890abcdef1234567890abcdef12345678",
		"abcdef1234567890abcdef1234567890abcdef12",
		filepath.Join(t.TempDir(), "eval.output"),
		seatLines(l.currentPolicy()),
		words,
		requestWordsSuffix,
		criteria,
		statement,
		landings,
	)
	checkPreflight("closing-check", "closing-preflight-1", closingBrief)

	extBrief := splitclosing.FormatExtensionBrief(
		parent,
		"closing-extend-preflight-1",
		l.cfg.Root,
		filepath.Join(t.TempDir(), "split-extension.json"),
		seatLines(l.currentPolicy()),
		words,
		requestWordsSuffix,
		criteria,
		statement,
		landings,
		&splitclosing.FindingPayload{
			ItemID:   parent,
			RunID:    "closing-preflight-1",
			TrunkSHA: "1234567890abcdef1234567890abcdef12345678",
			Findings: []splitclosing.FindingEntry{{CriterionID: "c1", Reason: "missing seam"}},
		},
		&splitclosing.RefusedPayload{Reason: "prior refusal"},
	)
	checkPreflight("closing-extend", "closing-extend-preflight-1", extBrief)

	defBrief := splitclosing.FormatDefectiveBrief(
		parent,
		"defective-extend-preflight-1",
		l.cfg.Root,
		filepath.Join(t.TempDir(), "split-extension.json"),
		seatLines(l.currentPolicy()),
		words,
		requestWordsSuffix,
		criteria,
		statement,
		landings,
		[]item.DefectivePartFindings{{
			PartID:      parts["first"],
			CloseReason: "defective: forbidden test change",
			ClosedBy:    "verifier",
			Verdicts:    []item.DefectiveVerdict{{CriterionID: "c1", Reason: "forbidden test change"}},
		}},
		&splitclosing.RefusedPayload{Reason: "prior refusal"},
	)
	checkPreflight("defective-extend", "defective-extend-preflight-1", defBrief)
}

func TestSplitClosingDefectiveRequiredPartReplacedByDesign(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()
	for _, seat := range []string{"plan", "verify", "test-run", "test-write", "edit", "tricky"} {
		l.cfg.Policy.Seats[seat] = "fixture"
	}

	// Close "first" as defective with a defective verdict.
	// "second" follows "first" in the fixture.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var cID, cText, cClass string
		if err := tx.QueryRow(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id LIMIT 1`, parts["first"]).Scan(&cID, &cText, &cClass); err != nil {
			return err
		}
		vPayload, err := json.Marshal(map[string]any{
			"item_id":        parts["first"],
			"criterion_id":   cID,
			"criterion_hash": item.CriterionHash(cText),
			"verdict":        "defective",
			"claim_class":    cClass,
			"reason":         "part words forbid test change the work needs",
			"evidence":       []string{"file:tools/tillr-drain/drain/split.go:1"},
			"actor_kind":     "agent",
			"actor":          "verifier",
		})
		if err != nil {
			return err
		}
		firstID := parts["first"]
		if _, err := tx.Append(event.Event{
			Kind:       item.VerdictRecordKind,
			ObjectType: "item",
			ObjectID:   &firstID,
			Payload:    vPayload,
			At:         l.cfg.Now(),
		}); err != nil {
			return err
		}
		_, err = item.Close(ctx, tx, parts["first"], "defective: part words forbid test change the work needs", "verifier", l.cfg.Now().Add(time.Second))
		return err
	})

	// Before advancing: parent Why must NOT name owner, and sibling Why must NOT name operator/remove the dependency.
	pWhy := parentWhyRow(t, ctx, l, f.storePath, parent)
	if pWhy.Class != "waits on work" || pWhy.Mover != "design" || strings.Contains(pWhy.Mover, "owner") {
		t.Fatalf("parent why before design = %+v, want waits on work / design", pWhy)
	}
	sWhy := parentWhyRow(t, ctx, l, f.storePath, parts["second"])
	if sWhy.Class != "waits on work" || !strings.Contains(sWhy.Mover, "design") || strings.Contains(sWhy.Mover, "operator") || strings.Contains(sWhy.Mover, "remove the dependency") {
		t.Fatalf("sibling why before design = %+v, want waits on work / design", sWhy)
	}

	// Advancing the loop starts a design run on the parent carrying the verifier's finding,
	// and files a replacement SplitExtension.
	var designBrief string
	steps.onDesign = func(req run.Request, brief, extOut string) error {
		if req.Seat != "design" || req.ItemID != parent {
			return fmt.Errorf("unexpected run %+v", req)
		}
		designBrief = brief
		body := `{
  "replaces": ["first"],
  "parts": [
    {
      "label": "first-v2",
      "title": "First part corrected",
      "share": "Deliver A including needed test change",
      "leaves": {"second": "Deliver B"},
      "criteria": ["A works with tests"],
      "follows": [],
      "required": true
    }
  ],
  "coverage": {
    "c5": [
      {"clause": "A works；", "part": "first-v2"},
      {"clause": "B works.", "part": "second"}
    ],
    "c6": [{"closing_check": true}]
  }
}`
		return os.WriteFile(extOut, []byte(body), 0o644)
	}

	worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{})
	if err != nil || !worked {
		t.Fatalf("advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}
	if len(steps.runs) != 1 {
		t.Fatalf("expected 1 design run, got %d", len(steps.runs))
	}
	if !strings.Contains(designBrief, "part words forbid test change the work needs") {
		t.Fatalf("design brief missing verifier finding:\n%s", designBrief)
	}

	// Verify replacement took closed part's place in parent's required parts, coverage, and sibling's dependencies.
	firstV2ID := item.SplitPartID(parent, "first-v2")
	s, err := store.Open(ctx, f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		req, err := item.SplitRequiredParts(r, parent)
		if err != nil {
			return err
		}
		wantReq := []string{firstV2ID, parts["second"]}
		slices.Sort(req)
		slices.Sort(wantReq)
		if !slices.Equal(req, wantReq) {
			t.Fatalf("SplitRequiredParts = %v, want %v", req, wantReq)
		}
		stmt, _, err := item.ReadSplit(r, parent)
		if err != nil {
			return err
		}
		if len(stmt.Coverage["c5"]) != 2 || stmt.Coverage["c5"][0].Part != "first-v2" {
			t.Fatalf("unexpected coverage c5: %+v", stmt.Coverage["c5"])
		}
		var sibBlocked string
		if err := r.QueryRow(`SELECT group_concat(blocked_by) FROM item_edges WHERE item_id=? AND kind='blocked_by'`, parts["second"]).Scan(&sibBlocked); err != nil {
			return err
		}
		if sibBlocked != firstV2ID {
			t.Fatalf("second blocked_by = %q, want %q", sibBlocked, firstV2ID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSplitClosingDefectiveRequiredPartOwnerDecision(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()
	for _, seat := range []string{"plan", "verify", "test-run", "test-write", "edit", "tricky"} {
		l.cfg.Policy.Seats[seat] = "fixture"
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, parts["first"], "defective: request contradicts itself", "verifier", l.cfg.Now())
		return err
	})

	steps.onDesign = func(_ run.Request, _, extOut string) error {
		body := `{"owner_decision": true, "question": "Should A or B win?"}`
		return os.WriteFile(extOut, []byte(body), 0o644)
	}

	worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{})
	if err != nil || !worked {
		t.Fatalf("advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}

	pWhy := parentWhyRow(t, ctx, l, f.storePath, parent)
	if pWhy.Class != "needs you" || pWhy.Mover != "owner; Should A or B win?" {
		t.Fatalf("parent why after owner_decision = %+v", pWhy)
	}
	sWhy := parentWhyRow(t, ctx, l, f.storePath, parts["second"])
	if sWhy.Class != "needs you" || sWhy.Mover != "owner; Should A or B win?" {
		t.Fatalf("sibling why after owner_decision = %+v", sWhy)
	}
}

func TestSplitClosingCheckNonBlockingLetsSecondItemLand(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Land both required parts so parent is ready for its closing check.
	// Resolve optional extra and queue-after so the only active work is
	// parent's closing check and the independent second item "queue-before".
	landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
	landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, id := range []string{parts["extra"], "queue-after"} {
			if _, err := item.SetStateWithReceipt(ctx, tx, id, StateResolvedElsewhere, l.cfg.Now(), item.MoveReceipt{
				By: "operator", Reason: "out of scope for test", ResolvesVia: "item:" + parts["first"],
			}); err != nil {
				return err
			}
		}
		return nil
	})

	trunkAtStart := runGit(t, f.root, "rev-parse", TrunkRef)
	treeAtStart := runGit(t, f.root, "rev-parse", trunkAtStart+"^{tree}")

	closingStarted := make(chan string, 1)
	releaseClosing := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClosing) }) }
	defer release()
	var closingRuns int
	var closingMu sync.Mutex

	steps.onClosingCtx = func(runCtx context.Context, req run.Request, _ string, evalOut string) (run.Record, string, error) {
		if req.TimeoutMS != 0 {
			t.Errorf("closing check should not add a time limit, got TimeoutMS=%d", req.TimeoutMS)
		}
		closingMu.Lock()
		closingRuns++
		closingMu.Unlock()
		select {
		case closingStarted <- req.RunID:
		default:
		}
		select {
		case <-releaseClosing:
		case <-runCtx.Done():
			return run.Record{}, "", runCtx.Err()
		}
		writeClosingEvalOutput(t, f, parent, evalOut, trunkAtStart, treeAtStart, "pass", nil)
		now := l.cfg.Now().UTC().Format(time.RFC3339)
		code := 0
		return run.Record{
			RunID:        req.RunID,
			ItemID:       req.ItemID,
			LaneID:       req.LaneID,
			Backend:      req.Backend,
			Seat:         req.Seat,
			Workflow:     req.Workflow,
			State:        run.StateExited,
			ExitCode:     &code,
			StartedAt:    now,
			FinishedAt:   now,
			ArtifactRefs: []run.ArtifactRef{},
		}, "RESULT: done\n", nil
	}

	steps.onWorker = func(runCtx context.Context, req run.Request) (run.Record, string, error) {
		wt := l.lanePath(req.LaneID)
		if err := os.WriteFile(filepath.Join(wt, "second-item.txt"), []byte("second item work\n"), 0o644); err != nil {
			return run.Record{}, "", err
		}
		runGit(t, wt, "add", "second-item.txt")
		runGit(t, wt, "commit", "-m", "second item commit")
		now := l.cfg.Now().UTC().Format(time.RFC3339)
		code := 0
		rec := run.Record{
			RunID:        req.RunID,
			ItemID:       req.ItemID,
			LaneID:       req.LaneID,
			Backend:      req.Backend,
			Seat:         req.Seat,
			Workflow:     req.Workflow,
			State:        run.StateExited,
			ExitCode:     &code,
			StartedAt:    now,
			FinishedAt:   now,
			ArtifactRefs: []run.ArtifactRef{},
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, rec)
		})
		return rec, "RESULT: done\n", nil
	}

	l.cfg.ExitWhenDrained = true
	l.cfg.StorePollInterval = 100 * time.Millisecond

	loopDone := make(chan LoopResult, 1)
	loopErr := make(chan error, 1)
	go func() {
		res, err := l.Run(ctx)
		if err != nil {
			loopErr <- err
			return
		}
		loopDone <- res
	}()

	var closingRunID string
	select {
	case closingRunID = <-closingStarted:
	case err := <-loopErr:
		t.Fatalf("loop failed before closing check started: %v", err)
	case res := <-loopDone:
		t.Fatalf("loop exited prematurely before closing check started: %+v", res)
	case <-ctx.Done():
		t.Fatal("timed out waiting for closing check to start")
	}

	// While the closing check is still live, the second item ("queue-before")
	// must be dispatched, settled, gated, and landed, and passes must be recorded.
	readDB, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer readDB.Close()
	readStateAndPasses := func() (secondState, parentState string, passCount int) {
		if err := readDB.ReadTx(ctx, func(r *store.Reader) error {
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id='queue-before'`).Scan(&secondState); err != nil {
				return err
			}
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, parent).Scan(&parentState); err != nil {
				return err
			}
			return r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, LoopPassKind).Scan(&passCount)
		}); err != nil {
			t.Fatal(err)
		}
		return secondState, parentState, passCount
	}

	deadline := time.Now().Add(hangGuard)
	for {
		secondState, parentState, passCount := readStateAndPasses()
		if secondState == StateLanded && passCount > 0 {
			if parentState != StateBlocked {
				t.Fatalf("parent settled before closing check finished: state=%q", parentState)
			}
			break
		}
		select {
		case err := <-loopErr:
			t.Fatalf("loop failed while closing check was live: %v", err)
		case res := <-loopDone:
			t.Fatalf("loop exited while closing check was live: %+v", res)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("second item did not land while closing check was live: second=%q parent=%q passes=%d why=%+v", secondState, parentState, passCount, parentWhyRow(t, ctx, l, f.storePath, "queue-before"))
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Release the closing check; a later pass must settle it and land the parent.
	release()

	select {
	case err := <-loopErr:
		t.Fatalf("loop failed after closing check finished: %v", err)
	case res := <-loopDone:
		if res.Stop != StopQueueDrained {
			t.Fatalf("loop stop = %q, want %q (detail=%q)", res.Stop, StopQueueDrained, res.Detail)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for loop to settle closing check and drain")
	}

	_, parentState, _ := readStateAndPasses()
	if parentState != StateLanded {
		t.Fatalf("parent state after closing check settled = %q, want %q", parentState, StateLanded)
	}
	closingMu.Lock()
	gotRuns := closingRuns
	closingMu.Unlock()
	if gotRuns != 1 {
		t.Fatalf("expected 1 closing check run, got %d", gotRuns)
	}
	if _, err := os.Stat(filepath.Join(RunDir(f.root, closingRunID), "closing-check.json")); err != nil {
		t.Fatalf("closing-check.json missing after settlement: %v", err)
	}
}

func TestSplitClosingCheckSurvivesLoopRestart(t *testing.T) {
	t.Run("live across restart neither loses result nor starts a second check", func(t *testing.T) {
		f, l1, steps1, parent, parts := splitClosingFixture(t)
		ctx1, cancel1 := context.WithCancel(context.Background())
		defer cancel1()

		landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
		landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)
		trunkBefore := runGit(t, f.root, "rev-parse", TrunkRef)
		treeBefore := runGit(t, f.root, "rev-parse", trunkBefore+"^{tree}")

		// Create a fake run-face script ("sh exec --input x --lane-root y")
		// whose argv matches IsRunFaceArgv so liveRunFaces recognizes it across restart.
		faceDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(faceDir, "exec"), []byte("read _\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		storeIdentity, err := processStoreIdentity(f.storePath)
		if err != nil {
			t.Fatal(err)
		}

		started := make(chan struct {
			runID   string
			evalOut string
			cmd     *exec.Cmd
			stdin   io.WriteCloser
		}, 1)

		steps1.onClosingCtx = func(runCtx context.Context, req run.Request, _ string, evalOut string) (run.Record, string, error) {
			cmd := exec.Command("sh", "exec", "--input", "req.json", "--lane-root", f.laneRoot)
			cmd.Dir = faceDir
			cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+storeIdentity)
			stdin, err := cmd.StdinPipe()
			if err != nil {
				return run.Record{}, "", err
			}
			if err := cmd.Start(); err != nil {
				return run.Record{}, "", err
			}
			started <- struct {
				runID   string
				evalOut string
				cmd     *exec.Cmd
				stdin   io.WriteCloser
			}{runID: req.RunID, evalOut: evalOut, cmd: cmd, stdin: stdin}
			<-runCtx.Done()
			return run.Record{}, "", runCtx.Err()
		}

		// Loop 1 starts the closing check asynchronously.
		if worked, err := l1.advanceSplitClosingChecks(ctx1, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("loop 1 start: worked=%v err=%v", worked, err)
		}
		live := <-started
		defer func() {
			if live.stdin != nil {
				_ = live.stdin.Close()
			}
			if live.cmd.Process != nil {
				_ = live.cmd.Process.Kill()
				_ = live.cmd.Wait()
			}
		}()

		// Loop 1 restarts while the closing check run face stays alive.
		cancel1()
		deadline := time.Now().Add(hangGuard)
		for {
			l1.mu.Lock()
			n := len(l1.splitClosing.active)
			l1.mu.Unlock()
			if n == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("loop 1 closing check waiter did not exit on restart")
			}
			time.Sleep(5 * time.Millisecond)
		}

		// Loop 2 starts on the same store with fresh in-memory state.
		l2 := f.loop(t, &recordingSteps{}, &itemContentStager{})
		l2.cfg.Policy = l1.cfg.Policy
		steps2 := newSplitClosingSteps(t, f, l2)
		l2.cfg.Steps = steps2

		ctx2 := context.Background()
		// Pass on Loop 2 while fakeFace is still alive: must NOT start a second check.
		if worked, err := l2.advanceSplitClosingChecks(ctx2, "", &LoopResult{}); err != nil || worked {
			t.Fatalf("loop 2 pass while live: worked=%v err=%v", worked, err)
		}
		if len(steps2.runs) != 0 {
			t.Fatalf("loop 2 started a second closing check while first was live: %+v", steps2.runs)
		}
		if !l2.hasSplitClosingWork() {
			t.Fatal("loop 2 did not retain pending state for live inherited closing check")
		}

		// Now the inherited closing check finishes: writes eval.output and terminal run record, then exits.
		writeClosingEvalOutput(t, f, parent, live.evalOut, trunkBefore, treeBefore, "pass", nil)
		now := l2.cfg.Now().UTC().Format(time.RFC3339)
		code := 0
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, run.Record{
				RunID:        live.runID,
				ItemID:       parent,
				LaneID:       "lane_" + live.runID,
				Backend:      "reviewer",
				Seat:         "review",
				Workflow:     item.SplitClosingWorkflow,
				State:        run.StateExited,
				ExitCode:     &code,
				StartedAt:    now,
				FinishedAt:   now,
				ArtifactRefs: []run.ArtifactRef{},
			})
		})
		_ = live.cmd.Process.Kill()
		_ = live.cmd.Wait()

		// Later pass on Loop 2 settles the finished check without starting a second check.
		if worked, err := l2.advanceSplitClosingChecks(ctx2, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("loop 2 settle pass: worked=%v err=%v", worked, err)
		}
		if len(steps2.runs) != 0 {
			t.Fatalf("loop 2 started a second closing check during settlement: %+v", steps2.runs)
		}
		if l2.hasSplitClosingWork() {
			t.Fatal("loop 2 still reports pending split closing work after settlement")
		}

		var parentState string
		s, err := store.OpenRead(ctx2, f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ReadTx(ctx2, func(r *store.Reader) error {
			return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, parent).Scan(&parentState)
		}); err != nil {
			s.Close()
			t.Fatal(err)
		}
		s.Close()
		if parentState != StateLanded {
			t.Fatalf("parent state after restart settlement = %q, want %q", parentState, StateLanded)
		}
		if _, err := os.Stat(filepath.Join(RunDir(f.root, live.runID), "closing-check.json")); err != nil {
			t.Fatalf("closing-check.json missing after restart settlement: %v", err)
		}
	})

	t.Run("finished during restart gap is settled without starting a second check", func(t *testing.T) {
		f, l1, steps1, parent, parts := splitClosingFixture(t)
		ctx1, cancel1 := context.WithCancel(context.Background())
		defer cancel1()

		landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
		landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)
		trunkBefore := runGit(t, f.root, "rev-parse", TrunkRef)
		treeBefore := runGit(t, f.root, "rev-parse", trunkBefore+"^{tree}")

		started := make(chan struct{ runID, evalOut string }, 1)
		steps1.onClosingCtx = func(runCtx context.Context, req run.Request, _ string, evalOut string) (run.Record, string, error) {
			started <- struct{ runID, evalOut string }{runID: req.RunID, evalOut: evalOut}
			<-runCtx.Done()
			return run.Record{}, "", runCtx.Err()
		}

		if worked, err := l1.advanceSplitClosingChecks(ctx1, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("loop 1 start: worked=%v err=%v", worked, err)
		}
		info := <-started
		cancel1()

		// Run finishes while no loop is running.
		writeClosingEvalOutput(t, f, parent, info.evalOut, trunkBefore, treeBefore, "pass", nil)
		now := l1.cfg.Now().UTC().Format(time.RFC3339)
		code := 0
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return applyTerminal(tx, run.Record{
				RunID:        info.runID,
				ItemID:       parent,
				LaneID:       "lane_" + info.runID,
				Backend:      "reviewer",
				Seat:         "review",
				Workflow:     item.SplitClosingWorkflow,
				State:        run.StateExited,
				ExitCode:     &code,
				StartedAt:    now,
				FinishedAt:   now,
				ArtifactRefs: []run.ArtifactRef{},
			})
		})

		l2 := f.loop(t, &recordingSteps{}, &itemContentStager{})
		l2.cfg.Policy = l1.cfg.Policy
		steps2 := newSplitClosingSteps(t, f, l2)
		l2.cfg.Steps = steps2

		if worked, err := l2.advanceSplitClosingChecks(context.Background(), "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("loop 2 settle: worked=%v err=%v", worked, err)
		}
		if len(steps2.runs) != 0 {
			t.Fatalf("loop 2 started a second check: %+v", steps2.runs)
		}
		var parentState string
		s, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, parent).Scan(&parentState)
		}); err != nil {
			s.Close()
			t.Fatal(err)
		}
		s.Close()
		if parentState != StateLanded {
			t.Fatalf("parent state = %q, want %q", parentState, StateLanded)
		}
	})

	t.Run("failed closing check holds without retry", func(t *testing.T) {
		f, l, steps, parent, parts := splitClosingFixture(t)
		ctx := context.Background()

		landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
		landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)

		steps.onClosingCtx = func(_ context.Context, req run.Request, _, _ string) (run.Record, string, error) {
			now := l.cfg.Now().UTC().Format(time.RFC3339)
			code := 1
			return run.Record{
				RunID:        req.RunID,
				ItemID:       req.ItemID,
				LaneID:       req.LaneID,
				Backend:      req.Backend,
				Seat:         req.Seat,
				Workflow:     req.Workflow,
				State:        run.StateExited,
				ExitCode:     &code,
				StartedAt:    now,
				FinishedAt:   now,
				ArtifactRefs: []run.ArtifactRef{},
			}, "", nil
		}

		if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("first check start+settle: worked=%v err=%v", worked, err)
		}
		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || worked {
			t.Fatalf("failed closing check retried: worked=%v err=%v", worked, err)
		}
		if len(steps.runs) != 1 {
			t.Fatalf("expected 1 closing check run (no retry), got %d", len(steps.runs))
		}
		why := parentWhyRow(t, ctx, l, f.storePath, parent)
		if why.State != StateBlocked || why.Class != "needs you" || !strings.HasPrefix(why.Mover, "operator;") || !strings.Contains(why.Why, "exited with code 1") {
			t.Fatalf("unexpected parent why after failed closing check: %+v", why)
		}
	})
}

func TestSplitClosingBarePathLocatorAcceptedAndBriefStatesForm(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()

	landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
	landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)

	var reviewBrief string
	steps.onReview = func(_ run.Request, brief string, evalOut string) error {
		reviewBrief = brief
		head := runGit(t, f.root, "rev-parse", TrunkRef)
		tree := runGit(t, f.root, "rev-parse", head+"^{tree}")
		// Write raw verdict JSON with bare repo-relative paths (no file: prefix).
		type rawFinding struct {
			Kind  string   `json:"kind"`
			Case  string   `json:"case"`
			Paths []string `json:"paths"`
		}
		type rawEntry struct {
			CriterionID   string       `json:"criterion_id"`
			CriterionHash string       `json:"criterion_hash"`
			Verdict       string       `json:"verdict"`
			ClaimClass    string       `json:"claim_class"`
			Reason        string       `json:"reason"`
			Evidence      []string     `json:"evidence"`
			Findings      []rawFinding `json:"findings,omitempty"`
		}
		var entries []rawEntry
		if err := l.readStore(ctx, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, parent)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, text, class string
				if err := rows.Scan(&id, &text, &class); err != nil {
					return err
				}
				e := rawEntry{
					CriterionID:   id,
					CriterionHash: item.CriterionHash(text),
					Verdict:       item.VerdictDone,
					ClaimClass:    class,
					Reason:        "verified on trunk",
					Evidence:      []string{"cmd:closing-check"},
				}
				if id == "c6" {
					e.Verdict = item.VerdictNotDone
					e.Reason = "missing seam between parts"
					e.Findings = []rawFinding{{
						Kind:  "gap",
						Case:  "seam missing in pass.go",
						Paths: []string{"tools/tillr-drain/drain/pass.go", "tools/tillr-drain/drain/why.go:42"},
					}}
				}
				entries = append(entries, e)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return splitclosing.WriteAtomicJSON(evalOut, map[string]any{
			"evaluated_head": head,
			"evaluated_tree": tree,
			"outcome":        "fail",
			"verdicts":       entries,
		})
	}

	if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
		t.Fatalf("advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}

	if !strings.Contains(reviewBrief, "file:<repo-relative-path>") || !strings.Contains(reviewBrief, `"file:tools/tillr-drain/drain/pass.go"`) {
		t.Fatalf("closing-check brief does not state locator form with example:\n%s", reviewBrief)
	}

	var st splitclosing.State
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var err error
		st, err = splitclosing.ReadState(r, parent)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if st.Held != nil {
		t.Fatalf("expected parent to be judged, not held: %+v", st.Held)
	}
	if st.Finding == nil || len(st.Finding.Findings) != 1 || len(st.Finding.Findings[0].Findings) != 1 {
		t.Fatalf("expected recorded closing-check finding, got %+v", st.Finding)
	}
	gotPaths := st.Finding.Findings[0].Findings[0].Paths
	if len(gotPaths) != 2 || gotPaths[0] != "file:tools/tillr-drain/drain/pass.go" || gotPaths[1] != "file:tools/tillr-drain/drain/why.go:42" {
		t.Fatalf("normalized finding paths = %v", gotPaths)
	}

	why := parentWhyRow(t, ctx, l, f.storePath, parent)
	if why.Class != "waits on work" || !strings.HasPrefix(why.Mover, "design") || !strings.Contains(why.Why, "missing seam between parts") {
		t.Fatalf("unexpected parent why after bare-path finding: %+v", why)
	}
}

func TestSplitClosingHeldShowsRealReasonAndActionableMoverInWhy(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()

	landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
	landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)

	steps.onReview = func(_ run.Request, _ string, evalOut string) error {
		// Write invalid JSON so the closing check is genuinely held.
		return os.WriteFile(evalOut, []byte("{not-json"), 0o644)
	}

	if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
		t.Fatalf("advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}

	var st splitclosing.State
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var err error
		st, err = splitclosing.ReadState(r, parent)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if st.Held == nil || st.Held.Mover != "operator" || !strings.Contains(st.Held.Reason, "invalid verdict") {
		t.Fatalf("unexpected held state: %+v", st.Held)
	}

	why := parentWhyRow(t, ctx, l, f.storePath, parent)
	if why.Class != "needs you" {
		t.Fatalf("held closing check class = %q, want %q (row=%+v)", why.Class, "needs you", why)
	}
	if !strings.HasPrefix(why.Mover, "operator;") || !strings.Contains(why.Mover, "invalid verdict") {
		t.Fatalf("held closing check mover = %q, want operator with real reason", why.Mover)
	}
	if !strings.Contains(why.Why, "invalid verdict") || strings.Contains(why.Why, "split parent; check its criteria") {
		t.Fatalf("held closing check why = %q, want real reason", why.Why)
	}

	// Held item must not dispatch another closing check run.
	if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || worked {
		t.Fatalf("held parent re-dispatched: worked=%v err=%v", worked, err)
	}
}

func TestSplitClosingAutoRerunsPreFixLocatorHoldWithoutOperatorCommand(t *testing.T) {
	f, l, steps, parent, parts := splitClosingFixture(t)
	ctx := context.Background()

	landPartOnTrunk(t, f, parts["first"], "run-first", "first.txt", "A works\n", 101)
	landPartOnTrunk(t, f, parts["second"], "run-second", "second.txt", "B works\n", 102)

	// Record the exact pre-fix hold state: drain.split-closing-started + drain.split-closing-held
	// with mover "review" and the bare-path locator rejection reason.
	var stmtHash string
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT statement FROM item_splits WHERE parent_id=?`, parent).Scan(&raw); err != nil {
			return err
		}
		stmtHash = item.CriterionHash(string(raw))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldRunID := "closing-" + parent + "-1"
	ws, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendEffectOn(tx, splitClosingStartedKind, "item", parent, splitclosing.StartedPayload{
			ItemID:        parent,
			RunID:         oldRunID,
			Kind:          splitclosing.RunKindCheck,
			StatementHash: stmtHash,
			Seat:          "review",
			Workflow:      item.SplitClosingWorkflow,
			NodeID:        item.SplitClosingNode,
		}, l.cfg.Now().Add(-time.Hour)); err != nil {
			return err
		}
		return appendEffectOn(tx, splitClosingHeldKind, "item", parent, splitclosing.HeldPayload{
			ItemID:        parent,
			RunID:         oldRunID,
			StatementHash: stmtHash,
			Reason:        `closing check ` + oldRunID + ` invalid verdict: reading verdict file: finding 1: "tools/tillr-drain/drain/pass.go" is not a file:<repo-relative-path>[:line] locator`,
			Mover:         "review",
		}, l.cfg.Now().Add(-30*time.Minute))
	}); err != nil {
		ws.Close()
		t.Fatal(err)
	}
	ws.Close()

	// Without any operator command, the loop reruns the closing check and lands the parent.
	steps.onReview = func(_ run.Request, _ string, evalOut string) error {
		head := runGit(t, f.root, "rev-parse", TrunkRef)
		tree := runGit(t, f.root, "rev-parse", head+"^{tree}")
		writeClosingEvalOutput(t, f, parent, evalOut, head, tree, "pass", nil)
		return nil
	}

	if worked, err := advanceSplitClosingAndSettle(t, ctx, l, "", &LoopResult{}); err != nil || !worked {
		t.Fatalf("auto-rerun advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}
	if len(steps.runs) != 1 || steps.runs[0].Seat != "review" {
		t.Fatalf("expected 1 fresh closing check run, got %+v", steps.runs)
	}
	if got := itemStateInStore(t, f, parent); got != StateLanded {
		t.Fatalf("parent state after auto-rerun = %q, want %q", got, StateLanded)
	}
}
