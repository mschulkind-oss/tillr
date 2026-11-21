package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyChangesWithPlannerHoldRule(t *testing.T) {
	f, l := passFixture(t, "blocker", "dependent")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='custom-done' WHERE item_id='blocker'; INSERT INTO item_edges(item_id,blocked_by) VALUES ('dependent','blocker'); INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle')`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	read := func(want string) {
		t.Helper()
		err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			envs, err := Plan(r, Pass{}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			var plannerDecision Decision
			if err := json.Unmarshal(envs[1].Payload, &plannerDecision); err != nil {
				return err
			}
			for _, row := range answer.Rows {
				if row.ItemID == "dependent" {
					if row.Reason != want || row.Reason != plannerDecision.Reason {
						t.Fatalf("why %q; planner %q; want %q", row.Reason, plannerDecision.Reason, want)
					}
					return nil
				}
			}
			t.Fatal("dependent missing")
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read(ReasonBlocked)
	// Change the planner's rule for which states satisfy an edge. No why rule changes.
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.States = append(d.States, "custom-done")
	d.Finished = append(d.Finished, "custom-done")
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := definition.Set(context.Background(), writer, raw); err != nil {
		t.Fatal(err)
	}
	read(ReasonReady)
}

func TestWhyConflictContextDoesNotReplaceCurrentMover(t *testing.T) {
	f, l := passFixture(t, "conflicted")
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='blocked'; INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES ('lane-1','leased','busy-run',NULL)`); err != nil {
			return err
		}
		return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-a", ItemID: "conflicted", RunID: "blocked-run", BodyRef: "docs/a.md", Text: "requires design", Status: "raised", WaitingOn: "design", BlockedCriteria: []string{}}, l.cfg.Now())
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	for _, test := range []struct {
		policy        *Policy
		reason, mover string
	}{{l.cfg.Policy, ReasonNoLane, "drain; lane-1"}, {nil, ReasonNoPolicy, "operator; supply the loop's --policy file"}} {
		err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			answer, err := Why(r, Pass{}, test.policy, nil)
			if err != nil {
				return err
			}
			row := answer.Rows[0]
			if row.Reason != test.reason || row.Mover != test.mover || row.Decision.ConflictID != "conflict-a" {
				t.Fatalf("current cause lost behind ancillary conflict: %+v, want %s / %s", row, test.reason, test.mover)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNamedWaitSurvivesDesignAnswer(t *testing.T) {
	f, l, _, base := designAnswerFixture(t)
	candidate := landedAnswer(t, base, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := item.RecordConflict(tx, *candidate.BlockConflict, l.cfg.Now()); err != nil {
			return err
		}
		_, err := item.SetState(context.Background(), tx, "item-a", StateBlocked, l.cfg.Now(), item.WaitingOn{Waiter: "owner", Reason: "host acknowledgement"})
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	check := func() {
		t.Helper()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			row := answer.Rows[0]
			if row.State != StateBlocked || row.Decision.Action != ActionHold || row.Reason != ReasonBlocked || row.Mover != "owner" {
				t.Fatalf("named wait lost: %+v", row)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	check()
	if err := l.answerBlock(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "answered" {
		t.Fatalf("answer was not recorded: %+v", c)
	}
	check()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		d := Decision{ItemID: "item-a", LaneID: "lane-1", Action: ActionDispatch, Reason: ReasonReady, Attempt: 1, RunRequest: &run.Request{ItemID: "item-a", RunID: "live-run", LaneID: "lane-1"}}
		raw, err := json.Marshal(pendingWork{Decision: d})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (0,?,'run','live-run',?)`, dispatchStartedKind, string(raw))
		return err
	})
	check()
}

func TestWhyAccountsForBusyLanesAndPendingRun(t *testing.T) {
	f, l := passFixture(t, "running", "queued", "held")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='blocked' WHERE item_id='held'; INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES ('lane-1','leased','live-run',NULL),('lane-2','idle',NULL,NULL)`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	// A pending dispatch is the same durable fact that Plan advances on its next pass.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		d := Decision{ItemID: "running", LaneID: "lane-1", Action: ActionDispatch, Reason: ReasonReady, Attempt: 1, From: "queued", To: "running", RunRequest: &run.Request{}}
		d.RunRequest.ItemID, d.RunRequest.RunID, d.RunRequest.LaneID = "running", "live-run", "lane-1"
		identifyDecision(&d)
		raw, err := json.Marshal(pendingWork{Decision: d})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (0,?,'run','live-run',?)`, dispatchStartedKind, string(raw))
		return err
	})
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		total := answer.Summary.Running + answer.Summary.CouldRun
		for _, n := range answer.Summary.Held {
			total += n
		}
		if total != len(answer.Rows) || answer.Summary.LanesBusy != 1 || answer.Summary.LanesOffered != 2 || answer.Summary.Running != 1 {
			t.Fatalf("answer: %+v", answer)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWhyCountsPlacesWithoutExposingKeys(t *testing.T) {
	f, l := passFixture(t, "first", "a", "z", "parked", "done")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET rank=''; UPDATE items SET rank='secretkey' WHERE item_id='first'; UPDATE items SET state='not-started' WHERE item_id='parked'; UPDATE items SET state='landed' WHERE item_id='done'")
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			raw, err := json.Marshal(row)
			if err != nil {
				return err
			}
			var fields map[string]any
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			if _, ok := fields["rank"]; ok {
				t.Fatalf("key leaked: %s", raw)
			}
			want := map[string]any{"first": float64(1), "a": float64(2), "z": float64(3)}[row.ItemID]
			if fields["place"] != want {
				t.Fatalf("%s place %v want %v", row.ItemID, fields["place"], want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWhyPlacesFollowKeysNotPlannerPromotions(t *testing.T) {
	f, l := passFixture(t, "ordinary", "fixer-main-test", "rewrite-item-test")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET rank=CASE item_id WHEN 'ordinary' THEN 'a' WHEN 'fixer-main-test' THEN 'z' ELSE 'zz' END")
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			want := map[string]int{"ordinary": 1, "fixer-main-test": 2, "rewrite-item-test": 3}[row.ItemID]
			if row.Place != want {
				t.Fatalf("%s place %d want %d", row.ItemID, row.Place, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// On 2026-11-19 the trunk fixer's candidate sat in lane-4 while the loop's gate examined
// it there, under the examination's no-expiry lease. The plan held it no-lane behind
// that lease, and `why` answered "drain; lane-2, lane-3, lane-4" — every busy lane — so
// a fixer that could only ever continue in its own worktree read as waiting for any of
// three lanes while seventeen were idle. Work bound to its lane waits on that lane alone.
func TestWhyNamesOnlyTheOwnLaneOfWorkHeldBehindItsLease(t *testing.T) {
	f, l := passFixture(t, "cand", "other")
	p := mustPlan(t, l)[0]
	if p.decision.ItemID != "cand" {
		t.Fatalf("fixture: first dispatch is %s", p.decision.ItemID)
	}
	deadOwnerClaim(t, f, l, p)
	id, lane := p.decision.RunRequest.RunID, p.decision.LaneID
	owner, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
		if err := applyTerminal(tx, run.Record{RunID: id, ItemID: "cand", LaneID: lane, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationOwnerKind, id, owner, l.cfg.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, id, lane); err != nil {
			return err
		}
		other := "lane-2"
		if lane == other {
			other = "lane-1"
		}
		_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "run-other-live", ItemID: "other", Backend: "fixture"}, other, l.cfg.Now())
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID != "cand" {
				continue
			}
			if row.Reason != ReasonNoLane {
				t.Fatalf("fixture: cand planned %s, want no-lane behind its own lease: %+v", row.Reason, row.Decision)
			}
			if len(row.WaitingOn) != 1 || row.WaitingOn[0] != lane || row.Mover != "drain; "+lane {
				t.Fatalf("cand waits on %v (mover %q), want only its own %s", row.WaitingOn, row.Mover, lane)
			}
			return nil
		}
		t.Fatal("no row for cand")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWhyRunLinkMissingNamesUnansweredRunAndRetryCommand(t *testing.T) {
	f, l := passFixture(t, "item_041")
	runID := "run-item_041-a1-n1"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
		return applyTerminal(tx, run.Record{
			RunID:        runID,
			ItemID:       "item_041",
			LaneID:       "lane-1",
			Backend:      "fixture",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    at,
			FinishedAt:   at,
			ArtifactRefs: []run.ArtifactRef{},
			EndCause:     run.EndNormalExit,
		})
	})

	s := openFixture(t, f.storePath)
	defer s.Close()
	wantCmd := "tillr drain retry --item item_041 --run " + runID + " --by <who> --why <reason>"
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(answer.Rows) != 1 {
			t.Fatalf("rows = %+v, want 1", answer.Rows)
		}
		row := answer.Rows[0]
		if !strings.HasPrefix(row.Reason, ReasonRunLinkMissing) {
			t.Fatalf("reason = %q, want %q (%+v)", row.Reason, ReasonRunLinkMissing, row)
		}
		if row.Decision.SupersedesRunID != runID {
			t.Fatalf("decision SupersedesRunID = %q, want %q", row.Decision.SupersedesRunID, runID)
		}
		if len(row.WaitingOn) != 1 || row.WaitingOn[0] != runID {
			t.Fatalf("WaitingOn = %v, want [%s]", row.WaitingOn, runID)
		}
		for _, field := range []struct {
			name string
			got  string
		}{
			{"Why", row.Why},
			{"Mover", row.Mover},
		} {
			if strings.Contains(field.got, "cause not recorded") {
				t.Fatalf("%s still says 'cause not recorded': %q", field.name, field.got)
			}
			if !strings.Contains(field.got, runID) || !strings.Contains(field.got, wantCmd) {
				t.Fatalf("%s = %q, want both %q and %q", field.name, field.got, runID, wantCmd)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Recording the pass decision also persists the action in dispatches.deferred_by so
	// history-backed waiting views name the run and retry command instead of 'cause not recorded'.
	heldDecision := planFor(t, l, "item_041")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return recordPassDecision(tx, heldDecision, l.cfg.Now())
	})
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var deferredBy string
		if err := r.QueryRow(`SELECT COALESCE(deferred_by, '') FROM dispatches WHERE item_id = 'item_041' ORDER BY dispatched_at DESC LIMIT 1`).Scan(&deferredBy); err != nil {
			return err
		}
		if !strings.Contains(deferredBy, runID) || !strings.Contains(deferredBy, wantCmd) {
			t.Fatalf("dispatches.deferred_by = %q, want both %q and %q", deferredBy, runID, wantCmd)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeRetryReleasesBlockedItemWithoutOwnerConflict(t *testing.T) {
	f, l := passFixture(t, "item_041")
	runID := "run-item_041-a1-n1"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now()
		atStr := at.Format("2006-01-02T15:04:05Z07:00")
		if err := applyTerminal(tx, run.Record{
			RunID:        runID,
			ItemID:       "item_041",
			LaneID:       "lane-1",
			Backend:      "fixture",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    atStr,
			FinishedAt:   atStr,
			ArtifactRefs: []run.ArtifactRef{},
			EndCause:     run.EndNormalExit,
		}); err != nil {
			return err
		}
		_, err := item.SetState(context.Background(), tx, "item_041", StateBlocked, at, item.WaitingOn{
			Waiter: "owner",
			Reason: "manual owner block after run",
		})
		return err
	})

	ws, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: "item_041",
			RunID:  runID,
			By:     "matt",
			Why:    "unblock and supersede",
			At:     l.cfg.Now().Add(1),
		})
		return err
	}); err != nil {
		t.Fatalf("AuthorizeRetry: %v", err)
	}
	d := planFor(t, l, "item_041")
	if d.Action != ActionRetry || d.SupersedesRunID != runID {
		t.Fatalf("after AuthorizeRetry got %+v, want ActionRetry superseding %s", d, runID)
	}
}
