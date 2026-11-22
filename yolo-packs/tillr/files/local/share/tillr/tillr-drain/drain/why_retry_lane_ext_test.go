package drain_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWhyCommandOperatorRetryWaitingForLanes(t *testing.T) {
	path := fixture(t, itemRecordRanked("retry-item", "in-queue", "a"), itemRecordRanked("work-holder", "in-queue", "b"), runRecord("retry-old-run", "retry-item", "lane-1", "fixture", run.StateExited, exitCode(0), ""))
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET express=1 WHERE item_id='retry-item'; INSERT OR IGNORE INTO lanes(lane_id,state) VALUES ('lane-1','idle'),('express-1','idle')`); err != nil {
			return err
		}
		if _, err := item.SetState(context.Background(), tx, "retry-item", drain.StateBlocked, at, item.WaitingOn{Waiter: "owner", Reason: "obstacle"}); err != nil {
			return err
		}
		if _, err := drain.AuthorizeRetry(context.Background(), tx, drain.RetryRequest{ItemID: "retry-item", RunID: "retry-old-run", By: "operator", Why: "fence widened", At: at.Add(time.Second)}); err != nil {
			return err
		}
		d := drain.Decision{ItemID: "work-holder", LaneID: "lane-1", Action: drain.ActionDispatch, Reason: drain.ReasonReady, Attempt: 1, From: "queued", To: "running", RunRequest: &run.Request{ItemID: "work-holder", RunID: "work-holder-run", LaneID: "lane-1", Backend: "fixture"}}
		raw, err := json.Marshal(map[string]any{"decision": d})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (0,'drain.started','run','work-holder-run',?)`, string(raw)); err != nil {
			return err
		}
		_, err = run.ClaimLease(context.Background(), tx, run.Request{RunID: "lease-holder-run", ItemID: "lease-holder", Backend: "fixture"}, "express-1", at)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	common := []string{"why", "--store", path, "--policy", policy(t, "true"), "--lanes", "lane-1,express-1", "--now", at.Format(time.RFC3339)}
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			args := append([]string(nil), common...)
			if format == "json" {
				args = append(args, "--json")
			}
			code, out := invoke(t, args...)
			if code != 0 {
				t.Fatalf("why: %d %s", code, out)
			}
			var text string
			if format == "json" {
				var answer drain.WhyAnswer
				if err := json.Unmarshal(out, &answer); err != nil {
					t.Fatal(err)
				}
				for _, row := range answer.Rows {
					if row.ItemID == "retry-item" {
						text = row.Reason + " " + row.Why + " " + row.Mover
						if row.Mover == "the next pass" {
							t.Error("mover is the next pass alone")
						}
					}
				}
			} else {
				for _, line := range strings.Split(string(out), "\n") {
					if strings.Contains(line, "\tretry-item\t") {
						text = line
					}
				}
			}
			if text == "" {
				t.Fatalf("retry row missing: %s", out)
			}
			lower := strings.ToLower(text)
			for _, want := range []string{"operator retry", "waiting", "lane-1", "work-holder", "work-holder-run", "express-1", "lease-holder-run"} {
				if !strings.Contains(lower, want) {
					t.Errorf("%s retry row %q does not name %q", format, text, want)
				}
			}
			if strings.Contains(lower, "harness-refused") {
				t.Errorf("%s retry row reports harness refusal: %q", format, text)
			}
		})
	}
}
