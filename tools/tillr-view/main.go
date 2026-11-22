// Architecture and invariants: docs/reference/observatory-redesign.md
// Command tillr-view renders a board's status and event log from a store snapshot or
// from a stream of records, and writes nothing anywhere: it is safe to point at the
// live store while writers run (design §5.8).
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
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// utility is the producer name every record this binary emits carries.
const utility = "view"

// version is this binary's producer version. The version set is released together
// (design §11), so this moves with the set rather than on its own.
const version = "0.1.0"

// main flushes the SQLite trace on every route out of the process: before os.Exit,
// which skips defers, and through the deferred call when a panic unwinds this
// goroutine. Tracing off, each flush is one atomic load.
func main() {
	defer store.FlushSQLiteTraceAtExit()
	code := run(os.Args[1:])
	store.FlushSQLiteTraceAtExit()
	os.Exit(code)
}

// run dispatches one face and prints the record it produced. A face returns its record
// and its exit code together, so the exit is fixed where the outcome is known rather
// than reconstructed here.
func run(args []string) int {
	if len(args) == 0 {
		return usagef("a face is required: %s", faceList)
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("tillr-view: %s\nRun `tillr-view <face> --help` for one face's flags.\n", faceList)
		return record.ExitOK
	}
	face, rest := args[0], args[1:]

	var (
		env  record.Envelope
		code int
	)
	switch face {
	case "status":
		var asText bool
		env, code = statusFaceFormat(rest, &asText)
		if env.Kind == "view.status" && asText {
			var p statusPayload
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return usagef("reading status: %v", err)
			}
			fmt.Print(statusText(p))
			return code
		}
	case "events":
		env, code = eventsFace(rest)
	case "heavy-misses":
		env, code = heavyMissesFace(rest)
	case "passes":
		env, code = passesFace(rest)
	case "waiting":
		env, code = waitingFace(rest)
	case "alarms":
		return alarmsFace(rest)
	case "lanes":
		env, code = lanesFace(rest)
	case "running":
		env, code = runningFace(rest)
	case "run":
		env, code = runFace(rest, os.Stdout)
	case "trace":
		return traceFace(rest, os.Stdout)
	case "restart":
		return restartFace(rest)
	case "serve":
		env, code = serveFace(rest)
	case "perf-log":
		env, code = perfLogFace(rest)
	default:
		return usagef("unknown face %q: want %s", face, faceList)
	}
	if code == helpExit {
		// The face answered `--help`: its flags went to stdout and nothing ran.
		return record.ExitOK
	}

	// A face that produced a record prints it whatever its exit code: exit 1's record
	// is the answer "the subject is not there", and exit 3's is the version the store
	// holds. A face that could not read its source at all returns no record and prints
	// only its diagnostic.
	if env.Kind != "" {
		raw, err := record.Encode(env)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
			return record.ExitInternal
		}
		fmt.Println(string(raw))
	}
	return code
}

// statusFace reports the moment the source describes, whether it names the subject the
// caller asked about, and the assembled subject.
func statusFace(args []string) (record.Envelope, int) {
	return statusFaceFormat(args, nil)
}

func statusFaceFormat(args []string, asText *bool) (record.Envelope, int) {
	fs := newFlagSet("status")
	src := newSourceFlags(fs)
	item := fs.String("item", "", "the item to report on; without it the report covers the whole board")
	policy := fs.String("policy", "", "the backend policy, when not using the loop's accepted policy")
	// root is where a relative design_doc resolves, the same flag and default item's
	// show face uses: the index answers for the tree rooted here.
	root := fs.String("root", ".", "the directory a relative design_doc resolves against")
	asJSON := fs.Bool("json", false, "print the view.status record (the default)")
	text := fs.Bool("text", false, "print a human-readable status summary and sync residue counts")
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	nowFlag := fs.String("now", "", "the observation clock, RFC 3339 (default: now)")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	now := time.Now().UTC()
	if *nowFlag != "" {
		parsed, err := time.Parse(time.RFC3339, *nowFlag)
		if err != nil {
			return record.Envelope{}, usagef("--now %q is not an RFC 3339 timestamp: %v", *nowFlag, err)
		}
		now = parsed.UTC()
	}

	if asText != nil {
		*asText = *text && !*asJSON
	}
	v, refusal, code := openSource(context.Background(), src, "view.status")
	if code != record.ExitOK {
		return refusal, code
	}
	var subject *record.Subject
	if *item != "" {
		subject = &record.Subject{Kind: "item", ID: *item}
	}
	// The index is read lazily, on the first item that names a design document, so a
	// report whose items name none pays nothing. An absent tool is `questions-unknown`
	// for the items whose documents are in the tree, which is the answer rather than a
	// refusal of the report.
	questions := &vantage.VantageQuestions{Root: *root, Command: commandFields(*questionsCmd), Context: context.Background()}
	payload, code := assemble(v, subject, questions)
	if code != record.ExitOK && code != record.ExitNegative {
		// A record's payload this utility cannot describe is malformed input. §5.1 answers
		// that with exit 2 and no record. A design document that is not there is a state
		// the report carries, not a refusal.
		return record.Envelope{}, code
	}
	if *nowFlag != "" {
		payload.GeneratedAt = now
	}
	if *src.store != "" {
		s, err := store.OpenRead(context.Background(), *src.store)
		if err != nil {
			return record.Envelope{}, exitFor(err)
		}
		err = s.ReadTx(context.Background(), func(r *store.Reader) error {
			var err error
			payload.Alarms, err = readAlarms(r, *root, *policy, now)
			if err == nil && subject == nil {
				payload.Push = readPushSummary(context.Background(), r, *root)
			}
			return err
		})
		s.Close()
		if err != nil {
			return record.Envelope{}, exitFor(err)
		}
	}
	if *src.store != "" && subject == nil {
		proof, err := heavy.ReadStatus(context.Background(), *root, *src.store)
		if err != nil {
			payload.HeavyFault = err.Error()
		} else {
			payload.Heavy = &proof
		}
	}
	return envelope("view.status", payload), code
}

// eventsPayload is the payload of view.events: the moment, the cursor the caller gave,
// the events after it, and the cursor to advance to.
type eventsPayload struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Snapshot    snapshot          `json:"snapshot"`
	Since       int64             `json:"since"`
	Events      []eventRecord     `json:"events"`
	Conflicts   []conflictHistory `json:"conflicts"`
	// NextSince is the cursor the caller advances to: the last seq in the record, or
	// the unchanged since when there are none. It is never event_head_seq — the head is
	// the store's, and a caller that advanced to it would skip every event appended
	// after this read.
	NextSince int64 `json:"next_since"`
}

// eventsFace is the log follower: the events after --since, in order.
func eventsFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("events")
	src := newSourceFlags(fs)
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}

	v, refusal, code := openSource(context.Background(), src, "view.events")
	if code != record.ExitOK {
		return refusal, code
	}
	events := v.events
	if events == nil {
		// An empty read is an empty list, not null: a caller that iterates the field
		// should not have to special-case the absence of events.
		events = []eventRecord{}
	}
	next := *src.since
	if len(events) > 0 {
		next = events[len(events)-1].Seq
	}
	return envelope("view.events", eventsPayload{
		GeneratedAt: time.Now().UTC(),
		Snapshot:    v.snapshot,
		Since:       *src.since,
		Events:      events,
		Conflicts:   v.conflicts,
		NextSince:   next,
	}), record.ExitOK
}

// questionsCmdHelp is the flag text every face that reads Vantage's index shares. The
// default is the reader's own, so the sentence names it from one place.
var questionsCmdHelp = "the program that answers Vantage's index, as one command line (default: " +
	strings.Join(vantage.DefaultIndexCommand, " ") + ")"

// commandFields turns one command line into the argv the reader runs. A blank value is no
// command, which the reader answers with its default.
func commandFields(line string) []string { return strings.Fields(line) }

// sourceFlags are the two ways a face is given its input, plus the cursor both faces
// read from. Exactly one source is required.
type sourceFlags struct {
	store   *string
	records *string
	since   *int64
}

func newSourceFlags(fs *flag.FlagSet) sourceFlags {
	return sourceFlags{
		store:   fs.String("store", "", "the store to read"),
		records: fs.String("records", "", "a JSON array of records to assemble from, with no store involved"),
		since:   fs.Int64("since", 0, "read events after this cursor: an event seq, or a record's 1-based position in a records file"),
	}
}

// open reads the one source the flags name. A face given both would have to choose,
// and a face given neither has nothing to describe; both are usage errors.
func (f sourceFlags) open(ctx context.Context) (view, error) {
	hasStore, hasRecords := *f.store != "", *f.records != ""
	switch {
	case hasStore && hasRecords:
		return view{}, usageErrorf("--store and --records name two sources; give one")
	case !hasStore && !hasRecords:
		return view{}, usageErrorf("a source is required: --store <path> or --records <file>")
	case hasStore:
		return openStore(ctx, *f.store, *f.since)
	default:
		return openRecords(*f.records, *f.since)
	}
}

// refusal is the payload of a record that answers "this store's schema is not one I
// can read". Both faces emit it under their own kind, because the record answers the
// face that was asked.
type refusal struct {
	GeneratedAt time.Time `json:"generated_at"`
	Snapshot    snapshot  `json:"snapshot"`
}

// openSource opens the source and answers the refusal record for a store whose schema
// this binary does not support. §5.1 defines exit 3 as the schema-incompatible answer
// generally, so both faces answer it the same way, and the record is the answer: a
// caller that learns neither the store's version nor the supported one cannot act on
// the refusal.
func openSource(ctx context.Context, src sourceFlags, kind string) (view, record.Envelope, int) {
	v, err := src.open(ctx)
	if err == nil {
		return v, record.Envelope{}, record.ExitOK
	}
	var mismatch schemaMismatch
	if errors.As(err, &mismatch) {
		fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
		return view{}, envelope(kind, refusal{
			GeneratedAt: time.Now().UTC(),
			Snapshot:    snapshot{SchemaVersion: mismatch.version, Source: sourceStore},
		}), record.ExitIncompatible
	}
	return view{}, record.Envelope{}, exitFor(err)
}

// envelope wraps a payload in the common record envelope.
func envelope(kind string, payload any) record.Envelope {
	raw, err := json.Marshal(payload)
	if err != nil {
		// The payload is a struct this package defines, so an encoding failure is a bug.
		panic(err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          kind,
		ID:            newID(),
		Producer:      record.Producer{System: record.System, Utility: utility, Version: version},
		CreatedAt:     time.Now().UTC(),
		Payload:       raw,
	}
}

// newID is this record's opaque identifier. It is random rather than derived from the
// source, because two reports of one store are two records.
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// faceList is this binary's faces, in the order the usage lines print them. One home,
// so the refusal, the unknown-face line and `--help` cannot disagree.
const faceList = "status, events, heavy-misses, waiting, alarms, passes, lanes, running, run, trace, serve, restart or perf-log"

// helpExit is what parseFlags answers for a `--help`. A face's flags are the layer-1
// surface (docs/design/tillr-distribution.md Section 3), so a help request is an answer
// and not a usage error: run turns this into exit 0, and no face ever sees it.
const helpExit = -1

func newFlagSet(face string) *flag.FlagSet {
	fs := flag.NewFlagSet("tillr-view "+face, flag.ContinueOnError)
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
	fmt.Fprintf(os.Stderr, "tillr-view: "+format+"\n", args...)
	return record.ExitUsage
}

// storeFailure is the exit-3 constructor: the store could not be read, so this binary
// cannot answer a question about it.
func storeFailure(err error) int {
	fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
	return record.ExitIncompatible
}
