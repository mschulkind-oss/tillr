package item_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func boolPtr(b bool) *bool { return &b }

func setupSplitFixture(t *testing.T) (*store.Store, string, map[string]string, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(scratch(t), "item.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	at := time.Date(2026, 11, 27, 6, 0, 0, 0, time.UTC)

	var parentID string
	var partIDs map[string]string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		req := item.RequestFiling{
			Words:       []byte("Handle both alpha and beta cases."),
			Title:       "Handle alpha and beta",
			Destination: item.Destination{To: "last"},
			At:          at,
			By:          "owner",
			Via:         "cli",
		}
		env, err := item.FileRequest(ctx, tx, req)
		if err != nil {
			return err
		}
		var body struct {
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		parentID = body.ItemID

		stmt := item.SplitStatement{
			Parts: []item.SplitPart{
				{
					Label:    "alpha",
					Title:    "Handle alpha",
					Share:    "Handle the alpha case.",
					Leaves:   map[string]string{"beta": "Handle the beta case."},
					Required: boolPtr(true),
				},
				{
					Label:    "beta",
					Title:    "Handle beta",
					Share:    "Handle the beta case.",
					Leaves:   map[string]string{"alpha": "Handle the alpha case."},
					Required: boolPtr(true),
				},
			},
			Coverage: map[string][]item.SplitCoverage{},
		}
		partIDs, err = item.FileSplit(ctx, tx, parentID, stmt, at.Add(time.Minute))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS workflow_selections(item_id TEXT PRIMARY KEY, workflow TEXT, source TEXT, graph TEXT)`); err != nil {
			return err
		}
		graph := `{"stages":[{"id":"verify","kind":"agent","access":"read_only","judge_classes":["verification-only"]}],"exit":{"evaluator_slot":"verify","requires_nodes":["verify"]}}`
		for _, id := range []string{parentID, partIDs["alpha"], partIDs["beta"]} {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO workflow_selections VALUES(?,?,?,?)`, id, "review@v1", "block", graph); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, parentID, partIDs, at
}

func verdictsForItem(t *testing.T, r *store.Reader, itemID string) []item.JudgeEntry {
	t.Helper()
	rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, itemID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries []item.JudgeEntry
	for rows.Next() {
		var id, text, class string
		if err := rows.Scan(&id, &text, &class); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, item.JudgeEntry{
			CriterionID:   id,
			CriterionHash: item.CriterionHash(text),
			Verdict:       item.VerdictDone,
			ClaimClass:    class,
			Reason:        "checked",
			Evidence:      []string{"diff:candidate.diff"},
		})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestSplitPartContextAndGaps(t *testing.T) {
	ctx := context.Background()
	s, parentID, partIDs, at := setupSplitFixture(t)
	alphaID := partIDs["alpha"]
	betaID := partIDs["beta"]
	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)

	err := s.ReadTx(ctx, func(r *store.Reader) error {
		nonPart, err := item.SplitPartContext(r, parentID)
		if err != nil {
			return err
		}
		if nonPart != nil {
			t.Fatalf("SplitPartContext(%s) = %+v, want nil", parentID, nonPart)
		}
		info, err := item.SplitPartContext(r, alphaID)
		if err != nil {
			return err
		}
		if info == nil {
			t.Fatalf("SplitPartContext(%s) = nil, want info", alphaID)
		}
		if info.ParentID != parentID || info.Part.Label != "alpha" {
			t.Fatalf("unexpected info: %+v", info)
		}
		sib, ok := info.Siblings["beta"]
		if !ok || sib.ItemID != betaID || !sib.Required || sib.State != "in-queue" {
			t.Fatalf("unexpected beta sibling: %+v (ok=%v)", sib, ok)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// CheckJudge rejections on sibling_gaps.
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		parentBinding, err := item.ReadEvaluatorBinding(r, parentID)
		if err != nil {
			return err
		}
		parentVerdicts := verdictsForItem(t, r, parentID)
		nonPartRaw, _ := json.Marshal(item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts:      parentVerdicts,
			SiblingGaps: item.SiblingGaps{{
				Sibling:  "beta",
				Case:     "Beta is not handled.",
				Paths:    []string{"file:pkg/beta.go"},
				Evidence: []string{"diff:candidate.diff"},
			}},
		})
		if _, err := item.CheckJudge(ctx, r, nonPartRaw, item.JudgeOptions{
			EvaluatorBinding: parentBinding,
			ItemID:           parentID,
			Head:             head,
			Tree:             tree,
			Actor:            "judge-1",
			JudgedActor:      "worker-1",
			At:               at,
		}); !hasRejection(err, "sibling-gaps-not-part") {
			t.Fatalf("non-part sibling_gaps err = %v, want sibling-gaps-not-part", err)
		}

		alphaBinding, err := item.ReadEvaluatorBinding(r, alphaID)
		if err != nil {
			return err
		}
		alphaVerdicts := verdictsForItem(t, r, alphaID)
		alphaOpts := item.JudgeOptions{
			EvaluatorBinding: alphaBinding,
			ItemID:           alphaID,
			Head:             head,
			Tree:             tree,
			Actor:            "judge-1",
			JudgedActor:      "worker-1",
			At:               at,
		}

		// Self target.
		selfRaw, _ := json.Marshal(item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts:      alphaVerdicts,
			SiblingGaps: item.SiblingGaps{{
				Sibling:  "alpha",
				Case:     "Self target.",
				Paths:    []string{"file:pkg/alpha.go"},
				Evidence: []string{"diff:candidate.diff"},
			}},
		})
		if _, err := item.CheckJudge(ctx, r, selfRaw, alphaOpts); !hasRejection(err, "sibling-gap-self") {
			t.Fatalf("self sibling_gaps err = %v, want sibling-gap-self", err)
		}

		// Unknown sibling.
		unknownRaw, _ := json.Marshal(item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts:      alphaVerdicts,
			SiblingGaps: item.SiblingGaps{{
				Sibling:  "gamma",
				Case:     "Unknown sibling.",
				Paths:    []string{"file:pkg/gamma.go"},
				Evidence: []string{"diff:candidate.diff"},
			}},
		})
		if _, err := item.CheckJudge(ctx, r, unknownRaw, alphaOpts); !hasRejection(err, "sibling-gap-unknown") {
			t.Fatalf("unknown sibling_gaps err = %v, want sibling-gap-unknown", err)
		}

		// Empty array is allowed and treated as nil.
		emptyArrayRaw, _ := json.Marshal(map[string]any{
			"evaluated_head": head,
			"evaluated_tree": tree,
			"outcome":        "pass",
			"verdicts":       alphaVerdicts,
			"sibling_gaps":   []any{},
		})
		res, err := item.CheckJudge(ctx, r, emptyArrayRaw, alphaOpts)
		if err != nil || len(res.Output.SiblingGaps) != 0 {
			t.Fatalf("empty sibling_gaps = (%+v, %v), want nil with no error", res.Output.SiblingGaps, err)
		}

		// Malformed case / paths / evidence.
		for _, bad := range []map[string]any{
			{"sibling": "beta", "case": "   ", "paths": []string{"file:pkg/beta.go"}, "evidence": []string{"diff:candidate.diff"}},
			{"sibling": "beta", "case": "Missing path.", "paths": []string{"pkg/beta.go"}, "evidence": []string{"diff:candidate.diff"}},
			{"sibling": "beta", "case": "Bad evidence.", "paths": []string{"file:pkg/beta.go"}, "evidence": []string{"has space"}},
		} {
			badRaw, _ := json.Marshal(map[string]any{
				"evaluated_head": head,
				"evaluated_tree": tree,
				"outcome":        "pass",
				"verdicts":       alphaVerdicts,
				"sibling_gaps":   []any{bad},
			})
			if _, err := item.CheckJudge(ctx, r, badRaw, alphaOpts); !hasRejection(err, "judge-output") {
				t.Fatalf("malformed sibling_gaps (%v) err = %v, want judge-output", bad, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// RecordJudge atomically records sibling gaps against parent and sibling, and replay is idempotent.
	var validRaw []byte
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		binding, err := item.ReadEvaluatorBinding(tx.Reader(), alphaID)
		if err != nil {
			return err
		}
		verdicts := verdictsForItem(t, tx.Reader(), alphaID)
		validRaw, _ = json.Marshal(item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts:      verdicts,
			SiblingGaps: item.SiblingGaps{{
				Sibling:  "beta",
				Case:     "Beta input is not handled yet.",
				Paths:    []string{"file:pkg/beta.go:12"},
				Evidence: []string{"diff:candidate.diff"},
			}},
		})
		opts := item.JudgeOptions{
			EvaluatorBinding: binding,
			ItemID:           alphaID,
			Head:             head,
			Tree:             tree,
			Actor:            "judge-1",
			JudgedActor:      "worker-1",
			At:               at.Add(2 * time.Minute),
		}
		if _, err := item.RecordJudge(ctx, tx, validRaw, opts); err != nil {
			return err
		}
		// Idempotent replay of RecordSplitGaps.
		return item.RecordSplitGaps(ctx, tx, alphaID, []item.SiblingGap{{
			Sibling:  "beta",
			Case:     "Beta input is not handled yet.",
			Paths:    []string{"file:pkg/beta.go:12"},
			Evidence: []string{"diff:candidate.diff"},
		}}, head, "judge-1", at.Add(2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.ReadTx(ctx, func(r *store.Reader) error {
		parentGaps, err := item.SplitGapsForParent(r, parentID)
		if err != nil {
			return err
		}
		siblingGaps, err := item.SplitGapsForSibling(r, betaID)
		if err != nil {
			return err
		}
		if len(parentGaps) != 1 || !reflect.DeepEqual(parentGaps, siblingGaps) {
			t.Fatalf("parentGaps=%+v, siblingGaps=%+v", parentGaps, siblingGaps)
		}
		g := parentGaps[0]
		if g.FromPartID != alphaID || g.FromPartLabel != "alpha" || g.SiblingID != betaID || g.SiblingLabel != "beta" || g.Case != "Beta input is not handled yet." {
			t.Fatalf("unexpected recorded gap: %+v", g)
		}
		var eventCount int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_type='item' AND object_id=?`, item.SplitGapKind, parentID).Scan(&eventCount); err != nil {
			return err
		}
		if eventCount != 1 {
			t.Fatalf("eventCount = %d, want 1", eventCount)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func hasRejection(err error, rule string) bool {
	rej, ok := item.RejectionOf(err)
	return ok && rej.Rule == rule
}
