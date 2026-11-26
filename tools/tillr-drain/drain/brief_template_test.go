package drain_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
)

func TestConsumerRepoBriefHasNoTillrRepoRules(t *testing.T) {
	// Stage a brief in a repository other than this one using the installed default
	// brief template and confirm none of this repository's rules appear in it.
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "src/app.go"))

	installRoot := filepath.Join(scratch(t, "consumer-install"), "install")
	installedTemplate := filepath.Join(installRoot, "tillr-run", "backends", "brief-template.md")
	if err := os.MkdirAll(filepath.Dir(installedTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	rawTemplate, err := os.ReadFile(filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, installedTemplate, string(rawTemplate))
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)

	resolved, err := run.ResolveBriefTemplate()
	if err != nil {
		t.Fatalf("ResolveBriefTemplate: %v", err)
	}
	stager := &drain.LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.store,
		Template: resolved,
		Now:      func() time.Time { return time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC) },
	}
	if err := stager.Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging consumer brief: %v", err)
	}

	briefPath := filepath.Join(f.laneRoot, "lane-1", "brief.md")
	worktree := filepath.Join(f.laneRoot, "lane-1", "worktree")
	if code, msg := adapterPreflight(t, worktree, briefPath); code != 0 {
		t.Fatalf("preflight refused staged consumer brief (exit %d): %s", code, msg)
	}
	stagedBytes, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	staged := string(stagedBytes)
	for _, forbidden := range []string{
		"pack mirror",
		"pack-sync",
		"go-test-shards",
		"just done",
		"tools/tillr-drain",
		"tools/tillr-gate",
	} {
		if strings.Contains(staged, forbidden) {
			t.Fatalf("consumer repository brief must not contain %q", forbidden)
		}
	}
}

func TestLoopUsesBriefTemplateFromSettings(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "src/app.go"))
	baseTemplate, err := os.ReadFile(filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	marker := "REPO_CUSTOM_BRIEF_TEMPLATE_MARKER"
	customRel := filepath.Join("config", "custom-brief.md")
	if err := os.MkdirAll(filepath.Join(f.root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, customRel), string(baseTemplate)+"\n"+marker+"\n")
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[brief]\ntemplate = \"config/custom-brief.md\"\n")
	git(t, f.root, "add", ".tillr.toml", customRel)
	git(t, f.root, "commit", "-m", "add custom brief template")

	captureFile := filepath.Join(scratch(t, "brief-capture"), "captured-brief.md")
	adapterDir := filepath.Join(scratch(t, "brief-adapter"), "adapter")
	if err := os.MkdirAll(adapterDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stubAdapter := filepath.Join(adapterDir, "run-stub")
	if err := writeForkSafe(stubAdapter, []byte("#!/bin/sh\ncp ../brief.md "+captureFile+"\nprintf 'RESULT: done\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeForkSafe(filepath.Join(adapterDir, "extract-report"), []byte("#!/bin/sh\nprintf 'complete: yes\\nresult: done\\n\\nRESULT: done\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(adapterDir, "pause-kind"), "cannot-resume\n")

	policyPath := filepath.Join(scratch(t, "brief-policy"), "policy.json")
	writeFile(t, policyPath, `{
  "backends": {
    "stub": { "adapter": "`+stubAdapter+`", "model": "fixture", "effort": "low" }
  },
  "default_backend": "stub",
  "seats": { "build": "stub" },
  "default_seat": "build"
}
`)

	// Point TILLR_INSTALL_ROOT at a decoy default template so the test proves
	// .tillr.toml's [brief] template wins over the installed default.
	installRoot := filepath.Join(scratch(t, "decoy-install"), "install")
	decoyTemplate := filepath.Join(installRoot, "tillr-run", "backends", "brief-template.md")
	if err := os.MkdirAll(filepath.Dir(decoyTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, decoyTemplate, "DECOY_INSTALLED_TEMPLATE\n")

	cmd := exec.Command(drainBin(t), "loop",
		"--exit-when-drained",
		"--root", f.root,
		"--store", f.store,
		"--lane-root", f.laneRoot,
		"--lanes", "1",
		"--policy", policyPath,
		"--run-bin", sibling(t, "run"),
		"--item-bin", sibling(t, "item"),
		"--gate", always(t, "gate", 0),
		"--land", always(t, "land", 0),
		"--verdict", always(t, "verdict", 0),
	)
	cmd.Env = append(os.Environ(),
		"TILLR_INSTALL_ROOT="+installRoot,
		"TILLR_SESSION_MODE=new",
		"TILLR_RESUME_PROMPT_FILE=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("loop with [brief] template failed: %v\n%s", err, out)
	}
	captured, err := os.ReadFile(captureFile)
	if err != nil {
		t.Fatalf("adapter did not capture brief: %v\nloop output:\n%s", err, out)
	}
	if !strings.Contains(string(captured), marker) {
		t.Fatalf("staged brief did not come from .tillr.toml [brief] template:\n%s", captured)
	}
}

func TestPlainStageNonLandingBriefTemplatePassesPiPreflight(t *testing.T) {
	root := filepath.Join(scratch(t, "non-landing-root"), "work")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	laneRoot := filepath.Join(scratch(t, "non-landing-lanes"), "lanes")
	if err := os.MkdirAll(laneRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	tplAbs, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "brief-template-non-landing.md"))
	if err != nil {
		t.Fatal(err)
	}
	storePath := fixture(t, fencedItemRecord("finding-1", "in-queue"))
	def := plannerFixtureDefinition(t)
	def.BriefTemplate = tplAbs
	setPlannerDefinition(t, storePath, def)

	stager := &drain.LaneStager{
		Root:     root,
		LaneRoot: laneRoot,
		Store:    storePath,
		Now:      func() time.Time { return time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC) },
	}
	spec := drain.StageSpec{
		LaneID:     "lane-1",
		ItemID:     "finding-1",
		RunID:      "run-finding-1-a1",
		Backend:    "pi",
		WorkingDir: drain.PlainDir,
		TimeoutMS:  60000,
	}
	if err := stager.Stage(context.Background(), spec); err != nil {
		t.Fatalf("plain staging with brief-template-non-landing.md: %v", err)
	}
	briefPath := filepath.Join(laneRoot, "lane-1", "brief.md")
	workDir := filepath.Join(laneRoot, "lane-1", drain.PlainDir)
	if code, msg := adapterPreflight(t, workDir, briefPath); code != 0 {
		t.Fatalf("pi preflight refused staged non-landing brief (exit %d): %s", code, msg)
	}
}
