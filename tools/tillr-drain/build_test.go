package main_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
)

// The loop names its own build before it runs, and the result it writes names the same
// build. The measured failure this closes: on 2026-11-14 a fence fix was committed, gated
// and pack-mirrored while the loop ran a binary built at 00:37 that morning, and three
// items were refused by the very trap the fix closed. The record carried only the release
// version, so the two builds were indistinguishable.
//
// Negative controls: without the startup line the first assertion fails; without the
// payload field the record assertion fails; with the stamp hardcoded rather than read from
// the running executable the digest assertion fails.
func TestTheLoopStampsItsOwnBuildInTheStartupLineAndTheRecord(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	var report strings.Builder
	loop := f.newLoopWith(t, 1, f.steps(t, gate, land, verdict),
		passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q, want %q: %s", result.Stop, drain.StopQueueDrained, result.Detail)
	}

	// The first line of the loop's report is the build, before any tick's own line.
	first, _, _ := strings.Cut(report.String(), "\n")
	if !strings.HasPrefix(first, "loop: build ") {
		t.Fatalf("the loop's first line is %q, want a `loop: build <stamp>` line", first)
	}
	if !strings.Contains(first, "digest ") {
		t.Fatalf("the loop's build line %q names no digest", first)
	}

	// The record carries the same stamp, so the answer outlives the terminal.
	path := drain.LoopResultFile(f.root, result.FirstGeneration, result.LastGeneration)
	body := readJSONFile(t, path)
	env, err := record.Decode(mustJSON(t, body))
	if err != nil {
		t.Fatalf("the loop's result at %s is not a decodable record: %v", path, err)
	}
	var kept drain.LoopResult
	if err := json.Unmarshal(env.Payload, &kept); err != nil {
		t.Fatalf("decoding the loop's result payload failed: %v", err)
	}
	if kept.Build == nil {
		t.Fatalf("the loop's record names no build: %s", env.Payload)
	}
	if kept.Build.Digest == "" {
		t.Fatalf("the recorded build carries no digest: %s", env.Payload)
	}
	if got, want := first, "loop: build "+kept.Build.String(); got != want {
		t.Fatalf("the startup line is %q and the record's stamp is %q: the two must agree", got, want)
	}
}

// A stamp that identifies nothing is drawn as a build that cannot be named, not as a build
// with nothing to say. This is the same distinction the observatory makes for a server with
// no build block: the absence is the answer, and it is spelled out.
func TestABuildStampThatNamesNothingIsDrawnAsUnnamed(t *testing.T) {
	got := drain.BuildStamp{}.String()
	for _, want := range []string{"no-revision", "no-digest", "unknown-exe"} {
		if !strings.Contains(got, want) {
			t.Fatalf("an empty build stamp prints %q, want it to name the absence %q", got, want)
		}
	}
}
