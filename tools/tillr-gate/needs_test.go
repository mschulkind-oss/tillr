package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingRefUnavailableBeforeMemoryOrCommand(t *testing.T) {
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Needs = []string{"ref:refs/heads/patch/pi"}
	// A tool that does not exist must not be probed before the missing need.
	check.Tools = []string{"missing-tool-for-unavailable-check"}
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Result != "unavailable" {
		t.Fatalf("missing ref: %+v %v", got, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command ran: %v", err)
	}
	if len(memoryFiles(t, memoryStore(t, root))) != 0 {
		t.Fatal("unavailable was remembered")
	}
}

func TestRefPatternFingerprintTracksMembership(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	check.Needs = []string{"ref:refs/heads/patch/*"}
	memoryGit(t, root, "update-ref", "refs/heads/patch/one", "HEAD")
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Memory != "passed" {
		t.Fatalf("first: %+v %v", first, err)
	}
	memoryGit(t, root, "update-ref", "refs/heads/patch/two", "HEAD")
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "passed" || second.Fingerprint == first.Fingerprint {
		t.Fatalf("added match: %+v %v", second, err)
	}
	memoryGit(t, root, "update-ref", "-d", "refs/heads/patch/one")
	third, err := memoryRun(t, root, tree, check)
	if err != nil || third.Memory != "passed" || third.Fingerprint == second.Fingerprint {
		t.Fatalf("removed match: %+v %v", third, err)
	}
}

func TestRefNeedRejectsMalformedPatternAndExactPrefixMatch(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	for _, need := range []string{"ref:patch/pi", "ref:refs/heads/patch/pi-?", "ref:refs/heads/patch/pi-[ab]", "ref:refs/heads/patch/**", "ref:refs/heads/patch/"} {
		check.Needs = []string{need}
		got, err := memoryRun(t, root, tree, check)
		if err == nil || got.Result != ResultError {
			t.Fatalf("malformed ref %q: %+v %v", need, got, err)
		}
	}
	memoryGit(t, root, "update-ref", "refs/heads/patch/pi/other", "HEAD")
	check.Needs = []string{"ref:refs/heads/patch/pi"}
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Result != ResultUnavailable {
		t.Fatalf("prefix matched exact ref: %+v %v", got, err)
	}
}

func TestNeededRefChangedDuringCommandIsNotRemembered(t *testing.T) {
	root, tree := memoryRepo(t)
	memoryGit(t, root, "update-ref", "refs/heads/patch/pi", "HEAD")
	commit := memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit-tree", tree, "-p", "HEAD", "-m", "moved ref")
	check := memoryCheck(filepath.Join(t.TempDir(), "marker"))
	check.Needs = []string{"ref:refs/heads/patch/pi"}
	check.Command = []string{"git", "update-ref", "refs/heads/patch/pi", commit}
	got, err := memoryRun(t, root, tree, check)
	if err != nil || got.Result != ResultPass || got.Memory != "not remembered" {
		t.Fatalf("mid-command move: %+v %v", got, err)
	}
	if len(memoryFiles(t, memoryStore(t, root))) != 0 {
		t.Fatal("result remembered under stale ref")
	}
}

func TestNeededRefMovesInvalidateMemory(t *testing.T) {
	root, tree := memoryRepo(t)
	ref := "refs/heads/patch/pi"
	memoryGit(t, root, "update-ref", ref, "HEAD")
	marker := filepath.Join(t.TempDir(), "marker")
	check := memoryCheck(marker)
	check.Needs = []string{"ref:" + ref}
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Memory != "passed" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "remembered" {
		t.Fatalf("second: %+v %v", second, err)
	}
	parent := memoryGit(t, root, "rev-parse", "HEAD")
	commit := memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit-tree", tree, "-p", parent, "-m", "moved ref")
	memoryGit(t, root, "update-ref", ref, commit)
	third, err := memoryRun(t, root, tree, check)
	if err != nil || third.Memory != "passed" || third.Fingerprint == first.Fingerprint {
		t.Fatalf("moved: %+v %v", third, err)
	}
	raw, _ := os.ReadFile(marker)
	if strings.Count(string(raw), "ran\n") != 2 {
		t.Fatalf("runs: %q", raw)
	}
	memoryGit(t, root, "update-ref", "-d", ref)
	missing, err := memoryRun(t, root, tree, check)
	if err != nil || missing.Result != "unavailable" {
		t.Fatalf("removed ref reused memory: %+v %v", missing, err)
	}
}
