package drain_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/internal/testinputs"
	"github.com/mschulkind-oss/tillr-run/run"
)

// sibling builds another utility's binary from its own module and answers its path.
//
// The composed case is the reason: design §5.9's steps are separate invocations, so the
// case drives the real binaries rather than calling into another utility's package. A
// sibling that will not build is reported as a missing binary rather than papered over
// with a weaker case.
func sibling(t *testing.T, name string) string {
	t.Helper()
	if err := testinputs.Track(".."); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(drainBinDir(t), "tillr-"+name)
	siblingMu.Lock()
	defer siblingMu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return path
	}
	build := exec.Command("go", "build", "-o", path, ".")
	build.Dir = filepath.Join("..", "..", "tillr-"+name)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the sibling binary tillr-%s failed: %v\n%s", name, err, out)
	}
	return path
}

var siblingMu sync.Mutex

// drive runs one binary and answers its exit code and combined output.
func drive(t *testing.T, path string, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(path, args...)
	// A program a face runs reaches tillr-gate by name, so the binaries
	// this case just built go first on PATH. Otherwise it runs whatever launcher the
	// host delivered, which can predate the source under test and refuse its flags.
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"),
		"PATH="+drainBinDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out
}

// driveOK runs one binary and fails the case on a non-zero exit.
func driveOK(t *testing.T, path string, args ...string) []byte {
	t.Helper()
	code, out := drive(t, path, args...)
	if code != 0 {
		t.Fatalf("%s %v exited %d: %s", filepath.Base(path), args, code, out)
	}
	return out
}

// sinkElement answers one element of the record sink's file as the bytes it holds, so
// the case carries the record the sink captured rather than a re-encoding of it.
func sinkElement(t *testing.T, path string, index int) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the sink's file %s failed: %v", path, err)
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		t.Fatalf("decoding the sink's file %s failed: %v", path, err)
	}
	if index >= len(elements) {
		t.Fatalf("the sink holds %d records, want at least %d", len(elements), index+1)
	}
	return elements[index]
}

// writeFile writes one file a face is given.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := writeForkSafe(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

func requireFixtureCommitRepo(t testing.TB, dir string, args []string) {
	t.Helper()
	commit := false
	for _, arg := range args {
		if arg == "commit" || arg == "commit-tree" {
			commit = true
			break
		}
	}
	if !commit {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving fixture git dir %q: %v", dir, err)
	}
	cmd := exec.Command("git", "-C", absDir, "rev-parse", "--git-dir")
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") || strings.HasPrefix(entry, "GIT_CEILING_DIRECTORIES=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(absDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refusing git %v in %s: directory is not its own repository (%v: %s)", args, dir, err, strings.TrimSpace(string(out)))
	}
}

// git runs one git command in a fixture repository and answers its stdout. The
// repository variables are removed rather than blanked: git reads an empty GIT_DIR as a
// path and refuses it.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, dir, args)
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// The fixture's commits carry the identity the deployed global config states, because the
	// gate's commit-identity check judges the range the candidate adds against that identity
	// and a repo-local one is refused rather than read.
	cmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME=Fixture Landing", "GIT_AUTHOR_EMAIL=landing@example.invalid",
		"GIT_COMMITTER_NAME=Fixture Landing", "GIT_COMMITTER_EMAIL=landing@example.invalid")
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

// gitEnv is this process's environment without the repository variables, so the fixture
// repository is the one the command is pointed at.
func gitEnv() []string {
	kept := []string{}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// repoFixture is a repository with a trunk at baseSHA and a candidate commit headSHA,
// plus a worktree checked out at the candidate: the shape gate run and land pass each
// need.
type repoFixture struct {
	dir      string
	worktree string
	baseSHA  string
	headSHA  string
	treeSHA  string
}

// repoRoot is the repository this suite runs in: the clone source for the fixture, and the
// tree the gate's registry is a property of.
func repoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("locating the repository root failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func newRepoFixture(t *testing.T) repoFixture {
	t.Helper()
	dir := filepath.Join(scratch(t, "repo"), "repo")
	// The fixture is a clone of the repository this suite runs in, not a synthetic stub:
	// the gate leg plans from the real registry and runs the real gate, so the fixture has
	// to carry the tree those read. `--shared` hardlinks the objects, and the fetch refspec
	// takes every local branch, because the gate's structural check resolves each pack's
	// patch branch as refs/heads/<name> and a plain clone leaves those as
	// refs/remotes/origin/*.
	clone := exec.Command("git", "clone", "--shared", "--quiet",
		"--config", "remote.origin.fetch=+refs/heads/*:refs/heads/*",
		repoRoot(t), dir)
	clone.Env = gitEnv()
	if out, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("cloning the repository for the fixture failed: %v\n%s", err, out)
	}
	// The repository's configured identity is what a landing normalizes authorship to, and
	// it must come from a scope a lane cannot write: a repo-local identity is refused by
	// design, so the fixture supplies a global config file instead. The candidate's own
	// commits carry that same identity, because the gate leg judges the range they add
	// against it.
	globalConfig := filepath.Join(scratch(t, "identity"), "gitconfig")
	writeFile(t, globalConfig, "[user]\n\tname = Fixture Landing\n\temail = landing@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)

	base := git(t, dir, "rev-parse", "HEAD")
	// A clone checks out whatever branch the source repository had checked out, and git
	// refuses `branch -f` on a branch that is checked out in a worktree. Detaching first is
	// what makes the fixture independent of where the suite runs: without it the case passes
	// from a lane worktree (on `tillr/lanes/...`) and fails from a checkout of `main`, which
	// is the one place `just done` runs it and the one place a landing cannot verify.
	git(t, dir, "checkout", "--detach")
	// The trunk is the base the candidate is built on. `origin/main` is pinned to the same
	// commit so the gate's commit-identity check judges the range this case adds, not the
	// clone's history.
	git(t, dir, "branch", "-f", "main", base)
	git(t, dir, "update-ref", "refs/remotes/origin/main", base)

	git(t, dir, "checkout", "-b", "candidate")
	writeFile(t, filepath.Join(dir, "candidate.txt"), "candidate\n")
	git(t, dir, "add", "candidate.txt")
	git(t, dir, "commit", "-m", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")

	// The trunk goes back to base, and the candidate gets its own worktree: gate run
	// executes the plan in a tree, and the trunk has not taken the candidate yet.
	git(t, dir, "checkout", "main")
	worktree := filepath.Join(scratch(t, "worktree"), "candidate")
	git(t, dir, "worktree", "add", worktree, "candidate")
	return repoFixture{dir: dir, worktree: worktree, baseSHA: base, headSHA: head, treeSHA: tree}
}

// registry answers the candidate's own check declarations: the directory the gate plans
// from, one JSON file per check.
func (repo repoFixture) registry(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repo.worktree, "tools", "gate", "checks")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the candidate carries no check declarations: %v", err)
	}
	return path
}

// echoPolicy is the backend policy the run leg dispatches through: one backend named
// `echo`, whose adapter is the echo provider. The name is the record's `backend` field, so
// the record names a backend a reader can check against the policy.
func echoPolicy(t *testing.T) string {
	t.Helper()
	return policyDoc(t, map[string]any{
		"default_backend": "echo",
		"preference":      []string{"echo"},
		"max_failovers":   1,
		"backends": map[string]any{
			"echo": map[string]any{
				"adapter":         echoProvider(t),
				"model":           "echo-model",
				"effort":          "low",
				"timeout_seconds": 60,
			},
		},
	})
}

// echoProvider is the adapter the run leg names: the harness's no-token rehearsal, stood in
// for by a stub because the harness is not installed here. It answers with the prompt and
// writes nothing, which is the shape the echo provider produces -- a JSONL stream with no
// RESULT: footer.
func echoProvider(t *testing.T) string {
	t.Helper()
	return stub(t, "echo-provider", `cat <<'STREAM'
{"schema_version":1,"record_type":"status","payload_type":"run.output.delta","payload":{"kind":"run_output_delta","text":"the staged brief"}}
STREAM
`)
}

// The lane's acceptance test: design §5.9's steps 1–8, each a separate invocation
// carrying records between faces, with `drain once` as step 2.
//
// Nothing calls another utility in-process. What this proves is the boundary the design
// claims: every step's input is a record a fixture can supply, every step's output is a
// record the next face consumes, and the drain's dispatch is the link that starts it.
func TestTheComposedPipelineRunsEndToEndOnAFixture(t *testing.T) {
	const now = "2026-11-10T00:00:00Z"
	storePath := filepath.Join(scratch(t, "store"), "board.sqlite3")
	repo := newRepoFixture(t)

	// Step 1: item apply. The item is queued, which is the one state drain dispatches.
	itemPath := filepath.Join(scratch(t, "records"), "item.json")
	writeFile(t, itemPath, itemRecord("item_042", "in-queue"))
	driveOK(t, sibling(t, "item"), "apply", "--store", storePath, "--input", itemPath, "--now", now)

	// Step 2: drain once. The plan's decision is recorded and its run.request handed to
	// the record sink instead of launched.
	tickPath := tick(t, now, []string{"lane-1"}, 1, echoPolicy(t))
	sink := filepath.Join(scratch(t, "sink"), "requests.json")
	onceOK(t, storePath, tickPath, "record-sink", sink)
	requests := readRequests(t, sink)
	if len(requests) != 1 {
		t.Fatalf("the sink holds %d requests, want 1", len(requests))
	}
	if rows := dispatchRows(t, storePath); len(rows) != 1 || rows[0].RunID != requests[0].Payload.RunID {
		t.Fatalf("the dispatch row is %+v, want the run %q the sink was handed", rows, requests[0].Payload.RunID)
	}

	// Step 3: run exec, on the request the sink captured. The record travels as a file,
	// which is the only coupling the design allows.
	requestPath := filepath.Join(scratch(t, "records"), "run-request.json")
	writeFile(t, requestPath, string(sinkElement(t, sink, 0)))
	laneRoot := filepath.Join(scratch(t, "lanes"), "lanes")
	runCode, runOut := driveRecord(t, sibling(t, "run"), "exec", "--store", storePath,
		"--lane-root", laneRoot, "--input", requestPath, "--now", now)
	if runCode != 0 {
		t.Fatalf("run exec exited %d: %s", runCode, runOut)
	}
	var runRecord struct {
		Kind    string     `json:"kind"`
		Payload run.Record `json:"payload"`
	}
	if err := json.Unmarshal(runOut, &runRecord); err != nil {
		t.Fatalf("the run face emitted no record: %v\n%s", err, runOut)
	}
	if runRecord.Kind != run.RecordKind || runRecord.Payload.RunID != requests[0].Payload.RunID {
		t.Fatalf("the run record is %q for run %q, want %s for %q",
			runRecord.Kind, runRecord.Payload.RunID, run.RecordKind, requests[0].Payload.RunID)
	}
	if runRecord.Payload.State != run.StateExited || runRecord.Payload.ExitCode == nil || *runRecord.Payload.ExitCode != 0 {
		t.Fatalf("the run finished %q with exit %v, want exited 0", runRecord.Payload.State, runRecord.Payload.ExitCode)
	}
	// The backend drain chose is on the record, not only on the request: the request the
	// sink captured named it, and the run face carried that name through to the record a
	// reader answers "which backend ran this" from. The name is the echo provider's, so the
	// record names a backend the policy declares rather than a fixture label the case
	// invented.
	if requests[0].Payload.Backend != "echo" {
		t.Fatalf("the request names backend %q, want the echo provider's", requests[0].Payload.Backend)
	}
	if runRecord.Payload.Backend != "echo" {
		t.Fatalf("the run record names backend %q, want the echo provider's", runRecord.Payload.Backend)
	}
	if runRecord.Payload.ItemID != "item_042" {
		t.Fatalf("the run record names item %q, want the dispatched item_042", runRecord.Payload.ItemID)
	}
	// The echo provider writes nothing: the run's worktree is still empty, which is the
	// shape the no-token rehearsal exists to produce.
	if entries, err := os.ReadDir(filepath.Join(laneRoot, requests[0].Payload.LaneID, "worktree")); err != nil {
		t.Fatalf("reading the run's worktree failed: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("the run's worktree holds %d entries, want none: the echo provider writes nothing", len(entries))
	}

	// Steps 5 and 6: gate plan from the candidate's own declarations, then gate run in the
	// candidate worktree. The judgment is a fixture program that passes: this case proves
	// that the plan, the verdict and the landing bind one another, and the repository's
	// declared checks have their own suites.
	changePath := filepath.Join(scratch(t, "records"), "change.json")
	driveOK(t, sibling(t, "gate"), "change", "--item", "item_042", "--lane", requests[0].Payload.LaneID,
		"--run", requests[0].Payload.RunID, "--worktree", repo.worktree,
		"--base", repo.baseSHA, "--head", repo.headSHA, "--now", now, "--output", changePath)
	planPath := filepath.Join(scratch(t, "records"), "gate-plan.json")
	driveOK(t, sibling(t, "gate"), "plan", "--registry", repo.registry(t), "--input", changePath,
		"--output", planPath, "--root", repo.worktree)

	verdictPath := filepath.Join(scratch(t, "records"), "gate-verdict.json")
	driveOK(t, sibling(t, "gate"), "run", "--input", planPath, "--worktree", repo.worktree,
		"--output", verdictPath, "--logs", filepath.Join(scratch(t, "gate-logs"), "logs"),
		"--gate", "true")

	// Step 7: land pass, which squashes the judged head onto the trunk because the verdict
	// binds the tree. There is no queue step: the landing takes the item, the run, the base
	// and the head directly.
	attemptCode, attemptOut := driveRecord(t, sibling(t, "land"), "pass", "--store", storePath, "--repo", repo.dir,
		"--item", "item_042", "--run", requests[0].Payload.RunID,
		"--base", repo.baseSHA, "--head", repo.headSHA,
		"--verdict", verdictPath, "--now", now)
	if attemptCode != 0 {
		t.Fatalf("land pass exited %d: %s", attemptCode, attemptOut)
	}
	var attempt struct {
		Payload struct {
			Result string `json:"result"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(attemptOut, &attempt); err != nil {
		t.Fatalf("the pass face emitted no record: %v\n%s", err, attemptOut)
	}
	if attempt.Payload.Result != "landed" {
		t.Fatalf("the attempt is %q, want landed: %s", attempt.Payload.Result, attemptOut)
	}
	// The trunk takes the candidate's tree. The landing re-authors the commit to the
	// repository's configured identity, so the landed commit is not the candidate's sha and
	// asserting sha equality would pin a coincidence: the tree, the author and the count are
	// what the pipeline promises.
	if trunk := git(t, repo.dir, "rev-parse", "main^{tree}"); trunk != repo.treeSHA {
		t.Fatalf("the trunk's tree is %s, want the candidate's tree %s", trunk, repo.treeSHA)
	}
	if author := git(t, repo.dir, "log", "-1", "--format=%an <%ae>", "main"); author != "Fixture Landing <landing@example.invalid>" {
		t.Fatalf("the landed commit's author is %q, want the repository's configured identity", author)
	}
	if count := git(t, repo.dir, "rev-list", "--count", repo.baseSHA+"..main"); count != "1" {
		t.Fatalf("the landing put %s commits on the trunk, want 1", count)
	}

	// Step 8: view status, from a read-only snapshot of the same store. Exit 0 is the
	// assertion that matters: the face answers 1 for a subject the snapshot does not
	// hold, so a completed status record says the item the drain dispatched is there.
	statusCode, statusOut := driveRecord(t, sibling(t, "view"), "status", "--store", storePath, "--item", "item_042")
	if statusCode != 0 {
		t.Fatalf("view status exited %d: %s", statusCode, statusOut)
	}
	var status struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(statusOut, &status); err != nil {
		t.Fatalf("the view face emitted no record: %v\n%s", err, statusOut)
	}
	if status.Kind != "view.status" {
		t.Fatalf("the status record is %q, want view.status", status.Kind)
	}
}
