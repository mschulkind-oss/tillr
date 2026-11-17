package main_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tillr-store-test-")
	if err != nil {
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "tillr-store")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func run(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+t.TempDir())
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out
}

// The store's own report is what every other utility's fixture starts from, so its
// shape is a contract: a caller that cannot read schema_version cannot decide
// whether to proceed.
func TestCheckReportsAFixtureStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	code, out := run(t, "check", "--store", path)
	if code != 0 {
		t.Fatalf("check exited %d: %s", code, out)
	}
	var record struct {
		Kind    string `json:"kind"`
		Payload struct {
			SchemaVersion int    `json:"schema_version"`
			Fixture       bool   `json:"fixture"`
			Integrity     string `json:"integrity"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("check did not emit a record: %v\n%s", err, out)
	}
	if record.Kind != "store.report" {
		t.Fatalf("check emitted kind %q", record.Kind)
	}
	if !record.Payload.Fixture {
		t.Fatal("a store created with --fixture does not report itself as a fixture")
	}
	if record.Payload.Integrity != "ok" {
		t.Fatalf("integrity is %q", record.Payload.Integrity)
	}
}

// Seeding a live store is refused, and the refusal is exit 2 with nothing written.
func TestSeedRefusesALiveStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	seed := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(seed, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "seed", "--store", path, "--input", seed); code != 2 {
		t.Fatalf("seeding a live store exited %d, want 2: %s", code, out)
	}
}

// The events file is a cursor's payload, so each event carries the seq the next
// `--since` advances to. A file without it is a log a caller can read once and
// never resume, which is the whole reason the face exists.
func TestEventsWritesTheSeqSoTheCursorAdvances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	seed := filepath.Join(dir, "seed.json")
	if err := os.WriteFile(seed, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "seed", "--store", path, "--input", seed); code != 0 {
		t.Fatalf("seed exited %d: %s", code, out)
	}

	all := filepath.Join(dir, "events.json")
	if code, out := run(t, "events", "--store", path, "--since", "0", "--output", all); code != 0 {
		t.Fatalf("events exited %d: %s", code, out)
	}
	raw, err := os.ReadFile(all)
	if err != nil {
		t.Fatal(err)
	}
	var events []struct {
		Seq  int64  `json:"seq"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("the events file is not a JSON array: %v\n%s", err, raw)
	}
	if len(events) != 1 {
		t.Fatalf("reading from 0 wrote %d events, want 1", len(events))
	}
	if events[0].Seq != 1 {
		t.Fatalf("the event's seq is %d, want 1", events[0].Seq)
	}

	// Advancing to the seq just read is a tail, not a repeat: that is what makes
	// the cursor usable.
	tail := filepath.Join(dir, "tail.json")
	tailCode, tailOut := run(t, "events", "--store", path, "--since", "1", "--output", tail)
	if tailCode != 0 {
		t.Fatalf("events exited %d: %s", tailCode, tailOut)
	}
	raw, err = os.ReadFile(tail)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("the tail file is not a JSON array: %v\n%s", err, raw)
	}
	if len(events) != 0 {
		t.Fatalf("reading from the seq just read wrote %d events, want 0", len(events))
	}
	// An empty file leaves the cursor where it was: next_since is the last seq in the
	// file, and a file with no events has none to advance to.
	var tailRecord struct {
		Payload struct {
			NextSince int64 `json:"next_since"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(tailOut, &tailRecord); err != nil {
		t.Fatalf("events did not emit a record: %v\n%s", err, tailOut)
	}
	if tailRecord.Payload.NextSince != 1 {
		t.Fatalf("next_since is %d for an empty file read from 1, want the unchanged 1", tailRecord.Payload.NextSince)
	}

	// A cursor past the head is still the caller's: next_since is the last seq in the
	// file, and an empty file has none, so the cursor does not move backwards to the
	// store's head.
	ahead := filepath.Join(dir, "ahead.json")
	aheadCode, aheadOut := run(t, "events", "--store", path, "--since", "5", "--output", ahead)
	if aheadCode != 0 {
		t.Fatalf("events exited %d: %s", aheadCode, aheadOut)
	}
	var aheadRecord struct {
		Payload struct {
			NextSince int64 `json:"next_since"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(aheadOut, &aheadRecord); err != nil {
		t.Fatalf("events did not emit a record: %v\n%s", err, aheadOut)
	}
	if aheadRecord.Payload.NextSince != 5 {
		t.Fatalf("next_since is %d for an empty file read from 5, want the unchanged 5", aheadRecord.Payload.NextSince)
	}
}

// runStderr is run with the diagnostic stream kept, because exit 3's contract is a
// report on stdout *and* a reason on stderr: a caller that learns neither the store's
// version nor the supported one cannot act on the refusal.
func runStderr(t *testing.T, args ...string) (int, []byte, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out, stderr.String()
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out, stderr.String()
}

// setSchemaVersion moves a store off the version this binary migrates to, so a face's
// refusal is observable. It writes the migration rows directly: the store package
// migrates forward only, and a test that could not build an incompatible store could
// not test the refusal at all.
func setSchemaVersion(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening %s to set its schema version failed: %v", path, err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clearing the migration rows failed: %v", err)
	}
	if version == 0 {
		return
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)`, version); err != nil {
		t.Fatalf("writing schema version %d failed: %v", version, err)
	}
}

// payloadKeys answers the exact key set of a record's payload, sorted. A struct with
// a json tag decodes a renamed tag to a zero value and says nothing, so the tag names
// are pinned by reading the document as a map instead.
func payloadKeys(t *testing.T, out []byte) []string {
	t.Helper()
	var record struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
	keys := make([]string, 0, len(record.Payload))
	for key := range record.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// The report is a contract, not a convenience: §5.2 names six fields, and a field that
// is decoded and never compared is a field a rename deletes without a red test. Every
// one is asserted against a known value on a store whose contents are known.
func TestCheckReportsEveryContractField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	code, out := run(t, "check", "--store", path)
	if code != 0 {
		t.Fatalf("check exited %d: %s", code, out)
	}
	var record struct {
		Payload struct {
			Action                  string         `json:"action"`
			SchemaVersion           int            `json:"schema_version"`
			SupportedRecordVersions []int          `json:"supported_record_versions"`
			EventHeadSeq            int64          `json:"event_head_seq"`
			TableCounts             map[string]int `json:"table_counts"`
			Fixture                 bool           `json:"fixture"`
			Integrity               string         `json:"integrity"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("check did not emit a record: %v\n%s", err, out)
	}
	p := record.Payload
	if p.Action != "check" {
		t.Fatalf("action is %q, want %q", p.Action, "check")
	}
	if p.SchemaVersion != store.SchemaVersion {
		t.Fatalf("schema_version is %d, want %d", p.SchemaVersion, store.SchemaVersion)
	}
	if len(p.SupportedRecordVersions) != 1 || p.SupportedRecordVersions[0] != 1 {
		t.Fatalf("supported_record_versions is %v, want [1]", p.SupportedRecordVersions)
	}
	if p.EventHeadSeq != 0 {
		t.Fatalf("event_head_seq is %d, want 0 on a store with no events", p.EventHeadSeq)
	}
	for table, want := range map[string]int{"meta": 1, "schema_migrations": 1, "events": 0} {
		if got, ok := p.TableCounts[table]; !ok || got != want {
			t.Fatalf("table_counts[%q] is %d (present %v), want %d: %v", table, got, ok, want, p.TableCounts)
		}
	}
	if !p.Fixture {
		t.Fatal("fixture is false on a store created with --fixture")
	}
	if p.Integrity != "ok" {
		t.Fatalf("integrity is %q, want ok", p.Integrity)
	}
}

// The payload's key set is the contract's spelling, so a renamed tag fails here even
// though it decodes cleanly into a struct. The events face carries the cursor fields
// on top of the six, and nothing else.
func TestTheReportPayloadCarriesExactlyTheContractKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	seed := filepath.Join(dir, "seed.json")
	if err := os.WriteFile(seed, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "seed", "--store", path, "--input", seed); code != 0 {
		t.Fatalf("seed exited %d: %s", code, out)
	}

	report := []string{
		"action", "event_head_seq", "fixture", "integrity",
		"schema_version", "supported_record_versions", "table_counts",
	}
	// check also reports the write-ahead log, so its size is visible without a tool that
	// reads SQLite's files.
	checked := append(append([]string{}, report...), "log")
	sort.Strings(checked)
	code, out := run(t, "check", "--store", path)
	if code != 0 {
		t.Fatalf("check exited %d: %s", code, out)
	}
	if got := payloadKeys(t, out); !slices.Equal(got, checked) {
		t.Fatalf("check's payload keys are %v, want %v", got, checked)
	}

	events := append(append([]string{}, report...), "event_count", "next_since", "since")
	sort.Strings(events)
	code, out = run(t, "events", "--store", path, "--since", "0", "--output", filepath.Join(dir, "events.json"))
	if code != 0 {
		t.Fatalf("events exited %d: %s", code, out)
	}
	if got := payloadKeys(t, out); !slices.Equal(got, events) {
		t.Fatalf("events' payload keys are %v, want %v", got, events)
	}
}

// §5.1 defines exit 3 as the schema-incompatible answer generally, so every face
// answers it -- before it does anything, with the report on stdout because the report
// is the answer, and with a reason on stderr because a silent refusal teaches the
// caller nothing.
func TestEveryFaceRefusesAnIncompatibleSchema(t *testing.T) {
	// None applied, and one ahead of the version this binary writes: the fixture names
	// the version it is incompatible with rather than a number, so a schema change does
	// not silently make this case compatible.
	for _, version := range []int{0, store.SchemaVersion + 1} {
		for _, face := range []string{"init", "seed", "check", "events", "checkpoint"} {
			t.Run(fmt.Sprintf("%s/version-%d", face, version), func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "board.sqlite3")
				if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
					t.Fatalf("init exited %d: %s", code, out)
				}
				setSchemaVersion(t, path, version)

				output := filepath.Join(dir, "events.json")
				args := map[string][]string{
					"init":       {"init", "--store", path, "--fixture"},
					"seed":       {"seed", "--store", path, "--input", seedBundle(t, dir)},
					"check":      {"check", "--store", path},
					"events":     {"events", "--store", path, "--since", "0", "--output", output},
					"checkpoint": {"checkpoint", "--store", path},
				}[face]
				code, out, stderr := runStderr(t, args...)
				if code != 3 {
					t.Fatalf("%s on a version-%d store exited %d, want 3: %s", face, version, code, stderr)
				}
				var record struct {
					Payload struct {
						SchemaVersion int `json:"schema_version"`
					} `json:"payload"`
				}
				if err := json.Unmarshal(out, &record); err != nil {
					t.Fatalf("%s exited 3 without a report on stdout: %v\n%s", face, err, out)
				}
				if record.Payload.SchemaVersion != version {
					t.Fatalf("%s reported schema_version %d, want the store's %d", face, record.Payload.SchemaVersion, version)
				}
				if strings.TrimSpace(stderr) == "" {
					t.Fatalf("%s exited 3 with nothing on stderr", face)
				}
				if face == "events" {
					if _, err := os.Stat(output); err == nil {
						t.Fatal("events wrote its output file for a store it refused")
					}
				}
			})
		}
	}
}

// seedBundle writes an empty seed bundle and answers its path.
func seedBundle(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "seed.json")
	if err := os.WriteFile(path, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// §5.1: seeding a live store must be impossible, not merely unlikely. `init --fixture`
// is the second command that could defeat it, so it refuses a store that already
// exists and is not already a fixture -- and refuses before it writes, so the store is
// unchanged byte for byte.
func TestInitFixtureRefusesALiveStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.sqlite3")
	if code, out := run(t, "init", "--store", path); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	// The store's content, not its file: a commit stays in the write-ahead log, so the
	// database file's bytes do not move when a face writes (store.Fingerprint).
	before, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runStderr(t, "init", "--store", path, "--fixture")
	if code != 2 {
		t.Fatalf("init --fixture on a live store exited %d, want 2: %s%s", code, out, stderr)
	}
	after, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("init --fixture changed a live store it refused")
	}
}

// A missing store is not a schema mismatch: exit 3 sends the caller looking for a
// migration, and the answer is that the path is wrong.
func TestAMissingStoreIsNotASchemaRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sqlite3")
	code, _, stderr := runStderr(t, "check", "--store", path)
	if code == 3 {
		t.Fatalf("check on a missing store exited 3, which means schema incompatible: %s", stderr)
	}
	if code != 2 {
		t.Fatalf("check on a missing store exited %d, want 2: %s", code, stderr)
	}
}

// --output naming the store would truncate the store it was asked to read, so the face
// refuses it rather than destroying the subject.
func TestEventsRefusesToWriteOverItsOwnStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runStderr(t, "events", "--store", path, "--since", "0", "--output", path)
	if code != 2 {
		t.Fatalf("events --output <the store> exited %d, want 2: %s%s", code, out, stderr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The bytes catch a truncation of the file; the fingerprint catches a write, which
	// stays in the write-ahead log and leaves the file's bytes alone (store.Fingerprint).
	if !bytes.Equal(before, after) {
		t.Fatal("events --output <the store> truncated the store")
	}
	if now, err := store.Fingerprint(context.Background(), path); err != nil || now != content {
		t.Fatalf("events --output <the store> changed the store's content (%v)", err)
	}
}

// TestHelpIsTheFaceSurface pins the layer-1 surface (docs/design/tillr-distribution.md
// Section 3): `--help` names the faces and a face's `--help` names that face's flags, both
// on stdout and both exit 0. A prosa flag table is what this surface exists to replace, so
// `--help` returning a usage error would make the documents cite a page that is not there.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := run(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, out)
	}
	for _, face := range []string{"init", "seed", "check", "events", "checkpoint"} {
		if !strings.Contains(string(out), face) {
			t.Errorf("--help does not name the face %q: %s", face, out)
		}
	}
	code, out = run(t, "events", "--help")
	if code != 0 {
		t.Fatalf("events --help exited %d, want 0: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of tillr-store events") {
		t.Errorf("events --help does not name the face's flags: %s", out)
	}
	if code, out = run(t, "bogus", "--help"); code != 2 {
		t.Errorf("a face that does not exist exited %d, want 2: %s", code, out)
	}
}

// logPayload is the "log" object a check or checkpoint report carries.
type logPayload struct {
	Bytes  int64 `json:"bytes"`
	Frames int   `json:"frames"`
	Known  bool  `json:"known"`
}

// check reports the write-ahead log's size: no program's own work copies the log into the
// database file any more, so how far it has grown is something an operator reads here.
func TestCheckReportsTheLogsSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	code, out := run(t, "check", "--store", path)
	if code != 0 {
		t.Fatalf("check exited %d: %s", code, out)
	}
	var record struct {
		Payload struct {
			Log *logPayload `json:"log"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("check did not emit a record: %v\n%s", err, out)
	}
	info, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatalf("init left no write-ahead log: %v", err)
	}
	l := record.Payload.Log
	if l == nil || !l.Known || l.Frames == 0 || l.Bytes != info.Size() {
		t.Fatalf("check's log is %+v, want known, with frames, and %d bytes", l, info.Size())
	}
}

// rowsInDatabaseFileAlone counts the migration rows in a copy of the database file without its
// log. The test process holds no connection to the store, so reading the file is safe.
func rowsInDatabaseFileAlone(t *testing.T, path string) (int, error) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	alone := filepath.Join(t.TempDir(), "alone.sqlite3")
	if err := os.WriteFile(alone, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+alone)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	err = db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n)
	return n, err
}

// The checkpoint face is the supported way to make the database file alone current: after
// it, a copy of that one file holds every commit, which is what a backup that copies only
// the database file needs. Its report says what it copied and that the log restarted.
func TestTheCheckpointFaceMakesTheDatabaseFileAloneCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	if _, err := rowsInDatabaseFileAlone(t, path); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("before the checkpoint the database file alone already held the schema (%v); the case proves nothing", err)
	}
	code, out, stderr := runStderr(t, "checkpoint", "--store", path)
	if code != 0 {
		t.Fatalf("checkpoint exited %d: %s%s", code, out, stderr)
	}
	var record struct {
		Payload struct {
			Action     string      `json:"action"`
			Log        *logPayload `json:"log"`
			Checkpoint *struct {
				LogFrames    int  `json:"log_frames"`
				CopiedFrames int  `json:"copied_frames"`
				Restarted    bool `json:"restarted"`
			} `json:"checkpoint"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &record); err != nil {
		t.Fatalf("checkpoint did not emit a record: %v\n%s", err, out)
	}
	p := record.Payload
	if p.Action != "checkpoint" || p.Checkpoint == nil || p.Log == nil {
		t.Fatalf("the checkpoint report is incomplete: %s", out)
	}
	if !p.Checkpoint.Restarted || p.Checkpoint.LogFrames == 0 || p.Checkpoint.CopiedFrames != p.Checkpoint.LogFrames {
		t.Fatalf("the checkpoint did not copy the whole log and restart it: %+v", *p.Checkpoint)
	}
	if n, err := rowsInDatabaseFileAlone(t, path); err != nil || n != 1 {
		t.Fatalf("after the checkpoint the database file alone holds %d migration rows (%v), want 1", n, err)
	}
}

// While a drain loop owns the store's checkpoints the face refuses: the loop checkpoints
// every minute and when it stops, and two cycles at once defeat each other's restart. The
// refusal is a negative answer (exit 1) with the reason on stderr, and nothing is
// checkpointed.
func TestTheCheckpointFaceRefusesWhileALoopOwnsCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	owner, err := store.OwnCheckpoints(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	code, out, stderr := runStderr(t, "checkpoint", "--store", path)
	if code != 1 {
		t.Fatalf("checkpoint with a loop owning checkpoints exited %d, want 1: %s%s", code, out, stderr)
	}
	if !strings.Contains(stderr, "drain loop") {
		t.Errorf("the refusal does not say a drain loop owns the checkpoints: %q", stderr)
	}
}

// truncateReport is the part of a `checkpoint --truncate` record these cases read.
type truncateReport struct {
	Payload struct {
		Log        *logPayload `json:"log"`
		Checkpoint *struct {
			Truncated *bool `json:"truncated"`
		} `json:"checkpoint"`
	} `json:"payload"`
}

// growLogByFace writes enough rows through the store's own handle that its close's cycle
// leaves a log file of several megabytes: a restart keeps the file's length.
func growLogByFace(t *testing.T, path string) {
	t.Helper()
	s, err := store.Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("x", 4000)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for i := 0; i < 1100; i++ {
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, fmt.Sprintf("grow-%d", i), value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// `checkpoint --truncate` with nothing else holding the store shrinks the log file to one
// frame and says so (truncated true, and the log's size).
func TestTheCheckpointFaceTruncatesTheLogWhenNothingElseHasTheStoreOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	growLogByFace(t, path)
	grown := store.ReadLog(path).Bytes
	if grown < 4<<20 {
		t.Fatalf("the log file is %d bytes; the case needs a long one", grown)
	}
	code, out, stderr := runStderr(t, "checkpoint", "--store", path, "--truncate")
	if code != 0 {
		t.Fatalf("checkpoint --truncate exited %d: %s%s", code, out, stderr)
	}
	var r truncateReport
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("checkpoint --truncate did not emit a record: %v\n%s", err, out)
	}
	p := r.Payload
	if p.Checkpoint == nil || p.Checkpoint.Truncated == nil || !*p.Checkpoint.Truncated || p.Log == nil {
		t.Fatalf("the report does not say the log was truncated: %s", out)
	}
	if p.Log.Frames != 1 || p.Log.Bytes >= grown/100 {
		t.Fatalf("the reported log is %+v (it was %d bytes), want one frame", *p.Log, grown)
	}
}

// Another process holding the store open refuses the truncate, exit 1, with the reason on
// stderr, and the log file keeps its size. The other process is this test's own: the face
// runs as a child.
func TestTheCheckpointFaceRefusesToTruncateWhileAnotherProcessHasTheStoreOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	if code, out := run(t, "init", "--store", path, "--fixture"); code != 0 {
		t.Fatalf("init exited %d: %s", code, out)
	}
	growLogByFace(t, path)
	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Describe(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := store.ReadLog(path)
	code, out, stderr := runStderr(t, "checkpoint", "--store", path, "--truncate")
	if code != 1 {
		t.Fatalf("checkpoint --truncate with the store open elsewhere exited %d, want 1: %s%s", code, out, stderr)
	}
	if !strings.Contains(stderr, "another process has the store open") || !strings.Contains(stderr, fmt.Sprint(os.Getpid())) {
		t.Errorf("the refusal does not name the process holding the store: %q", stderr)
	}
	if after := store.ReadLog(path); after != before {
		t.Fatalf("a refused truncate changed the log: %+v then %+v", before, after)
	}
}
