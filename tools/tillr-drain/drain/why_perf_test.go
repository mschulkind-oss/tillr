package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// TestWhyFoldsPendingHistoryOnce pins that Why shares the folded pending
// history with planWithDetails instead of folding the entire pending event
// stream twice in the same read transaction.
func TestWhyFoldsPendingHistoryOnce(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	before := pendingFolds.Load()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		lanes := []string{"lane-1"}
		_, err := Why(r, Pass{
			Now:            "2026-11-24T00:00:00Z",
			StorePath:      f.storePath,
			AvailableLanes: &lanes,
		}, nil, nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := pendingFolds.Load() - before; got != 1 {
		t.Fatalf("Why ran %d full pending-history folds, want 1", got)
	}
}

// TestReadRunHistoryUsesIndexedItemLookupAndBatchObstacleCalls pins that
// readRunHistory resolves item state history through events_by_object (never
// scanning unrelated run.child-session rows) and resolves obstacle call
// backends/routes in a single batch query.
func TestReadRunHistoryUsesIndexedItemLookupAndBatchObstacleCalls(t *testing.T) {
	f, l := passFixture(t, "item-a")
	workerRecordFlood(t, f, 200)

	runID := "run-item-a-a1"
	callID := "call-obs-1"
	itemID := "item-a"
	at := l.cfg.Now()

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Append(event.Event{
			Kind:       "item.update",
			ObjectType: "item",
			ObjectID:   &itemID,
			Payload:    []byte(`{"state":"in-progress"}`),
			At:         at,
		}); err != nil {
			return err
		}
		startPayload, err := json.Marshal(pendingWork{
			Decision: Decision{
				ItemID: itemID,
				LaneID: "lane-1",
				RunRequest: &run.Request{
					RunID:   runID,
					ItemID:  itemID,
					LaneID:  "lane-1",
					Backend: "pi-build",
				},
			},
		})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event.Event{
			Kind:       dispatchStartedKind,
			ObjectType: "run",
			ObjectID:   &runID,
			Payload:    startPayload,
			At:         at.Add(time.Second),
		}); err != nil {
			return err
		}
		callPayload, err := json.Marshal(landingCall{
			landingOffer: landingOffer{Base: "deadbeef"},
			Tool:         "obstacle",
			RunID:        runID,
			SessionID:    "sess-obs-1",
			RunLog:       "/tmp/lanes/lane-1/run.log",
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind:       callOffered,
			ObjectType: "call",
			ObjectID:   &callID,
			Payload:    callPayload,
			At:         at.Add(2 * time.Second),
		})
		return err
	})

	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		// Verify the item-state query plan uses events_by_object rather than scanning events.
		rows, err := r.Query(`EXPLAIN QUERY PLAN SELECT object_id,
 MAX(CASE WHEN json_extract(payload, '$.state') = ? THEN 1 ELSE 0 END)
 FROM events
 WHERE object_id IN (SELECT item_id FROM items)
  AND object_type = 'item'
  AND json_valid(payload)
  AND json_type(payload, '$.state') = 'text'
  AND json_extract(payload, '$.state') <> ''
 GROUP BY object_id`, StateInProgress)
		if err != nil {
			return err
		}
		var details []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				return err
			}
			details = append(details, detail)
		}
		rows.Close()
		plan := strings.Join(details, " | ")
		if !strings.Contains(plan, "events_by_object") {
			t.Fatalf("item-state history query did not use events_by_object: %s", plan)
		}

		hist, err := readRunHistory(r)
		if err != nil {
			return err
		}
		if !hist.known[itemID] || !hist.ranBefore[itemID] {
			t.Fatalf("expected item-a known=true ranBefore=true, got known=%v ranBefore=%v", hist.known[itemID], hist.ranBefore[itemID])
		}
		ws := hist.obstacleCalls[runID]
		if ws == nil {
			t.Fatalf("missing obstacle call for %s", runID)
		}
		if ws.Backend != "pi-build" {
			t.Fatalf("obstacle call backend = %q, want pi-build", ws.Backend)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestBatchAttemptInputsMatchesPerItemAndLazyInputDecisions pins that the
// batched attempt-input lookup matches readAttemptInputs for every item, and
// that planner.inputDecision loads the latest drain.inputs record on demand.
func TestBatchAttemptInputsMatchesPerItemAndLazyInputDecisions(t *testing.T) {
	f, l := passFixture(t, "item-a")
	at := l.cfg.Now()

	fixtureWrite(t, f, func(tx *store.Tx) error {
		ts := at.Format(time.RFC3339)
		if err := applyTerminal(tx, run.Record{
			RunID: "run-item-a-a2", ItemID: "item-a", LaneID: "lane-1",
			Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: ts, FinishedAt: ts, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit,
		}); err != nil {
			return err
		}
		runID := "run-item-a-a2"
		first, err := json.Marshal(Decision{ItemID: "item-a", Action: ActionHold, RetryID: "old-retry"})
		if err != nil {
			return err
		}
		second, err := json.Marshal(Decision{
			ItemID:    "item-a",
			Action:    ActionRetry,
			RetryID:   "retry-2",
			NodeIndex: 0,
			Workflow:  &Workflow{Stages: []WorkflowStage{{Access: "write"}}},
		})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event.Event{Kind: inputRecordedKind, ObjectType: "run", ObjectID: &runID, Payload: first, At: at}); err != nil {
			return err
		}
		_, err = tx.Append(event.Event{Kind: inputRecordedKind, ObjectType: "run", ObjectID: &runID, Payload: second, At: at.Add(time.Second)})
		return err
	})

	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		allRunIDs, err := readAllRunIDs(r)
		if err != nil {
			return err
		}
		byRun := map[string]*pendingWork{}
		wantNext, wantCount, err := readAttemptInputs(r, false, "item-a", nil, byRun)
		if err != nil {
			return err
		}
		gotNext, gotCount := computeAttemptInputs("item-a", 0, nil, allRunIDs["item-a"], byRun)
		if gotNext != wantNext || gotCount != wantCount {
			t.Fatalf("batch attempt inputs = (%d, %d), want (%d, %d)", gotNext, gotCount, wantNext, wantCount)
		}

		// Verify planner.inputDecision lazily loads the latest drain.inputs event.
		p := &planner{inputDecisionReader: r}
		d := p.inputDecision("run-item-a-a2")
		if d.Action != ActionRetry || d.RetryID != "retry-2" || d.Workflow == nil || len(d.Workflow.Stages) != 1 {
			t.Fatalf("lazy inputDecision = %+v, want retry-2 with workflow", d)
		}
		// Missing run returns zero Decision and is marked loaded.
		if missing := p.inputDecision("run-missing"); missing.Action != "" {
			t.Fatalf("expected empty Decision for missing run, got %+v", missing)
		}
		if !p.inputDecisionsLoaded["run-missing"] {
			t.Fatalf("expected run-missing to be marked loaded")
		}

		// Verify readInputDecisions deduplicates to the latest event per run.
		all, err := readInputDecisions(r)
		if err != nil {
			return err
		}
		if all["run-item-a-a2"].RetryID != "retry-2" {
			t.Fatalf("readInputDecisions retry_id = %q, want retry-2", all["run-item-a-a2"].RetryID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestObstaclesReadSkipsPreHoldEvents pins that obstacles.Read ignores
// drain.started events before any obstacle hold exists and still clears a hold
// when a newer drain.started arrives for that item after the hold.
func TestObstaclesReadSkipsPreHoldEvents(t *testing.T) {
	f, l := passFixture(t, "item-a")
	at := l.cfg.Now()
	preRunID := "run-pre-hold"
	postRunID := "run-post-hold"
	itemID := "item-a"

	fixtureWrite(t, f, func(tx *store.Tx) error {
		prePayload, err := json.Marshal(pendingWork{
			Decision: Decision{
				ItemID:     itemID,
				RunRequest: &run.Request{RunID: preRunID, ItemID: itemID, LaneID: "lane-1"},
			},
		})
		if err != nil {
			return err
		}
		if _, err := tx.Append(event.Event{
			Kind:       dispatchStartedKind,
			ObjectType: "run",
			ObjectID:   &preRunID,
			Payload:    prePayload,
			At:         at,
		}); err != nil {
			return err
		}
		holdPayload, err := json.Marshal(obstacles.NewHold(itemID, "run-hold-1", "owner", map[string]string{"QUESTION": "which?"}, nil, "", nil))
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind:       obstacles.HeldKind,
			ObjectType: "item",
			ObjectID:   &itemID,
			Payload:    holdPayload,
			At:         at.Add(time.Second),
		})
		return err
	})

	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		active, _, err := obstacles.Read(r)
		if err != nil {
			return err
		}
		if _, ok := active[itemID]; !ok {
			t.Fatalf("expected active obstacle hold for %s", itemID)
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}

	// A drain.started after the hold clears the hold.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		postPayload, err := json.Marshal(pendingWork{
			Decision: Decision{
				ItemID:     itemID,
				RunRequest: &run.Request{RunID: postRunID, ItemID: itemID, LaneID: "lane-1"},
			},
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind:       dispatchStartedKind,
			ObjectType: "run",
			ObjectID:   &postRunID,
			Payload:    postPayload,
			At:         at.Add(2 * time.Second),
		})
		return err
	})

	s, err = store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		active, _, err := obstacles.Read(r)
		if err != nil {
			return err
		}
		if _, ok := active[itemID]; ok {
			t.Fatalf("expected post-hold drain.started to clear obstacle hold for %s", itemID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestClonePendingHistoryForDeltaPreservesByParent(t *testing.T) {
	parentRunID := "run-parent-cow"
	childRunID := "run-child-cow"
	childItemID := "item-child-cow"
	at := time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)

	childPayload, err := json.Marshal(pendingWork{
		Ready:       true,
		Examining:   true,
		ParentRunID: parentRunID,
		Decision: Decision{
			ItemID:     childItemID,
			LaneID:     "lane-1",
			RunRequest: &run.Request{RunID: childRunID, ItemID: childItemID, LaneID: "lane-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := newPendingHistoryState()
	if err := foldPendingHistoryWithMeta(nil, 0, base, []event.Event{{
		Seq:        1,
		Kind:       dispatchStartedKind,
		ObjectType: "run",
		ObjectID:   &childRunID,
		Payload:    childPayload,
		At:         at,
	}}, nil); err != nil {
		t.Fatal(err)
	}
	if !base.works[childItemID].Examining {
		t.Fatalf("base child should start examining")
	}

	meta := &pendingFoldMeta{cow: true}
	if err := foldPendingHistoryWithMeta(nil, 1, base, []event.Event{{
		Seq:        2,
		Kind:       examinationReleasedKind,
		ObjectType: "run",
		ObjectID:   &parentRunID,
		Payload:    json.RawMessage(`{}`),
		At:         at.Add(time.Second),
	}}, meta); err != nil {
		t.Fatal(err)
	}
	if meta.state == nil {
		t.Fatalf("expected cloned state in meta")
	}
	if meta.state.works[childItemID].Examining {
		t.Fatalf("delta examination-released did not clear child Examining via byParent")
	}
	if !base.works[childItemID].Examining {
		t.Fatalf("delta examination-released mutated base snapshot")
	}
}
