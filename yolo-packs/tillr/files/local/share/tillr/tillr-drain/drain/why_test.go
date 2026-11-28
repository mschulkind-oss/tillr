package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
	_ "github.com/mschulkind-oss/tillr/watch"
	"github.com/mschulkind-oss/tillr/watchlinks"
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
			for _, env := range envs {
				var d Decision
				if err := json.Unmarshal(env.Payload, &d); err != nil {
					return err
				}
				if d.ItemID == "dependent" {
					plannerDecision = d
					break
				}
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

// Built from the 2026-11-25 item-request-e9348f4501 history: a workflow verify node
// finished and passed, then a pre-gate rebase triggered a fresh re-judgment run
// (`...-fresh`, recorded with drain.judge.request and no node_runs row) that finished
// without a refusal or landing. The planner's latest planned run (`...-n3`) and the
// store's latest finished run (`...-n3-fresh`) differ; the retry command the
// run-link-missing hold prints must be accepted and release the hold.
func TestRunLinkMissingRetryReleasesAfterFreshVerifyRun(t *testing.T) {
	f, l := passFixture(t, "item_041")
	buildRun := "run-item_041-a2-n2"
	verifyRun := "run-item_041-a2-n3"
	freshRun := "run-item_041-a2-n3-fresh"
	baseAt := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, r := range []struct {
			id, node, outcome string
			judge             bool
		}{
			{buildRun, "build", "passed", false},
			{verifyRun, "verify", "passed", true},
			{freshRun, "", "", true},
		} {
			at := baseAt.Add(time.Duration(i+2) * time.Second)
			atStr := at.Format("2006-01-02T15:04:05Z07:00")
			if r.node != "" {
				if _, err := tx.Exec(
					`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome) VALUES ('item_041', 2, ?, ?, ?)`,
					r.node, r.id, r.outcome,
				); err != nil {
					return err
				}
			}
			if r.judge {
				if err := appendEffect(tx, judgeRequestKind, r.id, map[string]any{
					"item_id":       "item_041",
					"writer_run_id": buildRun,
					"seat":          "review",
				}, at); err != nil {
					return err
				}
			}
			if err := applyTerminal(tx, run.Record{
				RunID:        r.id,
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
		}
		return nil
	})

	s := openFixture(t, f.storePath)
	defer s.Close()
	var printedRun string
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
		wantCmd := "tillr drain retry --item item_041 --run " + freshRun + " --by <who> --why <reason>"
		if !strings.Contains(row.Mover, "supersede run "+freshRun+" with "+wantCmd) {
			t.Fatalf("Mover = %q, want supersede run %s with %s", row.Mover, freshRun, wantCmd)
		}
		if row.Decision.SupersedesRunID != freshRun {
			t.Fatalf("decision SupersedesRunID = %q, want %q", row.Decision.SupersedesRunID, freshRun)
		}
		// Extract the --run value from the printed retry command.
		_, after, ok := strings.Cut(row.Mover, "--run ")
		if !ok {
			t.Fatalf("Mover missing --run flag: %q", row.Mover)
		}
		printedRun = strings.Fields(after)[0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Execute the exact retry run the hold printed.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: "item_041",
			RunID:  printedRun,
			By:     "operator",
			Why:    "release run-link-missing hold after fresh verify",
			At:     baseAt.Add(10 * time.Second),
		})
		return err
	})

	// Planning must now release the hold with ActionRetry.
	d := planFor(t, l, "item_041")
	if d.Action != ActionRetry || d.SupersedesRunID != printedRun {
		t.Fatalf("after AuthorizeRetry(%s) got %+v, want ActionRetry superseding %s", printedRun, d, printedRun)
	}

	// Once a newer run starts (even without finishing), the retry is retired and does not re-fire.
	newerRun := "run-item_041-a3-n1"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := baseAt.Add(20 * time.Second)
		_, err := tx.Exec(
			`INSERT INTO runs(run_id, item_id, lane_id, backend, state, started_at, finished_at, artifact_refs, event_seq)
			 VALUES (?, 'item_041', 'lane-1', 'fixture', ?, ?, 0, '[]', 999999)`,
			newerRun, "running", at.UnixMilli(),
		)
		return err
	})
	dAfter := planFor(t, l, "item_041")
	if dAfter.Action == ActionRetry {
		t.Fatalf("stale retry re-fired after newer unfinished run %s: %+v", newerRun, dAfter)
	}
}

func TestWhyIncludesLinkedAlerts(t *testing.T) {
	f, l := passFixture(t, "item-with-alerts", "item-without-alerts")
	now := time.Date(2026, 11, 26, 13, 0, 0, 0, time.UTC)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, settlement_state)
			VALUES ('alrt-why-1', 'drain-item-fault', 'subj-1', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', 'uninvestigated'),
			       ('alrt-why-2', 'drain-item-fault', 'subj-2', 1, 'open', 'medium', 'fp2', 2, ?, 2, ?, '{}', 'uninvestigated')`,
			now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
			return err
		}
		if err := watchlinks.LinkAlertItemInTx(tx, "alrt-why-1", "item-with-alerts", watchlinks.LinkRelationFiled, "tillr-triage", "", now); err != nil {
			return err
		}
		return watchlinks.LinkAlertItemInTx(tx, "alrt-why-2", "item-with-alerts", watchlinks.LinkRelationFiled, "tillr-triage", "", now.Add(time.Second))
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		byID := map[string]WhyRow{}
		for _, row := range answer.Rows {
			byID[row.ItemID] = row
		}
		withRow, ok := byID["item-with-alerts"]
		if !ok {
			t.Fatal("item-with-alerts missing from Why rows")
		}
		if len(withRow.LinkedAlerts) != 2 || withRow.LinkedAlerts[0].AlertID != "alrt-why-1" || withRow.LinkedAlerts[1].AlertID != "alrt-why-2" {
			t.Fatalf("item-with-alerts linked_alerts = %+v, want [alrt-why-1, alrt-why-2]", withRow.LinkedAlerts)
		}
		withoutRow, ok := byID["item-without-alerts"]
		if !ok {
			t.Fatal("item-without-alerts missing from Why rows")
		}
		if len(withoutRow.LinkedAlerts) != 0 {
			t.Fatalf("item-without-alerts linked_alerts = %+v, want empty", withoutRow.LinkedAlerts)
		}
		rawWithout, err := json.Marshal(withoutRow)
		if err != nil {
			return err
		}
		if strings.Contains(string(rawWithout), `"linked_alerts"`) {
			t.Fatalf("expected linked_alerts omitted in JSON when empty: %s", rawWithout)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
