package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// retryDesignRun calls the retry face for item-a and runID, and answers the
// authorization or the refusal.
func retryDesignRun(t *testing.T, f refusalLoopFixture, l *Loop, runID string) (Retry, error) {
	t.Helper()
	s, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var retry Retry
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		retry, err = AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: "item-a", RunID: runID, By: "the owner", Why: "the trunk is fixed now",
			At: l.cfg.Now().Add(time.Minute),
		})
		return err
	})
	return retry, err
}

// designRetries answers every drain.retry.design payload, oldest first.
func designRetries(t *testing.T, f refusalLoopFixture) []Retry {
	t.Helper()
	out := []Retry{}
	fixtureRead(t, f, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, DesignRetryKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var retry Retry
			if err := json.Unmarshal(raw, &retry); err != nil {
				return err
			}
			out = append(out, retry)
		}
		return rows.Err()
	})
	return out
}

// On 2026-11-17 eight items held as conflict-owner, each naming a design run that ended
// without answering, and no command could raise the conflict again: `drain retry` only
// supersedes a run with a new attempt of the same work. Naming the item and the design run
// whose objection the owner holds raises the conflict for one more design run, records who
// asked and why, and writes no retries row a plan would read as a build retry.
func TestARetryNamingTheUnansweredDesignRunRaisesItsConflictOnceMore(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, RefusalNoOp)
	objectToOwner(t, f, l, design)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" || c.DesignRunID != design {
		t.Fatalf("fixture: the conflict is not with the owner: %+v", c)
	}
	retry, err := retryDesignRun(t, f, l, design)
	if err != nil {
		t.Fatalf("the design retry was refused: %v", err)
	}
	c, _ := conflictOf(t, f, "item-a")
	if retry.ReraisesConflict != c.ConflictID || retry.SupersedesRunID != design || retry.RequestedBy != "the owner" {
		t.Fatalf("the authorization does not name the conflict and design run: %+v", retry)
	}
	if c.Status != "raised" || c.WaitingOn != "design" || c.DesignRunID != "" || !strings.Contains(c.Text, "the owner raised the conflict again after design run "+design) || !strings.Contains(c.Text, "the trunk is fixed now") {
		t.Fatalf("the conflict was not raised again naming who and why: %+v", c)
	}
	if got := designRetries(t, f); len(got) != 1 || got[0].SupersedesRunID != design || got[0].Reason != "the trunk is fixed now" {
		t.Fatalf("the re-raise is not recorded as one %s event: %+v", DesignRetryKind, got)
	}
	var rows int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM retries`).Scan(&rows)
	})
	if rows != 0 {
		t.Fatalf("the design retry wrote %d retries row(s); the plan would read them as a build retry", rows)
	}
	if p := decisionFor(t, l, "item-a"); p.decision.Action != ActionDispatch || p.decision.BlockConflict == nil {
		t.Fatalf("the design seat is not dispatched after the re-raise: %+v", p.decision)
	}
	// The conflict is now raised: naming the same run again is refused, and says the loop moves.
	if _, err := retryDesignRun(t, f, l, design); err == nil || !strings.Contains(err.Error(), "No retry command is accepted") || !strings.Contains(err.Error(), "the loop moves") {
		t.Fatalf("a second retry of a raised conflict was not refused naming the loop: %v", err)
	}
}

// A design run of an earlier round is stale: the refusal names the conflict's current
// design run and the exact command that would be accepted.
func TestADesignRetryNamingAStaleDesignRunNamesTheCurrentOne(t *testing.T) {
	f, l := designConflictFixture(t)
	first := endDesignRun(t, f, l, RefusalNoOp)
	objectToOwner(t, f, l, first)
	if _, err := retryDesignRun(t, f, l, first); err != nil {
		t.Fatalf("the first design retry was refused: %v", err)
	}
	second := endDesignRun(t, f, l, RefusalNoOp)
	objectToOwner(t, f, l, second)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" || c.DesignRunID != second {
		t.Fatalf("fixture: the second design run did not reach the owner: %+v", c)
	}
	_, err := retryDesignRun(t, f, l, first)
	want := "tillr drain retry --item item-a --run " + second + " --by <who> --why <reason>"
	if err == nil || !strings.Contains(err.Error(), second) || !strings.Contains(err.Error(), want) {
		t.Fatalf("the stale design run was not refused naming %q: %v", want, err)
	}
	if got := designRetries(t, f); len(got) != 1 {
		t.Fatalf("a refused retry recorded an event: %+v", got)
	}
}

// A design run whose end the loop has not yet settled is the loop's to move: the face
// refuses it and says so, rather than racing the settlement.
func TestADesignRetryOfAnUnsettledDesignRunIsTheLoops(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, RefusalGate)
	_, err := retryDesignRun(t, f, l, design)
	if err == nil || !strings.Contains(err.Error(), "No retry command is accepted") || !strings.Contains(err.Error(), "the loop moves") {
		t.Fatalf("an unsettled design run was not refused naming the loop: %v", err)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "awaiting" {
		t.Fatalf("the refused retry moved the conflict: %+v", c)
	}
}
