package main_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// changeRecord is the `gate.change` record as a case reads it: the envelope's identity
// fields and the payload `plan` consumes.
type changeRecord struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Producer struct {
		System  string `json:"system"`
		Utility string `json:"utility"`
		Version string `json:"version"`
	} `json:"producer"`
	CreatedAt string `json:"created_at"`
	Payload   struct {
		HeadSHA      string   `json:"head_sha"`
		BaseSHA      string   `json:"base_sha"`
		TreeSHA      string   `json:"tree_sha"`
		ChangedPaths []string `json:"changed_paths"`
	} `json:"payload"`
}

// changeRepoGit runs one git read in a fixture checkout. The suite is an external test
// package, so it reads the repository the same way the face does rather than through the
// face's own helper: an assertion made with the code under test cannot fail.
func changeRepoGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// changePaths is the diff between two commits as a list, the way the record carries it.
func changePaths(t *testing.T, root, base, head string) []string {
	t.Helper()
	out := changeRepoGit(t, root, "diff", "--name-only", base+"..."+head)
	paths := []string{}
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

// TestTheChangeFaceDerivesTheCandidatesRecord is the face's whole job: a candidate in, the
// `gate.change` record `plan` reads out, derived from git rather than typed by the caller.
//
// The record's identity is `change-<run>` and its producer is this binary, so a record that
// says who derived it cannot be mistaken for the loop's Python spelling. `--item` and
// `--lane` are the loop's candidate argv and are deliberately not fields: the change is a
// function of the commit range, and a case that asserted them into the payload would pin a
// shape `plan` does not read.
func TestTheChangeFaceDerivesTheCandidatesRecord(t *testing.T) {
	root, base := fixtureRepo(t)
	head := changeRepoGit(t, root, "rev-parse", "HEAD")
	out := filepath.Join(scratch(t, "change-face"), "gate.change.json")
	code, raw := run(t, "change",
		"--item", "item-1", "--lane", "lane-1", "--run", "run-1",
		"--worktree", root, "--base", base, "--head", head,
		"--now", "2026-11-13T00:00:00Z", "--output", out)
	if code != 0 {
		t.Fatalf("the change face answered exit %d: %s", code, raw)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the face named an output it did not write: %v", err)
	}
	var record changeRecord
	if err := json.Unmarshal(written, &record); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, written)
	}
	if record.Kind != "gate.change" || record.ID != "change-run-1" {
		t.Fatalf("the record is %q/%q, want gate.change/change-run-1", record.Kind, record.ID)
	}
	if record.Producer.System != "tillr" || record.Producer.Utility != "gate" {
		t.Fatalf("the record's producer is %q/%q, want tillr/gate",
			record.Producer.System, record.Producer.Utility)
	}
	if record.CreatedAt != "2026-11-13T00:00:00Z" {
		t.Fatalf("the record's created_at is %q, not the clock the caller named", record.CreatedAt)
	}
	if record.Payload.BaseSHA != base {
		t.Fatalf("the record's base is %q, want the base the caller named (%q)",
			record.Payload.BaseSHA, base)
	}
	if record.Payload.HeadSHA != head {
		t.Fatalf("the record's head is %q, want commit %q", record.Payload.HeadSHA, head)
	}
	if want := changeRepoGit(t, root, "rev-parse", head+"^{tree}"); record.Payload.TreeSHA != want {
		t.Fatalf("the record's tree is %q, want head^{tree} (%q)", record.Payload.TreeSHA, want)
	}
	want := changePaths(t, root, base, head)
	if len(want) == 0 {
		t.Fatal("the fixture change names no path: the assertion below would hold vacuously")
	}
	if strings.Join(record.Payload.ChangedPaths, ",") != strings.Join(want, ",") {
		t.Fatalf("the record's changed paths are %v, want the diff between the two commits (%v)",
			record.Payload.ChangedPaths, want)
	}
	// The record is what `plan` reads, so the face is not done until the gate's own
	// consumer accepts it without an edit.
	planPath := filepath.Join(scratch(t, "change-face-plan"), "gate.plan.json")
	code, planned := run(t, "plan", "--registry", filepath.Join("testdata", "registry.json"),
		"--input", out, "--root", root, "--output", planPath)
	if code != 0 {
		t.Fatalf("plan refused the record the change face wrote (exit %d): %s", code, planned)
	}
}

// TestTheChangeFaceRefusesARequestItCannotDerive is the refusal half: a revision the
// worktree does not hold, and a directory that is not a checkout, both fail rather than
// write a record.
//
// The assertion is the absence of the output file, not the exit code alone: a face that
// wrote a record with an empty `changed_paths` and then answered non-zero would plan fewer
// checks than the change owes, and the plan would be a verdict about a tree nobody judged.
//
// The last block is the control: the same argv with the head that does exist writes the
// file, so the two refusals are the revisions rather than the invocation.
func TestTheChangeFaceRefusesARequestItCannotDerive(t *testing.T) {
	root, base := fixtureRepo(t)
	head := changeRepoGit(t, root, "rev-parse", "HEAD")
	dir := scratch(t, "change-refusals")

	unknownHead := filepath.Join(dir, "unknown-head.json")
	code, out := run(t, "change", "--item", "item-1", "--run", "run-1",
		"--worktree", root, "--base", base,
		"--head", "0000000000000000000000000000000000000000",
		"--output", unknownHead)
	if code == 0 {
		t.Fatalf("the face derived a change from a head the worktree does not hold: %s", out)
	}
	if _, err := os.Stat(unknownHead); err == nil {
		t.Fatalf("the face wrote %s for a request it refused: a partial record plans fewer "+
			"checks than the change owes", unknownHead)
	}

	notACheckout := filepath.Join(dir, "not-a-repository")
	if err := os.MkdirAll(notACheckout, 0o755); err != nil {
		t.Fatal(err)
	}
	nowhere := filepath.Join(dir, "not-a-checkout.json")
	code, out = run(t, "change", "--item", "item-1", "--run", "run-1",
		"--worktree", notACheckout, "--base", base, "--head", head, "--output", nowhere)
	if code == 0 {
		t.Fatalf("the face derived a change in a directory that is not a checkout: %s", out)
	}
	if _, err := os.Stat(nowhere); err == nil {
		t.Fatalf("the face wrote %s for a worktree it could not read", nowhere)
	}

	// The control: the same argv, the head that exists.
	writable := filepath.Join(dir, "derivable.json")
	if code, out := run(t, "change", "--item", "item-1", "--run", "run-1",
		"--worktree", root, "--base", base, "--head", head, "--output", writable); code != 0 {
		t.Fatalf("the control run was refused too (exit %d): %s", code, out)
	}
	if _, err := os.Stat(writable); err != nil {
		t.Fatalf("the control run wrote no record: %v", err)
	}
}

// loopModule is the loop's own Go module, relative to this package.
const loopModule = "../tillr-loop"

// TestTheLoopWritesTheChangeRecordTheFaceWrites is the one-shape acceptance, byte for byte.
//
// The loop used to spell `gate.change` in Python. It now runs `tillr-gate change`, so the
// file `plan` reads under the run's scratch is the face's own bytes; the comparison below
// is against the same face driven directly with the same candidate argv, which is what a
// caller that is not the loop would do. A difference in either direction is two shapes
// again, so the assertion is `bytes.Equal` rather than a field-by-field comparison.
func TestTheLoopWritesTheChangeRecordTheFaceWrites(t *testing.T) {
	if _, err := os.Stat(filepath.Join(loopModule, "go.mod")); err != nil {
		t.Skipf("the loop's module is not beside this package: %v", err)
	}
	loopBin := filepath.Join(scratch(t, "loop-bin"), "tillr-loop")
	build := exec.Command("go", "build", "-o", loopBin, ".")
	build.Dir = loopModule
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-loop: %v\n%s", err, out)
	}
	root, base := fixtureRepo(t)
	head := changeRepoGit(t, root, "rev-parse", "HEAD")
	now := "2026-11-13T00:00:00Z"
	work := scratch(t, "loop-change-work")
	registry := builtRegistry(t, nil, nil)

	cmd := exec.Command(loopBin, "gate",
		"--item", "item-1", "--lane", "lane-1", "--run", "run-1",
		"--worktree", root, "--base", base, "--head", head,
		"--root", root, "--store", filepath.Join(scratch(t, "loop-store"), "state.db"),
		"--work-dir", work, "--now", now,
		"--registry", registry, "--gate-program", "/bin/true",
		"--tillr-gate", gateBin)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "loop-state"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the loop's gate adapter failed: %v\n%s", err, out)
	}
	fromLoop, err := os.ReadFile(filepath.Join(work, "run-1", "gate.change.json"))
	if err != nil {
		t.Fatalf("the loop's run directory holds no change record: %v", err)
	}

	direct := filepath.Join(scratch(t, "face-change-direct"), "gate.change.json")
	if code, out := run(t, "change",
		"--item", "item-1", "--lane", "lane-1", "--run", "run-1",
		"--worktree", root, "--base", base, "--head", head,
		"--now", now, "--output", direct); code != 0 {
		t.Fatalf("the face refused the candidate the loop judged (exit %d): %s", code, out)
	}
	fromFace, err := os.ReadFile(direct)
	if err != nil {
		t.Fatalf("the face wrote no record: %v", err)
	}
	if !bytes.Equal(fromLoop, fromFace) {
		t.Fatalf("the loop and the face wrote two records for one candidate:\nloop: %s\nface: %s",
			fromLoop, fromFace)
	}
}
