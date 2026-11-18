package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
)

func TestNonLandingStagesPlainDirectory(t *testing.T) {
	root := scratch(t, "plain-root")
	laneRoot := filepath.Join(scratch(t, "plain-lanes"), "lanes")
	path := fixture(t, fencedItemRecord("finding", "in-queue"))
	d := plannerFixtureDefinition(t)
	d.Ready = "in-queue"
	d.BriefTemplate = "finding-template.md"
	template := "# {{ITEM_ID}}: {{TITLE}}\nRun {{RUN_ID}} on {{BACKEND}}\nTarget {{TARGET}} ({{TARGET_KIND}})\nDate {{DATE}}, timeout {{TIMEOUT_SECONDS}}\n{{TASK}}\n1. {{CRITERION_1}}\n"
	writeFile(t, filepath.Join(root, d.BriefTemplate), template)
	setPlannerDefinition(t, path, d)
	code, out := invoke(t, "why", "finding", "--root", root, "--store", path, "--policy", policy(t, "true"), "--lanes", "lane-1", "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var row drain.WhyRow
	if err := json.Unmarshal(out, &row); err != nil {
		t.Fatal(err)
	}
	req := row.Decision.RunRequest
	if req == nil || req.WorkingDir != "work" {
		t.Fatalf("plain directory request: %s", out)
	}
	forbidden := stub(t, "git", "echo Git must not run >&2\nexit 99")
	t.Setenv("PATH", filepath.Dir(forbidden)+":"+os.Getenv("PATH"))
	stager := &drain.LaneStager{Root: root, LaneRoot: laneRoot, Store: path, Template: "must-not-read", Now: func() time.Time { return time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC) }}
	spec := drain.StageSpec{LaneID: req.LaneID, ItemID: req.ItemID, RunID: req.RunID, Backend: req.Backend, WorkingDir: req.WorkingDir, TimeoutMS: req.TimeoutMS}
	if err := stager.Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	lane := filepath.Join(laneRoot, req.LaneID)
	target := filepath.Join(lane, req.WorkingDir)
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("working directory: %v %v", info, err)
	}
	for _, dir := range []string{lane, drain.RunDir(root, req.RunID)} {
		for _, name := range []string{drain.WorktreeDir, drain.BaseSHAFile, drain.FenceFile, drain.ItemBaseSHAFile, drain.ItemFenceFile, drain.TargetFile, drain.PatchRefFile, ".git"} {
			if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Errorf("unexpected Git metadata %s: %v", filepath.Join(dir, name), err)
			}
		}
		got := readAll(t, filepath.Join(dir, drain.BriefFile))
		want := "# finding: the item finding\nRun " + req.RunID + " on " + req.Backend + "\nTarget " + target + " (plain directory)\nDate 2026-11-17, timeout 60 seconds\n" +
			"the item finding\n\nThe item's body is briefs/finding.md. Staging carries that reference, not a copy: read it there.\n1. the first criterion of finding\n"
		if got != want {
			t.Fatalf("brief:\n%s\nwant:\n%s", got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); !os.IsNotExist(err) {
		t.Fatalf("plain target has .git: %v", err)
	}
	// Every generic value is independent of the Git-layout vocabulary.
	writeFile(t, filepath.Join(root, d.BriefTemplate), "{{DURABLE_DIR}}\n{{SETTLED}}\n{{FACTS}}\n{{METHOD}}\n{{VERIFICATION}}\n")
	if err := stager.Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	generic := readAll(t, filepath.Join(lane, drain.BriefFile))
	// The scratch path is run data, not Git vocabulary: the sharded runner's
	// own go-shards directory contains the substring "sha".
	durable, prose, found := strings.Cut(generic, "\n")
	if !found || durable != run.DurableDir(lane, req.RunID) {
		t.Fatalf("scratch value is %q, want %q", durable, run.DurableDir(lane, req.RunID))
	}
	for _, word := range []string{"sha", "fence", "trunk"} {
		if strings.Contains(strings.ToLower(prose), word) {
			t.Fatalf("plain prose values carry %q: %s", word, prose)
		}
	}
	// Plain workflow nodes receive their own prompt, outputs, and wired inputs,
	// without instructions about a Git fence or candidate.
	spec.Node = &drain.WorkflowStage{ID: "investigate", Seat: "build", Access: "read_only", Prompt: "Inspect the reproduction", Inputs: map[string]string{"reproduction": "text"}, Outputs: map[string]string{"analysis": "text"}}
	spec.InputPaths = map[string][]drain.NodeInput{"reproduction": {{Producer: "probe", Path: "/fixture/reproduction.txt"}}}
	spec.PreviousReport = "/fixture/previous-report.md"
	if err := stager.Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	nodeBrief := readAll(t, filepath.Join(lane, drain.BriefFile))
	for _, text := range []string{"Run only node investigate", "Node task: Inspect the reproduction", "Output analysis (text):", "Input reproduction from node probe: /fixture/reproduction.txt"} {
		if !strings.Contains(nodeBrief, text) {
			t.Fatalf("node brief lacks %q: %s", text, nodeBrief)
		}
	}
	for _, text := range []string{"candidate", "fence", "or commit", spec.PreviousReport} {
		if strings.Contains(nodeBrief, text) {
			t.Fatalf("plain node brief carries %q: %s", text, nodeBrief)
		}
	}
	// Git-specific placeholders are not supplied, even when a template asks for them.
	for _, name := range []string{"BASE_SHA", "FENCE_PATHS", "OUT_OF_FENCE_PATHS"} {
		writeFile(t, filepath.Join(root, d.BriefTemplate), "{{"+name+"}}")
		err := stager.Stage(context.Background(), spec)
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("Git value %s supplied: %v", name, err)
		}
	}
}

func TestGitLandingStagingRefusesOtherWorkingDirectory(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "docs/"))
	spec := f.spec("lane-1")
	spec.WorkingDir = "."
	err := f.stager().Stage(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "working_dir") || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("want staging's worktree-only refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.laneRoot, "lane-1")); !os.IsNotExist(err) {
		t.Fatalf("refused staging created lane: %v", err)
	}
}
