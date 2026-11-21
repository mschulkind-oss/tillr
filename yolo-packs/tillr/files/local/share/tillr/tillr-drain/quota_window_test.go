package main_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestPlanHoldsWhenAllSeatWindowsAreShut(t *testing.T) {
	path := fixture(t, itemRecordSeated("item_042", "in-queue", "design"))
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reset := time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC)
	for _, name := range []string{"designer-a", "designer-b"} {
		if _, err := db.Exec(`INSERT INTO backend_windows VALUES (?, 'five_hour', 1, 'rejected', ?, 'refused')`, name, reset.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	policy := listedSeatPolicy(t)
	got := decisionFor(t, plan(t, path, tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy)), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendWindow || got.RunRequest != nil {
		t.Fatalf("shut seat: %+v", got)
	}
	got = decisionFor(t, plan(t, path, tick(t, "2026-11-10T01:00:00Z", []string{"lane-1"}, 1, policy)), "item_042")
	if got.RunRequest == nil || got.RunRequest.Backend != "designer-a" {
		t.Fatalf("reopened seat: %+v", got)
	}
}

func TestLoopSkipsSpentWindowAcrossRestartAndReopens(t *testing.T) {
	refused := stub(t, "spent", counting+"exit 1\n")
	fallback := stub(t, "fallback", committingAdapter)
	f := newLoopFixture(t, refused)
	f.policy.Preference = []string{"fixture", "fallback"}
	f.policy.SeatLists = map[string][]string{"build": {"fixture", "fallback"}}
	f.policy.Backends["fallback"] = drain.Backend{Adapter: fallback, Model: "floor"}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	now := steps.Now()
	reset := now.Add(time.Hour)
	steps.BackendsDir = spendBackends(t, refused, "# run report\ntokens: unknown\ncost usd: unknown\nrate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: "+strconv.FormatInt(reset.Unix(), 10)+"\n---\n"+strings.Repeat("x", 70000))
	fixtureAt(t, f.store, itemRecordSeated("item_051", "in-queue", "build"), itemRecordSeated("item_052", "in-queue", "build"))
	runLoop := func() drain.LoopResult {
		t.Helper()
		steps.Now = func() time.Time { return now }
		result, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, io.Discard).Run(context.Background())
		if err != nil || result.Stop != drain.StopQueueDrained {
			t.Fatalf("loop: %+v / %v", result, err)
		}
		return result
	}
	runLoop()
	first := runsForItem(t, f.store, "item_051")
	second := runsForItem(t, f.store, "item_052")
	if len(first) != 2 || first[0].Backend != "fixture" || first[1].Backend != "fallback" {
		t.Fatalf("first: %+v", first)
	}
	if len(second) != 1 || second[0].Backend != "fallback" {
		t.Fatalf("shut backend was started: %+v", second)
	}
	fixtureAt(t, f.store, itemRecordSeated("item_053", "in-queue", "build"))
	runLoop() // A new loop instance must read the hold from the same store.
	third := runsForItem(t, f.store, "item_053")
	if len(third) != 1 || third[0].Backend != "fallback" {
		t.Fatalf("restart lost hold: %+v", third)
	}
	now = reset
	// The provider admits work again at reset; don't keep returning the expired
	// refusal from the reopened backend.
	if err := os.WriteFile(refused, []byte("#!/bin/sh\n"+committingAdapter), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureAt(t, f.store, itemRecordSeated("item_054", "in-queue", "build"))
	result := runLoop()
	fourth := runsForItem(t, f.store, "item_054")
	if len(fourth) != 1 || fourth[0].Backend != "fixture" || result.Landed != 1 {
		t.Fatalf("reopened backend did not land the item in one run: %+v / %+v", fourth, result)
	}
}

func TestLoopHoldsInfrastructureFailureWithoutRefusedWindow(t *testing.T) {
	for _, header := range []string{"", "rate limit kind: unknown\nfraction: unknown\nstatus: unknown\nreset: unknown\n", "rate limit kind: five_hour\nfraction: 0.5\nstatus: allowed\nreset: 9999999999\n"} {
		t.Run(header, func(t *testing.T) {
			refused := stub(t, "pi", "[ \"${1:-}\" != --features ] || exit 2\n"+counting+"exit 1\n")
			fallback := stub(t, "fallback", committingAdapter)
			f := newLoopFixture(t, refused)
			f.policy.DefaultBackend = "pi"
			f.policy.Backends["pi"] = f.policy.Backends["fixture"]
			delete(f.policy.Backends, "fixture")
			f.policy.Preference = []string{"pi", "fallback"}
			f.policy.Backends["fallback"] = drain.Backend{Adapter: fallback, Model: "floor"}
			fixtureAt(t, f.store, itemRecord("item_051", "in-queue"), itemRecord("item_052", "in-queue"))
			steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
			steps.BackendsDir = spendBackends(t, refused, "# run report\ntokens: unknown\ncost usd: unknown\n"+header+"---\n")
			result, err := f.newLoop(t, 1, steps).Run(context.Background())
			if err != nil || result.Stop != drain.StopQueueDrained {
				t.Fatalf("loop: %+v / %v", result, err)
			}
			// Without a refusal window this is an infrastructure failure. The first
			// item holds on pi; only new work starts on the open backend.
			first := runsForItem(t, f.store, "item_051")
			if len(first) != 1 || first[0].Backend != "pi" || !strings.Contains(result.Detail, "item_051 ("+drain.ReasonBackendClosed+": pi:") {
				t.Fatalf("first item did not hold on pi: %+v / %s", first, result.Detail)
			}
			second := runsForItem(t, f.store, "item_052")
			if len(second) != 1 || second[0].Backend != "fallback" {
				t.Fatalf("second item was sent to the closed backend: %+v", second)
			}
		})
	}
}
