package goaffected_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-gate/goaffected"
)

func TestCfixRegateReusesUnchangedPackageVerdicts(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()
	tillrGateBin := filepath.Join(binDir, "tillr-gate")
	buildArgs := []string{"buil" + "d", "-o", tillrGateBin, "."}
	buildCmd := exec.Command("go", buildArgs...)
	buildCmd.Dir = filepath.Join(root, "tools", "tillr-gate")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build tillr-gate: %v\n%s", err, out)
	}

	tmpPath := filepath.Join(scratchRoot(t), "ga-cfix-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	loopDir := filepath.Join(tmpPath, "loop")
	durable := filepath.Join(tmpPath, "durable")
	for _, d := range []string{loopDir, durable} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, modRel := range []string{
		"tools/tillr/core",
		"tools/tillr/extra",
		"tools/tillr-gate",
		"tools/tillr-watch",
		"tools/tillr-view",
		"tools/gate",
		"config",
	} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(modRel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	writeRepoFile := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	writeRepoFile("tools/go-test-durations.json", "{}\n")
	writeRepoFile("config/view.txt", "v1\n")
	writeRepoFile("notes.txt", "base-line\n")
	writeRepoFile("tools/gate/go-test-edges.json", `{
  "github.com/mschulkind-oss/tillr-view": {
    "builds": [
      "github.com/mschulkind-oss/tillr-watch"
    ],
    "reads": [
      "config/view.txt"
    ]
  }
}
`)

	writeRepoFile("tools/tillr/go.mod", "module github.com/mschulkind-oss/tillr\n\ngo 1.22\n")
	writeRepoFile("tools/tillr/core/core.go", "package core\n\nfunc Value() int { return 1 }\n")
	writeRepoFile("tools/tillr/extra/extra.go", "package extra\n\nfunc Extra() int { return 10 }\n")
	writeRepoFile("tools/tillr-gate/go.mod",
		"module github.com/mschulkind-oss/tillr-gate\n\ngo 1.22\n\n"+
			"require github.com/mschulkind-oss/tillr v0.0.0\n"+
			"replace github.com/mschulkind-oss/tillr => ../tillr\n")
	writeRepoFile("tools/tillr-gate/gate.go",
		"package gate\n\nimport \"github.com/mschulkind-oss/tillr/core\"\n\nfunc GateVal() int { return core.Value() }\n")
	writeRepoFile("tools/tillr-watch/go.mod",
		"module github.com/mschulkind-oss/tillr-watch\n\ngo 1.22\n\n"+
			"require github.com/mschulkind-oss/tillr v0.0.0\n"+
			"replace github.com/mschulkind-oss/tillr => ../tillr\n")
	writeRepoFile("tools/tillr-watch/main.go",
		"package main\n\nimport \"github.com/mschulkind-oss/tillr/core\"\n\nfunc WatchVal() int { return core.Value() }\nfunc main() {}\n")
	writeRepoFile("tools/tillr-view/go.mod", "module github.com/mschulkind-oss/tillr-view\n\ngo 1.22\n")
	writeRepoFile("tools/tillr-view/view.go", "package view\n\nfunc ViewVal() int { return 7 }\n")

	writeLoggingTest := func(relPath, pkgName, marker, fnName string) {
		writeRepoFile(relPath,
			"package "+pkgName+"\n\n"+
				"import (\n"+
				"\t\"os\"\n"+
				"\t\"path/filepath\"\n"+
				"\t\"testing\"\n"+
				")\n\n"+
				"func "+fnName+"(t *testing.T) {\n"+
				"\tif dir := os.Getenv(\"CFIX_EXEC_LOG_DIR\"); dir != \"\" {\n"+
				"\t\t_ = os.WriteFile(filepath.Join(dir, \""+marker+"\"), []byte(\"ran\\n\"), 0o644)\n"+
				"\t}\n"+
				"}\n")
	}

	writeLoggingTest("tools/tillr/core/core_test.go", "core", "tillr-core", "TestCore")
	writeLoggingTest("tools/tillr/extra/extra_test.go", "extra", "tillr-extra", "TestExtra")
	writeLoggingTest("tools/tillr-gate/gate_test.go", "gate", "tillr-gate", "TestGate")
	writeLoggingTest("tools/tillr-watch/main_test.go", "main", "tillr-watch", "TestWatch")
	writeLoggingTest("tools/tillr-view/view_test.go", "view", "tillr-view", "TestView")

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "base-v1")
	baseV1 := runGit(t, repo, "rev-parse", "HEAD")

	// Candidate pre-conflict commit modifies tillr/core/core.go and notes.txt.
	runGit(t, repo, "checkout", "-qb", "cand")
	writeRepoFile("tools/tillr/core/core.go", "package core\n\nfunc Value() int { return 2 }\n")
	writeRepoFile("notes.txt", "candidate-notes\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "cand-v1")

	gocache := sharedGocache(t)
	stepCounter := 0

	runLandingGate := func(runID, sinceSHA, itemID string) (goaffected.Evidence, map[string]bool) {
		t.Helper()
		stepCounter++
		execDir := filepath.Join(tmpPath, "exec-"+string(rune('0'+stepCounter)))
		if err := os.MkdirAll(execDir, 0o755); err != nil {
			t.Fatal(err)
		}
		runDir := filepath.Join(loopDir, runID)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			t.Fatal(err)
		}
		recBytes, _ := json.Marshal(map[string]any{
			"payload": map[string]any{"run_id": runID, "item_id": itemID},
		})
		if err := os.WriteFile(filepath.Join(runDir, "run.record.json"), append(recBytes, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}

		evidencePath := filepath.Join(runDir, "go-affected.evidence.json")
		verdictsOut := filepath.Join(tmpPath, "verdicts-out-"+string(rune('0'+stepCounter))+".json")
		argv := []string{
			"go-affected",
			"--base", sinceSHA,
			"--head", "HEAD",
			"--evidence-out", evidencePath,
			"--verdicts-out", verdictsOut,
		}
		if goaffected.IsCfixRunID(runID) {
			if prior := goaffected.LoadPriorCfixVerdicts(runDir); prior != nil && len(prior.Packages) > 0 {
				verdictsIn := filepath.Join(tmpPath, "verdicts-in-"+string(rune('0'+stepCounter))+".json")
				if err := goaffected.SavePackageVerdictsAtomic(verdictsIn, prior); err != nil {
					t.Fatalf("SavePackageVerdictsAtomic(verdictsIn): %v", err)
				}
				argv = append(argv, "--verdicts-in", verdictsIn)
			}
		}

		var env []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if k == goaffected.GoVerdictsInEnv || k == goaffected.GoVerdictsOutEnv || k == "TILLR_RUN_ID" {
				continue
			}
			env = append(env, kv)
		}
		env = append(env,
			"TILLR_RUN_ID=",
			"YOLO_DURABLE_DIR="+durable,
			"GOCACHE="+gocache,
			"CFIX_EXEC_LOG_DIR="+execDir,
		)

		cmd := exec.Command(tillrGateBin, argv...)
		cmd.Dir = repo
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("run_id=%s failed: %v\n%s", runID, err, out)
		}

		entries, err := os.ReadDir(execDir)
		if err != nil {
			t.Fatal(err)
		}
		ranMarkers := make(map[string]bool, len(entries))
		for _, ent := range entries {
			ranMarkers[ent.Name()] = true
		}

		if recorded := goaffected.LoadPackageVerdictsFile(verdictsOut); recorded != nil {
			if err := goaffected.SavePackageVerdictsAtomic(filepath.Join(runDir, goaffected.GoPackageVerdictsFile), recorded); err != nil {
				t.Fatalf("SavePackageVerdictsAtomic(runDir): %v", err)
			}
		}

		evBytes, err := os.ReadFile(evidencePath)
		if err != nil {
			t.Fatal(err)
		}
		var evidence goaffected.Evidence
		if err := json.Unmarshal(evBytes, &evidence); err != nil {
			t.Fatal(err)
		}
		return evidence, ranMarkers
	}

	// 1. Pre-conflict gate runs only the affected Go packages via go-affected (skipping untouched sibling tillr/extra).
	origRun := "run-item-request-demo-a1-n2"
	_, ranOrig := runLandingGate(origRun, baseV1, "item-request-demo")
	affectedGoMarkers := map[string]bool{
		"tillr-core":  true,
		"tillr-gate":  true,
		"tillr-watch": true,
		"tillr-view":  true,
	}
	if !reflect.DeepEqual(ranOrig, affectedGoMarkers) {
		t.Fatalf("ranOrig = %v, want %v", ranOrig, affectedGoMarkers)
	}
	verdictsPath := filepath.Join(loopDir, origRun, "gate.go-packages.json")
	if _, err := os.Stat(verdictsPath); err != nil {
		t.Fatalf("expected %s to be recorded: %v", verdictsPath, err)
	}

	// 2. Trunk advances on notes.txt; conflict repair resolves ONLY notes.txt (no Go or edge file changed).
	runGit(t, repo, "checkout", "-q", "main")
	writeRepoFile("notes.txt", "trunk-notes\n")
	runGit(t, repo, "add", "notes.txt")
	runGit(t, repo, "commit", "-qm", "trunk-notes")
	baseV2 := runGit(t, repo, "rev-parse", "HEAD")

	runGit(t, repo, "checkout", "-qb", "cand-cfix", baseV2)
	writeRepoFile("tools/tillr/core/core.go", "package core\n\nfunc Value() int { return 2 }\n")
	writeRepoFile("notes.txt", "resolved-trunk-and-candidate-notes\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "cand-cfix-unrelated")

	evidenceCfix, ranCfix := runLandingGate(origRun+"-cfix", baseV2, "item-request-demo")
	if len(ranCfix) != 0 {
		t.Fatalf("expected zero Go packages to re-run on unrelated-file -cfix, got %v", ranCfix)
	}
	gaPkgs := make(map[string]goaffected.EvidencePackage, len(evidenceCfix.Packages))
	for _, p := range evidenceCfix.Packages {
		gaPkgs[p.ImportPath] = p
	}
	wantPkgs := map[string]bool{
		"github.com/mschulkind-oss/tillr/core":  true,
		"github.com/mschulkind-oss/tillr-gate":  true,
		"github.com/mschulkind-oss/tillr-watch": true,
		"github.com/mschulkind-oss/tillr-view":  true,
	}
	if len(gaPkgs) != len(wantPkgs) {
		t.Fatalf("gaPkgs = %+v", gaPkgs)
	}
	for ip := range wantPkgs {
		p, ok := gaPkgs[ip]
		if !ok || !p.Reused || p.ElapsedMS != 0 {
			t.Fatalf("expected reused=true, elapsed_ms=0 for %s, got %+v", ip, p)
		}
	}

	// 3. Ordinary (non-cfix) run — even when the item slug contains "cfix" — never reuses -cfix verdicts.
	if goaffected.IsCfixRunID("run-item-cfix-slug-a1-n1") {
		t.Fatal("IsCfixRunID should be false for run-item-cfix-slug-a1-n1")
	}
	_, ranOrdinary := runLandingGate("run-item-cfix-slug-a1-n1", baseV2, "item-cfix-slug")
	if !reflect.DeepEqual(ranOrdinary, affectedGoMarkers) {
		t.Fatalf("ranOrdinary = %v, want %v", ranOrdinary, affectedGoMarkers)
	}

	// 4. Cross-item mismatch never reuses another item's verdicts.
	_, ranWrongItem := runLandingGate(origRun+"-cfix", baseV2, "item-request-other")
	if !reflect.DeepEqual(ranWrongItem, affectedGoMarkers) {
		t.Fatalf("ranWrongItem = %v, want %v", ranWrongItem, affectedGoMarkers)
	}

	// 5. Selective invalidation: conflict repair modifies config/view.txt (declared edge of tillr-view)
	// and tools/tillr/extra/extra.go (sibling package in tools/tillr not imported by core/gate/watch).
	// Only tillr-extra and tillr-view re-run; tillr-core, tillr-gate, and tillr-watch stay reused!
	writeRepoFile("config/view.txt", "v2-from-conflict-fix\n")
	writeRepoFile("tools/tillr/extra/extra.go", "package extra\n\nfunc Extra() int { return 20 }\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-qm", "cand-cfix-partial")

	_, ranPartial := runLandingGate(origRun+"-cfix-restart", baseV2, "item-request-demo")
	wantPartial := map[string]bool{"tillr-extra": true, "tillr-view": true}
	if !reflect.DeepEqual(ranPartial, wantPartial) {
		t.Fatalf("ranPartial = %v, want %v", ranPartial, wantPartial)
	}

	// 6. Hashed boundRunIDBase -cfix run resolves pre-conflict run via brief.md's "Its first run was <orig>,".
	hashedCfix := "run-item-request-demo-long-stem-0123456789abcdef-cfix"
	hashedDir := filepath.Join(loopDir, hashedCfix)
	if err := os.MkdirAll(hashedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hashedDir, "brief.md"),
		[]byte("# Conflict Fix\n- **Item:** item-request-demo\nIts first run was "+origRun+", which passed.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ranHashed := runLandingGate(hashedCfix, baseV2, "item-request-demo")
	if !reflect.DeepEqual(ranHashed, wantPartial) {
		t.Fatalf("ranHashed = %v, want %v", ranHashed, wantPartial)
	}

	// 7. Narrowed read_tests verdict does NOT satisfy a whole-package selection, while matching narrowed reuses.
	narrowPrior := &goaffected.PackageVerdicts{
		Schema: 1,
		Packages: map[string]goaffected.PackageVerdictEntry{
			"github.com/mschulkind-oss/tillr-view": {
				WholeDigests: []string{},
				Narrowed:     map[string]goaffected.NarrowedVerdict{},
			},
		},
	}
	repoGraph, err := goaffected.LoadTillrGraph(repo, nil)
	if err != nil {
		t.Fatalf("LoadTillrGraph: %v", err)
	}
	repoEdges, err := goaffected.LoadEdges(repo)
	if err != nil {
		t.Fatalf("LoadEdges: %v", err)
	}
	cache := goaffected.NewVerdictDigestCache()
	narrowDigest := goaffected.ComputePackageVerdictDigest(
		repo,
		"github.com/mschulkind-oss/tillr-view",
		repoGraph,
		repoEdges,
		[]string{"TestView"},
		cache,
	)
	if narrowDigest == "" {
		t.Fatal("expected non-empty narrowDigest")
	}
	narrowPrior.Packages["github.com/mschulkind-oss/tillr-view"].Narrowed["TestView"] = goaffected.NarrowedVerdict{
		Tests:  []string{"TestView"},
		Digest: narrowDigest,
	}
	if !goaffected.PackageVerdictReusable(
		repo,
		"github.com/mschulkind-oss/tillr-view",
		repoGraph,
		repoEdges,
		narrowPrior,
		[]string{"TestView"},
		cache,
	) {
		t.Fatal("expected narrowed verdict to be reusable for matching selected tests")
	}
	if goaffected.PackageVerdictReusable(
		repo,
		"github.com/mschulkind-oss/tillr-view",
		repoGraph,
		repoEdges,
		narrowPrior,
		nil,
		cache,
	) {
		t.Fatal("narrowed verdict must NOT satisfy whole-package selection")
	}
}
