package main_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/spend"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRetryWithoutNewModelOutputAddsNoWindowedCost(t *testing.T) {
	f := newLoopFixture(t, stub(t, "retry-without-output", counting+`exit 124`))
	const original = "interrupted-original"
	fixtureAt(t, f.store,
		itemRecordWithFence("item_a", "in-queue", "candidate.txt"),
		runRecord(original, "item_a", "lane-1", "fixture", run.StateExited, exitCode(124), ""),
	)
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	now := time.Now().UTC()
	steps.Now = func() time.Time { return now }
	s, err := store.Open(context.Background(), f.store, false)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE runs SET usage_cost_usd=41.2,started_at=?,finished_at=? WHERE run_id=?`, now.Add(-time.Hour).UnixMilli(), now.Add(-time.Minute).UnixMilli(), original)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if code, out := authorizeRetryWith(t, f.store, "item_a", original, "the owner", "retry the interrupted attempt", now.Format(time.RFC3339), drain.RetryAckUnjudged); code != 0 {
		t.Fatalf("authorize retry: %d %s", code, out)
	}
	result, err := f.newLoop(t, 1, steps).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 1 {
		t.Fatalf("retry did not dispatch: %+v", result)
	}
	got, err := spend.Read(context.Background(), f.store, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.CostUSD != 41.2 || got.Runs != 2 || got.Missing != 1 {
		t.Fatalf("retry inherited interrupted cost: %+v", got)
	}
}

func TestLoopStartupMeasuresLast24HoursWithoutRestrictingDispatch(t *testing.T) {
	f := newLoopFixture(t, stub(t, "window-run", committingAdapter))
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	now := steps.Now()
	s, err := store.Open(context.Background(), f.store, false)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES('lane-1','free')`); err != nil {
			return err
		}
		for _, row := range []struct {
			id   string
			at   time.Time
			cost any
		}{
			{"recent", now.Add(-time.Hour), 1000000000.0},
			{"old", now.Add(-25 * time.Hour), 7.0},
			{"missing", now.Add(-2 * time.Hour), nil},
		} {
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_cost_usd) VALUES(?, NULL, 'lane-1', 'fixture', 'exited', ?, ?, ?)`, row.id, row.at.UnixMilli(), row.at.UnixMilli(), row.cost); err != nil {
				return err
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	var report strings.Builder
	result, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 1 || result.Landed != 1 {
		t.Fatalf("recorded spend restricted dispatch: %+v", result)
	}
	want := "spend, last 24h: $1000000000.00 over 2 runs; 1 run reported no cost"
	if !strings.Contains(report.String(), want) {
		t.Fatalf("startup missing %q:\n%s", want, report.String())
	}
	// A fresh attempt that reports no cost must not acquire historical cost.
	if result.StartedSpend.CostUSD != 0 || result.StartedSpend.CostUnreported != 1 {
		t.Fatalf("new run inherited earlier cost: %+v", result.StartedSpend)
	}
}
