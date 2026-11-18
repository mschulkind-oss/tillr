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
  git -C %[1]q commit -q -m "trunk move"
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
	// stop, when set with limit, is called once more than limit runs have been staged. It is a
	// count, not a duration: a loop that keeps dispatching the same run (the 2026-11-17
	// design-run loop that ran 59 minutes to the suite timeout) is ended by what it did, so a
	// test fails with the specs it staged instead of hanging until the suite's own timeout.
	limit int
	stop  context.CancelFunc
}

func (s *recordingStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	s.specs = append(s.specs, spec)
	if s.stop != nil && len(s.specs) > s.limit {
		s.stop()
	}
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

// TestARepeatedSquashConflictGoesToItsMoverInsteadOfAnotherRun replaces the exhausted half
// (the owner's ruling of 2026-11-17): a conflict is never counted. The fix run conflicts
// too, on an unmoved trunk, so it goes back to the worker with the conflict as evidence
// and a fresh run on the latest trunk; that run's conflict is identical to the fix run's
// (the same paths and the same hunks), so it is not sent again: the repeat goes to the
// design seat, and with none in the fixture's policy it waits on the owner.
//
// Negative control: drop the identical-repeat check in conflictFix and the conflicts keep
// being returned to the worker; the stager's limit then cancels the loop.
func TestARepeatedSquashConflictGoesToItsMoverInsteadOfAnotherRun(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", conflictAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, conflict, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt"}}, limit: 6, stop: cancel}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0)), stage, os.Stderr)

	result, err := loop.Run(ctx)
	if err != nil {
		t.Fatalf("the loop failed: %v (staged %d)", err, len(stage.specs))
	}
	if result.ConflictFixes != 1 {
		t.Fatalf("the loop staged %d conflict-fix run(s), want 1 (on the moved trunk): %s", result.ConflictFixes, result.Detail)
	}
	if result.Landed != 0 {
		t.Fatalf("the loop landed %d, want none: %s", result.Landed, result.Detail)
	}
	if result.Dispatched != 3 || len(stage.specs) != 3 {
		t.Fatalf("the loop dispatched %d run(s) and staged %d, want the original, its fix and one return: %s", result.Dispatched, len(stage.specs), result.Detail)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonConflictOwner+")") {
		t.Fatalf("the repeated conflict does not wait on its mover: %s", result.Detail)
	}
	refusals := refusalsOf(t, eventsOf(t, f.store))
	if len(refusals) != 3 {
		t.Fatalf("refusals %+v, want one conflict per run", refusals)
	}
	for _, r := range refusals {
		if r.step != drain.RefusalConflict {
			t.Fatalf("refusal %+v, want %q", r, drain.RefusalConflict)
		}
	}
	returned := stage.specs[2]
	if returned.Returned == nil || returned.Returned.Cause != drain.RefusalConflict || !strings.Contains(returned.Returned.Detail, "base.txt") {
		t.Fatalf("the returned run's brief does not carry the conflict: %+v", returned.Returned)
	}
	var routed bool
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "item.conflict" && strings.Contains(string(e.Payload), drain.IdenticalRepeatPrefix) {
			routed = true
		}
	}
	if !routed {
		t.Fatal("the identical conflict raised no repeat conflict for its mover")
	}
}

// sessionConflictStager supplies the durable session and base that the real stager
// writes, while retaining recordingStager's observable conflict specification.
type sessionConflictStager struct {
	recordingStager
	root string
}

func (s *sessionConflictStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	if err := s.recordingStager.Stage(ctx, spec); err != nil {
		return err
	}
	dir := filepath.Join(s.laneRoot, spec.LaneID)
	if err := os.WriteFile(filepath.Join(dir, drain.BriefFile), []byte("fixture brief\n"), 0644); err != nil {
		return err
	}
	if !spec.Continue {
		if err := os.WriteFile(filepath.Join(dir, drain.SessionIDFile), []byte("11111111-1111-4111-8111-111111111111\n"), 0644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, drain.BaseSHAFile), []byte(spec.BaseSHA+"\n"), 0644); err != nil {
		return err
	}
	runDir := drain.RunDir(s.root, spec.RunID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(runDir, drain.SessionIDFile), []byte("11111111-1111-4111-8111-111111111111\n"), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(runDir, drain.BaseSHAFile), []byte(spec.BaseSHA+"\n"), 0644)
}

func TestSquashConflictResumesOriginalWorkerOnItsBranch(t *testing.T) {
	adapter := stub(t, "adapter", `if [ "$TILLR_SESSION_MODE" = resume ]; then
  grep -q 'base.txt' "$TILLR_RESUME_PROMPT_FILE" || exit 71
  test "$(cat base.txt)" = candidate || exit 72
  echo fixed > base.txt
else
  echo candidate > base.txt
fi
git add base.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -qm candidate
printf 'RESULT: done\n'`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, landingEnvelope(t, "landed", nil), 0)
	stage := &sessionConflictStager{root: f.root, recordingStager: recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt"}}}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0)), stage, os.Stderr)
	result, err := loop.Run(context.Background())
	if err != nil || result.Landed != 1 {
		t.Fatalf("loop: %+v %v", result, err)
	}
	if len(stage.specs) != 2 || !stage.specs[1].Continue || stage.specs[1].SessionID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("fix did not retain session: %+v", stage.specs)
	}
	var found bool
	for _, e := range eventsOf(t, f.store) {
		if e.Kind != "drain.started" {
			continue
		}
		var payload struct {
			Decision struct {
				WorkerSessionReason string `json:"worker_session_reason"`
			} `json:"decision"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Decision.WorkerSessionReason == "resumed-worker" {
			found = true
		}
	}
	if !found {
		t.Fatal("fix dispatch did not record resumed-worker")
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
