package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	dir, err := os.MkdirTemp(root, "tillr-item-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	bin = filepath.Join(dir, "tillr-item")
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

// itemInput is design §5.3's payload list. A case varies one field and leaves the
// rest, so a refusal is attributable to the field the case names.
type itemInput struct {
	ItemID    string      `json:"item_id"`
	Title     string      `json:"title"`
	BodyRef   string      `json:"body_ref"`
	State     string      `json:"state"`
	Criteria  []criterion `json:"criteria"`
	BlockedBy []string    `json:"blocked_by"`
	DesignDoc string      `json:"design_doc,omitempty"`
	// Rank is the queue's order (item/rank.go); the rank face changes it independently.
	Rank string `json:"rank,omitempty"`
}

type criterion struct {
	CriterionID string `json:"criterion_id"`
	Text        string `json:"text"`
	State       string `json:"state"`
	ClaimClass  string `json:"claim_class,omitempty"`
}

// base is the record every case starts from: one item, one criterion, no edges.
func base(id, title string) itemInput {
	return itemInput{
		ItemID: id, Title: title, BodyRef: "briefs/" + id + ".md", State: "not-started",
		Criteria:  []criterion{{CriterionID: "c1", Text: "the first criterion", State: "open"}},
		BlockedBy: []string{},
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
		"producer":       map[string]string{"system": "tillr", "utility": "item", "version": "0.1.0"},
		"created_at":     "2026-11-10T00:00:00Z",
		"payload":        payload,
	})
	if err != nil {
		t.Fatalf("building the input record failed: %v", err)
	}
	return string(raw)
}

// withKey sets the envelope's idempotency_key, which design §5.1 makes an optional
// write-request field: replaying a request with the same key returns the original
// result instead of writing twice.
func withKey(t *testing.T, record, key string) string {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(record), &env); err != nil {
		t.Fatalf("re-reading the input record failed: %v", err)
	}
	env["idempotency_key"] = key
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("re-encoding the input record failed: %v", err)
	}
	return string(raw)
}

// input writes one record to a scratch file and returns its path, so a case can pass
// it to whichever face it drives.
func input(t *testing.T, record string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "input"), "record.json")
	if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
		t.Fatalf("writing the input record failed: %v", err)
	}
	return path
}

// apply runs the apply face against one input record.
func apply(t *testing.T, db, record string) (int, []byte) {
	t.Helper()
	return run(t, "apply", "--store", db, "--input", input(t, record), "--now", "2026-11-10T00:00:00Z")
}

// itemOut is the record a face emits, decoded. The fields live under `payload`:
// design §5.1 says the envelope carries `payload`, and §5.3's list describes a
// payload, not the envelope.
type itemOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		ItemID    string      `json:"item_id"`
		Title     string      `json:"title"`
		State     string      `json:"state"`
		UpdatedAt string      `json:"updated_at"`
		EventSeq  int64       `json:"event_seq"`
		Criteria  []criterion `json:"criteria"`
		BlockedBy []string    `json:"blocked_by"`
		Rule      string      `json:"rule"`
		Reason    string      `json:"reason"`
	} `json:"payload"`
}

func decode(t *testing.T, out []byte) itemOut {
	t.Helper()
	var got itemOut
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
	return got
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

// A successful apply echoes the record as stored, including the event sequence it
// produced (design §5.3). The echo is the stored row, not the input: the input
// carries no updated_at and no event_seq, so an echo of the input cannot pass.
func TestApplyEchoesTheStoredItem(t *testing.T) {
	db := filepath.Join(scratch(t, "apply"), "board.sqlite3")
	code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "the item face")))
	if code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "item.record" {
		t.Fatalf("apply emitted kind %q", got.Kind)
	}
	if got.Payload.ItemID != "item_042" || got.Payload.Title != "the item face" {
		t.Fatalf("apply echoed %q / %q", got.Payload.ItemID, got.Payload.Title)
	}
	if got.Payload.UpdatedAt != "2026-11-10T00:00:00Z" {
		t.Fatalf("updated_at is %q, want the injected clock", got.Payload.UpdatedAt)
	}
	if got.Payload.EventSeq == 0 {
		t.Fatal("the echo carries no event sequence")
	}
	if len(got.Payload.Criteria) != 1 || got.Payload.Criteria[0].CriterionID != "c1" {
		t.Fatalf("the echo carries %d criteria", len(got.Payload.Criteria))
	}
}

// show answers from the row, not from the newest record in the log: two items are
// applied and the first is read back.
func TestShowReadsTheRowNotTheNewestRecord(t *testing.T) {
	db := filepath.Join(scratch(t, "show"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "first"))); code != 0 {
		t.Fatalf("applying the first item exited %d: %s", code, out)
	}
	if code, out := apply(t, db, envelope(t, "item.record", "rec-2", base("item_043", "second"))); code != 0 {
		t.Fatalf("applying the second item exited %d: %s", code, out)
	}
	code, out := run(t, "show", "--store", db, "--item", "item_042")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	got := decode(t, out)
	if got.Payload.ItemID != "item_042" || got.Payload.Title != "first" {
		t.Fatalf("show answered %q / %q", got.Payload.ItemID, got.Payload.Title)
	}
}

// A missing subject is a negative decision, not a failure to run: exit 1 with a
// record that names the item (design §5.1's exit table).
func TestShowOfAMissingItemExitsOneWithARejection(t *testing.T) {
	db := filepath.Join(scratch(t, "show-missing"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "present"))); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
	}
	code, out := run(t, "show", "--store", db, "--item", "item_999")
	if code != 1 {
		t.Fatalf("show of a missing item exited %d, want 1: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "item.rejection" || got.Payload.Rule != "not-found" || got.Payload.ItemID != "item_999" {
		t.Fatalf("show emitted %q / rule %q / item %q", got.Kind, got.Payload.Rule, got.Payload.ItemID)
	}
}

// An edge to an item that does not exist is refused, and the refusal writes nothing:
// the item is not readable afterwards.
func TestApplyRefusesAnEdgeToAMissingItem(t *testing.T) {
	db := filepath.Join(scratch(t, "edge-missing"), "board.sqlite3")
	in := base("item_042", "blocked")
	in.BlockedBy = []string{"item_404"}
	code, out := apply(t, db, envelope(t, "item.record", "rec-1", in))
	if code != 1 {
		t.Fatalf("apply exited %d, want 1: %s", code, out)
	}
	got := decode(t, out)
	if got.Kind != "item.rejection" || got.Payload.Rule != "edge-target-missing" {
		t.Fatalf("apply emitted %q / rule %q", got.Kind, got.Payload.Rule)
	}
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 1 {
		t.Fatalf("a refused apply left the item readable (exit %d): %s", code, out)
	}
}

// An item cannot be blocked by itself: the edge would make it permanently
// undispatchable and no reader could tell it from a real wait.
func TestApplyRefusesASelfEdge(t *testing.T) {
	db := filepath.Join(scratch(t, "edge-self"), "board.sqlite3")
	in := base("item_042", "self")
	in.BlockedBy = []string{"item_042"}
	code, out := apply(t, db, envelope(t, "item.record", "rec-1", in))
	if code != 1 {
		t.Fatalf("apply exited %d, want 1: %s", code, out)
	}
	if got := decode(t, out); got.Payload.Rule != "edge-self" {
		t.Fatalf("apply refused with rule %q", got.Payload.Rule)
	}
}

// A cycle is refused at the edge that closes it, so the graph stays acyclic and the
// drain's readiness walk terminates.
func TestApplyRefusesACycle(t *testing.T) {
	db := filepath.Join(scratch(t, "edge-cycle"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "a"))); code != 0 {
		t.Fatalf("applying item_042 exited %d: %s", code, out)
	}
	b := base("item_043", "b")
	b.BlockedBy = []string{"item_042"}
	if code, out := apply(t, db, envelope(t, "item.record", "rec-2", b)); code != 0 {
		t.Fatalf("applying item_043 exited %d: %s", code, out)
	}
	back := base("item_042", "a")
	back.BlockedBy = []string{"item_043"}
	code, out := apply(t, db, envelope(t, "item.record", "rec-3", back))
	if code != 1 {
		t.Fatalf("apply exited %d, want 1: %s", code, out)
	}
	if got := decode(t, out); got.Payload.Rule != "edge-cycle" {
		t.Fatalf("apply refused with rule %q", got.Payload.Rule)
	}
}

// An unknown state is malformed input, not a negative decision: exit 2, no record,
// nothing written. A caller that cannot tell the two apart retries a typo forever.
func TestApplyRefusesAnUnknownStateAsMalformedInput(t *testing.T) {
	db := filepath.Join(scratch(t, "state-unknown"), "board.sqlite3")
	in := base("item_042", "unknown state")
	in.State = "invented"
	code, out := apply(t, db, envelope(t, "item.record", "rec-1", in))
	if code != 2 {
		t.Fatalf("apply exited %d, want 2: %s", code, out)
	}
	if len(bytes.TrimSpace(out)) != 0 {
		t.Fatalf("a usage error emitted a record: %s", out)
	}
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 1 {
		t.Fatalf("a refused apply left the item readable (exit %d): %s", code, out)
	}
}

// A frozen legacy value is a known state no transition may write, so it is a
// rejection with a rule rather than a usage error.
func TestApplyRefusesAFrozenStateWithARejection(t *testing.T) {
	db := filepath.Join(scratch(t, "state-frozen"), "board.sqlite3")
	in := base("item_042", "frozen")
	in.State = "deleted"
	code, out := apply(t, db, envelope(t, "item.record", "rec-1", in))
	if code != 1 {
		t.Fatalf("apply exited %d, want 1: %s", code, out)
	}
	if got := decode(t, out); got.Payload.Rule != "state-frozen" {
		t.Fatalf("apply refused with rule %q", got.Payload.Rule)
	}
	if n := eventsOfKind(t, db, "item.add"); n != 0 {
		t.Fatalf("a refused apply appended %d item.add events", n)
	}
}

// Replaying a request with the same idempotency key returns the original result
// instead of writing twice (design §5.1).
func TestApplyReplaysAnIdempotencyKeyWithoutWritingTwice(t *testing.T) {
	db := filepath.Join(scratch(t, "idempotency"), "board.sqlite3")
	rec := withKey(t, envelope(t, "item.record", "rec-1", base("item_042", "once")), "key-1")
	code, first := apply(t, db, rec)
	if code != 0 {
		t.Fatalf("the first apply exited %d: %s", code, first)
	}
	code, second := apply(t, db, rec)
	if code != 0 {
		t.Fatalf("the replay exited %d: %s", code, second)
	}
	if a, b := decode(t, first).Payload.EventSeq, decode(t, second).Payload.EventSeq; a != b {
		t.Fatalf("the replay produced a second event: %d then %d", a, b)
	}
	if n := eventsOfKind(t, db, "item.add"); n != 1 {
		t.Fatalf("the store holds %d item.add events, want 1", n)
	}
}

// A refused apply writes neither the row nor the event: the transition and its event
// are one transaction (design §5.1), so a half-written item cannot exist.
func TestARefusedApplyWritesNeitherRowNorEvent(t *testing.T) {
	db := filepath.Join(scratch(t, "rollback"), "board.sqlite3")
	in := base("item_042", "duplicate criteria")
	in.Criteria = []criterion{
		{CriterionID: "c1", Text: "first", State: "open"},
		{CriterionID: "c1", Text: "second", State: "open"},
	}
	code, out := apply(t, db, envelope(t, "item.record", "rec-1", in))
	if code != 1 {
		t.Fatalf("apply exited %d, want 1: %s", code, out)
	}
	if got := decode(t, out); got.Payload.Rule != "criterion-duplicate" {
		t.Fatalf("apply refused with rule %q", got.Payload.Rule)
	}
	if n := eventsOfKind(t, db, "item.add"); n != 0 {
		t.Fatalf("a refused apply appended %d item.add events", n)
	}
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 1 {
		t.Fatalf("a refused apply left the item readable (exit %d): %s", code, out)
	}
}

// The item tables come from the item binary's own registration, not from the
// substrate: the substrate's own DDL names no item table, and the first apply creates
// all three.
//
// The "before" observation is the substrate's DDL rather than a store created in this
// process, because a test binary links the package under test and its init() has
// already registered the item DDL before any test runs: a store created here always
// has the tables, so asserting otherwise would assert something false. The negative
// control is the registration itself -- remove it from schema.go and the apply below
// fails on a missing table.
func TestTheItemBinaryCreatesItsOwnTables(t *testing.T) {
	for _, ddl := range []string{store.MetaDDL, event.EventsDDL} {
		if strings.Contains(ddl, "item") {
			t.Fatalf("the substrate's own DDL names an item table:\n%s", ddl)
		}
	}
	db := filepath.Join(scratch(t, "ddl"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "ddl"))); code != 0 {
		t.Fatalf("apply exited %d: %s", code, out)
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
	for _, table := range []string{"items", "item_criteria", "item_edges"} {
		if _, ok := after[table]; !ok {
			t.Errorf("the item binary did not create %q", table)
		}
	}
}

// TestHelpIsTheFaceSurface pins the layer-1 surface (docs/design/tillr-distribution.md
// Section 3): `--help` names the faces, a leaf face's `--help` names its flags, and a noun
// whose faces are verbs names the verbs. A face that does not exist stays a usage error.
func TestHelpIsTheFaceSurface(t *testing.T) {
	code, out := run(t, "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, out)
	}
	for _, face := range []string{"apply", "rank", "show", "state", "decision", "mail", "verdict"} {
		if !strings.Contains(string(out), face) {
			t.Errorf("--help does not name the face %q: %s", face, out)
		}
	}
	code, out = run(t, "state", "--help")
	if code != 0 {
		t.Fatalf("state --help exited %d, want 0: %s", code, out)
	}
	if !strings.Contains(string(out), "Usage of tillr-item state") {
		t.Errorf("state --help does not name the face's flags: %s", out)
	}
	code, out = run(t, "decision", "--help")
	if code != 0 {
		t.Fatalf("decision --help exited %d, want 0: %s", code, out)
	}
	for _, verb := range []string{"record", "supersede", "revoke", "list"} {
		if !strings.Contains(string(out), verb) {
			t.Errorf("decision --help does not name the verb %q: %s", verb, out)
		}
	}
	if code, out = run(t, "bogus", "--help"); code != 2 {
		t.Errorf("a face that does not exist exited %d, want 2: %s", code, out)
	}
}
