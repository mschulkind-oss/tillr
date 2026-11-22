package heavy

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/checkresult"
)

func TestReadStatusUsesClassifier(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	dir, err := Directory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runPayload, err := json.Marshal(HeavyRun{
		Result: "red",
		Checks: []HeavyCheck{
			{CheckID: "chk-fail-override", Result: "pass"},
			{CheckID: "chk-unavail-override", Result: "pass"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy.json"), runPayload, 0o644); err != nil {
		t.Fatal(err)
	}

	prev := classifyCheck
	t.Cleanup(func() { classifyCheck = prev })
	var seen []checkresult.Observation
	classifyCheck = func(o checkresult.Observation) checkresult.Result {
		seen = append(seen, o)
		switch o.Check {
		case "chk-fail-override":
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		case "chk-unavail-override":
			return checkresult.Result{Outcome: checkresult.Unavailable, Detail: checkresult.DetailUnavailable}
		}
		return checkresult.Classify(o)
	}

	status, err := ReadStatus(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(status.FailingChecks) != 1 || status.FailingChecks[0].CheckID != "chk-fail-override" {
		t.Fatalf("FailingChecks = %+v, want [chk-fail-override]", status.FailingChecks)
	}
	if len(status.UnavailableChecks) != 1 || status.UnavailableChecks[0].CheckID != "chk-unavail-override" {
		t.Fatalf("UnavailableChecks = %+v, want [chk-unavail-override]", status.UnavailableChecks)
	}
	if len(seen) != 2 || seen[0].Source != checkresult.HeavyCheckWord || seen[1].Source != checkresult.HeavyCheckWord {
		t.Fatalf("observations = %+v, want HeavyCheckWord for both checks", seen)
	}
}
