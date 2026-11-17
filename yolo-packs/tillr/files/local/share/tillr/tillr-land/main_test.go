package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

var bin string

// Scratch lives under $YOLO_DURABLE_DIR, never /tmp: a suite that writes to /tmp loses its
// evidence when the box is cleaned, and the owner refuses it.
func scratchRoot() string {
	if root := os.Getenv("YOLO_DURABLE_DIR"); root != "" {
		return root
	}
	return filepath.Join("..", "..", ".yolo", "durable")
}

func scratch(t *testing.T, prefix string) string {
	t.Helper()
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, prefix+"-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns before TestMain exits, so cleanup also runs after setup or test failure.
func runTests(m *testing.M) int {
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 1
	}
	dir, err := os.MkdirTemp(root, "tillr-land-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "tillr-land")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}
	return m.Run()
}

// The identity a fixture repository deploys. It is the value every fixture commit is made
// with, so a squash that publishes the deployed identity is visible as a commit whose
// author and committer are this.
const (
	fixtureName  = "tillr"
	fixtureEmail = "tillr@example.invalid"
)

// deployedConfig writes the gitconfig a fixture landing reads and answers its path.
// GIT_CONFIG_GLOBAL is the scope a landing reads: a fixture that wrote the identity with
// `git config --local` would be refused as the override a lane can set, which is the
// opposite of what a fixture repository is.
func deployedConfig(t *testing.T, name, email string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "gitconfig"), "gitconfig")
	body := ""
	if name != "" || email != "" {
		body = "[user]\n\tname = " + name + "\n\temail = " + email + "\n"
	}
	write(t, path, body)
	return path
}

// fixtureEnv is the identity environment a fixture face runs with: the deployed config a
// landing reads, and the same identity in the commit variables so a fixture commit is
// authored by it.
func fixtureEnv(t *testing.T) []string {
	return []string{
		"GIT_CONFIG_GLOBAL=" + deployedConfig(t, fixtureName, fixtureEmail),
		"GIT_AUTHOR_NAME=" + fixtureName, "GIT_AUTHOR_EMAIL=" + fixtureEmail,
		"GIT_COMMITTER_NAME=" + fixtureName, "GIT_COMMITTER_EMAIL=" + fixtureEmail,
	}
}

// run drives the binary with the state home scrubbed, so a face that derives a store path
// instead of taking --store cannot reach the owner's board. The identity is set explicitly
// because a landing publishes the configured identity, and a landing must not depend on
// whose git config happens to be on the box.
func run(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	return runAs(t, fixtureEnv(t), args...)
}

// runAs drives the binary with the caller's identity environment, for the cases that judge
// what a repository deploys. The identity variables this process inherited are removed
// first, so the caller's list is the whole identity a face can read.
func runAs(t *testing.T, identity []string, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(append(withoutIdentity(os.Environ()),
		"BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state")), identity...)
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out
}

// withoutIdentity is an environment without the variables that state an identity or a
// config file, so a case's own environment is the only one a face reads.
func withoutIdentity(env []string) []string {
	kept := env[:0]
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GIT_AUTHOR_") || strings.HasPrefix(key, "GIT_COMMITTER_") ||
			key == "GIT_CONFIG_GLOBAL" || key == "GIT_CONFIG_SYSTEM" {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// git runs one git command in a fixture repository and answers its stdout. The repository
// variables are removed rather than blanked: git reads an empty GIT_DIR as a path and
// refuses it, so `GIT_DIR=` is not the same as unset.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitAs(t, dir, fixtureName, fixtureEmail, args...)
}

// gitAs runs one git command as another identity, which is how a fixture builds the lane
// commit a landing publishes over.
func gitAs(t *testing.T, dir, name, email string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME="+name, "GIT_AUTHOR_EMAIL="+email,
		"GIT_COMMITTER_NAME="+name, "GIT_COMMITTER_EMAIL="+email)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = string(exit.Stderr)
		}
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, stderr)
	}
	return strings.TrimSpace(string(out))
}

// fixture is a repository with a trunk at baseSHA and a candidate commit headSHA the trunk
// has not taken. It is the whole of what land needs from a repository: a ref, the commit it
// is at, and a commit to land.
type fixture struct {
	dir     string
	ref     string
	baseSHA string
	headSHA string
	treeSHA string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dir := filepath.Join(scratch(t, "repo"), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cannot create the fixture repository: %v", err)
	}
	git(t, dir, "init", "-b", "master")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	git(t, dir, "add", "base.txt")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "checkout", "-b", "candidate")
	write(t, filepath.Join(dir, "candidate.txt"), "candidate\n")
	git(t, dir, "add", "candidate.txt")
	git(t, dir, "commit", "-m", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")

	// The trunk goes back to base: the candidate commit exists and the trunk has not
	// taken it, which is the state every landing starts from.
	git(t, dir, "checkout", "master")
	return fixture{dir: dir, ref: "master", baseSHA: base, headSHA: head, treeSHA: tree}
}

// newConflictFixture is a repository whose candidate and trunk changed the same line: the
// change has no tree the verdict could be carried to.
func newConflictFixture(t *testing.T) fixture {
	t.Helper()
	dir := filepath.Join(scratch(t, "repo"), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cannot create the fixture repository: %v", err)
	}
	git(t, dir, "init", "-b", "master")
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	git(t, dir, "add", "base.txt")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "checkout", "-b", "candidate")
	write(t, filepath.Join(dir, "base.txt"), "candidate\n")
	git(t, dir, "commit", "-am", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")

	git(t, dir, "checkout", "master")
	write(t, filepath.Join(dir, "base.txt"), "trunk\n")
	git(t, dir, "commit", "-am", "trunk")
	return fixture{dir: dir, ref: "master", baseSHA: base, headSHA: head, treeSHA: tree}
}

// advance moves the trunk to a commit the candidate did not come from, which is what a
// concurrent landing looks like to this one.
func (f fixture) advance(t *testing.T) string {
	t.Helper()
	write(t, filepath.Join(f.dir, "other.txt"), "other\n")
	git(t, f.dir, "add", "other.txt")
	git(t, f.dir, "commit", "-m", "other")
	return git(t, f.dir, "rev-parse", f.ref)
}

func (f fixture) trunk(t *testing.T) string {
	t.Helper()
	return git(t, f.dir, "rev-parse", f.ref)
}

// gitEnv is this process's environment without the repository variables, so a fixture
// repository is the one the command is pointed at and not the one the test process happens
// to be in.
func gitEnv() []string {
	env := os.Environ()
	kept := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

// envelope wraps a payload in design §5.1's envelope. The clock is the caller's, so two
// runs of the same case produce the same bytes.
func envelope(t *testing.T, kind, id string, payload any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"record_version": 1,
		"kind":           kind,
		"id":             id,
		"producer":       map[string]string{"system": "tillr", "utility": "gate", "version": "0.1.0"},
		"created_at":     "2026-11-10T00:00:00Z",
		"payload":        payload,
	})
	if err != nil {
		t.Fatalf("building the input record failed: %v", err)
	}
	return string(raw)
}

func input(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "input"), "record.json")
	write(t, path, body)
	return path
}

// verdict is design §5.7's gate.verdict payload, under Assumption A-VERDICT: the field list
// verbatim, nested under the envelope's payload.
func (f fixture) verdict(result string) map[string]any {
	return map[string]any{
		"verdict_id":      "verdict_001",
		"plan_id":         "plan_001",
		"registry_digest": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"base_sha":        f.baseSHA,
		"tree_sha":        f.treeSHA,
		"result":          result,
		"checks": []map[string]any{
			{"check_id": "go-test", "result": result, "duration_ms": 1200, "log_ref": "logs/go-test.log"},
			{"check_id": "fork-build", "result": result, "duration_ms": 1, "log_ref": "logs/fork-build.log"},
		},
	}
}

// verdictFile writes the gate.verdict record `pass` consumes and answers its path.
func (f fixture) verdictFile(t *testing.T, result string) string {
	t.Helper()
	return input(t, envelope(t, "gate.verdict", "verdict_001", f.verdict(result)))
}

// land drives `pass` for this fixture, with one run id and one verdict result, and answers
// the store path, the exit code and the record.
func (f fixture) land(t *testing.T, runID, result string, extra ...string) (string, int, []byte) {
	t.Helper()
	db := filepath.Join(scratch(t, "store"), "state.db")
	args := []string{
		"pass", "--store", db, "--repo", f.dir,
		"--item", "item_042", "--run", runID,
		"--base", f.baseSHA, "--head", f.headSHA,
		"--verdict", f.verdictFile(t, result),
		"--now", "2026-11-10T00:00:00Z",
	}
	args = append(args, extra...)
	code, out := run(t, args...)
	return db, code, out
}

// landingOut is the `land.landing` record `pass` emits, decoded.
type landingOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		LandingID         string   `json:"landing_id"`
		ItemID            string   `json:"item_id"`
		RunID             string   `json:"run_id"`
		VerdictID         string   `json:"verdict_id"`
		Result            string   `json:"result"`
		Reason            string   `json:"reason"`
		BaseSHA           string   `json:"base_sha"`
		HeadSHA           string   `json:"head_sha"`
		TrunkBefore       string   `json:"trunk_before"`
		TrunkAfter        string   `json:"trunk_after"`
		GatedTree         string   `json:"gated_tree"`
		LandedTree        string   `json:"landed_tree"`
		ConflictPaths     []string `json:"conflict_paths"`
		GateSkippedReason string   `json:"gate_skipped_reason"`
	} `json:"payload"`
}

func decodeLanding(t *testing.T, out []byte) landingOut {
	t.Helper()
	var got landingOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
	return got
}

// eventsOfKind counts the events a store holds for one kind, through the substrate's
// reader.
func eventsOfKind(t *testing.T, db, kind string) int {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// eventOfKind answers the store's one event of a kind, through the substrate's reader, and
// fails the case when the store holds none.
func eventOfKind(t *testing.T, db, kind string) event.Event {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	for _, e := range events {
		if e.Kind == kind {
			return e
		}
	}
	t.Fatalf("the store holds no %s event", kind)
	return event.Event{}
}

// rowCount answers a count through the substrate's reader, never by opening the database.
func rowCount(t *testing.T, db, table string) int {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	var n int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow("SELECT count(*) FROM " + table).Scan(&n)
	}); err != nil {
		t.Fatalf("counting %s failed: %v", table, err)
	}
	return n
}

// landingRow answers one landing row, selected by run, through the substrate's reader.
func landingRow(t *testing.T, db, runID string) (result, reason, trunkBefore, trunkAfter, landedTree, conflicts string) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT result, reason, trunk_before, trunk_after, landed_tree, conflict_paths
			   FROM landings WHERE run_id = ?`, runID,
		).Scan(&result, &reason, &trunkBefore, &trunkAfter, &landedTree, &conflicts)
	}); err != nil {
		t.Fatalf("reading the landing of %q failed: %v", runID, err)
	}
	return result, reason, trunkBefore, trunkAfter, landedTree, conflicts
}

// TestPassLandsAJudgedHead is the whole landing: one row, one event, one squashed commit
// whose tree is the one the verdict judged and whose message names the item.
func TestPassLandsAJudgedHead(t *testing.T) {
	f := newFixture(t)
	db, code, out := f.land(t, "run-1", "pass")
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Kind != "land.landing" {
		t.Fatalf("the record is %q, want land.landing", got.Kind)
	}
	if got.Payload.Result != "landed" || got.Payload.Reason != "" {
		t.Fatalf("landing is %s/%s, want landed with no reason", got.Payload.Result, got.Payload.Reason)
	}
	if got.Payload.TrunkBefore != f.baseSHA {
		t.Fatalf("trunk_before is %s, want the base %s", got.Payload.TrunkBefore, f.baseSHA)
	}
	if got.Payload.TrunkAfter == f.baseSHA || got.Payload.TrunkAfter == f.headSHA {
		t.Fatalf("trunk_after is %s, want one squashed commit", got.Payload.TrunkAfter)
	}
	if got.Payload.GatedTree != f.treeSHA || got.Payload.LandedTree != f.treeSHA {
		t.Fatalf("gated/landed tree are %s/%s, want %s", got.Payload.GatedTree, got.Payload.LandedTree, f.treeSHA)
	}
	if f.trunk(t) != got.Payload.TrunkAfter {
		t.Fatalf("the trunk is at %s, the landing says %s", f.trunk(t), got.Payload.TrunkAfter)
	}
	if tree := git(t, f.dir, "rev-parse", f.ref+"^{tree}"); tree != f.treeSHA {
		t.Fatalf("the trunk tree is %s, want the judged tree %s", tree, f.treeSHA)
	}
	message := git(t, f.dir, "log", "-1", "--format=%B", f.ref)
	if strings.Contains(message, "Tillr-Item") || strings.Contains(message, "item_042") {
		t.Fatalf("the landed commit names its item:\n%s", message)
	}
	if n := rowCount(t, db, "landings"); n != 1 {
		t.Fatalf("the store holds %d landings, want 1", n)
	}
	if n := eventsOfKind(t, db, "land.landing"); n != 1 {
		t.Fatalf("the store holds %d land.landing events, want 1", n)
	}
}

// TestAMovedTrunkSquashesTheChangeOntoTheNewTip: a trunk that moved off the base does not
// refuse a passing verdict. The change is squashed onto the new tip, and the landing
// records that the tree the verdict judged is not the tree the trunk published.
func TestAMovedTrunkSquashesTheChangeOntoTheNewTip(t *testing.T) {
	f := newFixture(t)
	moved := f.advance(t)
	_, code, out := f.land(t, "run-1", "pass")
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "landed" {
		t.Fatalf("landing is %s/%s, want landed", got.Payload.Result, got.Payload.Reason)
	}
	if got.Payload.TrunkBefore != moved {
		t.Fatalf("trunk_before is %s, want the moved tip %s", got.Payload.TrunkBefore, moved)
	}
	if got.Payload.LandedTree == got.Payload.GatedTree {
		t.Fatalf("the landing published the judged tree %s, want a squashed one", got.Payload.GatedTree)
	}
	// Both the concurrent change and the candidate's change are on the trunk.
	if blob := git(t, f.dir, "rev-parse", f.ref+":other.txt"); blob == "" {
		t.Fatal("the concurrent change is gone from the trunk")
	}
	if blob := git(t, f.dir, "rev-parse", f.ref+":candidate.txt"); blob == "" {
		t.Fatal("the candidate's change did not reach the trunk")
	}
}

// TestASquashConflictIsRecordedAndPinsTheHead: a change that conflicts with the moved trunk
// lands nothing, records the conflict on the landing row, and pins the head so it survives.
func TestASquashConflictIsRecordedAndPinsTheHead(t *testing.T) {
	f := newConflictFixture(t)
	db, code, out := f.land(t, "run-1", "pass")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "squash-conflict" {
		t.Fatalf("landing is %s/%s, want squash-conflict", got.Payload.Result, got.Payload.Reason)
	}
	if len(got.Payload.ConflictPaths) == 0 {
		t.Fatal("the squash conflict named no path")
	}
	result, _, _, _, landedTree, conflicts := landingRow(t, db, "run-1")
	if result != "squash-conflict" || landedTree != "" {
		t.Fatalf("the row is %s/%q, want squash-conflict and no landed tree", result, landedTree)
	}
	if !strings.Contains(conflicts, "base.txt") {
		t.Fatalf("the row's conflict paths are %q, want base.txt", conflicts)
	}
	if pinned := git(t, f.dir, "rev-parse", "refs/tillr/runs/run-1"); pinned != f.headSHA {
		t.Fatalf("refs/tillr/runs/run-1 is %s, want the head %s", pinned, f.headSHA)
	}
	if f.trunk(t) == f.headSHA {
		t.Fatal("a conflicted landing moved the trunk")
	}
}

// TestAVerdictThatDoesNotBindTheHeadsTreeIsRefused: a verdict for another tree is no
// judgment about this head, so nothing moves and the head is pinned.
func TestAVerdictThatDoesNotBindTheHeadsTreeIsRefused(t *testing.T) {
	f := newFixture(t)
	other := f.verdict("pass")
	other["tree_sha"] = strings.Repeat("0", 40)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042",
		"--run", "run-1", "--base", f.baseSHA, "--head", f.headSHA,
		"--verdict", input(t, envelope(t, "gate.verdict", "verdict_001", other)),
		"--now", "2026-11-10T00:00:00Z")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "refused" || got.Payload.Reason != "verdict-mismatch" {
		t.Fatalf("landing is %s/%s, want refused/verdict-mismatch", got.Payload.Result, got.Payload.Reason)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a mismatched verdict moved the trunk")
	}
	if pinned := git(t, f.dir, "rev-parse", "refs/tillr/runs/run-1"); pinned != f.headSHA {
		t.Fatalf("refs/tillr/runs/run-1 is %s, want the head %s", pinned, f.headSHA)
	}
}

// TestAVerdictThatDoesNotBindTheBaseIsRefused.
func TestAVerdictThatDoesNotBindTheBaseIsRefused(t *testing.T) {
	f := newFixture(t)
	other := f.verdict("pass")
	other["base_sha"] = strings.Repeat("0", 40)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out := run(t, "pass", "--store", db, "--repo", f.dir, "--item", "item_042",
		"--run", "run-1", "--base", f.baseSHA, "--head", f.headSHA,
		"--verdict", input(t, envelope(t, "gate.verdict", "verdict_001", other)),
		"--now", "2026-11-10T00:00:00Z")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	if got := decodeLanding(t, out); got.Payload.Reason != "verdict-mismatch" {
		t.Fatalf("reason is %q, want verdict-mismatch", got.Payload.Reason)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a mismatched verdict moved the trunk")
	}
}

// TestAFailedVerdictLandsNothing: a completed negative verdict is a refusal, and the trunk
// does not move.
func TestAFailedVerdictLandsNothing(t *testing.T) {
	f := newFixture(t)
	_, code, out := f.land(t, "run-1", "fail")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "refused" || got.Payload.Reason != "verdict-failed" {
		t.Fatalf("landing is %s/%s, want refused/verdict-failed", got.Payload.Result, got.Payload.Reason)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a failed verdict moved the trunk")
	}
}

// TestAnErrorVerdictIsRefusedRatherThanReadAsAFailure: §5.7 says an error verdict is never
// a failure of the change, so it must not read as one.
func TestAnErrorVerdictIsRefusedRatherThanReadAsAFailure(t *testing.T) {
	f := newFixture(t)
	_, code, out := f.land(t, "run-1", "error")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "refused" || got.Payload.Reason != "verdict-error" {
		t.Fatalf("landing is %s/%s, want refused/verdict-error", got.Payload.Result, got.Payload.Reason)
	}
}

// TestALandingRefusesARepositoryThatConfiguresNoIdentity.
func TestALandingRefusesARepositoryThatConfiguresNoIdentity(t *testing.T) {
	f := newFixture(t)
	identity := []string{"GIT_CONFIG_GLOBAL=" + deployedConfig(t, "", "")}
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out := runAs(t, identity, "pass", "--store", db, "--repo", f.dir, "--item", "item_042",
		"--run", "run-1", "--base", f.baseSHA, "--head", f.headSHA,
		"--verdict", f.verdictFile(t, "pass"), "--now", "2026-11-10T00:00:00Z")
	if code != 4 {
		t.Fatalf("pass exited %d, want 4: %s", code, out)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a landing with no identity moved the trunk")
	}
}

// TestALandingRefusesARepositoryLocalIdentityOverride: the identity a landing publishes is
// the value a lane cannot write, and a local override is exactly that value.
func TestALandingRefusesARepositoryLocalIdentityOverride(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "config", "--local", "user.name", "lane")
	git(t, f.dir, "config", "--local", "user.email", "lane@example.invalid")
	_, code, out := f.land(t, "run-1", "pass")
	if code != 4 {
		t.Fatalf("pass exited %d, want 4: %s", code, out)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a landing with a lane-writable identity moved the trunk")
	}
}

// TestAPatchBranchLandingAdvancesThePatchRef: a fork-series delivery moves the patch branch
// rather than the trunk.
func TestAPatchBranchLandingAdvancesThePatchRef(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.baseSHA)
	cache := filepath.Join(f.dir, ".forks/pi")
	git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
	git(t, cache, "checkout", "-b", "main", f.baseSHA)
	db, code, out := f.land(t, "run-1", "pass", "--base-ref", "patch/pi")
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "landed" {
		t.Fatalf("landing is %s/%s, want landed", got.Payload.Result, got.Payload.Reason)
	}
	if got.Payload.TrunkAfter != f.headSHA {
		t.Fatalf("fork delivery rewrote the built head: got %s want %s", got.Payload.TrunkAfter, f.headSHA)
	}
	if moved := git(t, f.dir, "rev-parse", "refs/heads/patch/pi"); moved != got.Payload.TrunkAfter {
		t.Fatalf("patch/pi is at %s, want %s", moved, got.Payload.TrunkAfter)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a patch-branch landing moved the trunk")
	}
	if n := rowCount(t, db, "landings"); n != 1 {
		t.Fatalf("the store holds %d landings, want 1", n)
	}
}

// TestAPatchBranchAtTheHeadIsRefused: the run's own export delivered the head, so a second
// move would deliver one change twice.
func TestAPatchBranchAtTheHeadIsRefused(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.headSHA)
	_, code, out := f.land(t, "run-1", "pass", "--base-ref", "patch/pi")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	if got := decodeLanding(t, out); got.Payload.Reason != "patch-at-head" {
		t.Fatalf("reason is %q, want patch-at-head", got.Payload.Reason)
	}
}

// TestAPatchBranchThatRewindsIsRefused: a head that does not descend from the branch's tip
// would drop the commits between them.
func TestAPatchBranchThatRewindsIsRefused(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "checkout", "-B", "patch/pi", f.headSHA)
	write(t, filepath.Join(f.dir, "later.txt"), "later\n")
	git(t, f.dir, "add", "later.txt")
	git(t, f.dir, "commit", "-m", "later")
	git(t, f.dir, "checkout", f.ref)
	_, code, out := f.land(t, "run-1", "pass", "--base-ref", "patch/pi")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	if got := decodeLanding(t, out); got.Payload.Reason != "patch-rewind" {
		t.Fatalf("reason is %q, want patch-rewind", got.Payload.Reason)
	}
}

// TestALandingMovesTheTrunkWorktree: a landing is a write to the repository, not to a ref,
// so the worktree that has the trunk checked out matches it afterwards.
func TestALandingMovesTheTrunkWorktree(t *testing.T) {
	f := newFixture(t)
	if _, code, out := f.land(t, "run-1", "pass"); code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	if status := git(t, f.dir, "status", "--porcelain"); status != "" {
		t.Fatalf("the trunk worktree is dirty after a landing:\n%s", status)
	}
	if head := git(t, f.dir, "rev-parse", "HEAD"); head != f.trunk(t) {
		t.Fatalf("HEAD is %s, the trunk is %s", head, f.trunk(t))
	}
}

// TestALandingLeavesUncommittedContentOnAnUntouchedPathAlone.
func TestALandingLeavesUncommittedContentOnAnUntouchedPathAlone(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.dir, "untouched.txt"), "mine\n")
	if _, code, out := f.land(t, "run-1", "pass"); code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	if body, err := os.ReadFile(filepath.Join(f.dir, "untouched.txt")); err != nil || string(body) != "mine\n" {
		t.Fatalf("the untouched file is %q, %v", body, err)
	}
}

// TestALandingIsRefusedWhenTheTrunkWorktreeHoldsUncommittedContentOnALandedPath: a
// fast-forward would stop on the untracked file, and a landing that went through anyway
// would leave it mixed into the trunk. The refusal is recorded on the landing row.
func TestALandingIsRefusedWhenTheTrunkWorktreeHoldsUncommittedContentOnALandedPath(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.dir, "candidate.txt"), "mine\n")
	db, code, out := f.land(t, "run-1", "pass")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "refused" || got.Payload.Reason != "trunk-worktree-uncommitted" {
		t.Fatalf("landing is %s/%s, want refused/trunk-worktree-uncommitted",
			got.Payload.Result, got.Payload.Reason)
	}
	if len(got.Payload.ConflictPaths) == 0 || !strings.Contains(got.Payload.ConflictPaths[0], "candidate.txt") {
		t.Fatalf("the refusal named no clashing path: %v", got.Payload.ConflictPaths)
	}
	if result, _, _, _, _, _ := landingRow(t, db, "run-1"); result != "refused" {
		t.Fatalf("the row is %s, want refused", result)
	}
	if body, err := os.ReadFile(filepath.Join(f.dir, "candidate.txt")); err != nil || string(body) != "mine\n" {
		t.Fatalf("the uncommitted file is %q, %v", body, err)
	}
}

// TestALandingIntoARepositoryWithNoTrunkWorktreeMovesOnlyTheRef: with no worktree holding
// the branch, the guarded update-ref is the whole of the move.
func TestALandingIntoARepositoryWithNoTrunkWorktreeMovesOnlyTheRef(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "checkout", "--detach", f.baseSHA)
	_, code, out := f.land(t, "run-1", "pass", "--base-ref", f.ref)
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if f.trunk(t) != got.Payload.TrunkAfter {
		t.Fatalf("the trunk is at %s, the landing says %s", f.trunk(t), got.Payload.TrunkAfter)
	}
}

// gateScript writes a gate program that emits a pass verdict for the change it is handed,
// and — when LAND_PROBE is set — records whether the land lock is free while it runs.
func gateScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(scratch(t, "gate"), "gate.py")
	write(t, path, `import json, os, subprocess, sys
args = sys.argv[1:]
def opt(name):
    return args[args.index(name) + 1]
base, head, wt = opt("--base"), opt("--head"), opt("--worktree")
def git(*a):
    return subprocess.run(["git", "-C", wt, *a], capture_output=True, text=True, check=True).stdout.strip()
tree = git("rev-parse", head + "^{tree}")
common = git("rev-parse", "--path-format=absolute", "--git-common-dir")
probe = os.environ.get("LAND_PROBE")
if probe:
    import fcntl
    with open(os.path.join(common, "land.lock"), "a+") as fd:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            state = "free"
        except OSError:
            state = "held"
    open(probe, "w").write(state)
result = os.environ.get("LAND_VERDICT", "pass")
code = int(os.environ.get("LAND_EXIT", "0"))
print(json.dumps({"record_version": 1, "kind": "gate.verdict", "id": "verdict_001",
                  "producer": {"system": "tillr", "utility": "gate", "version": "0.1.0"},
                  "created_at": "2026-11-10T00:00:00Z",
                  "payload": {"verdict_id": "verdict_001", "plan_id": "plan_001",
                              "registry_digest": "sha256:0", "base_sha": base, "tree_sha": tree,
                              "result": result, "checks": []}}))
sys.exit(code)
`)
	return "python3 " + path
}

// TestOneGatesAndLandsABranch.
func TestOneGatesAndLandsABranch(t *testing.T) {
	f := newFixture(t)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out := run(t, "one", "--store", db, "--repo", f.dir, "--branch", "candidate",
		"--trunk", f.ref, "--item", "item_042", "--run", "run-1",
		"--gate", gateScript(t), "--now", "2026-11-10T00:00:00Z")
	if code != 0 {
		t.Fatalf("one exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "landed" {
		t.Fatalf("landing is %s/%s, want landed", got.Payload.Result, got.Payload.Reason)
	}
	if f.trunk(t) != got.Payload.TrunkAfter {
		t.Fatalf("the trunk is at %s, the landing says %s", f.trunk(t), got.Payload.TrunkAfter)
	}
}

// TestOneRefusesACandidateWhoseGateCouldNotJudge: a gate that could not run is not a
// failure of the change.
func TestOneRefusesACandidateWhoseGateCouldNotJudge(t *testing.T) {
	f := newFixture(t)
	t.Setenv("LAND_VERDICT", "error")
	t.Setenv("LAND_EXIT", "3")
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out := run(t, "one", "--store", db, "--repo", f.dir, "--branch", "candidate",
		"--trunk", f.ref, "--item", "item_042", "--run", "run-1",
		"--gate", gateScript(t), "--now", "2026-11-10T00:00:00Z")
	if code != 1 {
		t.Fatalf("one exited %d, want 1: %s", code, out)
	}
	if got := decodeLanding(t, out); got.Payload.Reason != "verdict-error" {
		t.Fatalf("reason is %q, want verdict-error", got.Payload.Reason)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a gate that could not judge moved the trunk")
	}
}

// TestTheLandLockIsFreeWhileTheGateRuns is the lockless half of the landing: the gate runs
// before the lock is taken, so a gate that takes minutes does not hold the trunk. The probe
// is a non-blocking flock inside the gate itself, and its answer is the evidence.
func TestTheLandLockIsFreeWhileTheGateRuns(t *testing.T) {
	f := newFixture(t)
	probe := filepath.Join(scratch(t, "probe"), "probe")
	t.Setenv("LAND_PROBE", probe)
	db := filepath.Join(scratch(t, "store"), "state.db")
	code, out := run(t, "one", "--store", db, "--repo", f.dir, "--branch", "candidate",
		"--trunk", f.ref, "--item", "item_042", "--run", "run-1",
		"--gate", gateScript(t), "--now", "2026-11-10T00:00:00Z")
	if code != 0 {
		t.Fatalf("one exited %d: %s", code, out)
	}
	body, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("the gate did not probe the lock: %v", err)
	}
	if string(body) != "free" {
		t.Fatalf("the land lock was %q while the gate ran, want free", body)
	}
}

// TestALandingIsExcludedWhileTheLockIsHeld is the swap half of the same rule: a second
// writer cannot enter the trunk-move window, and the waiter's bound is what proves it.
func TestALandingIsExcludedWhileTheLockIsHeld(t *testing.T) {
	f := newFixture(t)
	gitDir := git(t, f.dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	lockPath := filepath.Join(gitDir, "land.lock")
	holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("opening the land lock: %v", err)
	}
	defer holder.Close()
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("holding the land lock: %v", err)
	}
	db := filepath.Join(scratch(t, "store"), "state.db")
	args := []string{
		"pass", "--store", db, "--repo", f.dir, "--item", "item_042", "--run", "run-1",
		"--base", f.baseSHA, "--head", f.headSHA, "--verdict", f.verdictFile(t, "pass"),
		"--now", "2026-11-10T00:00:00Z", "--lock-timeout", "300ms",
	}
	code, out := run(t, args...)
	if code != 4 {
		t.Fatalf("pass exited %d while another writer held the lock, want 4: %s", code, out)
	}
	if f.trunk(t) != f.baseSHA {
		t.Fatal("a landing entered the swap window while the lock was held")
	}
	// The negative control: release the lock and the same landing succeeds.
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("releasing the land lock: %v", err)
	}
	if code, out := run(t, args...); code != 0 {
		t.Fatalf("pass exited %d after the lock was released: %s", code, out)
	}
	if f.trunk(t) == f.baseSHA {
		t.Fatal("the landing did not move the trunk after the lock was released")
	}
}

// TestHelpIsTheFaceSurface: the two faces are the whole surface, and the retired queue faces
// are unknown.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := run(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d: %s", code, out)
	}
	for _, face := range []string{"pass", "one"} {
		if !strings.Contains(string(out), face) {
			t.Fatalf("--help does not name %q:\n%s", face, out)
		}
	}
	for _, retired := range []string{"enqueue", "check", "list", "show", "cancel"} {
		if strings.Contains(string(out), retired) {
			t.Fatalf("--help still names the retired face %q:\n%s", retired, out)
		}
		if code, _ := run(t, retired, "--help"); code != 2 {
			t.Fatalf("the retired face %q exited %d, want 2", retired, code)
		}
	}
	if code, _ := run(t, "pass", "--help"); code != 0 {
		t.Fatalf("pass --help exited %d", code)
	}
}

// The event a landing appends carries the whole landing, so a reader answers why an item did
// not land without decoding a face's stdout.
func TestTheLandingEventCarriesTheWholeLanding(t *testing.T) {
	f := newFixture(t)
	db, code, out := f.land(t, "run-1", "pass")
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	got := decodeLanding(t, out)
	e := eventOfKind(t, db, "land.landing")
	if e.ObjectType != "item" || e.ObjectID == nil || *e.ObjectID != "item_042" {
		t.Fatalf("the event names %s/%v, want item/item_042", e.ObjectType, e.ObjectID)
	}
	var payload map[string]any
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("the event payload is not JSON: %v", err)
	}
	if payload["result"] != "landed" || payload["landing_id"] != got.Payload.LandingID {
		t.Fatalf("the event payload is %v, want the landed landing %s", payload, got.Payload.LandingID)
	}
}

// TestTheClockIsAnInput: the --now a face is given is the clock its record and its event are
// stamped with, so a fixture run is reproducible from the flag alone.
func TestTheClockIsAnInput(t *testing.T) {
	f := newFixture(t)
	db, code, out := f.land(t, "run-1", "pass")
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	want := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	if got := decodeLanding(t, out); got.Kind != "land.landing" {
		t.Fatalf("the record is %q", got.Kind)
	}
	if e := eventOfKind(t, db, "land.landing"); !e.At.Equal(want) {
		t.Fatalf("the event clock is %s, want %s", e.At, want)
	}
}
