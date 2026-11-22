package drain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
)

// loopResult is the part of a loop's terminal record these cases read: the stop it named,
// the fault's own words, and the status the process answered with.
type loopResult struct {
	Stop     string `json:"stop"`
	Detail   string `json:"detail"`
	ExitCode int    `json:"exit_code"`
}

// readLoopResult reads the one result file the loop left under root. A loop that wrote none
// fails the case: a run that died before it could print is the case this file exists for,
// and a missing file is the silence it is meant to end.
func readLoopResult(t *testing.T, root string) loopResult {
	t.Helper()
	dir := filepath.Join(root, ".tillr", "loop")
	matches, err := filepath.Glob(filepath.Join(dir, "drain-*.json"))
	if err != nil {
		t.Fatalf("looking for the loop's result under %s failed: %v", dir, err)
	}
	if len(matches) != 1 {
		t.Fatalf("the loop left %d result file(s) under %s, want 1: a loop that died before its first tick left no terminal record", len(matches), dir)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("reading %s failed: %v", matches[0], err)
	}
	env, err := record.Decode(raw)
	if err != nil {
		t.Fatalf("%s is not a decodable record: %v", matches[0], err)
	}
	if env.Kind != drain.LoopResultKind {
		t.Fatalf("%s is a %q record, want %q", matches[0], env.Kind, drain.LoopResultKind)
	}
	var result loopResult
	if err := json.Unmarshal(env.Payload, &result); err != nil {
		t.Fatalf("reading the loop result payload of %s failed: %v", matches[0], err)
	}
	return result
}

// A loop that cannot start records the fault it died on, not only four lines on a stderr
// nobody kept: the policy it could not read, the status it answered with, and the file an
// operator reads after the terminal is gone.
func TestALoopThatCannotStartRecordsTheFaultItDiedOn(t *testing.T) {
	root := scratch(t, "loop-startup")
	policy := filepath.Join(root, "config", "tillr", "backend-policy.json")

	code, out := invoke(t, "loop", "--root", root, "--policy", policy,
		"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")

	if code != 2 {
		t.Fatalf("a loop with an unreadable policy exited %d, want 2 (usage): %s", code, out)
	}
	if !strings.Contains(string(out), "reading the policy") {
		t.Fatalf("the diagnostic does not name the policy it could not read: %s", out)
	}
	result := readLoopResult(t, root)
	if result.Stop != "fault" {
		t.Errorf("the record's stop is %q, want %q", result.Stop, "fault")
	}
	if result.ExitCode != 2 {
		t.Errorf("the record's exit_code is %d, want 2", result.ExitCode)
	}
	if !strings.Contains(result.Detail, policy) {
		t.Errorf("the record's detail %q does not name the policy %s", result.Detail, policy)
	}
	if !strings.Contains(result.Detail, "no such file") {
		t.Errorf("the record's detail %q does not name the refusal", result.Detail)
	}
}

// A first tick that cannot open its store is the other way a loop dies before it has done
// anything: the record names the store and the incompatible-store status, which is the fact
// the terminal's four lines would have taken with them.
func TestALoopWhoseFirstTickCannotOpenItsStoreRecordsTheFault(t *testing.T) {
	fixture := newLoopFixture(t, stub(t, "adapter", "exit 0\n"))
	policyPath := filepath.Join(scratch(t, "loop-startup-policy"), "policy.json")
	raw, err := json.Marshal(fixture.policy)
	if err != nil {
		t.Fatalf("encoding the policy failed: %v", err)
	}
	writeFile(t, policyPath, string(raw))
	// A directory where the store belongs is the one way a store this face creates on
	// demand cannot be opened: it exists, and it is not a file.
	storePath := filepath.Join(scratch(t, "loop-startup-store"), "store")
	if err := os.MkdirAll(storePath, 0o755); err != nil {
		t.Fatalf("creating the store directory failed: %v", err)
	}

	// The item binary is the one built beside this module: the loop syncs the documents
	// before its first tick, and the `tillr-item` on PATH is whatever the machine installed.
	code, out := invoke(t, "loop", "--root", fixture.root, "--store", storePath,
		"--item-bin", sibling(t, "item"),
		"--lane-root", fixture.laneRoot, "--policy", policyPath,
		"--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true")

	if code != 3 {
		t.Fatalf("a loop whose store cannot be opened exited %d, want 3 (incompatible store): %s", code, out)
	}
	result := readLoopResult(t, fixture.root)
	if result.Stop != "fault" {
		t.Errorf("the record's stop is %q, want %q", result.Stop, "fault")
	}
	if result.ExitCode != 3 {
		t.Errorf("the record's exit_code is %d, want 3", result.ExitCode)
	}
	if !strings.Contains(result.Detail, storePath) {
		t.Errorf("the record's detail %q does not name the store %s", result.Detail, storePath)
	}
}
