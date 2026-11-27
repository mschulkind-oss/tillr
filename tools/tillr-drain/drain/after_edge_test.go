package drain

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAfterEdgesFinishedStatesReady(t *testing.T) {
	f, l := passFixture(t, "closed-dep", "landed-dep", "resolved-dep", "wait-after-finished")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`
			UPDATE items SET state='closed' WHERE item_id='closed-dep';
			UPDATE items SET state='landed' WHERE item_id='landed-dep';
			UPDATE items SET state='resolved-elsewhere' WHERE item_id='resolved-dep';
			INSERT INTO item_edges(item_id, blocked_by, kind) VALUES
				('wait-after-finished', 'closed-dep', 'after'),
				('wait-after-finished', 'landed-dep', 'after'),
				('wait-after-finished', 'resolved-dep', 'after');
			INSERT INTO lanes(lane_id, state) VALUES ('lane-1', 'idle');
		`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()

	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		blocked, err := itemDependencyBlocked(r, "wait-after-finished")
		if err != nil {
			return err
		}
		if blocked {
			t.Fatal("itemDependencyBlocked returned true for after edges on finished items")
		}

		envs, err := Plan(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			if d.ItemID == "wait-after-finished" && d.Reason == ReasonBlocked {
				t.Fatalf("wait-after-finished planned as ReasonBlocked: %+v", d)
			}
		}

		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "wait-after-finished" && row.Reason == ReasonBlocked {
				t.Fatalf("wait-after-finished Why reported ReasonBlocked: %+v", row)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAfterEdgeUnfinishedHoldsAndWhyNamesKind(t *testing.T) {
	f, l := passFixture(t, "active-dep", "closed-dep", "wait-after-active", "wait-blocked-active", "wait-blocked-closed")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`
			UPDATE items SET state='closed' WHERE item_id='closed-dep';
			INSERT INTO item_edges(item_id, blocked_by, kind) VALUES
				('wait-after-active', 'active-dep', 'after'),
				('wait-after-active', 'closed-dep', 'after'),
				('wait-blocked-active', 'active-dep', 'blocked_by'),
				('wait-blocked-closed', 'closed-dep', 'blocked_by');
			INSERT INTO lanes(lane_id, state) VALUES ('lane-1', 'idle');
		`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()

	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		for _, id := range []string{"wait-after-active", "wait-blocked-active", "wait-blocked-closed"} {
			blocked, err := itemDependencyBlocked(r, id)
			if err != nil {
				return err
			}
			if !blocked {
				t.Fatalf("%s: itemDependencyBlocked = false, want true", id)
			}
		}

		answer, err := Why(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		rows := map[string]WhyRow{}
		for _, row := range answer.Rows {
			rows[row.ItemID] = row
		}

		afterRow := rows["wait-after-active"]
		if afterRow.Reason != ReasonBlocked || afterRow.Class != "waits on work" {
			t.Fatalf("wait-after-active row: %+v", afterRow)
		}
		if !reflect.DeepEqual(afterRow.WaitingOn, []string{"active-dep"}) {
			t.Fatalf("wait-after-active WaitingOn = %v, want [active-dep]", afterRow.WaitingOn)
		}
		if !strings.Contains(afterRow.Why, "active-dep (after)") {
			t.Fatalf("wait-after-active Why = %q, want active-dep (after)", afterRow.Why)
		}
		if strings.Contains(afterRow.Why, "closed without landing") || strings.Contains(afterRow.Why, "closed-dep") {
			t.Fatalf("wait-after-active Why unexpectedly mentioned closed-dep: %q", afterRow.Why)
		}

		blockedActive := rows["wait-blocked-active"]
		if blockedActive.Reason != ReasonBlocked || blockedActive.Class != "waits on work" {
			t.Fatalf("wait-blocked-active row: %+v", blockedActive)
		}
		if !strings.Contains(blockedActive.Why, "active-dep (blocked_by)") {
			t.Fatalf("wait-blocked-active Why = %q, want active-dep (blocked_by)", blockedActive.Why)
		}

		blockedClosed := rows["wait-blocked-closed"]
		if blockedClosed.Reason != ReasonBlocked || blockedClosed.Class != "needs you" {
			t.Fatalf("wait-blocked-closed row: %+v", blockedClosed)
		}
		if !strings.Contains(blockedClosed.Why, "closed-dep (blocked_by)") || !strings.Contains(blockedClosed.Why, "closed without landing") {
			t.Fatalf("wait-blocked-closed Why = %q, want closed-dep (blocked_by) and closed without landing", blockedClosed.Why)
		}
		if !strings.Contains(blockedClosed.Mover, "remove the dependency or reopen the closed item") {
			t.Fatalf("wait-blocked-closed Mover = %q", blockedClosed.Mover)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAfterEdgeBaseChecksAndLandedDependents(t *testing.T) {
	f, l := passFixture(t, "after-target", "after-dep", "blocked-dep")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "base")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)

	runGit(t, f.root, "commit", "--allow-empty", "-qm", "after-target landed")
	targetSHA := runGit(t, f.root, "rev-parse", TrunkRef)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`
			UPDATE items SET state='landed' WHERE item_id='after-target';
			UPDATE items SET state='landed' WHERE item_id='after-dep';
			UPDATE items SET state='landed' WHERE item_id='blocked-dep';
			INSERT INTO item_edges(item_id, blocked_by, kind) VALUES
				('after-dep', 'after-target', 'after'),
				('blocked-dep', 'after-target', 'blocked_by');
			INSERT INTO landings(landing_id, item_id, run_id, verdict_id, result, base_sha, head_sha, trunk_before, trunk_after, event_seq)
			VALUES ('land-target', 'after-target', 'run-target', 'verdict-target', 'landed', ?, ?, ?, ?, 1);
		`, base, targetSHA, base, targetSHA); err != nil {
			return err
		}
		if err := appendEffectOn(tx, item.AddKind, "item", "after-dep", map[string]any{
			"item_id": "after-dep",
			"title":   "after-dep",
			"after":   []string{"after-target"},
		}, time.Now()); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, "run-after-dep", pendingWork{
			Decision: Decision{ItemID: "after-dep"},
			BaseSHA:  base,
		}, time.Now()); err != nil {
			return err
		}
		return nil
	})

	states := map[string]string{"after-target": StateLanded, "after-dep": StateInQueue}
	edges := map[string][]itemEdge{"after-dep": {{Target: "after-target", Kind: item.EdgeKindAfter}}}
	if !hasLandedBlocker(states, edges) {
		t.Fatal("hasLandedBlocker = false for landed after target, want true")
	}

	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		waits, err := unprovedBlockers(context.Background(), r, f.root, base, targetSHA, states, edges)
		if err != nil {
			return err
		}
		if waits["after-dep"] != ReasonTrunkUnproved {
			t.Fatalf("unprovedBlockers[after-dep] = %q, want %q", waits["after-dep"], ReasonTrunkUnproved)
		}

		stale, err := runBaseLacks(context.Background(), r, f.root, "run-after-dep", targetSHA)
		if err != nil {
			return err
		}
		if !strings.Contains(stale.Lacks, "after-target") {
			t.Fatalf("runBaseLacks.Lacks = %q, want mention of after-target", stale.Lacks)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	deps, err := l.landedDependents(context.Background(), "after-target")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(deps, []string{"blocked-dep"}) {
		t.Fatalf("landedDependents = %v, want [blocked-dep] (ignoring after-dep)", deps)
	}
}

func TestStartVersionAndSignatureDetectEdgeKindChange(t *testing.T) {
	f, _ := passFixture(t, "item-a", "item-b")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by, kind) VALUES ('item-a', 'item-b', 'blocked_by')`)
		return err
	})

	readVersion := func() string {
		t.Helper()
		s := openFixture(t, f.storePath)
		defer s.Close()
		var ver string
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			schema, err := readVersionSchema(r)
			if err != nil {
				return err
			}
			if !schema.hasEdgeKind {
				t.Fatal("expected versionSchema.hasEdgeKind to be true")
			}
			ver, err = readItemContentVersion(r, schema, "item-a")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return ver
	}

	verBlocked := readVersion()

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_edges SET kind = 'after' WHERE item_id = 'item-a' AND blocked_by = 'item-b'`)
		return err
	})

	verAfter := readVersion()
	if verBlocked == verAfter {
		t.Fatalf("content version did not change when edge kind changed from blocked_by to after: %s", verBlocked)
	}
}
