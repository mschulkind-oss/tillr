package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
)

// conflictAdapter is the model's first attempt in these cases: it changes the one file the
// trunk move also changes, so the squash onto the moved trunk conflicts.
const conflictAdapter = counting + `echo candidate > base.txt
git add base.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
`

// landingEnvelope writes the land.landing record the land face prints. The conflict case
// names the CONFLICT line git produced; the landed case is the positive control.
func landingEnvelope(t *testing.T, result string, conflicts []string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"landing_id":     "landing-1",
		"item_id":        "item_041",
		"run_id":         "run-1",
		"result":         result,
		"reason":         "",
		"conflict_paths": conflicts,
	})
	if err != nil {
		t.Fatalf("encoding the landing payload failed: %v", err)
	}
	raw, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          "land.landing",
		ID:            "landing-1",
		Producer:      record.Producer{System: record.System, Utility: "land", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
		Payload:       payload,
	})
	if err != nil {
		t.Fatalf("encoding the landing record failed: %v", err)
	}
	return string(raw)
}

// conflictLandScript writes a land program that moves the trunk once and answers the first
// call with a conflict; the second call answers whatever the case asks. The trunk move is
// what makes the next squash a conflict, and the marker file keeps the move to one commit.
func conflictLandScript(t *testing.T, root, first, second string, secondCode int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "land")
	script := fmt.Sprintf(`#!/bin/sh
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
if [ ! -f %[1]q/land-once ]; then
  touch %[1]q/land-once
  echo trunk > %[1]q/base.txt
  git -C %[1]q add base.txt
  git -C %[1]q -c user.name=stub -c user.email=stub@example.invalid commit -q -m "trunk move"
  printf '%%s\n' '%[2]s'
  exit 1
fi
printf '%%s\n' '%[3]s'
exit %[4]d
`, root, first, second, secondCode)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the land stub failed: %v", err)
	}
	return path
}

// recordingStager is the pass stager plus the specs it was handed: a conflict-fix run's
// brief is the thing under test, and this is where the brief's inputs are visible without a
// model reading the rendered file.
type recordingStager struct {
	passStager
	specs []drain.StageSpec
}

func (s *recordingStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	s.specs = append(s.specs, spec)
	return s.passStager.Stage(ctx, spec)
}

// TestASquashConflictStagesOneConflictFixRunWithTheMarkersInItsBrief is the first-conflict
// half of the rule: a moved trunk's conflict is answered by exactly one more run, its brief
// carries the paths and the three-way markers, and that run is gated and landed like any
// other change.
//
// Negative control: delete the LandSquashConflict branch in Verify and this case sees one
// run, no ConflictFixes and no landing — the same assertions fail.
func TestASquashConflictStagesOneConflictFixRunWithTheMarkersInItsBrief(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", conflictAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	landed := landingEnvelope(t, "landed", nil)
	land := conflictLandScript(t, f.root, conflict, landed, 0)
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt"}}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0)), stage, os.Stderr)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.ConflictFixes != 1 {
		t.Fatalf("the loop staged %d conflict-fix run(s), want 1: %s", result.ConflictFixes, result.Detail)
	}
	if result.Landed != 1 {
		t.Fatalf("the loop landed %d conflict-fix run(s), want 1: %s", result.Landed, result.Detail)
	}
	if len(stage.specs) != 2 {
		t.Fatalf("staging saw %d dispatch(es), want the original and one fix run: %+v", len(stage.specs), stage.specs)
	}
	fix := stage.specs[1]
	if fix.Conflict == nil {
		t.Fatal("the fix run's brief carries no conflict")
	}
	if !contains(fix.Conflict.Paths, "base.txt") {
		t.Fatalf("the fix brief's paths are %v, want base.txt", fix.Conflict.Paths)
	}
	if len(fix.Conflict.Reports) != 1 || !strings.Contains(fix.Conflict.Reports[0], "base.txt") {
		t.Fatalf("the fix brief's conflict reports are %v, want the CONFLICT line", fix.Conflict.Reports)
	}
	if !strings.Contains(fix.Conflict.Markers, "<<<<<<<") {
		t.Fatalf("the fix brief carries no three-way markers:\n%s", fix.Conflict.Markers)
	}
	refusals := refusalsOf(t, eventsOf(t, f.store))
	if len(refusals) != 1 || refusals[0].step != drain.RefusalConflict {
		t.Fatalf("the conflict recorded %+v, want one %q refusal", refusals, drain.RefusalConflict)
	}
	if refusals[0].fixRunID != fix.RunID {
		t.Fatalf("the conflict names fix run %q, want %q", refusals[0].fixRunID, fix.RunID)
	}
	if len(refusals[0].conflictPaths) != 1 || !strings.Contains(refusals[0].conflictPaths[0], "base.txt") {
		t.Fatalf("the conflict refusal's paths are %v, want the CONFLICT line", refusals[0].conflictPaths)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "landed" {
		t.Fatalf("the item is %q after the fix landed, want landed", got)
	}
}

// TestASecondSquashConflictHoldsTheItemInsteadOfSpendingAnotherRun is the exhausted half:
// the one fix run conflicts too, so the item is held with its own reason and no third run
// is spent.
//
// Negative control: remove the isConflictFixRun branch in conflictFix and this case stages
// a third run — the adapter count moves from two to three — and never reaches the hold.
func TestASecondSquashConflictHoldsTheItemInsteadOfSpendingAnotherRun(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", conflictAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, conflict, 1)
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt"}}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0)), stage, os.Stderr)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.ConflictFixes != 1 || result.ConflictExhausted != 1 {
		t.Fatalf("the loop recorded %d fix(es) and %d exhausted hold(s), want 1 and 1: %s",
			result.ConflictFixes, result.ConflictExhausted, result.Detail)
	}
	if result.Landed != 0 {
		t.Fatalf("the loop landed %d, want none: %s", result.Landed, result.Detail)
	}
	if result.Dispatched != 2 {
		t.Fatalf("the loop dispatched %d run(s), want the original and one fix: %s", result.Dispatched, result.Detail)
	}
	if len(stage.specs) != 2 {
		t.Fatalf("staging saw %d dispatch(es), want no third run: %+v", len(stage.specs), stage.specs)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("the exhausted conflict left the item %q, want in-queue", got)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonConflictExhausted+")") {
		t.Fatalf("the stop does not name the exhausted hold: %s", result.Detail)
	}
	runs := lines(t, f.count)
	if got := countMatching(runs, "adapter"); got != 2 {
		t.Fatalf("the adapter ran %d time(s), want 2 (the original and one fix): %v", got, runs)
	}
}

// contains is a small membership check so the case reads as the assertion it is.
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
