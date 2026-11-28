package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/boundary"
	"github.com/mschulkind-oss/tillr-drain/internal/policy"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func openDrainScopedTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAnsweredObstacleCountsForItems(t *testing.T) {
	ctx := context.Background()
	s := openDrainScopedTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	appendConflict := func(tx *store.Tx, c item.Conflict) error {
		body, err := json.Marshal(c)
		if err != nil {
			return err
		}
		id := c.ItemID
		_, err = tx.Append(event.Event{
			Kind:       item.ConflictKind,
			ObjectType: "item",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		return err
	}

	report := "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: mismatch\nRESULT: blocked"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		// Two events for the same conflict ID count once.
		if err := appendConflict(tx, item.Conflict{ItemID: "item-open", ConflictID: "c1", Status: "answered", AnswerHead: "abc", Text: report}); err != nil {
			return err
		}
		if err := appendConflict(tx, item.Conflict{ItemID: "item-open", ConflictID: "c1", Status: "answered", AnswerHead: "abc", Text: report}); err != nil {
			return err
		}
		if err := appendConflict(tx, item.Conflict{ItemID: "item-open", ConflictID: "c2", Status: "answered", AnswerHead: "def", Text: report}); err != nil {
			return err
		}
		// Unanswered or question-ref conflicts are ignored.
		if err := appendConflict(tx, item.Conflict{ItemID: "item-open", ConflictID: "c3", Status: "owner", Text: report}); err != nil {
			return err
		}
		if err := appendConflict(tx, item.Conflict{ItemID: "item-open", ConflictID: "c4", Status: "answered", AnswerHead: "ghi", QuestionRef: "q1", Text: report}); err != nil {
			return err
		}
		// Closed item is excluded when scoping to item-open.
		return appendConflict(tx, item.Conflict{ItemID: "item-closed", ConflictID: "c5", Status: "answered", AnswerHead: "jkl", Text: report})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		empty, err := answeredObstacleCountsForItems(r, nil)
		if err != nil || len(empty) != 0 {
			t.Fatalf("empty = %v, %v", empty, err)
		}
		got, err := answeredObstacleCountsForItems(r, []string{"item-open"})
		if err != nil {
			return err
		}
		if len(got) != 1 || got["item-open"] != 2 {
			t.Fatalf("got = %v, want map[item-open:2]", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadWhyRefusalsForItems(t *testing.T) {
	ctx := context.Background()
	s := openDrainScopedTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, artifact_refs, started_at, finished_at, event_seq) VALUES (?, ?, ?, ?, ?, ?, '[]', ?, ?, ?)`,
			"run-open-1", "item-open", "lane-1", "codex", run.StateExited, 0, at.UnixMilli(), at.UnixMilli(), 1,
		); err != nil {
			return err
		}
		appendRefusal := func(objType, objID string, payload map[string]any) error {
			body, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			var ptr *string
			if objID != "" {
				id := objID
				ptr = &id
			}
			_, err = tx.Append(event.Event{
				Kind:       RefusalKind,
				ObjectType: objType,
				ObjectID:   ptr,
				Payload:    body,
				At:         at,
			})
			return err
		}
		// Refusal attached to item object_id with item_id in payload (must not double-count).
		if err := appendRefusal("item", "item-open", map[string]any{"item_id": "item-open", "step": RefusalGate}); err != nil {
			return err
		}
		// Refusal attached to run object_id without item_id in payload (resolved via runs table).
		if err := appendRefusal("run", "run-open-1", map[string]any{"step": RefusalFence}); err != nil {
			return err
		}
		// Refusal attached to another object_id, linked only via payload.item_id.
		if err := appendRefusal("lane", "lane-1", map[string]any{"item_id": "item-open", "step": RefusalConflict}); err != nil {
			return err
		}
		// Non-why step is ignored.
		if err := appendRefusal("item", "item-open", map[string]any{"item_id": "item-open", "step": "other"}); err != nil {
			return err
		}
		// Closed item is ignored when scoping to item-open.
		return appendRefusal("item", "item-closed", map[string]any{"item_id": "item-closed", "step": RefusalGate})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		empty, err := readWhyRefusalsForItems(r, nil, nil)
		if err != nil || len(empty) != 0 {
			t.Fatalf("empty = %v, %v", empty, err)
		}
		got, err := readWhyRefusalsForItems(r, []string{"item-open"}, []string{"run-open-1"})
		if err != nil {
			return err
		}
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1 (%v)", len(got), got)
		}
		counts := got["item-open"]
		if counts[RefusalGate] != 1 || counts[RefusalFence] != 1 || counts[RefusalConflict] != 1 || len(counts) != 3 {
			t.Fatalf("counts = %v, want 1 each of gate, fence, conflict", counts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadItemTitlesAndSplitCallsForItems(t *testing.T) {
	ctx := context.Background()
	s := openDrainScopedTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, row := range [][2]string{{"item-open", "Open title"}, {"item-open-2", "Open 2"}, {"item-closed", "Closed title"}} {
			if _, err := tx.Exec(
				`INSERT INTO items(item_id, title, state, updated_at, event_seq) VALUES (?, ?, 'queued', ?, 1)`,
				row[0], row[1], at.UnixMilli(),
			); err != nil {
				return err
			}
		}
		for _, sc := range []struct {
			callID         int64
			itemID, status string
		}{
			{1, "item-open", item.SplitCallStatusPending},
			{2, "item-open-2", item.SplitCallStatusNoSplit},
			{3, "item-closed", item.SplitCallStatusPending},
		} {
			if _, err := tx.Exec(
				`INSERT INTO split_calls(call_id, item_id, caller, run_id, donor_ref, donor_sha, base_sha, signals, status, design_run_id, no_split_reason, last_rejection_reason, created_at, answered_at)
				 VALUES (?, ?, 'verifier', 'run-1', '', '', '', '[]', ?, '', '', '', ?, '')`,
				sc.callID, sc.itemID, sc.status, at.Format(time.RFC3339Nano),
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		emptyTitles, err := readItemTitlesFor(r, nil)
		if err != nil || len(emptyTitles) != 0 {
			t.Fatalf("empty titles = %v, %v", emptyTitles, err)
		}
		titles, err := readItemTitlesFor(r, []string{"item-open"})
		if err != nil {
			return err
		}
		if len(titles) != 1 || titles["item-open"] != "Open title" {
			t.Fatalf("titles = %v, want only item-open", titles)
		}

		pending, noSplit, err := readSplitCallsForPlannerForItems(r, []string{"item-open", "item-open-2"})
		if err != nil {
			return err
		}
		if len(pending) != 1 || pending["item-open"].CallID != 1 {
			t.Errorf("pending = %+v, want only item-open", pending)
		}
		if len(noSplit) != 1 || noSplit["item-open-2"].CallID != 2 {
			t.Errorf("noSplit = %+v, want only item-open-2", noSplit)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadPendingHistoryAndLiveForksForItems(t *testing.T) {
	ctx := context.Background()
	s := openDrainScopedTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		// Pending work for item-open and item-closed.
		for _, pair := range [][2]string{{"item-open", "run-open-1"}, {"item-closed", "run-closed-1"}} {
			if err := appendEffect(tx, dispatchStartedKind, pair[1], pendingWork{
				Decision: Decision{ItemID: pair[0], RunRequest: &run.Request{RunID: pair[1]}},
				BaseSHA:  "base123",
			}, at); err != nil {
				return err
			}
		}
		// A held lane owned by item-held-on-lane (not present in the caller's open items slice)
		// whose delivery decision names fork "pi".
		laneID := "lane-1"
		leaseUntil := at.Add(time.Hour).UnixMilli()
		if _, err := tx.Exec(
			`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES (?, ?, ?, ?)`,
			laneID, LaneStateLeased, "run-held-1", leaseUntil,
		); err != nil {
			return err
		}
		allocBody, err := json.Marshal(map[string]any{
			"lane_id": laneID,
			"run_id":  "run-held-1",
			"item_id": "item-held-on-lane",
		})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event.Event{
			Kind:       run.AllocateKind,
			ObjectType: "lane",
			ObjectID:   &laneID,
			Payload:    allocBody,
			At:         at,
		}); err != nil {
			return err
		}
		delivBody, err := json.Marshal(boundary.DeliveryDecision{Kind: "fork", Fork: "pi"})
		if err != nil {
			return err
		}
		heldItem := "item-held-on-lane"
		_, err = tx.Append(event.Event{
			Kind:       boundary.DeliveryDecisionKind,
			ObjectType: "item",
			ObjectID:   &heldItem,
			Payload:    delivBody,
			At:         at,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		emptyWorks, emptyByRun, err := readPendingHistoryForItems(r, nil, nil)
		if err != nil || len(emptyWorks) != 0 || len(emptyByRun) != 0 {
			t.Fatalf("empty pending = %v %v %v", emptyWorks, emptyByRun, err)
		}
		works, byRun, err := readPendingHistoryForItems(r, []string{"item-open"}, []string{"run-open-1"})
		if err != nil {
			return err
		}
		if len(works) != 1 || works["item-open"] == nil || byRun["run-open-1"] == nil {
			t.Fatalf("scoped pending = works:%v byRun:%v, want only item-open/run-open-1", works, byRun)
		}
		if works["item-closed"] != nil {
			t.Fatalf("unexpected item-closed in scoped pending works")
		}

		// liveForksForItems must find the fork held on lane-1 via events_run_allocate_run
		// and fetch its delivery decision even when item-held-on-lane is not in items.
		forks, err := liveForksForItems(r, at, works, []itemRow{{ItemID: "item-open"}})
		if err != nil {
			return err
		}
		if len(forks) != 1 || forks[0].itemID != "item-held-on-lane" || forks[0].fork != "pi" {
			t.Fatalf("liveForksForItems = %+v, want [{item-held-on-lane pi}]", forks)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadInfrastructureClosedFromIndexedPath(t *testing.T) {
	ctx := context.Background()
	s := openDrainScopedTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	appendFinish := func(tx *store.Tx, runID, backend string, exitCode int) int64 {
		rec := run.Record{RunID: runID, ItemID: "item-1", Backend: backend, State: run.StateExited, ExitCode: countPtr(exitCode)}
		body, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		id := runID
		seq, err := tx.Append(event.Event{Kind: run.FinishKind, ObjectType: "run", ObjectID: &id, Payload: body, At: at})
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		// Backend "closed-be" has a saved closing outcome.
		seqClosed := appendFinish(tx, "run-closed-be", "closed-be", 1)
		if _, err := tx.Exec(`INSERT INTO backend_outcomes(terminal_seq, class, closes) VALUES (?, ?, 1)`, seqClosed, string(policy.AttemptInfrastructure)); err != nil {
			return err
		}
		// Backend "probed-be" had a saved closing outcome, then a successful probe.
		seqProbed := appendFinish(tx, "run-probed-be", "probed-be", 1)
		if _, err := tx.Exec(`INSERT INTO backend_outcomes(terminal_seq, class, closes) VALUES (?, ?, 1)`, seqProbed, string(policy.AttemptInfrastructure)); err != nil {
			return err
		}
		be := "probed-be"
		if _, err := tx.Append(event.Event{
			Kind:       "drain.backend-probe",
			ObjectType: "backend",
			ObjectID:   &be,
			Payload:    []byte(`{"backend":"probed-be","success":true}`),
			At:         at,
		}); err != nil {
			return err
		}
		// Backend "recovered-be" had a saved closing outcome, then a newer non-interrupted outcome.
		seqRec := appendFinish(tx, "run-rec-1", "recovered-be", 1)
		if _, err := tx.Exec(`INSERT INTO backend_outcomes(terminal_seq, class, closes) VALUES (?, ?, 1)`, seqRec, string(policy.AttemptInfrastructure)); err != nil {
			return err
		}
		seqRecOK := appendFinish(tx, "run-rec-2", "recovered-be", 0)
		if _, err := tx.Exec(`INSERT INTO backend_outcomes(terminal_seq, class, closes) VALUES (?, ?, 0)`, seqRecOK, string(policy.AttemptAnswered)); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		closed, err := readInfrastructureClosedFrom(r, nil)
		if err != nil {
			return err
		}
		if _, ok := closed["closed-be"]; !ok {
			t.Errorf("expected closed-be to be closed, got %+v", closed)
		}
		if _, ok := closed["probed-be"]; ok {
			t.Errorf("expected probed-be to be reopened by probe, got %+v", closed)
		}
		if _, ok := closed["recovered-be"]; ok {
			t.Errorf("expected recovered-be to be reopened by newer outcome, got %+v", closed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunLinkMissingHoldUsesItemScopedConflicts(t *testing.T) {
	ctx := context.Background()
	s := openDrainScopedTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		c := item.Conflict{ItemID: "item-open", ConflictID: "c1", Status: "owner", DesignRunID: "run-design-1"}
		body, err := json.Marshal(c)
		if err != nil {
			return err
		}
		id := "item-open"
		_, err = tx.Append(event.Event{Kind: item.ConflictKind, ObjectType: "item", ObjectID: &id, Payload: body, At: at})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		runID, detail, action := runLinkMissingHold(r, "item-open", "fallback-run")
		if runID != "run-design-1" || detail == "" || action == "" {
			t.Fatalf("runLinkMissingHold = (%q, %q, %q), want run-design-1", runID, detail, action)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
