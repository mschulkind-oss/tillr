package remotesync

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/checkresult"
)

func TestReadForkConflictsAndReplay(t *testing.T) {
	dir := t.TempDir()
	spansPath := filepath.Join(dir, "gate.spans.json")

	obs := ForkObservation{
		Fork:     "pi-fork",
		Kind:     "release",
		Ref:      "refs/tags/v1.0.0",
		Commit:   strings.Repeat("a", 40),
		Delivery: "patch/pi-fork",
	}
	attrBytes, err := json.Marshal(map[string]any{"fork_conflict": obs})
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"skipped_checks": []map[string]any{
			{"kind": ForkDriftCheck, "planned": true, "owed": true},
		},
		"check_spans": []map[string]any{
			{
				"check_id":        "patch-branch-inv2/pi-fork",
				"kind":            ForkDriftCheck,
				"status":          "FAIL",
				"attributes_json": string(attrBytes),
			},
			{
				"check_id": "unit/other",
				"kind":     "go-test",
				"status":   "NOT_RUN",
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spansPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	got, unfinished, unansweredFork := ReadForkConflicts(spansPath)
	if !unfinished || !unansweredFork {
		t.Fatalf("unfinished=%v unansweredFork=%v, want both true", unfinished, unansweredFork)
	}
	gotObs, ok := got["patch-branch-inv2/pi-fork"]
	if !ok || !gotObs.Valid() || !gotObs.Requestable() || gotObs.Pin() != "yolo-packs/fork-pins/pi-fork" || gotObs.PatchRef() != "refs/heads/patch/pi-fork" {
		t.Fatalf("unexpected observation: %+v (ok=%v)", gotObs, ok)
	}
	if !gotObs.Touches([]string{"yolo-packs/fork-pins/pi-fork"}) {
		t.Fatal("Touches should match pin path")
	}

	// Verify ClassifyCheck hook is respected.
	prev := ClassifyCheck
	t.Cleanup(func() { ClassifyCheck = prev })
	ClassifyCheck = func(o checkresult.Observation) checkresult.Result {
		if o.Check == "patch-branch-inv2/pi-fork" {
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
		}
		return checkresult.Classify(o)
	}
	gotPass, _, _ := ReadForkConflicts(spansPath)
	if len(gotPass) != 0 {
		t.Fatalf("expected 0 conflicts when classifier overrides to Pass, got %+v", gotPass)
	}

	replayLine := "fork-check replay target: " + string(mustMarshal(t, obs)) + "\n"
	replayed, ok := ReplayForkConflict("patch-branch-inv2/pi-fork", replayLine)
	if !ok || replayed.CheckID != "patch-branch-inv2/pi-fork" || replayed.Fork != "pi-fork" {
		t.Fatalf("ReplayForkConflict = (%+v, %v)", replayed, ok)
	}
	if _, ok := ReplayForkConflict("c", replayLine+replayLine); ok {
		t.Fatal("expected ReplayForkConflict to reject multiple target lines")
	}
	if !IsForkRebaseItem("rebase-fork-pi-fork") || IsForkRebaseItem("item-1") {
		t.Fatal("IsForkRebaseItem mismatch")
	}
}

func TestForkCheckDeclIDAndFailingSpanMeta(t *testing.T) {
	root := "/workspace/.tillr/lanes/lane-1/worktree"
	if got := ForkCheckDeclID("storybook", "Roster Storybook build", root); got != "storybook/roster" {
		t.Fatalf("ForkCheckDeclID storybook roster = %q", got)
	}
	if got := ForkCheckDeclID("go-test", "Run tests in "+root+" for run-12345", root); got != "go-test/run-tests-in-root-for-run-pid" {
		t.Fatalf("ForkCheckDeclID = %q", got)
	}

	dir := t.TempDir()
	spansPath := filepath.Join(dir, "gate.spans.json")
	doc := map[string]any{
		"check_spans": []map[string]any{
			{"check_id": "c-pass", "kind": "k", "label": "ok", "status": "PASS"},
			{"check_id": "c-fail", "kind": "k", "label": "bad", "status": "FAIL"},
			{"check_id": "c-err", "kind": "k", "label": "err", "status": "ERROR"},
		},
	}
	if err := os.WriteFile(spansPath, mustMarshal(t, doc), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ReadFailingSpanMeta(spansPath)
	if len(got) != 2 || got[0].CheckID != "c-fail" || got[1].CheckID != "c-err" {
		t.Fatalf("ReadFailingSpanMeta = %+v", got)
	}
}

func TestFormatAndParseTriageBlock(t *testing.T) {
	checks := []FailingCheck{
		{
			ID:    "go-drain",
			Label: "tools/tillr-drain",
			Cases: []FailingCase{
				{
					Name:      "TestAlpha",
					Where:     "a_test.go:12",
					Assertion: "got 1, want 2",
					Reproduce: "go test ./remotesync -run '^TestAlpha$'",
				},
			},
		},
		{
			ID:        "lint",
			Label:     "lint",
			Reproduce: "just lint",
		},
	}
	block := FormatTriageBlock(checks)
	if !strings.Contains(block, "fork-check: triage -- 2 failed check(s)") {
		t.Fatalf("unexpected triage header: %q", block)
	}
	parsed := ParseTriageReader(bufio.NewScanner(strings.NewReader(block)))
	if !reflect.DeepEqual(parsed, checks) {
		t.Fatalf("round-trip mismatch:\ngot:  %+v\nwant: %+v", parsed, checks)
	}
	if !CommitMatchesRev("deadbeef1234", "deadbeef1234567890") || CommitMatchesRev("deadbeef", "deadbeef1234") {
		t.Fatal("CommitMatchesRev prefix matching mismatch")
	}
	if SanitizeLogComponent("a/b:c") != "a-b-c" {
		t.Fatal("SanitizeLogComponent mismatch")
	}
	filtered := FilterCheckLogs(map[string]string{"a": "1", "b": "", "c": "3"}, []string{"a", "b"})
	if !reflect.DeepEqual(filtered, map[string]string{"a": "1"}) {
		t.Fatalf("FilterCheckLogs = %+v", filtered)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
