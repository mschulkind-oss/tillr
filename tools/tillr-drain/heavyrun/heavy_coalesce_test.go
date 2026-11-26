package heavyrun

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCoalesceChecksByFailure(t *testing.T) {
	dir := t.TempDir()
	logA := filepath.Join(dir, "go-test-gate.log")
	logB := filepath.Join(dir, "tree-checks-test.log")
	logDiffLine := filepath.Join(dir, "other-line.log")
	logMulti := filepath.Join(dir, "multi.log")

	if err := os.WriteFile(logA, []byte(
		"=== RUN   TestTreeDeclarationReadingGoModuleReadsInRepoDependencies\n"+
			"--- FAIL: TestTreeDeclarationReadingGoModuleReadsInRepoDependencies (0.04s)\n"+
			"    tree_checks_test.go:456: go-test-tillr-cli.json: missing Go dependencies in reads\n"+
			"FAIL\n"+
			"go-test-shards: shard 2 failed (exit 1)\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logB, []byte(
		"--- FAIL: TestTreeDeclarationReadingGoModuleReadsInRepoDependencies (0.02s)\n"+
			"    tree_checks_test.go:456: go-test-tillr-cli.json: missing Go dependencies in reads\n"+
			"FAIL\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logDiffLine, []byte(
		"--- FAIL: TestTreeDeclarationReadingGoModuleReadsInRepoDependencies (0.02s)\n"+
			"    tree_checks_test.go:999: different failure\n"+
			"FAIL\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logMulti, []byte(
		"--- FAIL: TestOne (0.01s)\n"+
			"--- FAIL: TestTwo (0.01s)\n"+
			"    tree_checks_test.go:456: go-test-tillr-cli.json: missing Go dependencies in reads\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	byLanding := map[string][]HeavyCheck{
		"sha1": {{CheckID: "go-test-gate", LogPath: logA}},
		"sha2": {
			{CheckID: "tree-checks-test", LogPath: logB},
			{CheckID: "unrelated-check", LogPath: logDiffLine},
		},
		"sha3": {{CheckID: "multi-check", LogPath: logMulti}},
	}
	order := coalesceChecksByFailure([]string{"sha1", "sha2", "sha3"}, byLanding)

	wantOrder := []string{"sha1", "sha2", "sha3"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order=%v, want %v", order, wantOrder)
	}
	if len(byLanding["sha1"]) != 2 || byLanding["sha1"][0].CheckID != "go-test-gate" || byLanding["sha1"][1].CheckID != "tree-checks-test" {
		t.Fatalf("byLanding[sha1]=%+v, want go-test-gate and tree-checks-test", byLanding["sha1"])
	}
	if len(byLanding["sha2"]) != 1 || byLanding["sha2"][0].CheckID != "unrelated-check" {
		t.Fatalf("byLanding[sha2]=%+v, want only unrelated-check", byLanding["sha2"])
	}
	if len(byLanding["sha3"]) != 1 || byLanding["sha3"][0].CheckID != "multi-check" {
		t.Fatalf("byLanding[sha3]=%+v, want only multi-check", byLanding["sha3"])
	}

	// When sha2 has no other checks, sha2 is removed from order.
	byLandingOnly := map[string][]HeavyCheck{
		"sha1": {{CheckID: "go-test-gate", LogPath: logA}},
		"sha2": {{CheckID: "tree-checks-test", LogPath: logB}},
	}
	orderOnly := coalesceChecksByFailure([]string{"sha1", "sha2"}, byLandingOnly)
	if !reflect.DeepEqual(orderOnly, []string{"sha1"}) {
		t.Fatalf("orderOnly=%v, want [sha1]", orderOnly)
	}
	if len(byLandingOnly["sha1"]) != 2 {
		t.Fatalf("byLandingOnly[sha1]=%+v, want 2 checks", byLandingOnly["sha1"])
	}
}
