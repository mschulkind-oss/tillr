package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runDefaultLaneRootLoopTest(t *testing.T, withLegacyLanes bool, explicit bool) {
	t.Helper()
	runBin, itemBin := faceSiblingBins(t)
	itemID := "item_root_default"
	root, explicitRoot, storePath, tmpl := newFaceLoopRepo(t, "", faceItemRecord(itemID, "in-queue", ""))
	if withLegacyLanes {
		if err := os.MkdirAll(filepath.Join(root, ".tillr", "lanes"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	scripts := scratch(t, "scripts")
	observed := filepath.Join(scripts, "observed.txt")
	adapter := writeExecScript(t, scripts, "adapter.sh", fmt.Sprintf(`
printf 'PWD=%%s\n' "$(pwd)" > %q
printf 'work\n' > change.txt
git add change.txt
git commit -q -m "work for %s"
printf 'RESULT: done\n'
`, observed, itemID))
	writeFile(t, filepath.Join(scripts, "pause-kind"), "stops-where-it-stands\n")
	passStub := writeExecScript(t, scripts, "pass.sh", `exit 0`)
	landStub := writeFaceLandStub(t, scripts, root, storePath)
	policyPath := writeFaceAdapterPolicy(t, adapter)

	args := []string{
		"loop",
		"--root", root,
		"--store", storePath,
		"--policy", policyPath,
		"--run-bin", runBin,
		"--item-bin", itemBin,
		"--brief-template", tmpl,
		"--gate", passStub,
		"--land", landStub,
		"--verdict", passStub,
		"--replay", "",
		"--rolling", "",
		"--fixer-gate", "",
		"--lanes", "1",
		"--no-notify",
		"--fallback-poll", "50ms",
		"--exit-when-drained",
	}
	wantRoot := filepath.Join(root, ".tillr", "items")
	wantReason := "default"
	if withLegacyLanes {
		wantRoot = filepath.Join(root, ".tillr", "lanes")
		wantReason = fmt.Sprintf("%s already exists", wantRoot)
	}
	if explicit {
		args = append(args, "--lane-root", explicitRoot)
		wantRoot = explicitRoot
		wantReason = "--lane-root given explicitly"
	}

	code, outBytes := invoke(t, args...)
	out := string(outBytes)
	if code != 0 {
		t.Fatalf("tillr-drain loop exited %d:\n%s", code, out)
	}

	wantLine := fmt.Sprintf("loop: lane-root %s (%s)", wantRoot, wantReason)
	if !strings.Contains(out, wantLine) {
		t.Fatalf("startup output missing %q:\n%s", wantLine, out)
	}

	raw, err := os.ReadFile(observed)
	if err != nil {
		t.Fatalf("reading adapter observation: %v\nloop output:\n%s", err, out)
	}
	wantWorktree := filepath.Join(wantRoot, itemID, "worktree")
	if !strings.Contains(string(raw), "PWD="+wantWorktree+"\n") {
		t.Fatalf("adapter ran in %q, want PWD=%s", string(raw), wantWorktree)
	}
}

func TestLoopDefaultLaneRootUsesItemsOnFreshCheckout(t *testing.T) {
	runDefaultLaneRootLoopTest(t, false, false)
}

func TestLoopDefaultLaneRootKeepsExistingLanes(t *testing.T) {
	runDefaultLaneRootLoopTest(t, true, false)
}

func TestLoopExplicitLaneRootWinsInBothCases(t *testing.T) {
	t.Run("fresh-checkout", func(t *testing.T) {
		runDefaultLaneRootLoopTest(t, false, true)
	})
	t.Run("existing-lanes", func(t *testing.T) {
		runDefaultLaneRootLoopTest(t, true, true)
	})
}

func TestResumeFaceUsesSameDefaultLaneRootRule(t *testing.T) {
	runID := "run-missing-1"
	root, explicitRoot, storePath, _ := newFaceLoopRepo(t, "",
		faceItemRecord("item_resume_1", "in-queue", ""),
		envelope("run.record", runID, map[string]any{
			"run_id":      runID,
			"item_id":     "item_resume_1",
			"lane_id":     "item_resume_1",
			"backend":     "pi",
			"state":       "exited",
			"exit_code":   0,
			"started_at":  "2026-11-10T00:00:00Z",
			"finished_at": "2026-11-10T00:01:00Z",
		}),
	)

	// Fresh checkout defaults to <root>/.tillr/items when looking up the request record.
	code, outBytes := invoke(t, "resume", "--root", root, "--store", storePath, "--run", runID)
	out := string(outBytes)
	if code == 0 {
		t.Fatalf("expected resume to fail on missing run, got exit 0:\n%s", out)
	}
	wantItemsBrief := filepath.Join(root, ".tillr", "items", "item_resume_1", "brief.md")
	if !strings.Contains(out, wantItemsBrief) {
		t.Fatalf("fresh checkout resume error %q did not reference %s", out, wantItemsBrief)
	}

	// Existing <root>/.tillr/lanes keeps <root>/.tillr/lanes.
	if err := os.MkdirAll(filepath.Join(root, ".tillr", "lanes"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, outBytes = invoke(t, "resume", "--root", root, "--store", storePath, "--run", runID)
	out = string(outBytes)
	if code == 0 {
		t.Fatalf("expected resume to fail on missing run, got exit 0:\n%s", out)
	}
	wantLanesBrief := filepath.Join(root, ".tillr", "lanes", "item_resume_1", "brief.md")
	if !strings.Contains(out, wantLanesBrief) {
		t.Fatalf("legacy checkout resume error %q did not reference %s", out, wantLanesBrief)
	}

	// Explicit --lane-root wins even when .tillr/lanes exists.
	code, outBytes = invoke(t, "resume", "--root", root, "--store", storePath, "--lane-root", explicitRoot, "--run", runID)
	out = string(outBytes)
	if code == 0 {
		t.Fatalf("expected resume to fail on missing run, got exit 0:\n%s", out)
	}
	wantExplicitBrief := filepath.Join(explicitRoot, "item_resume_1", "brief.md")
	if !strings.Contains(out, wantExplicitBrief) {
		t.Fatalf("explicit --lane-root resume error %q did not reference %s", out, wantExplicitBrief)
	}
}
