package main_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

var bin string

// Scratch lives under $YOLO_DURABLE_DIR, never /tmp: a suite that writes to /tmp loses
// its evidence when the box is cleaned, and the owner refuses it.
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
	dir, err := os.MkdirTemp(root, "tillr-run-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "tillr-run")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}
	return m.Run()
}

// run drives the binary with the state home scrubbed, so a face that derives a store
// path instead of taking --store cannot reach the owner's board.
func run(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out
}

// request is design §5.6's run.request payload. A case varies one field and leaves
// the rest, so a refusal is attributable to the field the case names.
type request struct {
	RunID      string   `json:"run_id"`
	ItemID     string   `json:"item_id"`
	LaneID     string   `json:"lane_id"`
	Backend    string   `json:"backend"`
	Command    []string `json:"command"`
	WorkingDir string   `json:"working_dir"`
	TimeoutMS  int      `json:"timeout_ms"`
	EnvRefs    []string `json:"env_refs"`
	Resume     bool     `json:"resume,omitempty"`
}

func base(runID string, command ...string) request {
	return request{
		RunID: runID, ItemID: "item_042", LaneID: "", Backend: "muse", Command: command,
		WorkingDir: ".", TimeoutMS: 60000, EnvRefs: []string{},
	}
}

// envelope wraps a payload in design §5.1's envelope. The clock is the caller's, so
// two runs of the same case produce the same bytes.
func envelope(t *testing.T, kind, id string, payload any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"record_version": 1,
		"kind":           kind,
		"id":             id,
		"producer":       map[string]string{"system": "tillr", "utility": "run", "version": "0.1.0"},
		"created_at":     "2026-11-10T00:00:00Z",
		"payload":        payload,
	})
	if err != nil {
		t.Fatalf("building the input record failed: %v", err)
	}
	return string(raw)
}

func input(t *testing.T, record string) string {
	t.Helper()
	// Raw command fixtures still use the production adapter preflight. Put the
	// executable behind a symlink with a declaration rather than bypassing it.
	var env map[string]json.RawMessage
	if json.Unmarshal([]byte(record), &env) == nil {
		var req request
		if json.Unmarshal(env["payload"], &req) == nil && len(req.Command) > 0 {
			dir := scratch(t, "declared-command")
			target := req.Command[0]
			if !filepath.IsAbs(target) {
				if resolved, err := exec.LookPath(target); err == nil {
					target, _ = filepath.Abs(resolved)
				} else {
					target = filepath.Join(dir, "missing-command")
				}
			}
			// Keep argv[0]'s basename: /bin/true may be a multicall executable.
			adapter := filepath.Join(dir, filepath.Base(req.Command[0]))
			if err := os.Symlink(target, adapter); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "pause-kind"), []byte("stops-where-it-stands\n"), 0644); err != nil {
				t.Fatal(err)
			}
			req.Command[0] = adapter
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(env["payload"], &payload); err != nil {
				t.Fatal(err)
			}
			payload["command"], _ = json.Marshal(req.Command)
			env["payload"], _ = json.Marshal(payload)
			body, _ := json.Marshal(env)
			record = string(body)
		}
	}
	path := filepath.Join(scratch(t, "input"), "request.json")
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		t.Fatalf("writing the request failed: %v", err)
	}
	return path
}

// execFace runs the face against one request and one lane root.
func execFace(t *testing.T, db, laneRoot, record string) (int, []byte) {
	t.Helper()
	return run(t, "exec", "--store", db, "--lane-root", laneRoot, "--input", input(t, record))
}

// runOut is the record a face emits, decoded. The fields live under `payload`:
// design §5.1 says the envelope carries `payload`, and §5.6's list describes a
// payload, not the envelope.
type runOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		RunID        string `json:"run_id"`
		ItemID       string `json:"item_id"`
		LaneID       string `json:"lane_id"`
		Backend      string `json:"backend"`
		State        string `json:"state"`
		Resumed      bool   `json:"resumed"`
		ExitCode     *int   `json:"exit_code"`
		StartedAt    string `json:"started_at"`
		FinishedAt   string `json:"finished_at"`
		EventSeq     int64  `json:"event_seq"`
		ArtifactRefs []struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"artifact_refs"`
	} `json:"payload"`
}

func decode(t *testing.T, out []byte) runOut {
	t.Helper()
	var got runOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
	return got
}

// payloadKey answers one field of an emitted record's payload, and whether the record
// carries it at all. A decoded string cannot tell an absent field from an empty one, and
// the two are different answers: an id the record omits is an absence, an id it spells
// "" is a value it invented.
func payloadKey(t *testing.T, out []byte, path ...string) (json.RawMessage, bool) {
	t.Helper()
	var env struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
	current := env.Payload
	for i, name := range path {
		raw, carried := current[name]
		if !carried {
			return nil, false
		}
		if i == len(path)-1 {
			return raw, true
		}
		if err := json.Unmarshal(raw, &current); err != nil {
			t.Fatalf("the record's %q is not an object: %v\n%s", name, err, out)
		}
	}
	return nil, false
}

// eventsOfKind counts the events a store holds for one kind, through the substrate's
// reader: a test that opened the database itself would be the raw-SQL answer the
// lanes' definition of done forbids.
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

// rowCount answers a count through the substrate's reader, never by opening the
// database: a lane that answers a state question with raw SQL is the answer the
// definition of done forbids.
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

// A stub command that exits 0 is a completed run: the record says so, the lane and
// run rows exist, and the log is where the caller can find it.
func TestExecRunsAStubCommandAndRecordsTheTerminalState(t *testing.T) {
	dir := scratch(t, "exec-ok")
	db := filepath.Join(dir, "board.sqlite3")
	laneRoot := filepath.Join(dir, "lanes")
	code, out := execFace(t, db, laneRoot, envelope(t, "run.request", "req-1", base("run_001", "/bin/true")))
	if code != 0 {
		t.Fatalf("exec exited %d: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "run.record" {
		t.Fatalf("exec emitted kind %q", got.Kind)
	}
	if got.Payload.State != "exited" {
		t.Fatalf("the run state is %q", got.Payload.State)
	}
	if got.Payload.ExitCode == nil || *got.Payload.ExitCode != 0 {
		t.Fatalf("the run carries exit_code %v", got.Payload.ExitCode)
	}
	if got.Payload.StartedAt == "" || got.Payload.FinishedAt == "" {
		t.Fatalf("the run carries started_at %q and finished_at %q", got.Payload.StartedAt, got.Payload.FinishedAt)
	}
	if got.Payload.LaneID == "" {
		t.Fatal("the run carries no lane id")
	}
	if got.Payload.ItemID != "item_042" {
		t.Fatalf("the record names item %q, want the request's", got.Payload.ItemID)
	}
	// The field is carried by the payload at the path the absence case reads, so that
	// case's "not there" is a finding rather than a wrong lookup.
	if raw, carried := payloadKey(t, out, "item_id"); !carried {
		t.Fatalf("the record does not carry item_id at all: %s", out)
	} else if string(raw) != `"item_042"` {
		t.Fatalf("the record carries item_id %s, want %q", raw, "item_042")
	}
	if got.Payload.Backend != "muse" {
		t.Fatalf("the record names backend %q, want the request's", got.Payload.Backend)
	}
	if n := rowCount(t, db, "runs"); n != 1 {
		t.Fatalf("the store holds %d run rows, want 1", n)
	}
	if n := rowCount(t, db, "lanes"); n != 1 {
		t.Fatalf("the store holds %d lane rows, want 1", n)
	}
	var sawLog bool
	for _, ref := range got.Payload.ArtifactRefs {
		if ref.Kind == "log" {
			sawLog = true
			if _, err := os.Stat(ref.Ref); err != nil {
				t.Fatalf("the log ref %q does not exist: %v", ref.Ref, err)
			}
		}
	}
	if !sawLog {
		t.Fatalf("the run carries no log artifact: %+v", got.Payload.ArtifactRefs)
	}
}

// A command that exits nonzero is a completed run with that exit code in the record, and the
// face answers the run's own exit code: a shell-driven caller's only portable signal is the
// exit status, and a red run answering 0 is a red run a `set -e` chain reads as a green.
//
// 3 is also the face's own "incompatible" code, and this case is the collision: the record on
// stdout is what tells the two apart, which is why the assertion is that both are here — the
// run's 3 in the payload, and the record the caller reads it from.
func TestExecAnswersTheRunsExitCodeAndStillRecordsIt(t *testing.T) {
	dir := scratch(t, "exec-exit3")
	db := filepath.Join(dir, "board.sqlite3")
	code, out := execFace(t, db, filepath.Join(dir, "lanes"),
		envelope(t, "run.request", "req-1", base("run_001", "/bin/sh", "-c", "exit 3")))
	if code != 3 {
		t.Fatalf("exec exited %d, want the run's own 3: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "run.record" {
		t.Fatalf("exec emitted kind %q beside the run's exit code", got.Kind)
	}
	if got.Payload.State != "exited" || got.Payload.ExitCode == nil || *got.Payload.ExitCode != 3 {
		t.Fatalf("the run is %q with exit_code %v", got.Payload.State, got.Payload.ExitCode)
	}
	// The exit code the record carries and the one the face answered are the same number:
	// a caller that reads either is reading the run, not the utility.
	if *got.Payload.ExitCode != code {
		t.Fatalf("the face answered %d while the record says %d", code, *got.Payload.ExitCode)
	}
}

// The reserved codes still mean the face's own failures, so a caller can tell "the run failed
// with 7" from "the face could not start" by the code alone. Each case names the one condition
// that produces it.
func TestExecKeepsTheReservedCodesForTheFacesOwnFailures(t *testing.T) {
	t.Run("usage is 2", func(t *testing.T) {
		dir := scratch(t, "reserved-usage")
		code, out := run(t, "exec", "--store", filepath.Join(dir, "board.sqlite3"))
		if code != 2 {
			t.Fatalf("a face called with no flags exited %d, want 2: %s", code, out)
		}
		if len(out) != 0 {
			t.Fatalf("a usage refusal emitted a record: %s", out)
		}
	})
	t.Run("an incompatible store is 3", func(t *testing.T) {
		dir := scratch(t, "reserved-incompatible")
		db := filepath.Join(dir, "board.sqlite3")
		if err := os.WriteFile(db, []byte("not a store"), 0o644); err != nil {
			t.Fatalf("writing the bogus store failed: %v", err)
		}
		code, out := execFace(t, db, filepath.Join(dir, "lanes"),
			envelope(t, "run.request", "req-1", base("run_001", "/bin/true")))
		if code != 3 {
			t.Fatalf("exec exited %d for a store it cannot open, want 3: %s", code, out)
		}
		if len(out) != 0 {
			t.Fatalf("exec emitted a record for a store it could not open: %s", out)
		}
	})
	t.Run("a command that cannot start is 4", func(t *testing.T) {
		dir := scratch(t, "reserved-external")
		db := filepath.Join(dir, "board.sqlite3")
		code, out := execFace(t, db, filepath.Join(dir, "lanes"),
			envelope(t, "run.request", "req-1", base("run_001", "/nonexistent/tillr-run-stub")))
		if code != 4 {
			t.Fatalf("exec exited %d, want 4: %s", code, out)
		}
	})
	t.Run("a bug is 5", func(t *testing.T) {
		// A lane root the face cannot create is not an outcome about a run: the package
		// classifies it as a bug (run.Internalf), and 5 is what a caller must see.
		dir := scratch(t, "reserved-internal")
		db := filepath.Join(dir, "board.sqlite3")
		blocked := filepath.Join(dir, "lanes")
		if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("writing the blocking file failed: %v", err)
		}
		code, out := execFace(t, db, blocked,
			envelope(t, "run.request", "req-1", base("run_001", "/bin/true")))
		if code != 5 {
			t.Fatalf("exec exited %d for a lane root it cannot create, want 5: %s", code, out)
		}
	})
}

// A command that cannot be started is not a failed run: exit 4, and the record says
// failed-to-start with no exit code, because there was no process to have one.
func TestExecRefusesACommandThatCannotStart(t *testing.T) {
	dir := scratch(t, "exec-nostart")
	db := filepath.Join(dir, "board.sqlite3")
	code, out := execFace(t, db, filepath.Join(dir, "lanes"),
		envelope(t, "run.request", "req-1", base("run_001", "/nonexistent/tillr-run-stub")))
	if code != 4 {
		t.Fatalf("exec exited %d, want 4: %s", code, out)
	}
	got := decode(t, out)
	if got.Payload.State != "failed-to-start" {
		t.Fatalf("the run state is %q", got.Payload.State)
	}
	if got.Payload.ExitCode != nil {
		t.Fatalf("a run that never started carries exit_code %d", *got.Payload.ExitCode)
	}
}

// A command that outlives its bound is killed, and the record says timed-out. The
// stub writes its own pid and then sleeps, so the test asserts the process is gone
// rather than asserting how long anything took: a duration assertion fails under load
// and is a bug in the test, not a flake.
func TestExecTimesOutACommandThatOutlivesItsBound(t *testing.T) {
	dir := scratch(t, "exec-timeout")
	db := filepath.Join(dir, "board.sqlite3")
	laneRoot := filepath.Join(dir, "lanes")
	pidFile := filepath.Join(dir, "pid")
	req := base("run_001", "sh", "-c", "echo $$ > "+pidFile+"; sleep 30")
	req.TimeoutMS = 300
	code, out := execFace(t, db, laneRoot, envelope(t, "run.request", "req-1", req))
	if code != 0 {
		t.Fatalf("exec exited %d, want 0: %s", code, out)
	}
	got := decode(t, out)
	if got.Payload.State != "timed-out" {
		t.Fatalf("the run state is %q", got.Payload.State)
	}
	if got.Payload.ExitCode != nil {
		t.Fatalf("a timed-out run carries exit_code %d", *got.Payload.ExitCode)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the stub did not record its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("the stub's pid file holds %q", raw)
	}
	// Wait for the state, not for a duration: the process must be gone, and the
	// bound is on how long the test is willing to wait for that event.
	deadline := time.Now().Add(hangGuard)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the timed-out run's process %d is still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A request that names no lane gets one allocated, and the lane directory exists under
// the caller's lane root: a run whose artifacts are not under the lane root cannot be
// found again.
func TestExecAllocatesALaneWhenTheRequestNamesNone(t *testing.T) {
	dir := scratch(t, "exec-allocate")
	db := filepath.Join(dir, "board.sqlite3")
	laneRoot := filepath.Join(dir, "lanes")
	code, out := execFace(t, db, laneRoot, envelope(t, "run.request", "req-1", base("run_001", "/bin/true")))
	if code != 0 {
		t.Fatalf("exec exited %d: %s", code, out)
	}
	got := decode(t, out)
	if got.Payload.LaneID != "lane_run_001" {
		t.Fatalf("the allocated lane is %q", got.Payload.LaneID)
	}
	if _, err := os.Stat(filepath.Join(laneRoot, got.Payload.LaneID)); err != nil {
		t.Fatalf("the lane directory does not exist: %v", err)
	}
	if n := eventsOfKind(t, db, "run.allocate"); n != 1 {
		t.Fatalf("the store holds %d run.allocate events, want 1", n)
	}
}

// A request that names a lane uses it rather than allocating a second one.
func TestExecUsesTheLaneTheRequestNames(t *testing.T) {
	dir := scratch(t, "exec-named-lane")
	db := filepath.Join(dir, "board.sqlite3")
	req := base("run_001", "/bin/true")
	req.LaneID = "lane_007"
	code, out := execFace(t, db, filepath.Join(dir, "lanes"), envelope(t, "run.request", "req-1", req))
	if code != 0 {
		t.Fatalf("exec exited %d: %s", code, out)
	}
	if got := decode(t, out); got.Payload.LaneID != "lane_007" {
		t.Fatalf("the run's lane is %q", got.Payload.LaneID)
	}
}

// env_refs are resolved by the substrate config loader, which is SUB-2 and has not
// landed. A request that needs one is refused as unsupported input rather than
// resolved from this process's environment, which would be a silent divergence.
func TestExecRefusesARequestThatNeedsEnvRefs(t *testing.T) {
	dir := scratch(t, "exec-envrefs")
	db := filepath.Join(dir, "board.sqlite3")
	// The store exists before the request, so "wrote nothing" is a count of zero rather
	// than a store that was never created.
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatalf("creating the store failed: %v", err)
	}
	s.Close()
	req := base("run_001", "/bin/true")
	req.EnvRefs = []string{"TILLR_TOKEN"}
	code, out := execFace(t, db, filepath.Join(dir, "lanes"), envelope(t, "run.request", "req-1", req))
	if code != 2 {
		t.Fatalf("exec exited %d, want 2: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("a usage error emitted a record: %s", out)
	}
	if n := rowCount(t, db, "runs"); n != 0 {
		t.Fatalf("a refused request wrote %d run rows", n)
	}
	if n := eventsOfKind(t, db, "run.allocate"); n != 0 {
		t.Fatalf("a refused request appended %d run.allocate events", n)
	}
}

// A request with no command is malformed input, not a run that did nothing: exit 2,
// no record, nothing written.
func TestExecRefusesARequestWithNoCommand(t *testing.T) {
	dir := scratch(t, "exec-nocommand")
	db := filepath.Join(dir, "board.sqlite3")
	req := base("run_001")
	req.Command = nil
	code, out := execFace(t, db, filepath.Join(dir, "lanes"), envelope(t, "run.request", "req-1", req))
	if code != 2 {
		t.Fatalf("exec exited %d, want 2: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("a usage error emitted a record: %s", out)
	}
}

// A request with no backend is malformed input: the field is what makes a run's
// provenance readable from its record, so a run that does not name one is refused before
// a lane directory or a row exists.
func TestExecRefusesARequestWithNoBackend(t *testing.T) {
	dir := scratch(t, "exec-nobackend")
	db := filepath.Join(dir, "board.sqlite3")
	req := base("run_001", "/bin/true")
	req.Backend = ""
	code, out := execFace(t, db, filepath.Join(dir, "lanes"), envelope(t, "run.request", "req-1", req))
	if code != 2 {
		t.Fatalf("exec exited %d, want 2: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("a usage error emitted a record: %s", out)
	}
}

// An absolute working_dir is refused: a run that can write anywhere is not confined to
// a lane root, and the lane root is the only thing that makes its artifacts findable.
func TestExecRefusesAnAbsoluteWorkingDir(t *testing.T) {
	dir := scratch(t, "exec-absdir")
	db := filepath.Join(dir, "board.sqlite3")
	req := base("run_001", "/bin/true")
	req.WorkingDir = "/tmp"
	code, out := execFace(t, db, filepath.Join(dir, "lanes"), envelope(t, "run.request", "req-1", req))
	if code != 2 {
		t.Fatalf("exec exited %d, want 2: %s", code, out)
	}
}

// The seed materializer is the write face's own mapping: a run.record seeded into a
// fixture store produces the same rows the face would, so a fixture cannot construct a
// state the face would reject. This is OQ-L2's one-mapping claim, and it is only
// testable because the mapping lives in an importable package.
func TestTheSeedMaterializerWritesTheSameRowsAsTheFace(t *testing.T) {
	dir := scratch(t, "seed")
	db := filepath.Join(dir, "board.sqlite3")
	s, err := store.InitFixture(context.Background(), db)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	defer s.Close()
	seeded := envelope(t, "run.record", "rec-1", map[string]any{
		"run_id": "run_009", "item_id": "item_042", "lane_id": "lane_run_009", "backend": "muse", "state": "exited",
		"exit_code": 0, "started_at": "2026-11-10T00:00:00Z",
		"finished_at":   "2026-11-10T00:00:01Z",
		"artifact_refs": []map[string]string{{"kind": "lane-root", "ref": "/scratch/lanes/lane_run_009"}},
	})
	bundle, err := json.Marshal(map[string]any{
		"kind": "fixture.seed", "payload": map[string]any{"records": []json.RawMessage{json.RawMessage(seeded)}},
	})
	if err != nil {
		t.Fatalf("building the seed bundle failed: %v", err)
	}
	if err := s.Seed(context.Background(), bundle); err != nil {
		t.Fatalf("seeding the run record failed: %v", err)
	}
	if n := rowCount(t, db, "runs"); n != 1 {
		t.Fatalf("the seed wrote %d run rows, want 1", n)
	}
	if n := rowCount(t, db, "lanes"); n != 1 {
		t.Fatalf("the seed wrote %d lane rows, want 1", n)
	}
	if n := eventsOfKind(t, db, "run.finish"); n != 1 {
		t.Fatalf("the seed appended %d run.finish events, want 1", n)
	}
	// The seeded record's item_id reaches the row, so a fixture states the same run the
	// write face would and `run status` reads it back from the row rather than the log.
	code, out := run(t, "status", "--store", db, "--run", "run_009")
	if code != 0 {
		t.Fatalf("status exited %d, want 0: %s", code, out)
	}
	var got statusOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("status did not emit a record: %v\n%s", err, out)
	}
	if got.Payload.Run == nil || got.Payload.Run.ItemID != "item_042" {
		t.Fatalf("the seeded row's item_id did not read back: %s", out)
	}
}

// item_id was added to a runs table that already shipped, so a store created before it
// has to gain the column on its next create-open: CREATE TABLE IF NOT EXISTS leaves an
// existing table's shape alone, and the write naming the column would fail on such a
// store. Dropping the column is how this test builds the shape the previous binary left.
func TestExecMigratesARunsTableThatPredatesTheItemColumn(t *testing.T) {
	dir := scratch(t, "old-store")
	db := filepath.Join(dir, "board.sqlite3")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatalf("creating the store failed: %v", err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`ALTER TABLE runs DROP COLUMN item_id`)
		return err
	}); err != nil {
		s.Close()
		t.Fatalf("dropping the item column failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}

	code, out := execFace(t, db, filepath.Join(dir, "lanes"), envelope(t, "run.request", "req-1", base("run_001", "/bin/true")))
	if code != 0 {
		t.Fatalf("exec exited %d against a store whose runs table predates item_id: %s", code, out)
	}
	code, out = run(t, "status", "--store", db, "--run", "run_001")
	if code != 0 {
		t.Fatalf("status exited %d, want 0: %s", code, out)
	}
	var got statusOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("status did not emit a record: %v\n%s", err, out)
	}
	if got.Payload.Run == nil || got.Payload.Run.ItemID != "item_042" {
		t.Fatalf("the migrated store did not carry the item id: %s", out)
	}
}

// The run tables come from the run binary's own registration, not from the substrate:
// the substrate's own DDL names no run table, and the first exec creates both.
//
// The "before" observation is the substrate's DDL rather than a store created in this
// process, because a test binary links the package under test and its init() has
// already registered the run DDL before any test runs: a store created here always has
// the tables, so asserting otherwise would assert something false. The negative control
// is the registration itself -- remove it from schema.go and the exec below fails on a
// missing table.
func TestTheRunBinaryCreatesItsOwnTables(t *testing.T) {
	for _, ddl := range []string{store.MetaDDL, event.EventsDDL} {
		if strings.Contains(ddl, "lanes") || strings.Contains(ddl, "runs") {
			t.Fatalf("the substrate's own DDL names a run table:\n%s", ddl)
		}
	}
	dir := scratch(t, "ddl")
	db := filepath.Join(dir, "board.sqlite3")
	if code, out := execFace(t, db, filepath.Join(dir, "lanes"),
		envelope(t, "run.request", "req-1", base("run_001", "/bin/true"))); code != 0 {
		t.Fatalf("exec exited %d: %s", code, out)
	}
	r, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer r.Close()
	after, err := r.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for _, table := range []string{"lanes", "runs"} {
		if _, ok := after[table]; !ok {
			t.Errorf("the run binary did not create %q", table)
		}
	}
}

// lanesOut is the run.lanes record, decoded.
type lanesOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		Lanes []struct {
			LaneID      string  `json:"lane_id"`
			State       string  `json:"state"`
			RunID       *string `json:"run_id"`
			LeasedUntil *string `json:"leased_until"`
		} `json:"lanes"`
	} `json:"payload"`
}

// statusOut is the run.status record, decoded.
type statusOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		RunID string `json:"run_id"`
		Found bool   `json:"found"`
		Run   *struct {
			RunID        string `json:"run_id"`
			ItemID       string `json:"item_id"`
			LaneID       string `json:"lane_id"`
			State        string `json:"state"`
			ExitCode     *int   `json:"exit_code"`
			StartedAt    string `json:"started_at"`
			FinishedAt   string `json:"finished_at"`
			EventSeq     int64  `json:"event_seq"`
			ArtifactRefs []struct {
				Kind string `json:"kind"`
				Ref  string `json:"ref"`
			} `json:"artifact_refs"`
		} `json:"run"`
	} `json:"payload"`
}

// seedLane writes one lane row through the substrate's write transaction. A lease is
// written by the claim `exec` makes, which needs a request and a clock; a case that wants
// a lane held without running a claim — an unexpired lease, a lease past its expiry, a
// lane with no run at all — says so here and reads the faces against it.
func seedLane(t *testing.T, db, laneID, state string, runID *string, leasedUntil *int64) {
	t.Helper()
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatalf("creating the store failed: %v", err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES (?, ?, ?, ?)`,
			laneID, state, runID, leasedUntil,
		)
		return err
	}); err != nil {
		t.Fatalf("writing lane %q failed: %v", laneID, err)
	}
}

// A lane with no runs renders as such: the lane is listed, and its run_id is null
// rather than an empty string or a missing row. The negative control is the lane's
// presence -- an empty list would also have no run_id, so the case asserts the row.
func TestLanesRendersALaneThatHasNoRuns(t *testing.T) {
	dir := scratch(t, "lanes-norun")
	db := filepath.Join(dir, "board.sqlite3")
	seedLane(t, db, "lane_001", "idle", nil, nil)

	code, out := run(t, "lanes", "--store", db)
	if code != 0 {
		t.Fatalf("lanes exited %d, want 0: %s", code, out)
	}
	var got lanesOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("lanes did not emit a record: %v\n%s", err, out)
	}
	if got.Kind != "run.lanes" {
		t.Fatalf("lanes emitted kind %q", got.Kind)
	}
	if len(got.Payload.Lanes) != 1 {
		t.Fatalf("lanes listed %d lanes, want 1: %s", len(got.Payload.Lanes), out)
	}
	lane := got.Payload.Lanes[0]
	if lane.LaneID != "lane_001" || lane.State != "idle" {
		t.Fatalf("the lane is %q in state %q", lane.LaneID, lane.State)
	}
	if lane.RunID != nil {
		t.Fatalf("a lane with no runs reports run_id %q", *lane.RunID)
	}
}

// A leased_until of NULL is reported as unleased, not as an error and not as a zero
// timestamp. The negative control is the second lane: a reader that hardcoded null
// would pass the first lane and fail the second, and one that refused NULL would fail
// the first.
func TestLanesReportsANullLeaseAsUnleased(t *testing.T) {
	dir := scratch(t, "lanes-lease")
	db := filepath.Join(dir, "board.sqlite3")
	runID := "run_001"
	leased := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC).UnixMilli()
	seedLane(t, db, "lane_001", "leased", &runID, nil)
	seedLane(t, db, "lane_002", "leased", &runID, &leased)

	code, out := run(t, "lanes", "--store", db)
	if code != 0 {
		t.Fatalf("lanes exited %d, want 0: %s", code, out)
	}
	var got lanesOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("lanes did not emit a record: %v\n%s", err, out)
	}
	if len(got.Payload.Lanes) != 2 {
		t.Fatalf("lanes listed %d lanes, want 2: %s", len(got.Payload.Lanes), out)
	}
	if got.Payload.Lanes[0].LeasedUntil != nil {
		t.Fatalf("a NULL lease is reported as %q", *got.Payload.Lanes[0].LeasedUntil)
	}
	if got.Payload.Lanes[1].LeasedUntil == nil {
		t.Fatalf("a set lease is reported as unleased: %s", out)
	}
	if want := "2026-11-10T12:00:00Z"; *got.Payload.Lanes[1].LeasedUntil != want {
		t.Fatalf("the lease is reported as %q, want %q", *got.Payload.Lanes[1].LeasedUntil, want)
	}
}

// A run's terminal state is reported from its row: the state, the exit code the
// process answered with, and the lane it ran in.
func TestStatusReportsARunsTerminalState(t *testing.T) {
	dir := scratch(t, "status-terminal")
	db := filepath.Join(dir, "board.sqlite3")
	if code, out := execFace(t, db, filepath.Join(dir, "lanes"),
		envelope(t, "run.request", "req-1", base("run_001", "/bin/false"))); code != 1 {
		t.Fatalf("exec exited %d, want the run's own 1: %s", code, out)
	}

	code, out := run(t, "status", "--store", db, "--run", "run_001")
	if code != 0 {
		t.Fatalf("status exited %d, want 0: %s", code, out)
	}
	var got statusOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("status did not emit a record: %v\n%s", err, out)
	}
	if got.Kind != "run.status" {
		t.Fatalf("status emitted kind %q", got.Kind)
	}
	if !got.Payload.Found || got.Payload.Run == nil {
		t.Fatalf("status did not find the run it was asked for: %s", out)
	}
	reported := got.Payload.Run
	if reported.RunID != "run_001" || reported.LaneID != "lane_run_001" {
		t.Fatalf("status reports run %q in lane %q", reported.RunID, reported.LaneID)
	}
	if reported.ItemID != "item_042" {
		t.Fatalf("status reports item %q, want the row's item_042", reported.ItemID)
	}
	if reported.State != "exited" {
		t.Fatalf("the run state is %q, want exited", reported.State)
	}
	if reported.ExitCode == nil || *reported.ExitCode != 1 {
		t.Fatalf("the run's exit code is %v, want 1", reported.ExitCode)
	}
	if reported.StartedAt == "" || reported.FinishedAt == "" {
		t.Fatalf("the run's timestamps are %q and %q", reported.StartedAt, reported.FinishedAt)
	}
	if len(reported.ArtifactRefs) == 0 {
		t.Fatalf("the run reports no artifact refs: %s", out)
	}
}

// A request that names no item still runs, and nothing pretends it named one: the record
// carries no item_id at all rather than an empty string, the row holds NULL, and status
// answers the same absence. The negative control is the naming case above, which proves
// the field is reachable at all as well as absent here.
func TestStatusOmitsTheItemIDWhenTheRequestNamesNone(t *testing.T) {
	dir := scratch(t, "status-noitem")
	db := filepath.Join(dir, "board.sqlite3")
	req := base("run_001", "/bin/true")
	req.ItemID = ""
	code, out := execFace(t, db, filepath.Join(dir, "lanes"), envelope(t, "run.request", "req-1", req))
	if code != 0 {
		t.Fatalf("exec exited %d, want 0 for a run dispatched for no item: %s", code, out)
	}
	if raw, carried := payloadKey(t, out, "item_id"); carried {
		t.Fatalf("the record carries item_id %s for a run dispatched for no item", raw)
	}

	code, out = run(t, "status", "--store", db, "--run", "run_001")
	if code != 0 {
		t.Fatalf("status exited %d, want 0: %s", code, out)
	}
	if raw, carried := payloadKey(t, out, "run", "item_id"); carried {
		t.Fatalf("status reports item_id %s for a run dispatched for no item", raw)
	}
}

// A run the store does not hold is a negative decision, not an error: exit 1, and the
// record says the run was not found rather than answering a run that is not there.
func TestStatusReportsAMissingRunAsANegativeDecision(t *testing.T) {
	dir := scratch(t, "status-missing")
	db := filepath.Join(dir, "board.sqlite3")
	if code, out := execFace(t, db, filepath.Join(dir, "lanes"),
		envelope(t, "run.request", "req-1", base("run_001", "/bin/true"))); code != 0 {
		t.Fatalf("exec exited %d: %s", code, out)
	}

	code, out := run(t, "status", "--store", db, "--run", "run_999")
	if code != 1 {
		t.Fatalf("status exited %d, want 1: %s", code, out)
	}
	var got statusOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("status did not emit a record: %v\n%s", err, out)
	}
	if got.Payload.Found {
		t.Fatalf("status reports a run the store does not hold: %s", out)
	}
	if got.Payload.Run != nil {
		t.Fatalf("a missing run is reported as a run: %s", out)
	}
	if got.Payload.RunID != "run_999" {
		t.Fatalf("the record is about %q, want run_999", got.Payload.RunID)
	}
}

// A store that is not there is the caller's mistake about the path, not a schema
// mismatch: both read faces answer the substrate's usage exit and print no record.
func TestTheReadFacesRefuseAMissingStore(t *testing.T) {
	dir := scratch(t, "read-nostore")
	db := filepath.Join(dir, "absent.sqlite3")
	for _, args := range [][]string{
		{"lanes", "--store", db},
		{"status", "--store", db, "--run", "run_001"},
	} {
		code, out := run(t, args...)
		if code != 2 {
			t.Fatalf("%v exited %d, want 2: %s", args, code, out)
		}
		if len(out) != 0 {
			t.Fatalf("%v emitted a record for a store that is not there: %s", args, out)
		}
	}
}

// A store that does not carry run's tables is incompatible with this binary, not a bug
// in it: exit 3, so the caller is sent to the store it named.
func TestLanesRefusesAStoreWithoutRunTables(t *testing.T) {
	dir := scratch(t, "lanes-notables")
	db := filepath.Join(dir, "board.sqlite3")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatalf("creating the store failed: %v", err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, stmt := range []string{`DROP TABLE runs`, `DROP TABLE lanes`} {
			if _, err := tx.Exec(stmt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		s.Close()
		t.Fatalf("dropping the run tables failed: %v", err)
	}
	s.Close()

	code, out := run(t, "lanes", "--store", db)
	if code != 3 {
		t.Fatalf("lanes exited %d, want 3: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("lanes emitted a record for a store it cannot read: %s", out)
	}
}

// The read faces write nothing: the event head and every table count are the same
// after both faces as before them. A read face that set or released a lease would move
// the head, and the lease is the one thing about a lane that changes without the lane
// being rewritten.
func TestTheReadFacesWriteNothing(t *testing.T) {
	dir := scratch(t, "read-nowrite")
	db := filepath.Join(dir, "board.sqlite3")
	if code, out := execFace(t, db, filepath.Join(dir, "lanes"),
		envelope(t, "run.request", "req-1", base("run_001", "/bin/true"))); code != 0 {
		t.Fatalf("exec exited %d: %s", code, out)
	}
	before := snapshotOf(t, db)
	for _, args := range [][]string{
		{"lanes", "--store", db},
		{"status", "--store", db, "--run", "run_001"},
	} {
		if code, out := run(t, args...); code != 0 {
			t.Fatalf("%v exited %d: %s", args, code, out)
		}
	}
	after := snapshotOf(t, db)
	if after.head != before.head {
		t.Fatalf("the read faces moved the event head from %d to %d", before.head, after.head)
	}
	if len(after.counts) != len(before.counts) {
		t.Fatalf("the read faces changed the table set: %v then %v", before.counts, after.counts)
	}
	for table, count := range before.counts {
		if after.counts[table] != count {
			t.Fatalf("the read faces changed %q from %d rows to %d", table, count, after.counts[table])
		}
	}
}

// storeSnapshot is the store state a read face must leave alone.
type storeSnapshot struct {
	head   int64
	counts map[string]int
}

func snapshotOf(t *testing.T, db string) storeSnapshot {
	t.Helper()
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	head, err := s.EventHeadSeq()
	if err != nil {
		t.Fatalf("reading the event head failed: %v", err)
	}
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	return storeSnapshot{head: head, counts: counts}
}

// A negative timeout_ms is refused at claim: a wall cannot run backwards, and a request
// naming one is a staging mistake. The refusal is before anything is created, so a refused
// request leaves no lane directory, no lane row and no run row.
//
// timeout_ms 0 is NOT refused: it is an unbounded run, whose lane lease carries no expiry
// (design D-RW1, docs/design/run-wall-ruling.md).
func TestExecRefusesANegativeTimeout(t *testing.T) {
	dir := scratch(t, "exec-notimeout")
	db := filepath.Join(dir, "board.sqlite3")
	laneRoot := filepath.Join(dir, "lanes")
	req := base("run_001", "/bin/true")
	req.TimeoutMS = -1
	code, out := execFace(t, db, laneRoot, envelope(t, "run.request", "req-1", req))
	if code != 2 {
		t.Fatalf("a request with timeout_ms -1 exited %d, want 2: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("a refused request emitted a record: %s", out)
	}
	if n := rowCount(t, db, "lanes"); n != 0 {
		t.Fatalf("a refused request left %d lane rows", n)
	}
	if n := rowCount(t, db, "runs"); n != 0 {
		t.Fatalf("a refused request left %d run rows", n)
	}
	if _, err := os.Stat(laneRoot); !os.IsNotExist(err) {
		t.Fatalf("a refused request created the lane root %q", laneRoot)
	}
}

// An unbounded request runs: no clock kills it, its lane is leased with `leased_until`
// NULL so no reaper will ever take it, and the terminal record releases the lane in the
// same write (design D-RW1).
func TestAnUnboundedRequestRunsAndHoldsANullExpiryLease(t *testing.T) {
	dir := scratch(t, "exec-unbounded")
	db := filepath.Join(dir, "board.sqlite3")
	laneRoot := filepath.Join(dir, "lanes")
	req := base("run_001", "/bin/true")
	req.TimeoutMS = 0
	code, out := execFace(t, db, laneRoot, envelope(t, "run.request", "req-1", req))
	if code != 0 {
		t.Fatalf("an unbounded request exited %d: %s", code, out)
	}
	recorded := decode(t, out)
	if recorded.Kind != "run.record" || recorded.Payload.State != "exited" {
		t.Fatalf("an unbounded request recorded %q / %q", recorded.Kind, recorded.Payload.State)
	}
	// The terminal write releases the lane it held: an unbounded lease is still released by
	// the run's own record, which is what keeps an unbounded lane from staying taken.
	if n := rowCount(t, db, "lanes"); n != 1 {
		t.Fatalf("the store holds %d lane rows after an unbounded run, want the one lane", n)
	}
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	var (
		state       string
		leasedUntil sql.NullInt64
	)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT state, leased_until FROM lanes`).Scan(&state, &leasedUntil)
	}); err != nil {
		t.Fatalf("reading the lane failed: %v", err)
	}
	if state != "idle" || leasedUntil.Valid {
		t.Fatalf("the lane reads state %q lease %v, want idle with no expiry", state, leasedUntil)
	}
}

// releaseOut is the run.release record, decoded.
type releaseOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		LaneID         string `json:"lane_id"`
		RunID          string `json:"run_id"`
		Result         string `json:"result"`
		Reason         string `json:"reason"`
		TerminalRecord bool   `json:"terminal_record"`
	} `json:"payload"`
}

// The standalone release frees a lane whose lease has expired and records the operator's
// reason, and the lane reads free afterwards: `run lanes` is how an operator identifies a
// stale lane and this is how it is taken back.
func TestReleaseFreesALaneWhoseLeaseExpired(t *testing.T) {
	dir := scratch(t, "release")
	db := filepath.Join(dir, "board.sqlite3")
	runID := "run_001"
	expired := time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC).UnixMilli()
	seedLane(t, db, "lane_003", "leased", &runID, &expired)

	code, out := run(t, "release", "--store", db, "--lane", "lane_003",
		"--reason", "the agent's process was killed by hand", "--now", "2026-11-10T12:00:00Z")
	if code != 0 {
		t.Fatalf("release exited %d, want 0: %s", code, out)
	}
	var got releaseOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("release did not emit a record: %v\n%s", err, out)
	}
	if got.Kind != "run.release" {
		t.Fatalf("release emitted kind %q", got.Kind)
	}
	if got.Payload.LaneID != "lane_003" || got.Payload.Result != "released" ||
		got.Payload.Reason != "the agent's process was killed by hand" {
		t.Fatalf("the release reads %+v, want lane_003 released with the reason given", got.Payload)
	}

	code, out = run(t, "lanes", "--store", db)
	if code != 0 {
		t.Fatalf("lanes exited %d, want 0: %s", code, out)
	}
	var lanes lanesOut
	if err := json.Unmarshal(out, &lanes); err != nil {
		t.Fatalf("lanes did not emit a record: %v\n%s", err, out)
	}
	if len(lanes.Payload.Lanes) != 1 {
		t.Fatalf("lanes listed %d lanes, want 1: %s", len(lanes.Payload.Lanes), out)
	}
	if lane := lanes.Payload.Lanes[0]; lane.State != "idle" || lane.LeasedUntil != nil {
		t.Fatalf("the released lane is %q with lease %v, want idle and unleased", lane.State, lane.LeasedUntil)
	}
}

// A release that names no reason is refused before the store is read, and refuses a
// blank one too: whitespace is not a reason, and a lane freed without one is a lane
// nobody can account for.
func TestReleaseRefusesAReleaseWithNoReason(t *testing.T) {
	dir := scratch(t, "release-noreason")
	db := filepath.Join(dir, "board.sqlite3")
	runID := "run_001"
	expired := time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC).UnixMilli()
	seedLane(t, db, "lane_003", "leased", &runID, &expired)

	for _, args := range [][]string{
		{"release", "--store", db, "--lane", "lane_003"},
		{"release", "--store", db, "--lane", "lane_003", "--reason", "   "},
	} {
		code, out := run(t, args...)
		if code != 2 {
			t.Fatalf("%v exited %d, want 2: %s", args, code, out)
		}
		if len(out) != 0 {
			t.Fatalf("%v emitted a record: %s", args, out)
		}
	}
	code, out := run(t, "lanes", "--store", db)
	if code != 0 {
		t.Fatalf("lanes exited %d, want 0: %s", code, out)
	}
	var lanes lanesOut
	if err := json.Unmarshal(out, &lanes); err != nil {
		t.Fatalf("lanes did not emit a record: %v\n%s", err, out)
	}
	if lane := lanes.Payload.Lanes[0]; lane.State != "leased" {
		t.Fatalf("a refused release left the lane %q, want it leased", lane.State)
	}
}

// Releasing a lease that has not run out is refused by name and exits 1 as a completed
// negative decision: the record says which refusal, so a supervisor reads an answer
// rather than a diagnostic. The longest anyone waits out a wedged lane is its timeout
// plus the grace.
func TestReleaseRefusesAnUnexpiredLease(t *testing.T) {
	dir := scratch(t, "release-live")
	db := filepath.Join(dir, "board.sqlite3")
	runID := "run_001"
	live := time.Date(2026, 11, 10, 13, 0, 0, 0, time.UTC).UnixMilli()
	seedLane(t, db, "lane_003", "leased", &runID, &live)

	code, out := run(t, "release", "--store", db, "--lane", "lane_003",
		"--reason", "tidy up", "--now", "2026-11-10T12:00:00Z")
	if code != 1 {
		t.Fatalf("release exited %d, want 1: %s", code, out)
	}
	var got releaseOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("release did not emit a record: %v\n%s", err, out)
	}
	if got.Payload.Result != "refused" || got.Payload.Reason != "lease-unexpired" {
		t.Fatalf("the release reads %+v / %q, want refused / lease-unexpired", got.Payload, got.Payload.Result)
	}
}

// policyFile writes the backend policy document a request is derived from, and answers its
// path. The backend's adapter is a stub that records the argv and the working directory it was
// run with, so the case reads what the run actually executed rather than what the record says.
func policyFile(t *testing.T, dir, adapter string) string {
	t.Helper()
	path := filepath.Join(dir, "backend-policy.json")
	document := `{"default_backend":"fixture","preference":["fixture"],"backends":` +
		`{"fixture":{"adapter":"` + adapter + `","model":"fixture-model","timeout_seconds":60}}}`
	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		t.Fatalf("writing the policy failed: %v", err)
	}
	return path
}

// recordingAdapter writes a stub adapter that appends the directory it ran in and then its
// argv, and answers its path: the two things a run's derivation decides that the run itself
// cannot be asked about afterwards.
func recordingAdapter(t *testing.T, dir string) (adapter, cwdOut, argvOut string) {
	t.Helper()
	adapter, cwdOut, argvOut = filepath.Join(dir, "adapter"), filepath.Join(dir, "cwd"), filepath.Join(dir, "argv")
	body := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" > " + cwdOut + "\nprintf '%s\\n' \"$@\" > " + argvOut + "\nexit 0\n"
	if err := os.WriteFile(adapter, []byte(body), 0o755); err != nil {
		t.Fatalf("writing the adapter failed: %v", err)
	}
	return adapter, cwdOut, argvOut
}

// A run.request the face derives is consumed by exec without editing: the record it prints
// goes to exec as it stands, the run it describes is the run that happens, and the argv and
// working directory the request named are the ones the adapter was actually run with.
func TestARequestFromTheRequestFaceExecutesAsItStands(t *testing.T) {
	dir := scratch(t, "request-roundtrip")
	adapter, cwdOut, argvOut := recordingAdapter(t, dir)
	policy := policyFile(t, dir, adapter)
	now := "2026-11-13T00:00:00Z"

	code, out := run(t, "request", "--policy", policy, "--backend", "fixture",
		"--item", "item_042", "--lane", "lane_1", "--run", "run_042", "--now", now)
	if code != 0 {
		t.Fatalf("request exited %d: %s", code, out)
	}
	var requestOut struct {
		Kind      string          `json:"kind"`
		ID        string          `json:"id"`
		CreatedAt string          `json:"created_at"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(out, &requestOut); err != nil {
		t.Fatalf("request did not emit a record: %v\n%s", err, out)
	}
	if requestOut.Kind != "run.request" {
		t.Fatalf("request emitted kind %q", requestOut.Kind)
	}
	if requestOut.ID != "run-request-run_042" {
		t.Fatalf("the record's id is %q", requestOut.ID)
	}
	if requestOut.CreatedAt != now {
		t.Fatalf("the record is stamped %q, want the injected clock %q", requestOut.CreatedAt, now)
	}
	var derived struct {
		RunID      string   `json:"run_id"`
		ItemID     string   `json:"item_id"`
		LaneID     string   `json:"lane_id"`
		Backend    string   `json:"backend"`
		Command    []string `json:"command"`
		WorkingDir string   `json:"working_dir"`
		TimeoutMS  int      `json:"timeout_ms"`
	}
	if err := json.Unmarshal(requestOut.Payload, &derived); err != nil {
		t.Fatalf("the payload is not a run.request: %v", err)
	}
	if derived.RunID != "run_042" || derived.ItemID != "item_042" || derived.LaneID != "lane_1" || derived.Backend != "fixture" {
		t.Fatalf("the request names run %q / item %q / lane %q / backend %q",
			derived.RunID, derived.ItemID, derived.LaneID, derived.Backend)
	}
	if derived.WorkingDir != "worktree" || derived.TimeoutMS != 60000 {
		t.Fatalf("the request's working_dir is %q and its timeout_ms %d", derived.WorkingDir, derived.TimeoutMS)
	}
	if want := strings.Join([]string{adapter, "--backend", "fixture", "--model", "fixture-model", "--timeout", "60"}, " "); strings.Join(derived.Command, " ") != want {
		t.Fatalf("the request's command is %v, want %q", derived.Command, want)
	}

	// The record goes to exec exactly as the face printed it, which is the whole claim: one
	// producer, and the consumer takes its output unedited.
	laneRoot := filepath.Join(dir, "lanes")
	code, execOut := execFace(t, filepath.Join(dir, "board.sqlite3"), laneRoot, string(out))
	if code != 0 {
		t.Fatalf("exec refused the derived request: exit %d: %s", code, execOut)
	}
	recorded := decode(t, execOut)
	if recorded.Kind != "run.record" || recorded.Payload.State != "exited" {
		t.Fatalf("the run did not complete: %+v", recorded.Payload)
	}
	if recorded.Payload.RunID != "run_042" || recorded.Payload.LaneID != "lane_1" || recorded.Payload.ItemID != "item_042" {
		t.Fatalf("the run is %q in lane %q for item %q", recorded.Payload.RunID, recorded.Payload.LaneID, recorded.Payload.ItemID)
	}
	if recorded.Payload.Backend != "fixture" {
		t.Fatalf("the record names backend %q, want the request's", recorded.Payload.Backend)
	}

	// The values the request carried reached the process: the argv it named, and the lane's
	// worktree as the directory it ran in.
	argv, err := os.ReadFile(argvOut)
	if err != nil {
		t.Fatalf("the adapter did not record its argv: %v", err)
	}
	if got := strings.TrimSpace(string(argv)); got != "--backend\nfixture\n--model\nfixture-model\n--timeout\n60" {
		t.Fatalf("the adapter was run with %q, want the request's argv", got)
	}
	cwd, err := os.ReadFile(cwdOut)
	if err != nil {
		t.Fatalf("the adapter did not record its working directory: %v", err)
	}
	if want := filepath.Join(laneRoot, "lane_1", "worktree"); strings.TrimSpace(string(cwd)) != want {
		t.Fatalf("the adapter ran in %q, want %q", strings.TrimSpace(string(cwd)), want)
	}
}

// The face refuses what it cannot derive, before it prints anything: a request that named a
// backend the policy does not carry, or one that cannot produce a runnable request, would be a
// record asserting a run that cannot happen.
func TestTheRequestFaceRefusesWhatItCannotDerive(t *testing.T) {
	dir := scratch(t, "request-refusals")
	adapter, _, _ := recordingAdapter(t, dir)
	policy := policyFile(t, dir, adapter)

	t.Run("no backend named", func(t *testing.T) {
		code, out := run(t, "request", "--policy", policy, "--item", "item_042", "--lane", "lane_1", "--run", "run_042")
		if code != 2 {
			t.Fatalf("request exited %d, want 2: %s", code, out)
		}
		if len(out) != 0 {
			t.Fatalf("a refused request emitted a record: %s", out)
		}
	})
	t.Run("a backend the policy does not carry", func(t *testing.T) {
		code, out := run(t, "request", "--policy", policy, "--backend", "elsewhere",
			"--item", "item_042", "--lane", "lane_1", "--run", "run_042")
		if code != 2 {
			t.Fatalf("request exited %d, want 2: %s", code, out)
		}
	})
	t.Run("a backend that cannot build a request", func(t *testing.T) {
		broken := filepath.Join(dir, "broken-policy.json")
		document := `{"default_backend":"fixture","backends":{"fixture":{"adapter":"` + adapter + `","model":"m","timeout_seconds":-1}}}`
		if err := os.WriteFile(broken, []byte(document), 0o644); err != nil {
			t.Fatalf("writing the policy failed: %v", err)
		}
		code, out := run(t, "request", "--policy", broken, "--backend", "fixture",
			"--item", "item_042", "--lane", "lane_1", "--run", "run_042")
		if code != 2 {
			t.Fatalf("request exited %d for a backend with a negative timeout, want 2: %s", code, out)
		}
	})
	t.Run("a backend with no timeout derives an unbounded request", func(t *testing.T) {
		unbounded := filepath.Join(dir, "unbounded-policy.json")
		document := `{"default_backend":"fixture","backends":{"fixture":{"adapter":"` + adapter + `","model":"m"}}}`
		if err := os.WriteFile(unbounded, []byte(document), 0o644); err != nil {
			t.Fatalf("writing the policy failed: %v", err)
		}
		code, out := run(t, "request", "--policy", unbounded, "--backend", "fixture",
			"--item", "item_042", "--lane", "lane_1", "--run", "run_042")
		if code != 0 {
			t.Fatalf("request exited %d for a backend with no timeout: %s", code, out)
		}
		var emitted struct {
			Payload   json.RawMessage `json:"payload"`
			CreatedAt string          `json:"created_at"`
		}
		if err := json.Unmarshal(out, &emitted); err != nil {
			t.Fatalf("request did not emit a record: %v\n%s", err, out)
		}
		var derived struct {
			TimeoutMS int      `json:"timeout_ms"`
			Command   []string `json:"command"`
		}
		if err := json.Unmarshal(emitted.Payload, &derived); err != nil {
			t.Fatalf("the payload is not a run.request: %v", err)
		}
		if derived.TimeoutMS != 0 {
			t.Fatalf("an unbounded request carries timeout_ms %d, want 0", derived.TimeoutMS)
		}
		for _, element := range derived.Command {
			if element == "--timeout" {
				t.Fatalf("an unbounded request passes a wall to the adapter: %v", derived.Command)
			}
		}
	})
	t.Run("a policy that is not there", func(t *testing.T) {
		code, out := run(t, "request", "--policy", filepath.Join(dir, "absent.json"), "--backend", "fixture",
			"--item", "item_042", "--lane", "lane_1", "--run", "run_042")
		if code != 2 {
			t.Fatalf("request exited %d, want 2: %s", code, out)
		}
	})
}

// TestHelpIsTheFaceSurface pins the layer-1 surface (docs/design/tillr-distribution.md
// Section 3): `--help` names the faces and a face's `--help` names that face's flags, both
// on stdout and both exit 0.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := run(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, out)
	}
	for _, face := range []string{"exec", "request", "lanes", "status", "release"} {
		if !strings.Contains(string(out), face) {
			t.Errorf("--help does not name the face %q: %s", face, out)
		}
	}
	code, out = run(t, "exec", "--help")
	if code != 0 {
		t.Fatalf("exec --help exited %d, want 0: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of tillr-run exec") {
		t.Errorf("exec --help does not name the face's flags: %s", out)
	}
	if code, out = run(t, "bogus", "--help"); code != 2 {
		t.Errorf("a face that does not exist exited %d, want 2: %s", code, out)
	}
}

// A resume continues the run instead of starting one: the same runs row, a run.resume span on
// it, the session staging pinned, and the run's log appended to rather than truncated.
//
// This is the second criterion's store half, measured where the store is written. The log is
// the half that is easy to lose: `os.Create` here would delete the killed segment's transcript
// — the evidence a resume exists to keep and the report a run is classified from — while every
// row still looked right.
//
// The negative control is the last case: a resume with no `session-id` in the lane is refused
// before anything is claimed, and its refusal leaves no row and no run.resume event, so a cold
// start cannot wear a continuation's name.
func TestExecResumeContinuesTheSameRunAndAppendsItsLog(t *testing.T) {
	dir := scratch(t, "exec-resume")
	db := filepath.Join(dir, "board.sqlite3")
	laneRoot := filepath.Join(dir, "lanes")
	laneDir := filepath.Join(laneRoot, "lane_1")
	if err := os.MkdirAll(laneDir, 0o755); err != nil {
		t.Fatalf("creating the lane directory failed: %v", err)
	}
	const sessionID = "11111111-1111-4111-8111-111111111111"
	if err := os.WriteFile(filepath.Join(laneDir, "session-id"), []byte(sessionID+"\n"), 0o644); err != nil {
		t.Fatalf("writing the lane's session id failed: %v", err)
	}
	req := base("run_001", "/bin/sh", "-c", "echo segment-one")
	req.LaneID = "lane_1"
	if code, out := execFace(t, db, laneRoot, envelope(t, "run.request", "req-1", req)); code != 0 {
		t.Fatalf("the first segment exited %d: %s", code, out)
	}
	if resumes := eventsOfKind(t, db, "run.resume"); resumes != 0 {
		t.Fatalf("a first execution appended %d run.resume event(s)", resumes)
	}
	req.Resume = true
	req.Command = []string{"/bin/sh", "-c", "echo segment-two"}
	code, out := execFace(t, db, laneRoot, envelope(t, "run.request", "req-2", req))
	if code != 0 {
		t.Fatalf("the resumed segment exited %d: %s", code, out)
	}
	if got := decode(t, out); !got.Payload.Resumed {
		t.Fatalf("the resumed segment's record does not say it was resumed: %s", out)
	}
	if rows := rowCount(t, db, "runs"); rows != 1 {
		t.Fatalf("the store holds %d runs rows, want 1: a resume is one attempt continued, not a second attempt", rows)
	}
	if resumes := eventsOfKind(t, db, "run.resume"); resumes != 1 {
		t.Fatalf("the store holds %d run.resume event(s), want 1", resumes)
	}
	logText, err := os.ReadFile(filepath.Join(laneDir, "run_001.log"))
	if err != nil {
		t.Fatalf("reading the run's log failed: %v", err)
	}
	for _, want := range []string{"segment-one", "segment-two"} {
		if !strings.Contains(string(logText), want) {
			t.Fatalf("the run's log does not hold %q, so the resume truncated the killed segment's log:\n%s", want, logText)
		}
	}
	// The negative control: no session id, no resume — and nothing claimed.
	if err := os.Remove(filepath.Join(laneDir, "session-id")); err != nil {
		t.Fatalf("removing the session id failed: %v", err)
	}
	req.Resume = true
	req.Command = []string{"/bin/sh", "-c", "echo must-not-run"}
	code, out = execFace(t, db, laneRoot, envelope(t, "run.request", "req-3", req))
	// A usage refusal (2) and no record: the face refused before it claimed anything.
	if code != 2 {
		t.Fatalf("a resume with no session to continue exited %d, want 2 (usage): %s", code, out)
	}
	if len(bytes.TrimSpace(out)) != 0 {
		t.Fatalf("the refused resume printed a record: %s", out)
	}
	if resumes := eventsOfKind(t, db, "run.resume"); resumes != 1 {
		t.Fatalf("the refused resume appended a run.resume event (%d), so it claimed the lane before it was refused", resumes)
	}
}
