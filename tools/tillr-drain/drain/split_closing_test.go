package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	t        *testing.T
	f        refusalLoopFixture
	l        *Loop
	mu       sync.Mutex
	runs     []run.Request
	briefs   map[string]string
	onReview func(req run.Request, brief, evalOut string) error
	onDesign func(req run.Request, brief, extOut string) error
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

func (s *splitClosingSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
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

	worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{})
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
	if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
		t.Fatalf("step 1 advanceSplitClosingChecks: worked=%v err=%v", worked, err)
	}
	whyFinding := parentWhyRow(t, ctx, l, f.storePath, parent)
	if whyFinding.State != StateBlocked || whyFinding.Class != "waits on work" || !strings.HasPrefix(whyFinding.Mover, "design") || !strings.Contains(whyFinding.Why, "missing seam between A and B") {
		t.Fatalf("parent why after finding: %+v", whyFinding)
	}

	// Step 2: Design seat extends the split.
	if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
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
	if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
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
		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
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
		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
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
		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("second design pass: worked=%v err=%v", worked, err)
		}
		if len(designBriefs) != 2 || !strings.Contains(designBriefs[1], "## Prior Extension Refusal") || !strings.Contains(designBriefs[1], "unknown field") {
			t.Fatalf("second design brief missing prior refusal:\n%v", designBriefs)
		}

		whyHeld := parentWhyRow(t, ctx, l, f.storePath, parent)
		if whyHeld.State != StateBlocked || whyHeld.Class != "waits on work" || !strings.HasPrefix(whyHeld.Mover, "design") {
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

		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("finding pass: worked=%v err=%v", worked, err)
		}
		if worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{}); err != nil || !worked {
			t.Fatalf("missing output pass: worked=%v err=%v", worked, err)
		}
		if designCalls != 1 {
			t.Fatalf("expected 1 design call, got %d", designCalls)
		}

		whyHeld := parentWhyRow(t, ctx, l, f.storePath, parent)
		if whyHeld.State != StateBlocked || whyHeld.Class != "waits on work" || !strings.HasPrefix(whyHeld.Mover, "design") || !strings.Contains(whyHeld.Why, "missing split-extension.json") {
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

	worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{})
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

	worked, err := l.advanceSplitClosingChecks(ctx, "", &LoopResult{})
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
