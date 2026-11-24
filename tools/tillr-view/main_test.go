package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

var viewBin string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns before TestMain exits, so cleanup also runs after setup or test failure.
func runTests(m *testing.M) int {
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 1
	}
	dir, err := os.MkdirTemp(root, "tillr-view-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	viewBin = filepath.Join(dir, "tillr-view")
	build := exec.Command("go", "build", "-o", viewBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}
	return m.Run()
}

// runView runs one face and answers its exit code and stdout. The environment is
// scrubbed of BOARD_DB and pointed at a scratch state home, so a face that derived a
// store instead of reading the one it was given would read nothing rather than the
// live board.
func runView(t *testing.T, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(viewBin, args...)
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

// fixture creates a fixture store in-test. The suite builds its own stores rather
// than shelling out to tillr-store: a module that needs another binary to test itself
// is not independently usable (design §5).
func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating the fixture failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture failed: %v", err)
	}
	return path
}

// A seed fixture must not depend on another test registering its event kinds first.
func TestSeedRegistersItsRecordKinds(t *testing.T) {
	path := fixture(t)
	seed(t, path, envelope(t, "view-test.independent-seed", "r-independent", nil))
}

// seed imports records into a fixture store through the substrate's own seed path.
func seed(t *testing.T, path string, records ...record.Envelope) {
	t.Helper()
	for _, r := range records {
		if err := event.Register(r.Kind, "view-test"); err != nil {
			t.Fatalf("registering seed kind %q: %v", r.Kind, err)
		}
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("encoding the seed records: %v", err)
	}
	bundle, err := json.Marshal(map[string]any{
		"kind":    "fixture.seed",
		"payload": map[string]json.RawMessage{"records": raw},
	})
	if err != nil {
		t.Fatalf("encoding the seed bundle: %v", err)
	}
	s, err := store.Open(context.Background(), path, false)
	if err != nil {
		t.Fatalf("opening the fixture failed: %v", err)
	}
	defer s.Close()
	if err := s.Seed(context.Background(), bundle); err != nil {
		t.Fatalf("seeding the fixture failed: %v", err)
	}
}

// appendEvent writes one event that names a subject. The store package exposes no
// write transaction and Seed carries no object columns, so a fixture that must name a
// subject opens its own transaction and appends through event.Append -- the same
// primitive a writer uses, so the row is the row a writer would have written.
func appendEvent(t *testing.T, path, kind, objectType, objectID string) {
	t.Helper()
	if err := event.Register(kind, "view-test"); err != nil {
		t.Fatalf("registering %q: %v", kind, err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening the fixture for a write: %v", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("beginning the append: %v", err)
	}
	id := objectID
	if _, err := event.Append(context.Background(), tx, event.Event{
		Kind:       kind,
		ObjectType: objectType,
		ObjectID:   &id,
		Payload:    json.RawMessage(`{}`),
		At:         time.Now().UTC(),
	}); err != nil {
		tx.Rollback()
		t.Fatalf("appending the event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("committing the append: %v", err)
	}
}

// envelope builds one record of the shape a writer emits.
func envelope(t *testing.T, kind, id string, subject *record.Subject) record.Envelope {
	t.Helper()
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          kind,
		ID:            id,
		Producer:      record.Producer{System: record.System, Utility: "item", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		Subject:       subject,
		Payload:       json.RawMessage(`{"state":"ready"}`),
	}
}

// recordsFile writes a JSON array of envelopes and answers its path.
func recordsFile(t *testing.T, records ...record.Envelope) string {
	t.Helper()
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("encoding the records file: %v", err)
	}
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the records file: %v", err)
	}
	return path
}

// statusRecord is the part of a view.status record these tests read.
type statusRecord struct {
	Kind    string `json:"kind"`
	Payload struct {
		GeneratedAt time.Time `json:"generated_at"`
		Snapshot    struct {
			SchemaVersion int    `json:"schema_version"`
			EventHeadSeq  int64  `json:"event_head_seq"`
			Source        string `json:"source"`
			RecordCount   *int   `json:"record_count"`
		} `json:"snapshot"`
		Subject *record.Subject `json:"subject"`
		Found   *bool           `json:"found"`
	} `json:"payload"`
}

func decodeStatus(t *testing.T, out []byte) statusRecord {
	t.Helper()
	var status statusRecord
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatalf("status did not emit a record: %v\n%s", err, out)
	}
	return status
}

// Two reports are comparable only if each says which moment it describes, so the
// snapshot is part of the record and not a rendering detail.
//
// Negative control: drop `snapshot` from the payload (or report a version other than
// the store's) and the schema_version assertion below fails.
func TestStatusCarriesTheSnapshotItDescribes(t *testing.T) {
	path := fixture(t)
	code, out := runView(t, "status", "--store", path)
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeStatus(t, out)
	if status.Kind != "view.status" {
		t.Fatalf("status emitted kind %q", status.Kind)
	}
	if status.Payload.Snapshot.SchemaVersion != store.SchemaVersion {
		t.Fatalf("snapshot schema_version is %d, want %d",
			status.Payload.Snapshot.SchemaVersion, store.SchemaVersion)
	}
	if status.Payload.Snapshot.Source != "store" {
		t.Fatalf("snapshot source is %q", status.Payload.Snapshot.Source)
	}
	if status.Payload.GeneratedAt.IsZero() {
		t.Fatal("generated_at is zero, so two reports cannot be ordered")
	}
}

// A subject that is not in the snapshot is a negative decision, not an error: the
// utility ran and the answer is no. The record is still emitted, because a caller
// that gets exit 1 and no record cannot tell "absent" from "crashed".
//
// Negative control: the same store with an event naming item_042 exits 0
// (TestStatusFindsASubjectTheSnapshotNames), so this test fails if the face answers 1
// unconditionally.
func TestStatusExitsOneForAMissingSubject(t *testing.T) {
	path := fixture(t)
	code, out := runView(t, "status", "--store", path, "--item", "999999")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}
	status := decodeStatus(t, out)
	if status.Payload.Found == nil || *status.Payload.Found {
		t.Fatalf("a missing subject reported found=%v", status.Payload.Found)
	}
	if status.Payload.Subject == nil || status.Payload.Subject.ID != "999999" {
		t.Fatalf("the record does not name the subject it was asked about: %+v", status.Payload.Subject)
	}
}

// The positive control for the exit-1 test, and the negative control for its own
// second half: the id comparison is what decides, not the presence of any event.
func TestStatusFindsASubjectTheSnapshotNames(t *testing.T) {
	path := fixture(t)
	appendEvent(t, path, "item.record", "item", "item_042")

	code, out := runView(t, "status", "--store", path, "--item", "item_042")
	if code != 0 {
		t.Fatalf("want exit 0 for a subject the snapshot names, got %d: %s", code, out)
	}
	status := decodeStatus(t, out)
	if status.Payload.Found == nil || !*status.Payload.Found {
		t.Fatalf("a present subject reported found=%v", status.Payload.Found)
	}
	if status.Payload.Snapshot.EventHeadSeq == 0 {
		t.Fatal("the snapshot reports no events, so it does not describe the store it read")
	}

	// Negative control: a different id in the same store is still absent.
	if code, out := runView(t, "status", "--store", path, "--item", "item_043"); code != 1 {
		t.Fatalf("want exit 1 for a different id, got %d: %s", code, out)
	}
}

// A store this binary cannot read is exit 3, and the record still carries the version
// the store holds: a caller that learns neither version cannot act on the refusal.
//
// Negative control: the same store before the migration rows are dropped exits 0, so
// this test fails if the gate answers 3 for every store.
func TestStatusExitsThreeForAnUnsupportedSchema(t *testing.T) {
	path := fixture(t)
	if code, out := runView(t, "status", "--store", path); code != 0 {
		t.Fatalf("a supported store exited %d: %s", code, out)
	}

	s, err := store.Open(context.Background(), path, false)
	if err != nil {
		t.Fatalf("opening the fixture failed: %v", err)
	}
	if err := s.ForgetMigrations(); err != nil {
		s.Close()
		t.Fatalf("dropping the migration rows: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture failed: %v", err)
	}

	code, out := runView(t, "status", "--store", path)
	if code != 3 {
		t.Fatalf("want exit 3 for an unsupported schema, got %d: %s", code, out)
	}
	status := decodeStatus(t, out)
	if status.Payload.Snapshot.SchemaVersion != 0 {
		t.Fatalf("the refusal reports schema_version %d, want the store's 0",
			status.Payload.Snapshot.SchemaVersion)
	}
}

// events is the log follower: --since is exclusive, and next_since is the cursor the
// caller advances to. It is never the head, because a caller that advanced to the head
// would skip every event appended after this read.
//
// Negative control: --since 0 returns more events than --since next_since, so a face
// that ignored the cursor fails the second half.
func TestEventsReadsAfterTheCursor(t *testing.T) {
	path := fixture(t)
	seed(t, path,
		envelope(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"}),
		envelope(t, "item.record", "r-2", &record.Subject{Kind: "item", ID: "item_043"}),
	)

	code, out := runView(t, "events", "--store", path, "--since", "0")
	if code != 0 {
		t.Fatalf("events exited %d: %s", code, out)
	}
	var all struct {
		Kind    string `json:"kind"`
		Payload struct {
			Snapshot struct {
				EventHeadSeq int64 `json:"event_head_seq"`
			} `json:"snapshot"`
			Since     int64 `json:"since"`
			NextSince int64 `json:"next_since"`
			Events    []struct {
				Seq  int64  `json:"seq"`
				Kind string `json:"kind"`
			} `json:"events"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &all); err != nil {
		t.Fatalf("events did not emit a record: %v\n%s", err, out)
	}
	if all.Kind != "view.events" {
		t.Fatalf("events emitted kind %q", all.Kind)
	}
	// The seed marker plus the two seeded records.
	if len(all.Payload.Events) != 3 {
		t.Fatalf("events --since 0 returned %d events, want 3: %s", len(all.Payload.Events), out)
	}
	if all.Payload.NextSince != all.Payload.Snapshot.EventHeadSeq {
		t.Fatalf("next_since is %d, head is %d", all.Payload.NextSince, all.Payload.Snapshot.EventHeadSeq)
	}

	code, out = runView(t, "events", "--store", path, "--since", "3")
	if code != 0 {
		t.Fatalf("events exited %d: %s", code, out)
	}
	var rest struct {
		Payload struct {
			NextSince int64             `json:"next_since"`
			Events    []json.RawMessage `json:"events"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &rest); err != nil {
		t.Fatalf("events did not emit a record: %v\n%s", err, out)
	}
	if len(rest.Payload.Events) != 0 {
		t.Fatalf("events --since 3 returned %d events, want 0", len(rest.Payload.Events))
	}
	if rest.Payload.NextSince != 3 {
		t.Fatalf("an empty read advanced the cursor to %d", rest.Payload.NextSince)
	}
}

// The records mode is the same report for a recipient who was sent records and has no
// store: the same payload shape, assembled from the stream.
//
// Negative control: --item item_043 over the same file exits 1, so the subject lookup
// is doing the work rather than the file's presence.
func TestStatusAssemblesFromARecordsFile(t *testing.T) {
	path := recordsFile(t,
		envelope(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"}),
		envelope(t, "gate.verdict", "v-1", &record.Subject{Kind: "candidate", ID: "cand_007"}),
	)

	code, out := runView(t, "status", "--records", path, "--item", "item_042")
	if code != 0 {
		t.Fatalf("status --records exited %d: %s", code, out)
	}
	status := decodeStatus(t, out)
	if status.Kind != "view.status" {
		t.Fatalf("status emitted kind %q", status.Kind)
	}
	if status.Payload.Snapshot.Source != "records" {
		t.Fatalf("snapshot source is %q, want records", status.Payload.Snapshot.Source)
	}
	if status.Payload.Snapshot.RecordCount == nil || *status.Payload.Snapshot.RecordCount != 2 {
		t.Fatalf("snapshot record_count is %v, want 2", status.Payload.Snapshot.RecordCount)
	}
	if status.Payload.Found == nil || !*status.Payload.Found {
		t.Fatalf("a subject the stream names reported found=%v", status.Payload.Found)
	}

	// Negative control: a subject the stream does not name.
	if code, out := runView(t, "status", "--records", path, "--item", "item_043"); code != 1 {
		t.Fatalf("want exit 1 for an absent subject, got %d: %s", code, out)
	}
}

// events over a record stream: the stream has no seq of its own, so a record's
// 1-based position in the file is its cursor.
//
// Negative control: --since 1 returns one fewer event than --since 0.
func TestEventsOverARecordsFile(t *testing.T) {
	path := recordsFile(t,
		envelope(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"}),
		envelope(t, "gate.verdict", "v-1", &record.Subject{Kind: "candidate", ID: "cand_007"}),
	)

	code, out := runView(t, "events", "--records", path, "--since", "0")
	if code != 0 {
		t.Fatalf("events --records exited %d: %s", code, out)
	}
	var all struct {
		Payload struct {
			NextSince int64 `json:"next_since"`
			Events    []struct {
				Seq  int64  `json:"seq"`
				Kind string `json:"kind"`
			} `json:"events"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &all); err != nil {
		t.Fatalf("events did not emit a record: %v\n%s", err, out)
	}
	if len(all.Payload.Events) != 2 {
		t.Fatalf("events --since 0 returned %d events, want 2: %s", len(all.Payload.Events), out)
	}
	if all.Payload.Events[0].Seq != 1 || all.Payload.Events[1].Seq != 2 {
		t.Fatalf("positions are %d and %d, want 1 and 2",
			all.Payload.Events[0].Seq, all.Payload.Events[1].Seq)
	}
	if all.Payload.NextSince != 2 {
		t.Fatalf("next_since is %d, want 2", all.Payload.NextSince)
	}

	code, out = runView(t, "events", "--records", path, "--since", "1")
	if code != 0 {
		t.Fatalf("events --records exited %d: %s", code, out)
	}
	var rest struct {
		Payload struct {
			Events []json.RawMessage `json:"events"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &rest); err != nil {
		t.Fatalf("events did not emit a record: %v\n%s", err, out)
	}
	if len(rest.Payload.Events) != 1 {
		t.Fatalf("events --since 1 returned %d events, want 1", len(rest.Payload.Events))
	}
}

// Exactly one source is required. A face given both would have to choose, and a face
// given neither has nothing to describe; both are usage errors, not decisions.
//
// Negative control: one source exits 0, so this test fails if the face refuses every
// invocation.
func TestStatusNeedsExactlyOneSource(t *testing.T) {
	path := fixture(t)
	records := recordsFile(t, envelope(t, "item.record", "r-1", nil))

	if code, out := runView(t, "status"); code != 2 {
		t.Fatalf("want exit 2 with no source, got %d: %s", code, out)
	}
	if code, out := runView(t, "status", "--store", path, "--records", records); code != 2 {
		t.Fatalf("want exit 2 with two sources, got %d: %s", code, out)
	}
	if code, out := runView(t, "status", "--store", path); code != 0 {
		t.Fatalf("want exit 0 with one source, got %d: %s", code, out)
	}
}

// A records file that is not a stream of this system's records is malformed input:
// exit 2, nothing assembled.
//
// Negative control: the well-formed file in the same test exits 0.
func TestStatusRefusesAMalformedRecordsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte(`[{"record_version":1,"kind":"item.record"}]`), 0o644); err != nil {
		t.Fatalf("writing the records file: %v", err)
	}
	if code, out := runView(t, "status", "--records", path); code != 2 {
		t.Fatalf("want exit 2 for a malformed record, got %d: %s", code, out)
	}

	good := recordsFile(t, envelope(t, "item.record", "r-1", nil))
	if code, out := runView(t, "status", "--records", good); code != 0 {
		t.Fatalf("want exit 0 for a well-formed file, got %d: %s", code, out)
	}
}

// A record version this binary does not know is incompatible, not malformed: exit 3,
// the same answer the store's schema gate gives, because both mean "this reader
// cannot read that".
//
// Negative control: the same file at the supported version exits 0.
func TestStatusRefusesAnUnsupportedRecordVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	raw := `[{"record_version":99,"kind":"item.record","id":"r-1",
"producer":{"system":"tillr","utility":"item","version":"0.1.0"},
"created_at":"2026-11-10T12:00:00Z","payload":{}}]`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("writing the records file: %v", err)
	}
	if code, out := runView(t, "status", "--records", path); code != 3 {
		t.Fatalf("want exit 3 for an unsupported record version, got %d: %s", code, out)
	}

	good := recordsFile(t, envelope(t, "item.record", "r-1", nil))
	if code, out := runView(t, "status", "--records", good); code != 0 {
		t.Fatalf("want exit 0 at the supported version, got %d: %s", code, out)
	}
}

// TestHelpIsTheFaceSurface pins the layer-1 surface (docs/reference/tillr-distribution.md,
// "Documentation split"): `--help` names the faces and a face's `--help` names that face's flags, both
// on stdout and both exit 0.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := runView(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, out)
	}
	for _, face := range []string{"status", "events", "serve"} {
		if !strings.Contains(string(out), face) {
			t.Errorf("--help does not name the face %q: %s", face, out)
		}
	}
	code, out = runView(t, "status", "--help")
	if code != 0 {
		t.Fatalf("status --help exited %d, want 0: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of tillr-view status") {
		t.Errorf("status --help does not name the face's flags: %s", out)
	}
	if code, out = runView(t, "bogus", "--help"); code != 2 {
		t.Errorf("a face that does not exist exited %d, want 2: %s", code, out)
	}
}

// stubQuestionsIndex writes a script that answers the given Vantage index JSON and
// returns the --questions-cmd value the face runs. Each document is keyed by the
// repository-relative path the item's design_doc names, the way the real index spells it.
func stubQuestionsIndex(t *testing.T, index string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	if err := os.WriteFile(path, []byte(index), 0o644); err != nil {
		t.Fatalf("writing the stub index: %v", err)
	}
	script := filepath.Join(dir, "index.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat "+path+"\n"), 0o755); err != nil {
		t.Fatalf("writing the stub index script: %v", err)
	}
	return script
}
