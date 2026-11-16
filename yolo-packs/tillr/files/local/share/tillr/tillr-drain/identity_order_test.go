package main_test

import (
	"fmt"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
)

func TestFixedClockRetriesFollowDoubleDigitAttemptOrder(t *testing.T) {
	records := []string{itemRecord("item_041", "in-queue")}
	for i := 1; i <= 9; i++ {
		records = append(records, runRecord(fmt.Sprintf("run-item_041-a%d", i), "item_041", "lane-9", "first", run.StateFailedToStart, nil, ""))
	}
	path := fixture(t, records...)
	at := "2026-11-10T00:00:00Z"
	seedRetry(t, path, "item_041", "run-item_041-a9", "the owner", "retry nine", at)
	first, _ := transitionPlan(t, path, at, []string{"lane-1"}, 1, transitionPolicy())
	if first[0].Action != drain.ActionRetry || first[0].RunRequest.RunID != "run-item_041-a10" {
		t.Fatalf("attempt ten: %+v", first[0])
	}
	fixtureAt(t, path, runRecord("run-item_041-a10", "item_041", "lane-9", "first", run.StateFailedToStart, nil, ""))
	consumed, _ := transitionPlan(t, path, at, []string{"lane-1"}, 1, transitionPolicy())
	if consumed[0].Action != drain.ActionHold || consumed[0].Reason != drain.ReasonRetryStale {
		t.Fatalf("authorization nine was not consumed: %+v", consumed[0])
	}
	seedRetry(t, path, "item_041", "run-item_041-a10", "the owner", "retry ten", at)
	next, _ := transitionPlan(t, path, at, []string{"lane-1"}, 1, transitionPolicy())
	if next[0].Action != drain.ActionRetry || next[0].RunRequest.RunID != "run-item_041-a11" {
		t.Fatalf("latest tied authorization: %+v", next[0])
	}
	fixtureAt(t, path, runRecord("run-item_041-a11", "item_041", "lane-9", "first", run.StateFailedToStart, nil, ""))
	final, _ := transitionPlan(t, path, at, []string{"lane-1"}, 1, transitionPolicy())
	if final[0].Action != drain.ActionHold || final[0].Reason != drain.ReasonRetryStale {
		t.Fatalf("authorization ten was not consumed: %+v", final[0])
	}
}
