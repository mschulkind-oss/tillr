package main_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAnsweredConflictOutranksEarlierRetry(t *testing.T) {
	for _, status := range []string{"none", "answered", "closed"} {
		t.Run(status, func(t *testing.T) {
			const (
				itemID     = "item_042"
				older      = "run-item_042-a6"
				used       = "run-item_042-a7"
				fix        = "run-item_042-a7-cfix"
				designer   = "run-item_042-a9"
				conflictID = "conflict-" + fix
				at         = "2026-11-10T00:00:00Z"
			)
			// a7 consumed the retry for a6, but its conflict-fix run raised the
			// conflict. The answer must not depend on that raising run being a retry.
			path := fixture(t, itemRecordSeated(itemID, "in-queue", "build"),
				runRecord(older, itemID, "lane-8", "fixture", run.StateExited, exitCode(2), ""),
				runRecord(used, itemID, "lane-8", "fixture", run.StateExited, exitCode(0), ""),
				runRecord(fix, itemID, "lane-8", "fixture", run.StateExited, exitCode(0), ""),
			)
			seedRetry(t, path, itemID, older, "owner", "retry earlier failure", at)
			s, err := store.Open(context.Background(), path, true)
			if err != nil {
				t.Fatal(err)
			}
			err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
				payload, err := json.Marshal(drain.Decision{ItemID: itemID, Action: drain.ActionRetry, RetryID: "retry-" + itemID + "-" + older, SupersedesRunID: older})
				if err != nil {
					return err
				}
				usedID := used
				if _, err := tx.Append(event.Event{Kind: "drain.inputs", ObjectType: "run", ObjectID: &usedID, Payload: payload, At: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}); err != nil {
					return err
				}
				if status == "none" {
					return nil
				}
				return item.RecordConflict(tx, item.Conflict{ConflictID: conflictID, ItemID: itemID, RunID: fix, BodyRef: "docs/work.md#work", Text: "requires a design answer", Status: "raised", WaitingOn: "design"}, time.Date(2026, 11, 10, 0, 1, 0, 0, time.UTC))
			})
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			if status != "none" {
				seedRunRecord(t, path, runRecord(designer, itemID, "lane-8", "fixture", run.StateExited, exitCode(0), ""))
				s, err = store.Open(context.Background(), path, true)
				if err != nil {
					t.Fatal(err)
				}
				err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
					return item.RecordConflict(tx, item.Conflict{ConflictID: conflictID, ItemID: itemID, RunID: fix, BodyRef: "docs/work.md#work", Text: "requires a design answer", Status: status, WaitingOn: "build", DesignRunID: designer, AnswerHead: "landed-answer"}, time.Date(2026, 11, 10, 0, 2, 0, 0, time.UTC))
				})
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			ref := seatPolicy(t, map[string]string{"build": "fixture", "design": "designer"}, nil)
			p, err := readPolicyForWhyTest(ref)
			if err != nil {
				t.Fatal(err)
			}
			decisions, _ := transitionPlan(t, path, "2026-11-10T00:03:00Z", []string{"lane-1"}, 1, p)
			got := decisions[0]
			if status != "answered" {
				if got.Action != drain.ActionHold || got.Reason != drain.ReasonRetryStale || got.SupersedesRunID != older {
					t.Fatalf("stale authorization dispatched without an unconsumed answer: %+v", got)
				}
				return
			}
			if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonConflictAnswered || got.ConflictID != conflictID || got.RetryID != "" || got.Seat != "build" || got.RunRequest == nil || got.RunRequest.Backend != "fixture" {
				t.Fatalf("answer did not dispatch original build: %+v", got)
			}
			code, out := invoke(t, "why", "--store", path, "--policy", ref, "--lanes", "lane-1", "--now", "2026-11-10T00:03:00Z", "--json", itemID)
			if code != 0 {
				t.Fatalf("why: %d %s", code, out)
			}
			var row drain.WhyRow
			if err := json.Unmarshal(out, &row); err != nil {
				t.Fatal(err)
			}
			if row.Reason != drain.ReasonConflictAnswered || row.Decision.ConflictID != conflictID || !strings.Contains(string(out), conflictID) {
				t.Fatalf("why lost answered conflict: %s", out)
			}
		})
	}
}
