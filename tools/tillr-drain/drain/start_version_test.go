package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func dispatchCount(t *testing.T, storePath string) int {
	t.Helper()
	s, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM dispatches`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRerankViaPlaceDoesNotCancelPlannedStart(t *testing.T) {
	t.Run("move-first-and-last", func(t *testing.T) {
		f, l := passFixture(t, "item-a", "item-b", "item-c")
		l.cfg.Lanes = []string{"lane-1"}
		ctx := context.Background()
		plans := mustPlan(t, l)
		var first plannedApplication
		for _, p := range plans {
			if p.decision.Action == ActionDispatch {
				first = p
				break
			}
		}
		if first.decision.ItemID != "item-a" {
			t.Fatalf("first dispatch=%q, want item-a", first.decision.ItemID)
		}

		// Re-rank item-a to last and item-c to first between decision and claim.
		later := l.cfg.Now().Add(time.Minute)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := item.Place(ctx, tx, "item-a", item.Destination{To: "last"}, later, "owner", "test"); err != nil {
				return err
			}
			_, err := item.Place(ctx, tx, "item-c", item.Destination{To: "first"}, later.Add(time.Second), "owner", "test")
			return err
		})

		base := runGit(t, f.root, "rev-parse", TrunkRef)
		ok, work, err := l.applyPlan(ctx, first, base, later.Add(2*time.Second))
		if err != nil || !ok || work == nil {
			t.Fatalf("re-rank cancelled planned start: ok=%v work=%+v err=%v", ok, work, err)
		}
		if work.Decision.ItemID != "item-a" || work.Decision.LaneID != "lane-1" {
			t.Fatalf("unexpected claimed work: %+v", work.Decision)
		}

		// Subsequent plans observe the new queue order (item-c before item-b).
		l.cfg.Lanes = []string{"lane-2"}
		nextPlans := mustPlan(t, l)
		var nextDispatch string
		for _, p := range nextPlans {
			if p.decision.Action == ActionDispatch {
				nextDispatch = p.decision.ItemID
				break
			}
		}
		if nextDispatch != "item-c" {
			t.Fatalf("subsequent plan dispatched %q, want item-c (new first)", nextDispatch)
		}
	})

	t.Run("crowded-gap-respacing", func(t *testing.T) {
		f, l := passFixture(t, "item-a", "item-b", "item-c")
		l.cfg.Lanes = []string{"lane-1"}
		ctx := context.Background()
		// No key fits between a prefix and that prefix plus the smallest digit.
		// Placing item-c here respaces item-a too, though it was not the target.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET rank = 'a' WHERE item_id = 'item-a'`); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET rank = 'a0' WHERE item_id = 'item-b'`); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE items SET rank = 'z' WHERE item_id = 'item-c'`)
			return err
		})
		plans := mustPlan(t, l)
		var first plannedApplication
		for _, p := range plans {
			if p.decision.Action == ActionDispatch {
				first = p
				break
			}
		}
		if first.decision.ItemID != "item-a" {
			t.Fatalf("first dispatch=%q, want item-a", first.decision.ItemID)
		}

		later := l.cfg.Now().Add(time.Minute)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := item.Place(ctx, tx, "item-c", item.Destination{Before: "item-b"}, later, "owner", "test"); err != nil {
				return err
			}
			var rank string
			if err := tx.QueryRow(`SELECT rank FROM items WHERE item_id = 'item-a'`).Scan(&rank); err != nil {
				return err
			}
			if rank == "a" {
				t.Error("Place did not respace item-a")
			}
			return nil
		})

		base := runGit(t, f.root, "rev-parse", TrunkRef)
		ok, work, err := l.applyPlan(ctx, first, base, later.Add(time.Second))
		if err != nil || !ok || work == nil {
			t.Fatalf("crowded-gap respace cancelled planned start: ok=%v work=%+v err=%v", ok, work, err)
		}

		// Subsequent plans observe item-c ahead of item-b.
		l.cfg.Lanes = []string{"lane-2"}
		nextPlans := mustPlan(t, l)
		var nextDispatch string
		for _, p := range nextPlans {
			if p.decision.Action == ActionDispatch {
				nextDispatch = p.decision.ItemID
				break
			}
		}
		if nextDispatch != "item-c" {
			t.Fatalf("subsequent plan dispatched %q, want item-c", nextDispatch)
		}
	})
}

func TestStartContentChangeCancelsPlannedStart(t *testing.T) {
	testHoldBeforeClaim(t, false)
}

func TestHoldBeforeClaimCancelsStart(t *testing.T) {
	testHoldBeforeClaim(t, true)
}

func testHoldBeforeClaim(t *testing.T, moveTrunk bool) {
	t.Helper()
	cases := []struct {
		name  string
		setup func(t *testing.T, f refusalLoopFixture, l *Loop)
		hold  func(t *testing.T, f refusalLoopFixture, l *Loop)
	}{
		{
			name: "blocked-state",
			hold: func(t *testing.T, f refusalLoopFixture, _ *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET state = 'blocked' WHERE item_id = 'item-a'`)
					return err
				})
			},
		},
		{
			name: "open-conflict",
			hold: func(t *testing.T, f refusalLoopFixture, l *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return item.RecordConflict(tx, item.Conflict{
						ConflictID:      "conflict-1",
						ItemID:          "item-a",
						RunID:           "run-0",
						BodyRef:         "briefs/item-a.md",
						Text:            "needs clarification",
						BlockedCriteria: []string{"c1"},
						WaitingOn:       "design",
						Status:          "raised",
					}, l.cfg.Now().Add(time.Second))
				})
			},
		},
		{
			name: "conflict-transition-without-state-change",
			hold: func(t *testing.T, f refusalLoopFixture, l *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return item.RecordConflict(tx, item.Conflict{
						ConflictID: "conflict-1", ItemID: "item-a", RunID: "run-0",
						Text: "answered in a document", WaitingOn: "design", Status: "answered",
					}, l.cfg.Now().Add(time.Second))
				})
			},
		},
		{
			name: "item-fault",
			hold: func(t *testing.T, _ refusalLoopFixture, l *Loop) {
				if err := l.holdItemFault(context.Background(), Candidate{ItemID: "item-a"}, ReasonDecisionCollision, "fault recorded"); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unlanded-blocker",
			setup: func(t *testing.T, f refusalLoopFixture, _ *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					if _, err := tx.Exec(`INSERT INTO items(item_id, title, body_ref, state, updated_at) VALUES ('item-blocker', 'blocker', 'briefs/blocker.md', 'landed', 1000)`); err != nil {
						return err
					}
					_, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by) VALUES ('item-a', 'item-blocker')`)
					return err
				})
			},
			hold: func(t *testing.T, f refusalLoopFixture, _ *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET state = 'in-queue' WHERE item_id = 'item-blocker'`)
					return err
				})
			},
		},
		{
			name: "owner-only-criterion",
			hold: func(t *testing.T, f refusalLoopFixture, _ *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO item_criteria(item_id, criterion_id, text, state, claim_class) VALUES ('item-a', 'c-owner', 'owner decision', 'open', 'owner-only')`)
					return err
				})
			},
		},
		{
			name: "changed-criterion-text",
			hold: func(t *testing.T, f refusalLoopFixture, _ *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE item_criteria SET text = 'amended criterion' WHERE item_id = 'item-a' AND criterion_id = 'c1'`)
					return err
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			l.cfg.Lanes = []string{"lane-1"}
			if tc.setup != nil {
				tc.setup(t, f, l)
			}
			ctx := context.Background()
			plans := mustPlan(t, l)
			var p plannedApplication
			for _, candidate := range plans {
				if candidate.decision.ItemID == "item-a" && candidate.decision.Action == ActionDispatch {
					p = candidate
					break
				}
			}
			if p.decision.ItemID != "item-a" {
				t.Fatalf("item-a not planned for dispatch: %+v", plans)
			}

			base := runGit(t, f.root, "rev-parse", TrunkRef)
			if moveTrunk {
				runGit(t, f.root, "commit", "--allow-empty", "-qm", "trunk moved")
			}
			tc.hold(t, f, l)

			eventsBefore := eventCount(t, f.storePath)
			dispatchesBefore := dispatchCount(t, f.storePath)
			ok, work, err := l.applyPlan(ctx, p, base, l.cfg.Now().Add(2*time.Second))
			if err != nil || ok || work != nil {
				t.Fatalf("held start was not cancelled: ok=%v work=%+v err=%v", ok, work, err)
			}
			assertOnlyDispatchDecline(t, f.storePath, eventsBefore, p.decision, "item or lane state changed since planning")
			if got := dispatchCount(t, f.storePath); got != dispatchesBefore {
				t.Fatalf("cancelled start wrote dispatches: got=%d want=%d", got, dispatchesBefore)
			}
			// Lane must remain idle.
			s, err := store.OpenRead(ctx, f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				held, err := readLanes(r, l.cfg.Now().Add(2*time.Second))
				if err != nil {
					return err
				}
				if held["lane-1"] {
					t.Fatal("lane-1 is not idle after cancelled start")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBulkAndSingleStartContentVersionsAgree(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO item_criteria(item_id, criterion_id, text, state, claim_class)
			VALUES ('item-a', 'c3', 'third criterion', 'open', 'owner-only'),
			       ('item-a', 'c2', 'second criterion', 'open', 'verification-only')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by) VALUES ('item-a', 'item-b')`); err != nil {
			return err
		}
		return item.RecordConflict(tx, item.Conflict{
			ConflictID: "conflict-1", ItemID: "item-a", RunID: "run-0",
			Text: "clarify the request", WaitingOn: "design", Status: "raised",
		}, l.cfg.Now())
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		schema, err := readVersionSchema(r)
		if err != nil {
			return err
		}
		bulk, err := readAllItemContentVersions(r, schema)
		if err != nil {
			return err
		}
		for _, id := range []string{"item-a", "item-b"} {
			single, err := readItemContentVersion(r, schema, id)
			if err != nil {
				return err
			}
			if single == "" || single != bulk[id] {
				t.Errorf("%s: bulk version %q != single version %q", id, bulk[id], single)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLoopHoldSurvivesRankOnlyPlaceAndDropsOnContentEdit(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()

	l.holdLocal(ctx, "item-a", ReasonItemUnstagleable)

	// Re-rank item-a via item.Place (updates rank and updated_at, not content).
	later := l.cfg.Now().Add(time.Minute)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Place(ctx, tx, "item-a", item.Destination{To: "last"}, later, "owner", "test")
		return err
	})
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		if reason, held := l.heldFor(r, "item-a"); !held || reason != ReasonItemUnstagleable {
			t.Fatalf("rank-only Place dropped loop hold: held=%v reason=%q", held, reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Substantive content edit drops the hold.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET title = 'fixed title' WHERE item_id = 'item-a'`)
		return err
	})
	s, err = store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		if reason, held := l.heldFor(r, "item-a"); held {
			t.Fatalf("content edit did not drop loop hold: reason=%q", reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
