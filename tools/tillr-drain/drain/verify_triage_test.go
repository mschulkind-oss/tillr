package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestVerifyTriageRecoversRecordedRefusalsBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		refusals   int
		laterState string
		ack        string
		conflict   string
	}{
		{"three refusals", 3, "", "", ""},
		{"four refusals", 4, "", "", ""},
		{"later non-verifier run", 3, run.StateExited, "", ""},
		{"later timed-out run", 3, run.StateTimedOut, RetryAckUnjudged, ""},
		{"legacy raised repeat", 3, "", "", "raised"},
		{"legacy owner repeat", 3, "", "", "owner"},
		{"legacy repeat then separate conflict", 3, "", "", "replaced"},
		{"unrelated blocked conflict", 3, "", "", "unrelated"},
		{"later answered design", 3, run.StateExited, "", "answered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
				return &verifyReturnSteps{workflowSteps: w}
			})
			ctx := context.Background()
			first := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
			var want []string
			var last string
			fixtureWrite(t, f, func(tx *store.Tx) error {
				for i := 1; i <= tc.refusals; i++ {
					at := first.Add(time.Duration(i) * time.Hour)
					last = fmt.Sprintf("run-item-a-a%d", i)
					if err := applyTerminal(tx, run.Record{RunID: last, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
						State: run.StateExited, ExitCode: countPtr(0), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339)}); err != nil {
						return err
					}
					finding := fmt.Sprintf("c%d: finding %d", i, i)
					payload := refusalPayload{ItemID: "item-a", Step: RefusalVerifyNotDone, Detail: finding + "\nsecond finding"}
					if err := appendEffect(tx, RefusalKind, last, payload, at); err != nil {
						return err
					}
					if i <= 3 {
						want = append(want, at.Format(time.RFC3339)+": "+finding)
					}
					if i == 1 { // A replay of the same run is not another refusal.
						if err := appendEffect(tx, RefusalKind, last, payload, at); err != nil {
							return err
						}
					}
				}
				var conflict item.Conflict
				if tc.conflict != "" {
					conflict = item.Conflict{ConflictID: "conflict-" + last, ItemID: "item-a", RunID: last,
						Text:   fmt.Sprintf("%s run %s ended (refused at %s) with the same evidence", IdenticalRepeatPrefix, last, RefusalVerifyNotDone),
						Status: "raised", WaitingOn: "design"}
					if tc.conflict == "owner" || tc.conflict == "unrelated" {
						conflict.Status, conflict.WaitingOn = "owner", "owner"
					}
					if tc.conflict == "unrelated" {
						conflict.Text = "A separate owner decision is still needed."
					}
					if err := item.RecordConflict(tx, conflict, first.Add(4*time.Hour)); err != nil {
						return err
					}
				}
				if tc.laterState != "" {
					at := first.Add(24 * time.Hour).Format(time.RFC3339)
					last = "run-item-a-a4"
					var exitCode *int
					if tc.laterState == run.StateExited {
						exitCode = countPtr(1)
					}
					if tc.conflict == "answered" {
						exitCode = countPtr(0)
					}
					if err := applyTerminal(tx, run.Record{RunID: last, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
						State: tc.laterState, ExitCode: exitCode, StartedAt: at, FinishedAt: at}); err != nil {
						return err
					}
					if tc.conflict == "answered" {
						conflict.Status, conflict.WaitingOn, conflict.DesignRunID = "answered", "worker", last
						return item.RecordConflict(tx, conflict, first.Add(25*time.Hour))
					}
				}
				return nil
			})
			stateAndRank := func() [2]string {
				var pair [2]string
				fixtureRead(t, f, func(r *store.Reader) error {
					return r.QueryRow(`SELECT state, rank FROM items WHERE item_id='item-a'`).Scan(&pair[0], &pair[1])
				})
				return pair
			}
			before := stateAndRank()
			result := &LoopResult{}
			graphPass(t, l, result)
			if got := stateAndRank(); got != before {
				t.Fatalf("triage changed state/rank: %v -> %v", before, got)
			}
			fault, held := faultOf(t, f, "item-a")
			if tc.conflict == "unrelated" {
				if held || len(s.runs) != 0 {
					t.Fatalf("recovery replaced an unrelated conflict: hold=%+v, runs=%v", fault, s.runs)
				}
				return
			}
			cmd := retryCommand("item-a", last, tc.ack)
			if fault.Reason != reasonVerifyTriage || fault.RunID != last || len(s.runs) != 0 ||
				!strings.Contains(fault.ClearCommand(f.storePath), "operator; "+cmd) ||
				!strings.HasPrefix(fault.Detail, "Three verifier refusals: "+strings.Join(want, "; ")+".") ||
				!strings.Contains(fault.Detail, "The operator moves: "+cmd) {
				t.Fatalf("upgrade hold=%+v, plans=%d, verifies=%d; want %v and %q before dispatch", fault, s.plans, s.verifies, want, cmd)
			}
			graphPass(t, l, result)
			if got, _ := faultOf(t, f, "item-a"); got != fault {
				t.Fatalf("unchanged history rewrote the hold: %+v -> %+v", fault, got)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if tc.conflict == "replaced" {
					if err := item.RecordConflict(tx, item.Conflict{ConflictID: "new-conflict", ItemID: "item-a", RunID: last,
						Text: "Another owner decision is needed.", Status: "owner", WaitingOn: "owner"}, l.cfg.Now()); err != nil {
						return err
					}
				}
				_, err := AuthorizeRetry(ctx, tx, RetryRequest{ItemID: "item-a", RunID: last,
					By: "operator", Why: "reviewed the historical findings", Acknowledge: tc.ack, At: l.cfg.Now()})
				return err
			})
			fixtureRead(t, f, func(r *store.Reader) error {
				findings, err := readVerifyTriage(r, "item-a")
				if err != nil {
					return err
				}
				if len(findings) != 0 {
					t.Fatalf("retry did not reset the cycle: %v", findings)
				}
				if tc.conflict == "" {
					return nil
				}
				conflicts, err := item.ReadConflicts(r)
				want := "closed"
				if tc.conflict == "answered" {
					want = "answered"
				} else if tc.conflict == "replaced" {
					want = "owner"
				}
				if err == nil && conflicts["item-a"].Status != want {
					t.Fatalf("retry left conflict %+v, want %s", conflicts["item-a"], want)
				}
				return err
			})
			if count := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.step')=?`, RefusalKind, RefusalVerifyNotDone); count != tc.refusals+1 {
				t.Fatalf("retry lost refusal history: got %d rows", count)
			}
			if tc.conflict == "replaced" {
				if got, held := faultOf(t, f, "item-a"); held || stateAndRank() != before {
					t.Fatalf("retry did not leave the separate conflict blocked: hold=%+v state/rank=%v", got, stateAndRank())
				}
				return
			}
			for i := 0; i < 12 && s.plans == 0; i++ {
				graphPass(t, l, result)
			}
			if got, held := faultOf(t, f, "item-a"); held || s.plans != 1 {
				t.Fatalf("retry did not resume dispatch: hold=%+v plans=%d", got, s.plans)
			}
		})
	}
}

func TestVerifyTriageLegacyBuildRetryAfterDesignRetry(t *testing.T) {
	t.Run("verifier refusal", func(t *testing.T) { testVerifyTriageLegacyBuildRetry(t, false) })
	t.Run("non-verifier terminal run", func(t *testing.T) { testVerifyTriageLegacyBuildRetry(t, true) })
}

func testVerifyTriageLegacyBuildRetry(t *testing.T, retryNonVerifier bool) {
	t.Helper()
	l, _, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w}
	})
	ctx := context.Background()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return at }
	fixtureWrite(t, f, func(tx *store.Tx) error {
		id := "design-run"
		_, err := tx.Append(event.Event{Kind: DesignRetryKind, ObjectType: "run", ObjectID: &id,
			Payload: []byte(`{"item_id":"item-a"}`), At: at})
		return err
	})
	refuse := func(id, detail string) {
		t.Helper()
		c := Candidate{ItemID: "item-a", RunID: id, VerifyReplan: true, RefusalDetail: detail}
		if err := l.recordRefusal(ctx, c, RefusalVerifyNotDone); err != nil {
			t.Fatal(err)
		}
	}
	at = at.Add(time.Hour)
	refuse("old-1", "c1: earlier cycle")
	refuse("old-2", "c2: superseded finding")
	// Pre-upgrade builds wrote only the retry row. Its timestamp may tie with
	// the two earlier refusals, the run it supersedes and the first new refusal.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		supersedes := "old-2"
		if retryNonVerifier {
			supersedes = "interrupted-build"
			payload, err := json.Marshal(run.Record{
				RunID: supersedes, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited,
				ExitCode: countPtr(1), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
			})
			if err != nil {
				return err
			}
			if _, err := run.Apply(ctx, tx, record.Envelope{RecordVersion: 1, Kind: run.RecordKind, ID: supersedes, CreatedAt: at, Payload: payload}); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO retries(retry_id,item_id,supersedes_run_id,requested_by,reason,requested_at)
 VALUES('legacy-retry','item-a',?,'operator','reviewed the findings',?)`, supersedes, at.UnixMilli())
		return err
	})
	var want []string
	for i, finding := range []string{"changed input", "stale receipt", "missing check"} {
		first := "c2: " + finding
		refuse(fmt.Sprintf("new-%d", i+1), first+"\nnot the first finding")
		want = append(want, at.Format(time.RFC3339)+": "+first)
		fixtureRead(t, f, func(r *store.Reader) error {
			holds, err := ReadItemFaults(r)
			if err != nil {
				return err
			}
			fault := holds["item-a"]
			if i < 2 {
				if fault.Reason != "" {
					t.Fatalf("early hold after %d current-cycle refusals: %+v", i+1, fault)
				}
			} else if fault.Reason != reasonVerifyTriage || !strings.HasPrefix(fault.Detail, "Three verifier refusals: "+strings.Join(want, "; ")+".") {
				t.Fatalf("hold = %+v, want only %v", fault, want)
			}
			return nil
		})
		at = at.Add(time.Hour)
	}
}

func TestVerifyTriageCountsOnlyCurrentCycle(t *testing.T) {
	l, _, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w}
	})
	ctx := context.Background()
	at := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return at }
	var beforeState, beforeRank string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state, rank FROM items WHERE item_id='item-a'`).Scan(&beforeState, &beforeRank)
	})
	refuse := func(id, step, detail string) {
		t.Helper()
		c := Candidate{ItemID: "item-a", RunID: id, VerifyReplan: true, RefusalDetail: detail}
		if err := l.recordRefusal(ctx, c, step); err != nil {
			t.Fatal(err)
		}
	}
	readHold := func() ItemFault {
		t.Helper()
		var fault ItemFault
		fixtureRead(t, f, func(r *store.Reader) error {
			holds, err := ReadItemFaults(r)
			fault = holds["item-a"]
			return err
		})
		return fault
	}
	refuse("old-verify", RefusalVerifyNotDone, "c2: old finding")
	// A retry before a triage hold must reset the count, even if the run it
	// supersedes was not a verifier refusal and the clock has not advanced.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{RunID: "interrupted-build", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(1), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339)}); err != nil {
			return err
		}
		_, err := AuthorizeRetry(ctx, tx, RetryRequest{ItemID: "item-a", RunID: "interrupted-build", By: "operator", Why: "retry the failed build", At: at})
		return err
	})
	first := at
	refuse("verify-1", RefusalVerifyNotDone, "c1: first finding\nsecond line")
	refuse("verify-1", RefusalVerifyNotDone, "c1: replay of same run")
	refuse("fence", RefusalFence, "not a verifier refusal")
	at = at.Add(time.Hour)
	refuse("verify-2", RefusalVerifyNotDone, "c2: different finding")
	if fault := readHold(); fault.Reason != "" {
		t.Fatalf("early hold: %+v", fault)
	}
	at = at.Add(time.Hour)
	refuse("verify-3", RefusalVerifyNotDone, "c3: another finding")
	fault := readHold()
	want := fmt.Sprintf("Three verifier refusals: %s: c1: first finding; %s: c2: different finding; %s: c3: another finding.",
		first.Format(time.RFC3339), first.Add(time.Hour).Format(time.RFC3339), at.Format(time.RFC3339))
	if fault.Reason != reasonVerifyTriage || !strings.HasPrefix(fault.Detail, want) {
		t.Fatalf("hold = %+v, want %q", fault, want)
	}
	if mover := fault.ClearCommand(f.storePath); !strings.Contains(mover, "operator; tillr drain retry --item item-a --run verify-3") || strings.Contains(mover, "clear-fault") {
		t.Fatalf("waiting mover is not the operator retry: %s", mover)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var state, rank string
		if err := r.QueryRow(`SELECT state, rank FROM items WHERE item_id='item-a'`).Scan(&state, &rank); err != nil {
			return err
		}
		if state != beforeState || rank != beforeRank {
			t.Fatalf("hold changed state/rank from %s/%s to %s/%s", beforeState, beforeRank, state, rank)
		}
		return nil
	})
}
