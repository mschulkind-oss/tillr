package main_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestFinishExistingLanding(t *testing.T) {
	for _, tc := range []struct {
		name, from, via, rule    string
		landing, reverted, graph bool
	}{
		{name: "blocked with follow-up", from: "blocked", landing: true, via: "item:follow-up"},
		{name: "queued with commit", from: "in-queue", landing: true, via: "commit:abcd1234"},
		{name: "no landing", from: "in-queue", via: "commit:abcd1234", rule: "landed-receipt-missing"},
		{name: "graph only", from: "in-queue", graph: true, via: "commit:abcd1234", rule: "landed-receipt-missing"},
		{name: "reverted", from: "blocked", landing: true, reverted: true, via: "commit:abcd1234", rule: "landed-receipt-missing"},
		{name: "missing pointer", from: "in-queue", landing: true, rule: "resolved-pointer-missing"},
		{name: "unknown follow-up", from: "in-queue", landing: true, via: "item:missing", rule: "pointer-unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const id = "item_042"
			db := storedWithCriterion(t, "finish-existing-landing", "open")
			if code, out := apply(t, db, envelope(t, "item.record", "follow-up", base("follow-up", "remaining work"))); code != 0 {
				t.Fatalf("follow-up: %d %s", code, out)
			}
			var block []string
			if tc.from == "blocked" {
				block = []string{"--waiter", "owner", "--reason", "unanswered work"}
			}
			if code, out := findingState(t, db, id, tc.from, block...); code != 0 {
				t.Fatalf("setup state: %d %s", code, out)
			}
			if tc.landing {
				seedExistingLanding(t, db, tc.reverted)
			}
			if tc.graph {
				addTerminalEvidence(t, db, id, "drain.graph-completed")
			}
			before := existingLandingHistory(t, db, tc.landing)
			updates := eventsOfKind(t, db, "item.update")
			code, out := findingState(t, db, id, "landed", "--resolves-via", "commit:abcd1234")
			assertRefused(t, code, out, "landed-criteria-unmet")
			if tc.landing && !tc.reverted {
				code, out = lifecycle(t, db, "close", "superseded")
				assertRefused(t, code, out, "already-landed")
			}
			flags := []string{"--finish-existing-landing", "--reason", "remaining work covered", "--by", "operator"}
			if tc.via != "" {
				flags = append(flags, "--resolves-via", tc.via)
			}
			code, out = findingState(t, db, id, "landed", flags...)
			if tc.rule != "" {
				assertRefused(t, code, out, tc.rule)
				if got := showItem(t, db, id).Payload.State; got != tc.from || eventsOfKind(t, db, "item.update") != updates {
					t.Fatalf("refusal mutated item: state %s", got)
				}
			} else {
				if code != 0 {
					t.Fatalf("finish: %d %s", code, out)
				}
				stored := showItem(t, db, id)
				if stored.Payload.State != "landed" || criterionOf(t, stored, "c1").State != "open" || eventsOfKind(t, db, "item.update") != updates+1 {
					t.Fatalf("expected one state move, without judging the criterion: %+v", stored.Payload)
				}
				ctx := context.Background()
				s, err := store.OpenRead(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				err = s.ReadTx(ctx, func(r *store.Reader) error {
					var reason, by, via string
					if err := r.QueryRow(`SELECT lifecycle_reason, lifecycle_by, resolves_via FROM items WHERE item_id=?`, id).Scan(&reason, &by, &via); err != nil {
						return err
					}
					if reason != "remaining work covered" || by != "operator" || via != tc.via {
						t.Errorf("stored receipt: %q %q %q", reason, by, via)
					}
					var action, eventReason, eventBy, eventVia string
					if err := r.QueryRow(`SELECT json_extract(payload,'$.lifecycle_action'), json_extract(payload,'$.lifecycle_reason'),
						json_extract(payload,'$.lifecycle_by'), json_extract(payload,'$.resolves_via') FROM events
						WHERE kind='item.update' AND object_id=? ORDER BY seq DESC LIMIT 1`, id).Scan(&action, &eventReason, &eventBy, &eventVia); err != nil {
						return err
					}
					if action != "finish-existing-landing" || eventReason != reason || eventBy != by || eventVia != via {
						t.Errorf("audit event: %q %q %q %q", action, eventReason, eventBy, eventVia)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if code, out := findingState(t, db, id, "landed", flags...); code != 0 || eventsOfKind(t, db, "item.update") != updates+1 {
					t.Fatalf("retry should leave the receipt alone: %d %s", code, out)
				}
			}
			if after := existingLandingHistory(t, db, tc.landing); !slices.Equal(before, after) {
				t.Fatalf("landing history changed: before %q, after %q", before, after)
			}
		})
	}
}

func TestFinishExistingLandingRequiresExplicitEvidence(t *testing.T) {
	db := storedWithCriterion(t, "finish-existing-landing-flags", "open")
	seedExistingLanding(t, db, false)
	for _, tc := range []struct {
		name, target, reason, by, via string
		extra                         []string
	}{
		{name: "wrong target", target: "in-queue", reason: "covered", by: "operator", via: "commit:abcd"},
		{name: "missing reason", target: "landed", reason: " ", by: "operator", via: "commit:abcd"},
		{name: "missing actor", target: "landed", reason: "covered", via: "commit:abcd"},
		{name: "document pointer", target: "landed", reason: "covered", by: "operator", via: "doc:proof.md"},
		{name: "contradictory verdict check", target: "landed", reason: "covered", by: "operator", via: "commit:abcd", extra: []string{"--require-verdicts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := []string{"--finish-existing-landing", "--reason", tc.reason, "--by", tc.by, "--resolves-via", tc.via}
			if code, out := findingState(t, db, "item_042", tc.target, append(flags, tc.extra...)...); code != 2 {
				t.Fatalf("wanted usage refusal: %d %s", code, out)
			}
		})
	}
	if got := showItem(t, db, "item_042").Payload.State; got != "not-started" {
		t.Fatalf("refusal mutated item: %s", got)
	}
}

func seedExistingLanding(t *testing.T, db string, reverted bool) {
	t.Helper()
	_ = event.Register("drain.revert", "tillr-drain")
	ctx := context.Background()
	s, err := store.Open(ctx, db, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE landings (landing_id TEXT, item_id TEXT, run_id TEXT, result TEXT, trunk_after TEXT, event_seq INTEGER)`); err != nil {
			return err
		}
		commit, at := "abcd1234", time.Now()
		seq, err := tx.Append(event.Event{Kind: "landing.recorded", ObjectType: "landing", ObjectID: &commit, At: at,
			Payload: []byte(`{"item_id":"item_042","run_id":"run-1","result":"landed","trunk_after":"abcd1234"}`)})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings VALUES ('landing-1','item_042','run-1','landed',?,?)`, commit, seq); err != nil {
			return err
		}
		if reverted {
			_, err = tx.Append(event.Event{Kind: "drain.revert", ObjectType: "landing", ObjectID: &commit, At: at,
				Payload: []byte(`{"item_id":"item_042","run_id":"run-1","result":"reverted","landing":"abcd1234"}`)})
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func existingLandingHistory(t *testing.T, db string, hasLanding bool) []string {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var history []string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		if hasLanding {
			var allRows string
			if err := r.QueryRow(`SELECT json_group_array(json_array(landing_id,item_id,run_id,result,trunk_after,event_seq) ORDER BY landing_id) FROM landings`).Scan(&allRows); err != nil {
				return err
			}
			history = append(history, allRows)
		}
		rows, err := r.Query(`SELECT json_array(seq,kind,object_type,object_id,payload,ts) FROM events WHERE object_type='landing' ORDER BY seq`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				return err
			}
			history = append(history, row)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return history
}
