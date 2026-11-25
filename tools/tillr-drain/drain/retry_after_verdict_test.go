package drain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
)

func TestRetryFaceReplacesAnAuthorizationAfterGateFailure(t *testing.T) {
	const (
		itemID    = "item_042"
		runID     = "run-item_042-20261109T000000Z"
		ackTime   = "2026-11-09T00:06:00Z"
		ackBy     = "second operator"
		ackReason = "read the failing verdict"
	)
	storePath := fixture(t,
		itemRecord(itemID, "in-queue"),
		runRecord(runID, itemID, "lane-9", "fixture", run.StateExited, exitCode(0), ""),
	)
	if code, output := authorizeRetry(t, storePath, itemID, runID,
		"first operator", "the run needs another attempt", "2026-11-09T00:04:00Z"); code != 0 {
		t.Fatalf("retry before the verdict exited %d: %s", code, output)
	}
	original := retryRows(t, storePath)[0]
	seedGateVerdict(t, storePath, runID)
	tickPath := tick(t, "2026-11-09T00:08:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
	if got := plan(t, storePath, tickPath)[0].Payload; got.Action != drain.ActionHold || got.Reason != drain.ReasonRetryJudged {
		t.Fatalf("before acknowledgment: got %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonRetryJudged)
	}

	if code, output := authorizeRetryWith(t, storePath, itemID, runID,
		ackBy, ackReason, ackTime, drain.RetryAckGateFailed); code != 0 {
		t.Fatalf("acknowledged retry after the verdict exited %d: %s", code, output)
	}
	// Planning reopens the store: the replacement must not depend on process state.
	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionRetry || got.RetryID != original.RetryID || got.SupersedesRunID != runID || got.RunRequest == nil {
		t.Fatalf("after acknowledgment: got %+v, want a retry of %q", got, runID)
	}

	if code, output := authorizeRetryWith(t, storePath, itemID, runID,
		"third operator", "a duplicate claim", "2026-11-09T00:07:00Z", drain.RetryAckGateFailed); code != 2 || !strings.Contains(string(output), "already authorized") {
		t.Fatalf("duplicate acknowledged retry exited %d: %s", code, output)
	}
	if rows := retryRows(t, storePath); len(rows) != 1 || rows[0].RetryID != original.RetryID ||
		rows[0].RequestedBy != ackBy || rows[0].Reason != ackReason || rows[0].RequestedAt.Format(time.RFC3339) != ackTime {
		t.Fatalf("stored retries = %+v, want only the acknowledged replacement", rows)
	}
}
