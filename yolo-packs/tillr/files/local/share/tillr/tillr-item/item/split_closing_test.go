package item_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func splitClosingFixture(t *testing.T, criteria ...string) (*store.Store, string, map[string]string, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(scratch(t), "item.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	at := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	if len(criteria) == 0 {
		criteria = []string{"A works; B works.", "Together they work."}
	}
	var parent string
	var ids map[string]string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Title:       "Parent request",
			Words:       []byte("Owner's request words\n"),
			Criteria:    criteria,
			Ordinary:    true,
			Destination: item.Destination{To: "last"},
			At:          at,
		})
		if err != nil {
			return err
		}
		parent = env.ID
		stmt := item.SplitStatement{
			Parts: []item.SplitPart{
				{
					Label:    "first",
					Title:    "First part",
					Share:    "Deliver A",
					Leaves:   map[string]string{"second": "Deliver B"},
					Criteria: []string{"A works"},
					Required: boolPtr(true),
				},
				{
					Label:    "second",
					Title:    "Second part",
					Share:    "Deliver B",
					Leaves:   map[string]string{"first": "Deliver A"},
					Criteria: []string{"B works"},
					Follows:  []string{"first"},
					Required: boolPtr(true),
				},
				{
					Label:    "extra",
					Title:    "Optional part",
					Share:    "Add docs",
					Required: boolPtr(false),
				},
			},
			Coverage: map[string][]item.SplitCoverage{
				"c5": {
					{Clause: "A works;", Part: "first"},
					{Clause: "B works.", Part: "second"},
				},
				"c6": {{ClosingCheck: true}},
			},
		}
		ids, err = item.FileSplit(ctx, tx, parent, stmt, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, parent, ids, at
}

func TestReadSplitAndSplitPartLandings(t *testing.T) {
	s, parent, ids, at := splitClosingFixture(t)
	ctx := context.Background()

	// Missing parent returns sql.ErrNoRows.
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		_, _, err := item.ReadSplit(r, "nonexistent")
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("ReadSplit missing: got %v, want sql.ErrNoRows", err)
		}
		stmt, gotIDs, err := item.ReadSplit(r, parent)
		if err != nil {
			t.Fatal(err)
		}
		if len(stmt.Parts) != 3 || !reflect.DeepEqual(gotIDs, ids) {
			t.Fatalf("ReadSplit: %+v, %v (want %v)", stmt, gotIDs, ids)
		}
		// Without landings table, ReadSplitPartLandings still succeeds with empty commits.
		landings, err := item.ReadSplitPartLandings(r, parent)
		if err != nil {
			t.Fatal(err)
		}
		if len(landings) != 3 {
			t.Fatalf("landings len: %d", len(landings))
		}
		for _, l := range landings {
			if l.Commit != "" || l.State != "in-queue" {
				t.Fatalf("unexpected landing without landings table: %+v", l)
			}
			if l.Label == "extra" && l.Required {
				t.Fatalf("extra should not be required: %+v", l)
			}
			if l.Label != "extra" && !l.Required {
				t.Fatalf("part %s should be required: %+v", l.Label, l)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add landings table and record a landed commit for first, a failed landing for second,
	// and a reverted landing for extra.
	commitA := strings.Repeat("1", 40)
	commitReverted := strings.Repeat("2", 40)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE landings (
			landing_id TEXT PRIMARY KEY,
			item_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			result TEXT NOT NULL,
			trunk_after TEXT NOT NULL,
			event_seq INTEGER NOT NULL
		)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings VALUES ('land-1', ?, 'run-1', 'landed', ?, 1)`, ids["first"], commitA); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings VALUES ('land-2', ?, 'run-2', 'failed', '', 2)`, ids["second"]); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings VALUES ('land-3', ?, 'run-3', 'landed', ?, 3)`, ids["extra"], commitReverted); err != nil {
			return err
		}
		_ = event.Register("drain.revert", "tillr-drain")
		if _, err := tx.Append(event.Event{
			Kind:       "drain.revert",
			ObjectType: "landing",
			ObjectID:   &commitReverted,
			At:         at,
			Payload:    json.RawMessage(`{"result":"reverted","landing":"` + commitReverted + `","item_id":"` + ids["extra"] + `","run_id":"run-3"}`),
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = s.ReadTx(ctx, func(r *store.Reader) error {
		landings, err := item.ReadSplitPartLandings(r, parent)
		if err != nil {
			t.Fatal(err)
		}
		byLabel := map[string]item.SplitPartLanding{}
		for _, l := range landings {
			byLabel[l.Label] = l
		}
		if byLabel["first"].Commit != commitA {
			t.Fatalf("first commit: got %q, want %q", byLabel["first"].Commit, commitA)
		}
		if byLabel["second"].Commit != "" {
			t.Fatalf("second commit: got %q, want empty", byLabel["second"].Commit)
		}
		if byLabel["extra"].Commit != "" {
			t.Fatalf("reverted extra commit: got %q, want empty", byLabel["extra"].Commit)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSplitClosingEvaluatorBindingAndRecordJudge(t *testing.T) {
	s, parent, _, at := splitClosingFixture(t)
	ctx := context.Background()

	// Non-split item is rejected.
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		_, err := item.SplitClosingEvaluatorBinding(r, "nonexistent")
		if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "evaluator-selection" {
			t.Fatalf("expected evaluator-selection rejection, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	head, tree := strings.Repeat("a", 40), strings.Repeat("b", 40)
	// Read parent's criteria from store to build valid JudgeOutput.
	var verdicts []item.JudgeEntry
	var binding item.EvaluatorBinding
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		binding, err = item.SplitClosingEvaluatorBinding(r, parent)
		if err != nil {
			return err
		}
		if binding.Workflow != item.SplitClosingWorkflow || binding.EvaluatorNode != item.SplitClosingNode || binding.GraphSHA256 == "" {
			t.Fatalf("unexpected binding: %+v", binding)
		}
		rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, parent)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cid, text, class string
			if err := rows.Scan(&cid, &text, &class); err != nil {
				return err
			}
			verdicts = append(verdicts, item.JudgeEntry{
				CriterionID:   cid,
				CriterionHash: item.CriterionHash(text),
				Verdict:       item.VerdictDone,
				ClaimClass:    class,
				Reason:        "verified against trunk",
				Evidence:      []string{"commit:" + head},
			})
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}

	output, _ := json.Marshal(item.JudgeOutput{
		EvaluatedHead: head,
		EvaluatedTree: tree,
		Outcome:       "pass",
		Verdicts:      verdicts,
	})

	// Wrong GraphSHA256 is rejected.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		bad := binding
		bad.GraphSHA256 = strings.Repeat("0", 64)
		_, err := item.RecordJudge(ctx, tx, output, item.JudgeOptions{
			EvaluatorBinding: bad,
			ItemID:           parent,
			Head:             head,
			Tree:             tree,
			Actor:            "closing-judge",
			JudgedActor:      "split-parts",
			At:               at,
		})
		if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "evaluator-binding" {
			t.Fatalf("expected evaluator-binding rejection, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Valid RecordJudge succeeds without any workflow_selections table/row.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		res, err := item.RecordJudge(ctx, tx, output, item.JudgeOptions{
			EvaluatorBinding: binding,
			ItemID:           parent,
			Head:             head,
			Tree:             tree,
			Actor:            "closing-judge",
			JudgedActor:      "split-parts",
			At:               at,
		})
		if err != nil {
			t.Fatalf("RecordJudge failed: %v", err)
		}
		if res.Outcome != "pass" || len(res.Records) != len(verdicts) {
			t.Fatalf("unexpected judge result: %+v", res)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExtendSplit(t *testing.T) {
	s, parent, origIDs, at := splitClosingFixture(t)
	ctx := context.Background()

	var beforeBinding item.EvaluatorBinding
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		beforeBinding, err = item.SplitClosingEvaluatorBinding(r, parent)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	ext := item.SplitExtension{
		Parts: []item.SplitPart{
			{
				Label:    "third",
				Title:    "Third part",
				Share:    "Fix seam between A and B",
				Leaves:   map[string]string{"first": "Deliver A"},
				Criteria: []string{"Seam works"},
				Follows:  []string{"second"},
				Required: boolPtr(true),
			},
		},
	}

	var extIDs map[string]string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		extIDs, err = item.ExtendSplit(ctx, tx, parent, ext, at.Add(time.Minute))
		return err
	})
	if err != nil {
		t.Fatalf("ExtendSplit failed: %v", err)
	}
	if len(extIDs) != 1 || extIDs["third"] == "" {
		t.Fatalf("unexpected extIDs: %v", extIDs)
	}

	// Verify existing parts preserved, new part filed with BlockedBy=[second] only,
	// parent deps updated, and binding hash changed.
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		stmt, allIDs, err := item.ReadSplit(r, parent)
		if err != nil {
			return err
		}
		if len(stmt.Parts) != 4 || allIDs["first"] != origIDs["first"] || allIDs["third"] != extIDs["third"] {
			t.Fatalf("grown statement mismatch: %+v, %v", stmt, allIDs)
		}
		afterBinding, err := item.SplitClosingEvaluatorBinding(r, parent)
		if err != nil {
			return err
		}
		if afterBinding.GraphSHA256 == beforeBinding.GraphSHA256 {
			t.Fatalf("expected GraphSHA256 to change after ExtendSplit")
		}
		// Check third part's blocked_by only has second (not parent's blocked_by).
		var rawDeps string
		if err := r.QueryRow(`SELECT group_concat(blocked_by) FROM item_edges WHERE item_id=? AND kind='blocked_by'`, extIDs["third"]).Scan(&rawDeps); err != nil {
			return err
		}
		if rawDeps != origIDs["second"] {
			t.Fatalf("third part blocked_by: got %q, want %q", rawDeps, origIDs["second"])
		}
		// Check parent's blocked_by includes first, second, and third.
		rows, err := r.Query(`SELECT blocked_by FROM item_edges WHERE item_id=? AND kind='blocked_by'`, parent)
		if err != nil {
			return err
		}
		defer rows.Close()
		var parentDeps []string
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				return err
			}
			parentDeps = append(parentDeps, dep)
		}
		for _, want := range []string{origIDs["first"], origIDs["second"], extIDs["third"]} {
			if !slices.Contains(parentDeps, want) {
				t.Fatalf("parent deps %v missing %s", parentDeps, want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Idempotent replay makes no writes and returns same extIDs.
	var beforeEvents int
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM events`).Scan(&beforeEvents)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		replay, err := item.ExtendSplit(ctx, tx, parent, ext, at.Add(2*time.Minute))
		if err != nil {
			t.Fatalf("replay failed: %v", err)
		}
		if !reflect.DeepEqual(replay, extIDs) {
			t.Fatalf("replay IDs %v != %v", replay, extIDs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var afterEvents int
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM events`).Scan(&afterEvents)
	})
	if err != nil {
		t.Fatal(err)
	}
	if afterEvents != beforeEvents {
		t.Fatalf("replay wrote events: %d -> %d", beforeEvents, afterEvents)
	}

	// Refusal cases (each in a transaction that rolls back on error).
	refusalCases := []struct {
		name     string
		target   string
		ext      item.SplitExtension
		wantRule string
	}{
		{
			name:     "non-split parent",
			target:   origIDs["first"],
			ext:      item.SplitExtension{Parts: []item.SplitPart{{Label: "p", Share: "x", Required: boolPtr(true)}}},
			wantRule: "split-not-found",
		},
		{
			name:     "empty parts",
			target:   parent,
			ext:      item.SplitExtension{},
			wantRule: "split-parts",
		},
		{
			name:     "no required part",
			target:   parent,
			ext:      item.SplitExtension{Parts: []item.SplitPart{{Label: "opt", Share: "x", Required: boolPtr(false)}}},
			wantRule: "split-parts",
		},
		{
			name:     "duplicate label differing from existing",
			target:   parent,
			ext:      item.SplitExtension{Parts: []item.SplitPart{{Label: "first", Share: "Different", Required: boolPtr(true)}}},
			wantRule: "split-differs",
		},
		{
			name:     "duplicate label within extension",
			target:   parent,
			ext:      item.SplitExtension{Parts: []item.SplitPart{{Label: "fourth", Share: "x", Required: boolPtr(true)}, {Label: "fourth", Share: "y", Required: boolPtr(true)}}},
			wantRule: "split-label",
		},
		{
			name:     "malformed label",
			target:   parent,
			ext:      item.SplitExtension{Parts: []item.SplitPart{{Label: "bad label!", Share: "x", Required: boolPtr(true)}}},
			wantRule: "split-label",
		},
		{
			name:     "malformed missing share",
			target:   parent,
			ext:      item.SplitExtension{Parts: []item.SplitPart{{Label: "fourth", Share: " ", Required: boolPtr(true)}}},
			wantRule: "split-part",
		},
		{
			name:     "malformed nil required",
			target:   parent,
			ext:      item.SplitExtension{Parts: []item.SplitPart{{Label: "fourth", Share: "x", Required: nil}}},
			wantRule: "split-part",
		},
		{
			name:   "cycle across new parts",
			target: parent,
			ext: item.SplitExtension{Parts: []item.SplitPart{
				{Label: "c1", Share: "x", Follows: []string{"c2"}, Required: boolPtr(true)},
				{Label: "c2", Share: "y", Follows: []string{"c1"}, Required: boolPtr(true)},
			}},
			wantRule: "split-cycle",
		},
	}
	for _, tc := range refusalCases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := item.ExtendSplit(ctx, tx, tc.target, tc.ext, at.Add(3*time.Minute))
				return err
			})
			if rej, ok := item.RejectionOf(err); !ok || rej.Rule != tc.wantRule {
				t.Fatalf("got %v, want rule %q", err, tc.wantRule)
			}
		})
	}
}

func TestExtendSplitReplacesDefectivePart(t *testing.T) {
	s, parent, origIDs, at := splitClosingFixture(t)
	ctx := context.Background()

	// Record a defective verdict on "first" and close it as defective:.
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var cID, cText, cClass string
		if err := tx.QueryRow(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id LIMIT 1`, origIDs["first"]).Scan(&cID, &cText, &cClass); err != nil {
			return err
		}
		vPayload, err := json.Marshal(map[string]any{
			"item_id":        origIDs["first"],
			"criterion_id":   cID,
			"criterion_hash": item.CriterionHash(cText),
			"verdict":        "defective",
			"claim_class":    cClass,
			"reason":         "part words forbid test change the work needs",
			"evidence":       []string{"file:tools/tillr-item/item/split.go:1"},
			"actor_kind":     "agent",
			"actor":          "verifier",
		})
		if err != nil {
			return err
		}
		firstID := origIDs["first"]
		if _, err := tx.Append(event.Event{
			Kind:       item.VerdictRecordKind,
			ObjectType: "item",
			ObjectID:   &firstID,
			Payload:    vPayload,
			At:         at.Add(time.Minute),
		}); err != nil {
			return err
		}
		_, err = item.Close(ctx, tx, origIDs["first"], "defective: part words forbid test change the work needs", "verifier", at.Add(2*time.Minute))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify IsDefectiveClose and ReadDefectivePartFindings.
	if !item.IsDefectiveClose("closed", "defective: bad") || item.IsDefectiveClose("closed", "obsolete") || item.IsDefectiveClose("in-queue", "defective: bad") {
		t.Fatalf("unexpected IsDefectiveClose results")
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		findings, err := item.ReadDefectivePartFindings(r, origIDs["first"])
		if err != nil {
			return err
		}
		if findings.CloseReason != "defective: part words forbid test change the work needs" || findings.ClosedBy != "verifier" || len(findings.Verdicts) != 1 {
			t.Fatalf("unexpected findings: %+v", findings)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Rejections before successful replacement.
	badCases := []struct {
		name     string
		ext      item.SplitExtension
		wantRule string
	}{
		{
			name: "coverage still names replaced part",
			ext: item.SplitExtension{
				Replaces: []string{"first"},
				Parts:    []item.SplitPart{{Label: "first-v2", Title: "First v2", Share: "Deliver A with tests", Required: boolPtr(true)}},
			},
			wantRule: "split-coverage",
		},
		{
			name: "replaces unclosed part",
			ext: item.SplitExtension{
				Replaces: []string{"second"},
				Parts:    []item.SplitPart{{Label: "second-v2", Title: "Second v2", Share: "Deliver B", Required: boolPtr(true)}},
			},
			wantRule: "split-replaces",
		},
		{
			name: "replaces optional part",
			ext: item.SplitExtension{
				Replaces: []string{"extra"},
				Parts:    []item.SplitPart{{Label: "extra-v2", Title: "Extra v2", Share: "Add docs", Required: boolPtr(true)}},
			},
			wantRule: "split-replaces",
		},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := item.ExtendSplit(ctx, tx, parent, tc.ext, at.Add(3*time.Minute))
				return err
			})
			if rej, ok := item.RejectionOf(err); !ok || rej.Rule != tc.wantRule {
				t.Fatalf("got %v, want rule %q", err, tc.wantRule)
			}
		})
	}

	// Successful replacement of "first" with "first-v2".
	ext := item.SplitExtension{
		Replaces: []string{"first"},
		Parts: []item.SplitPart{{
			Label:    "first-v2",
			Title:    "First part v2",
			Share:    "Deliver A including test change",
			Leaves:   map[string]string{"second": "Deliver B"},
			Criteria: []string{"A works with tests"},
			Required: boolPtr(true),
		}},
		Coverage: map[string][]item.SplitCoverage{
			"c5": {
				{Clause: "A works;", Part: "first-v2"},
				{Clause: "B works.", Part: "second"},
			},
			"c6": {{ClosingCheck: true}},
		},
	}
	var extIDs map[string]string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		extIDs, err = item.ExtendSplit(ctx, tx, parent, ext, at.Add(4*time.Minute))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify SplitRequiredParts, statement history, parent deps, and sibling rewiring.
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		req, err := item.SplitRequiredParts(r, parent)
		if err != nil {
			return err
		}
		slices.Sort(req)
		wantReq := []string{extIDs["first-v2"], origIDs["second"]}
		slices.Sort(wantReq)
		if !slices.Equal(req, wantReq) {
			t.Fatalf("SplitRequiredParts = %v, want %v", req, wantReq)
		}

		stmt, _, err := item.ReadSplit(r, parent)
		if err != nil {
			return err
		}
		if len(stmt.Parts) != 4 {
			t.Fatalf("expected closed part retained in history (4 parts), got %d", len(stmt.Parts))
		}
		byLabel := map[string]item.SplitPart{}
		for _, p := range stmt.Parts {
			byLabel[p.Label] = p
		}
		if !slices.Equal(byLabel["first"].ReplacedBy, []string{"first-v2"}) {
			t.Fatalf("first.ReplacedBy = %v, want [first-v2]", byLabel["first"].ReplacedBy)
		}
		if !slices.Equal(byLabel["second"].Follows, []string{"first-v2"}) {
			t.Fatalf("second.Follows = %v, want [first-v2]", byLabel["second"].Follows)
		}

		var secondBlocked string
		if err := r.QueryRow(`SELECT group_concat(blocked_by) FROM item_edges WHERE item_id=? AND kind='blocked_by'`, origIDs["second"]).Scan(&secondBlocked); err != nil {
			return err
		}
		if secondBlocked != extIDs["first-v2"] {
			t.Fatalf("second.BlockedBy = %q, want %q", secondBlocked, extIDs["first-v2"])
		}

		parentRows, err := r.Query(`SELECT blocked_by FROM item_edges WHERE item_id=? AND kind='blocked_by' ORDER BY blocked_by`, parent)
		if err != nil {
			return err
		}
		defer parentRows.Close()
		var parentBlocked []string
		for parentRows.Next() {
			var dep string
			if err := parentRows.Scan(&dep); err != nil {
				return err
			}
			parentBlocked = append(parentBlocked, dep)
		}
		if err := parentRows.Err(); err != nil {
			return err
		}
		if !slices.Equal(parentBlocked, wantReq) {
			t.Fatalf("parent.BlockedBy = %v, want %v", parentBlocked, wantReq)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Idempotent replay succeeds.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		replay, err := item.ExtendSplit(ctx, tx, parent, ext, at.Add(5*time.Minute))
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(replay, extIDs) {
			t.Fatalf("replay IDs %v != %v", replay, extIDs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Re-replacing an already-replaced part is rejected.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.ExtendSplit(ctx, tx, parent, item.SplitExtension{
			Replaces: []string{"first"},
			Parts:    []item.SplitPart{{Label: "first-v3", Share: "x", Required: boolPtr(true)}},
		}, at.Add(6*time.Minute))
		return err
	})
	if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "split-replaces" {
		t.Fatalf("re-replacing got %v, want split-replaces", err)
	}
}

func TestVerdictLocatorAcceptsBareRepoRelativePath(t *testing.T) {
	var findings item.VerdictFindings
	raw := `[{"kind":"gap","case":"bare path locator","paths":["tools/tillr-drain/drain/pass.go","./tools/tillr-drain/drain/why.go:42","file:tools/tillr-item/item/verdict.go:10"]}]`
	if err := json.Unmarshal([]byte(raw), &findings); err != nil {
		t.Fatalf("Unmarshal bare paths: %v", err)
	}
	want := []string{
		"file:tools/tillr-drain/drain/pass.go",
		"file:tools/tillr-drain/drain/why.go:42",
		"file:tools/tillr-item/item/verdict.go:10",
	}
	if !reflect.DeepEqual(findings[0].Paths, want) {
		t.Fatalf("normalized paths = %v, want %v", findings[0].Paths, want)
	}

	var fates item.EarlierFindingFates
	if err := json.Unmarshal([]byte(`[{"finding":"prior","paths":["tools/tillr-drain/drain/pass.go"],"fate":"fixed"}]`), &fates); err != nil {
		t.Fatalf("Unmarshal earlier_finding_fates bare path: %v", err)
	}
	if !reflect.DeepEqual(fates[0].Paths, []string{"file:tools/tillr-drain/drain/pass.go"}) {
		t.Fatalf("normalized fates paths = %v", fates[0].Paths)
	}

	var examined item.VerdictExamined
	if err := json.Unmarshal([]byte(`{"whole":false,"unexamined_paths":["tools/tillr-drain/drain/pass.go"]}`), &examined); err != nil {
		t.Fatalf("Unmarshal examined bare path: %v", err)
	}
	if !reflect.DeepEqual(examined.UnexaminedPaths, []string{"file:tools/tillr-drain/drain/pass.go"}) {
		t.Fatalf("normalized unexamined_paths = %v", examined.UnexaminedPaths)
	}

	for _, bad := range []string{
		"",
		"   ",
		"see tools/tillr-drain/drain/pass.go",
		"/etc/passwd",
		"../outside.go",
		"tools/tillr-drain/drain/pass.go:0",
		"tools/tillr-drain/drain/pass.go:abc",
		"diff:candidate.diff",
		"http://example.com/a.go",
	} {
		var badFindings item.VerdictFindings
		badRaw, _ := json.Marshal([]map[string]any{{"kind": "gap", "case": "bad", "paths": []string{bad}}})
		if err := json.Unmarshal(badRaw, &badFindings); err == nil {
			t.Fatalf("expected %q to be rejected as finding path", bad)
		}
	}
}
