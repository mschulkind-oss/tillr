package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// landRecord writes one land.landing envelope as the land face prints it: the record the
// drain reads instead of inferring the outcome from an exit code.
func landRecord(t *testing.T, result string, conflicts []string) string {
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

// landStub writes a program that prints one land record and exits with code.
func landStub(t *testing.T, body string, code int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "land")
	script := "#!/bin/sh\nprintf '%s\\n' '" + body + "'\nexit " + strconv.Itoa(code) + "\n"
	if err := writeForkSafe(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the land stub failed: %v", err)
	}
	return path
}

// The drain forwards its recorded worker head only when document retirement bound one.
func TestLandForwardsOnlyAnExplicitWorkerHead(t *testing.T) {
	for _, worker := range []string{"", "worker-sha"} {
		t.Run("worker-"+worker, func(t *testing.T) {
			root := t.TempDir()
			argsPath := filepath.Join(root, "args")
			stub := filepath.Join(root, "land")
			if err := writeForkSafe(stub, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsPath+"'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			steps := &ProcessSteps{LandProgram: stub, Root: root}
			_, err := steps.Land(context.Background(), Candidate{ItemID: "item", RunID: "run", BaseSHA: "base", HeadSHA: "retired", WorkerHeadSHA: worker})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			hasFlag := strings.Contains(string(raw), "--message-from\n")
			if hasFlag != (worker != "") {
				t.Fatalf("wrong message-source argv: %s", raw)
			}
			if worker != "" && !strings.Contains(string(raw), "--message-from\n"+worker+"\n") {
				t.Fatalf("worker head not forwarded: %s", raw)
			}
		})
	}
}

// TestLandReadsTheSquashConflictRecord pins the capture: the lander exits 1 for both a
// refusal and a conflict, so only its record can tell the drain which one happened. The
// negative control is TestLandWithoutARecordFallsBackToTheExitCode below: the same exit 1
// with no record answers refused, which is what this test fails with if the record is
// discarded.
func TestLandReadsTheSquashConflictRecord(t *testing.T) {
	step := &ProcessSteps{
		LandProgram: landStub(t, landRecord(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"}), 1),
		Root:        t.TempDir(),
	}
	outcome, err := step.Land(context.Background(), Candidate{
		ItemID: "item_041", LaneID: "lane-1", RunID: "run-1",
		Worktree: t.TempDir(), BaseSHA: "base", HeadSHA: "head",
	})
	if err != nil {
		t.Fatalf("Land failed: %v", err)
	}
	if outcome.Result != LandSquashConflict {
		t.Fatalf("the land answered %q, want %q", outcome.Result, LandSquashConflict)
	}
	if len(outcome.ConflictPaths) != 1 || !strings.Contains(outcome.ConflictPaths[0], "base.txt") {
		t.Fatalf("the conflict paths are %v, want the CONFLICT line naming base.txt", outcome.ConflictPaths)
	}
}

// TestLandReadsALandedRecord is the positive half of the capture: a landed record is read as
// a landing even though the exit code is the positive one.
func TestLandReadsALandedRecord(t *testing.T) {
	step := &ProcessSteps{
		LandProgram: landStub(t, landRecord(t, "landed", nil), 0),
		Root:        t.TempDir(),
	}
	outcome, err := step.Land(context.Background(), Candidate{
		ItemID: "item_041", LaneID: "lane-1", RunID: "run-1",
		Worktree: t.TempDir(), BaseSHA: "base", HeadSHA: "head",
	})
	if err != nil {
		t.Fatalf("Land failed: %v", err)
	}
	if outcome.Result != LandLanded {
		t.Fatalf("the land answered %q, want %q", outcome.Result, LandLanded)
	}
}

// TestLandWithoutARecordFallsBackToTheExitCode is the capture's negative control made
// explicit: a land program that prints no record is a stub, and its exit code is the whole
// of what it said. Removing the record read leaves the conflict test above reading refused.
func TestLandWithoutARecordFallsBackToTheExitCode(t *testing.T) {
	step := &ProcessSteps{LandProgram: landStub(t, "", 1), Root: t.TempDir()}
	outcome, err := step.Land(context.Background(), Candidate{
		ItemID: "item_041", LaneID: "lane-1", RunID: "run-1",
		Worktree: t.TempDir(), BaseSHA: "base", HeadSHA: "head",
	})
	if err != nil {
		t.Fatalf("Land failed: %v", err)
	}
	if outcome.Result != LandRefused {
		t.Fatalf("the land answered %q, want %q", outcome.Result, LandRefused)
	}
}

// TestConflictPathsNamesThePathInEachReport pins the parse the fix brief's path list is
// built from, including the modify/delete spelling git uses.
func TestConflictPathsNamesThePathInEachReport(t *testing.T) {
	got := conflictPaths([]string{
		"CONFLICT (content): Merge conflict in base.txt",
		"CONFLICT (modify/delete): docs/one.md deleted in HEAD and modified in abc123",
		"CONFLICT (content): Merge conflict in base.txt",
	})
	want := []string{"base.txt", "docs/one.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("conflictPaths answered %v, want %v", got, want)
	}
}
