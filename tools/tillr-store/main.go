// Command tillr-store creates, seeds, verifies and interrogates a tillr store. It is
// the utility every other utility's fixture starts from, so its report is a contract
// rather than a convenience.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// utility is the producer name every record this binary emits carries.
const utility = "store"

// version is this binary's producer version. The version set is released together
// (design §11), so this moves with the set rather than on its own.
const version = "0.1.0"

func main() { os.Exit(run(os.Args[1:])) }

// run dispatches one face and prints the record it produced. A face returns its
// record and its exit code together, so the exit is fixed where the outcome is
// known rather than reconstructed here.
func run(args []string) int {
	if len(args) == 0 {
		return usagef("a face is required: %s", faceList)
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("tillr-store: %s\nRun `tillr-store <face> --help` for one face's flags.\n", faceList)
		return record.ExitOK
	}
	face, rest := args[0], args[1:]

	var (
		env  record.Envelope
		code int
	)
	switch face {
	case "init":
		env, code = initFace(rest)
	case "seed":
		env, code = seedFace(rest)
	case "check":
		env, code = checkFace(rest)
	case "recover":
		env, code = recoverFace(rest)
	case "events":
		env, code = eventsFace(rest)
	default:
		return usagef("unknown face %q: want %s", face, faceList)
	}
	if code == helpExit {
		// The face answered `--help`: its flags went to stdout and nothing ran.
		return record.ExitOK
	}

	// A face that produced a record prints it whatever its exit code: exit 3's report
	// is the answer to "what schema is this", not a dropped artifact. A face that could
	// not describe the store at all returns no record, and prints only its diagnostic.
	if env.Kind != "" {
		raw, err := record.Encode(env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-store: %v\n", err)
			return record.ExitInternal
		}
		fmt.Println(string(raw))
	}
	return code
}

// report is the payload of the store's one stdout record kind, store.report. Every
// face emits the same shape describing the store as it stands after the face ran,
// and action says which face produced it: a consumer that reads schema_version does
// not have to know which face wrote the record.
type report struct {
	Action                  string         `json:"action"`
	SchemaVersion           int            `json:"schema_version"`
	SupportedRecordVersions []int          `json:"supported_record_versions"`
	EventHeadSeq            int64          `json:"event_head_seq"`
	TableCounts             map[string]int `json:"table_counts"`
	Fixture                 bool           `json:"fixture"`
	Integrity               string         `json:"integrity"`
	Since                   *int64         `json:"since,omitempty"`
	EventCount              *int           `json:"event_count,omitempty"`
	// NextSince is the cursor the events face's caller advances to: the last seq in the
	// file it wrote, or the unchanged since when the file is empty. It is never
	// event_head_seq -- the head is the store's, and a caller that advanced to it would
	// skip every event appended after this read.
	NextSince    *int64 `json:"next_since,omitempty"`
	JournalFound string `json:"journal_found,omitempty"`
}

// initFace creates the store, migrating it, and marks it as a fixture when asked.
func initFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("init")
	path := fs.String("store", "", "the store to create")
	fixture := fs.Bool("fixture", false, "mark the store as a fixture, which is what makes it seedable")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" {
		return record.Envelope{}, usagef("init needs --store <path>")
	}

	ctx := context.Background()
	if env, code := schemaGate(ctx, *path, "init"); code != record.ExitOK {
		return env, code
	}
	var s *store.Store
	var err error
	if *fixture {
		s, err = store.InitFixture(ctx, *path)
	} else {
		s, err = store.Open(ctx, *path, true)
	}
	if err != nil {
		if errors.Is(err, store.ErrLiveStore) {
			return record.Envelope{}, usagef("%v", err)
		}
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()
	return describe(s, "init")
}

// seedFace imports a record bundle into a fixture store. Seeding a live store is the
// one mistake that cannot be undone, so the refusal is exit 2 and nothing is written.
func seedFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("seed")
	path := fs.String("store", "", "the store to seed")
	input := fs.String("input", "", "the seed bundle to import")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" || *input == "" {
		return record.Envelope{}, usagef("seed needs --store <path> and --input <bundle>")
	}

	ctx := context.Background()
	if env, code := schemaGate(ctx, *path, "seed"); code != record.ExitOK {
		return env, code
	}
	bundle, err := os.ReadFile(*input)
	if err != nil {
		return record.Envelope{}, usagef("reading %s: %v", *input, err)
	}
	s, err := store.Open(ctx, *path, false)
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()
	if err := s.Seed(ctx, bundle); err != nil {
		return record.Envelope{}, usagef("%v", err)
	}
	return describe(s, "seed")
}

// recoverFace is the physical recovery path: no read-only schema gate may precede
// SQLite's recovery, and no migrations or logical writes are performed here.
func recoverFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("recover")
	path := fs.String("store", "", "the existing store whose rollback journal SQLite should recover")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" {
		return record.Envelope{}, usagef("recover needs --store <path>")
	}
	s, journal, err := store.Recover(context.Background(), *path)
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()
	snap, err := s.Describe(context.Background())
	if err != nil {
		return record.Envelope{}, storeFailure(err)
	}
	r := reportOf(snap, "recover")
	r.JournalFound = journal
	if snap.SchemaVersion != store.SchemaVersion {
		return envelope(r), record.ExitIncompatible
	}
	if snap.Integrity != "ok" {
		return envelope(r), record.ExitNegative
	}
	return envelope(r), record.ExitOK
}

// checkFace reports the store's schema, head, counts and integrity, and decides
// whether this binary can work with it. It opens read-only: a check that migrated
// the store it was asked to describe would answer a different question.
func checkFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("check")
	path := fs.String("store", "", "the store to check")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" {
		return record.Envelope{}, usagef("check needs --store <path>")
	}

	ctx := context.Background()
	if env, code := schemaGate(ctx, *path, "check"); code != record.ExitOK {
		return env, code
	}
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()

	snap, err := s.Describe(ctx)
	if err != nil {
		return record.Envelope{}, storeFailure(err)
	}
	r := reportOf(snap, "check")
	// An intact store is the positive decision; a damaged one is a negative decision
	// rather than an error, because the utility ran and the answer is no.
	if r.Integrity != "ok" {
		return envelope(r), record.ExitNegative
	}
	return envelope(r), record.ExitOK
}

// eventsFace writes the events after --since to --output and reports the store they
// came from. --output is required: the record on stdout is the store's report, and a
// second JSON document on the same stream would make the output unparseable.
func eventsFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("events")
	path := fs.String("store", "", "the store to read")
	since := fs.Int64("since", 0, "read events with a seq greater than this")
	output := fs.String("output", "", "the file the events are written to")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *path == "" || *output == "" {
		return record.Envelope{}, usagef("events needs --store <path> and --output <file>")
	}
	// --output naming the store would truncate the store it was asked to read.
	if sameFile(*output, *path) {
		return record.Envelope{}, usagef("--output %s is the store it was asked to read", *output)
	}

	ctx := context.Background()
	if env, code := schemaGate(ctx, *path, "events"); code != record.ExitOK {
		return env, code
	}
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()

	// The events and the report come from one read transaction, so the report
	// describes the same moment as the file it is written beside.
	snap, err := s.ReadSnapshot(ctx, *since)
	if err != nil {
		return record.Envelope{}, storeFailure(err)
	}
	raw, err := json.Marshal(eventRecords(snap.Events))
	if err != nil {
		return record.Envelope{}, internalf("encoding the events: %v", err)
	}
	if err := writeAtomically(*output, raw); err != nil {
		return record.Envelope{}, internalf("writing %s: %v", *output, err)
	}
	r := reportOf(snap, "events")
	count := len(snap.Events)
	r.Since = since
	r.EventCount = &count
	next := *since
	if count > 0 {
		next = snap.Events[count-1].Seq
	}
	r.NextSince = &next
	return envelope(r), record.ExitOK
}

// eventRecord is one event as the events file spells it. The substrate's event.Event
// carries no JSON tags, so marshalling it directly would write Go field names into an
// artifact a caller reads; the file's spelling is this binary's business.
type eventRecord struct {
	Seq        int64           `json:"seq"`
	Kind       string          `json:"kind"`
	ObjectType string          `json:"object_type,omitempty"`
	ObjectID   *string         `json:"object_id,omitempty"`
	Revision   *int64          `json:"revision,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	At         time.Time       `json:"at"`
}

func eventRecords(events []event.Event) []eventRecord {
	out := make([]eventRecord, 0, len(events))
	for _, e := range events {
		out = append(out, eventRecord{
			Seq:        e.Seq,
			Kind:       e.Kind,
			ObjectType: e.ObjectType,
			ObjectID:   e.ObjectID,
			Revision:   e.Revision,
			// The payload is carried verbatim: re-encoding a decoded value would route
			// its numbers through float64 and change them.
			Payload: e.Payload,
			At:      e.At.UTC(),
		})
	}
	return out
}

// reportOf is the report one snapshot describes. Every field comes from the one read,
// so the report describes the same moment as the events beside it.
func reportOf(snap store.Snapshot, action string) report {
	return report{
		Action:                  action,
		SchemaVersion:           snap.SchemaVersion,
		SupportedRecordVersions: []int{record.SupportedVersion},
		EventHeadSeq:            snap.EventHeadSeq,
		TableCounts:             snap.TableCounts,
		Fixture:                 snap.Fixture,
		Integrity:               snap.Integrity,
	}
}

// describe is the report plus the envelope, for the faces whose record is the report
// and nothing else.
func describe(s *store.Store, action string) (record.Envelope, int) {
	snap, err := s.Describe(context.Background())
	if err != nil {
		return record.Envelope{}, storeFailure(err)
	}
	return envelope(reportOf(snap, action)), record.ExitOK
}

// schemaGate is the one place a face decides whether this binary can work with the
// store at path, and every logical face calls it before it does anything. Recovery
// instead acquires SQLite's write lock first, without changing the schema. §5.1 defines exit 3
// as the schema-incompatible answer generally, so a face that skipped the gate would
// answer a question about a store it cannot read: `events` would write a file from it
// and `seed` would write into it.
//
// The report is the answer, so it goes to stdout with the refusal, and the one-line
// reason goes to stderr: a caller that learns neither the store's version nor the
// supported one cannot act on the refusal. A store that does not exist yet is
// compatible by definition -- the face that creates it is the one that gives it a
// schema.
func schemaGate(ctx context.Context, path, action string) (record.Envelope, int) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return record.Envelope{}, record.ExitOK
		}
		return record.Envelope{}, openFailure(err)
	}
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()

	version, err := s.SchemaVersion()
	if err != nil {
		return record.Envelope{}, storeFailure(err)
	}
	if version == store.SchemaVersion {
		return record.Envelope{}, record.ExitOK
	}
	r := report{
		Action:                  action,
		SchemaVersion:           version,
		SupportedRecordVersions: []int{record.SupportedVersion},
		TableCounts:             map[string]int{},
	}
	// A store at version 0 has no tables to describe, so the rest of the report is
	// what the store could answer rather than a second failure.
	if snap, err := s.Describe(ctx); err == nil {
		r.EventHeadSeq = snap.EventHeadSeq
		r.TableCounts = snap.TableCounts
		r.Fixture = snap.Fixture
		r.Integrity = snap.Integrity
	}
	fmt.Fprintf(os.Stderr, "tillr-store: %s: store schema version %d is not the %d this binary supports\n",
		action, version, store.SchemaVersion)
	return envelope(r), record.ExitIncompatible
}

// sameFile reports whether two paths name the same file. It compares the resolved
// paths rather than the strings, because --output ./board.sqlite3 and --store
// board.sqlite3 are the same file and the truncation is the same truncation.
func sameFile(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	if absA == absB {
		return true
	}
	infoA, errA := os.Stat(absA)
	infoB, errB := os.Stat(absB)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}

// writeAtomically writes the events file through a temp file in the same directory and
// renames it into place, so a caller never reads a half-written log and a failed write
// leaves the previous file intact.
func writeAtomically(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tillr-events-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// envelope wraps a report in the common record envelope.
func envelope(r report) record.Envelope {
	raw, err := json.Marshal(r)
	if err != nil {
		// The payload is a struct this file defines, so an encoding failure is a bug.
		panic(err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          "store.report",
		ID:            newID(),
		Producer:      record.Producer{System: record.System, Utility: utility, Version: version},
		CreatedAt:     time.Now().UTC(),
		Payload:       raw,
	}
}

// newID is this record's opaque identifier. It is random rather than derived from
// the store, because two reports of one store are two records.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// faceList is this binary's faces, in the order the usage lines print them. One home,
// so the refusal, the unknown-face line and `--help` cannot disagree.
const faceList = "init, seed, check, recover or events"

// helpExit is what parseFlags answers for a `--help`. A face's flags are the layer-1
// surface (docs/design/tillr-distribution.md Section 3), so a help request is an answer
// and not a usage error: run turns this into exit 0, and no face ever sees it.
const helpExit = -1

func newFlagSet(face string) *flag.FlagSet {
	fs := flag.NewFlagSet("tillr-store "+face, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags turns a flag error into the usage exit, with the diagnostic on stderr.
func parseFlags(fs *flag.FlagSet, args []string) int {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(fs)
			return helpExit
		}
		return usagef("%v", err)
	}
	if fs.NArg() > 0 {
		return usagef("unexpected argument %q", fs.Arg(0))
	}
	return record.ExitOK
}

// printHelp writes one face's flags to stdout, under the face's own name: a reader
// learns a flag from the binary and never from a document.
func printHelp(fs *flag.FlagSet) {
	fs.SetOutput(os.Stdout)
	fs.Usage()
}

// usagef is the exit-2 constructor: a usage error or malformed input, with nothing
// written and no record on stdout.
func usagef(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "tillr-store: "+format+"\n", args...)
	return record.ExitUsage
}

// internalf is the exit-5 constructor: a bug, not an outcome.
func internalf(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "tillr-store: "+format+"\n", args...)
	return record.ExitInternal
}

// storeFailure is the exit-3 constructor: the store could not be read or migrated, so
// this binary cannot answer a question about it.
func storeFailure(err error) int {
	fmt.Fprintf(os.Stderr, "tillr-store: %v\n", err)
	return record.ExitIncompatible
}

// openFailure is the exit for a store this process could not open at all. A missing
// store or one it may not read is the caller's mistake about the path, not a schema
// mismatch: exit 3 means "incompatible", and answering it for a typo sends the caller
// looking for a migration that is not the problem.
func openFailure(err error) int {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return usagef("%v", err)
	}
	return storeFailure(err)
}
