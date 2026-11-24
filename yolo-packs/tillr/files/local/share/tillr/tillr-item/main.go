// Command tillr-item creates and reads work items in a named tillr store, and the
// decision, mail-delivery and verdict records that attach to them. It is
// the item utility of the tillr suite: apply writes one item.record, show answers one,
// state moves one item's own state (not its criteria's), the decision and mail nouns
// record and read the two ledgers design §5.1 gives this utility, the verdict
// noun records the one writer a criterion's state moves on, rank changes only an existing item's priority,
// and sync materializes the item blocks of the repository's tracked documents.
// main keeps the flags, the exit codes and the process lifecycle, and the item package
// keeps the schema, the kinds and the record→row mapping.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

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
		return fail(item.Usagef("a face is required: %s", faceList))
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("tillr-item: %s\nRun `tillr-item <face> --help` for one face's flags.\n", faceList)
		return record.ExitOK
	}
	face, rest := args[0], args[1:]

	var (
		env  record.Envelope
		code int
	)
	switch face {
	case "apply":
		env, code = applyFace(rest)
	case "seed":
		return fail(item.Usagef("seed has been retired; use tillr item sync to read items from tracked documents"))
	case "rank":
		env, code = rankFace(rest)
	case "dep":
		env, code = depFace(rest)
	case "sync":
		return syncFace(rest)
	case "reconcile":
		return reconcileFace(rest)
	case "comments":
		return commentsFace(rest)
	case "judge":
		return judgeFace(rest)
	case "list":
		env, code = listFace(rest)
	case "show":
		env, code = showFace(rest)
	case "state":
		env, code = stateFace(rest)
	case "place":
		env, code = placeFace(rest)
	case "idea":
		env, code = ideaFace(rest)
	case "doc-triage":
		env, code = docTriageFace(rest)
	case "request":
		env, code = requestFace(rest)
	case "close", "reopen":
		env, code = lifecycleFace(face, rest)
	case "decision":
		env, code = decisionFace(rest)
	case "mail":
		env, code = mailFace(rest)
	case "verdict":
		env, code = verdictFace(rest)
	default:
		return fail(item.Usagef("unknown face %q: want %s", face, faceList))
	}

	// A face that produced a record prints it whatever its exit code: a rejection is
	// the answer to "may this item be written", not a dropped artifact. A face that
	// could not answer at all returns no record and prints only its diagnostic.
	if env.Kind != "" {
		raw, err := encodeEnvelope(env)
		if err != nil {
			return fail(item.Internalf("encoding the record: %v", err))
		}
		fmt.Println(string(raw))
	}
	return code
}

// applyFace writes one item.record and echoes it as stored.
func applyFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("apply")
	path := fs.String("store", "", "the store to write")
	input := fs.String("input", "", "the item.record to apply")
	reason := fs.String("reason", "", "why a finished item leaves its state")
	by := fs.String("by", "", "the actor authorizing a finished-state move")
	resolvesVia := fs.String("resolves-via", "", "the item, commit or document resolving the item")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *input == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("apply needs --store <path>, --input <record> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	env, err := readInput(*input, item.RecordKind)
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	// The injected clock is the record's clock: updated_at and the event's timestamp
	// both come from it, so two runs of one input produce the same bytes.
	env.CreatedAt = at.UTC()
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.ApplyWithReceipt(ctx, tx, env, item.MoveReceipt{Reason: *reason, By: *by, ResolvesVia: *resolvesVia})
	})
}

// rankFace changes one existing item's priority without re-presenting its definition.
func rankFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("rank")
	path := fs.String("store", "", "the store to write")
	id := fs.String("item", "", "the item whose priority changes")
	rank := fs.String("rank", "", "the lexical priority token; empty clears priority")
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: wall clock)")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	hasRank := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "rank" {
			hasRank = true
		}
	})
	if *path == "" || *id == "" || !hasRank {
		return record.Envelope{}, fail(item.Usagef("rank needs --store <path>, --item <id> and --rank <token> (empty clears priority)"))
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = parsed.UTC()
	}
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.SetRank(ctx, tx, *id, *rank, at)
	})
}

// depFace adds or removes blocked_by edges on an existing item without rewriting other
// fields or changing its rank/place.
func depFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("dep")
	path := fs.String("store", "", "the store to write")
	id := fs.String("item", "", "the item whose dependencies change")
	var add, remove []string
	fs.Func("add", "an item id to add to blocked_by; repeat for each", func(v string) error {
		add = append(add, v)
		return nil
	})
	fs.Func("remove", "an item id to remove from blocked_by; repeat for each", func(v string) error {
		remove = append(remove, v)
		return nil
	})
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: wall clock)")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *id == "" || (len(add) == 0 && len(remove) == 0) {
		return record.Envelope{}, fail(item.Usagef("dep needs --store <path>, --item <id>, and at least one --add <id> or --remove <id>"))
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = parsed.UTC()
	}
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.SetDeps(ctx, tx, *id, add, remove, at)
	})
}

// syncFace reads the item blocks of every tracked Markdown file under <root>/docs/ and writes
// the items they declare through the one record→row mapping. It is the only intake from
// documents (docs/design/tillr-convergence.md Section 3.1). --dry-run reads the same documents
// and store and prints the same report, writing nothing.
//
// The report is text by default, since a person runs this face to see what their documents
// declare; --json prints the item.sync record instead, which is what the drain reads.
//
// Exit 0: every block was read and written or already matched. Exit 1: at least one block has
// a finding, and the documents without one were still written. Exit 2: usage. Exit 3: the
// store cannot be opened. Exit 4: git could not list the tracked documents.
func syncFace(args []string) int {
	fs := newFlagSet("sync")
	path := fs.String("store", "", "the store to write")
	root := fs.String("root", ".", "the repository root whose tracked docs/**/*.md are read")
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: the wall clock)")
	dryRun := fs.Bool("dry-run", false, "read and report, write nothing")
	asJSON := fs.Bool("json", false, "print the item.sync record instead of the text report")
	var roadmaps []string
	fs.Func("roadmap", "limit imports to this tracked docs/ roadmap and its unparked direct links (overrides [sync] roadmaps in .tillr.toml); repeat in desired order", func(path string) error {
		roadmaps = append(roadmaps, path)
		return nil
	})
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" {
		return fail(item.Usagef("sync needs --store <path>"))
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = parsed.UTC()
	}
	docs, selected, err := item.ReadRoadmapDocuments(*root, roadmaps)
	if err != nil {
		return fail(err)
	}
	in := item.SyncInput{Root: *root, Documents: docs, At: at, Partial: len(selected) > 0, Roadmaps: selected}

	ctx := context.Background()
	var report item.SyncReport
	switch _, statErr := os.Stat(*path); {
	case *dryRun && errors.Is(statErr, os.ErrNotExist):
		// A store nobody has created holds no item, so the preview is against an empty one.
		if report, err = item.Sync(ctx, nil, nil, in); err != nil {
			return fail(err)
		}
	case *dryRun:
		s, err := store.OpenRead(ctx, *path)
		if err != nil {
			return fail(openFailure(err))
		}
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			report, err = item.Sync(ctx, r, nil, in)
			return err
		}); err != nil {
			return fail(err)
		}
	default:
		s, err := store.Open(ctx, *path, true)
		if err != nil {
			return fail(openFailure(err))
		}
		defer s.Close()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			var err error
			report, err = item.Sync(ctx, tx.Reader(), tx, in)
			return err
		}); err != nil {
			return fail(err)
		}
	}

	if *asJSON {
		env, err := item.SyncEnvelope(report, at)
		if err != nil {
			return fail(err)
		}
		raw, err := record.Encode(env)
		if err != nil {
			return fail(item.Internalf("encoding the record: %v", err))
		}
		fmt.Println(string(raw))
	} else {
		fmt.Print(item.SyncText(report, at))
	}
	if len(report.Findings) > 0 {
		return record.ExitNegative
	}
	return record.ExitOK
}

// listFace answers item summaries from one read-only store snapshot.
func listFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("list")
	path := fs.String("store", "", "the store to read")
	var states []string
	fs.Func("state", "keep items in this state; repeat to include multiple states", func(state string) error {
		states = append(states, state)
		return nil
	})
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" {
		return record.Envelope{}, fail(item.Usagef("list needs --store <path>"))
	}
	return readStore(*path, func(_ context.Context, r *store.Reader) (record.Envelope, error) {
		return item.List(r, states)
	})
}

// showFace answers the stored item.record for one item, with the open questions of the
// design document it points at read from that document.
func showFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("show")
	path := fs.String("store", "", "the store to read")
	id := fs.String("item", "", "the item to show")
	root := fs.String("root", ".", "the directory a relative design_doc resolves against")
	questionsCmd := fs.String("questions-cmd", "", "the program that answers Vantage's index, as one command line (default: "+vantage.IndexCommandName(nil)+")")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *id == "" {
		return record.Envelope{}, fail(item.Usagef("show needs --store <path> and --item <id>"))
	}

	ctx := context.Background()
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return record.Envelope{}, fail(openFailure(err))
	}
	defer s.Close()

	var stored record.Envelope
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		stored, err = item.ShowWithQuestions(r, *id, *root, &vantage.VantageQuestions{
			Root: *root, Command: strings.Fields(*questionsCmd), Context: ctx,
		})
		return err
	})
	if err != nil {
		// show writes nothing, so it has no injected clock to stamp a rejection with.
		return rejectionOrFailure(err, time.Now().UTC())
	}
	return stored, record.ExitOK
}

// stateFace moves one item's own state and echoes the item as stored. It is the writer of
// the item states of docs/reference/tillr-item-states.md, as distinct from the criterion states the
// verdict face moves: an item's state says where its work stands, a criterion's says what a
// judge found, and one face writing both would make a criterion's freeze depend on a
// caller's care.
func stateFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("state")
	path := fs.String("store", "", "the store to write")
	id := fs.String("item", "", "the item whose state moves")
	state := fs.String("state", "", "the state to move it to")
	waiter := fs.String("waiter", "", "blocked waiter: owner, design, plan, or item:<id>")
	reason := fs.String("reason", "", "why the item waits or enters/leaves a finished state")
	by := fs.String("by", "", "the actor authorizing a finished-state move")
	resolvesVia := fs.String("resolves-via", "", "finished-state evidence: item:<id>, commit:<sha>, or doc:<path>")
	requireVerdicts := fs.Bool("require-verdicts", false, "refuse the move unless every stored criterion has a recorded verdict")
	finishExistingLanding := fs.Bool("finish-existing-landing", false, "finish as landed using an unreverted landing; requires --reason, --by and --resolves-via item:<id> or commit:<sha>; incompatible with --require-verdicts")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *id == "" || *state == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("state needs --store <path>, --item <id>, --state <state> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	var flags []string
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "reason", "by", "resolves-via", "waiter":
			flags = append(flags, f.Name)
		}
	})
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.SetStateWithReceipt(ctx, tx, *id, *state, at.UTC(), item.MoveReceipt{
			Reason: *reason, By: *by, ResolvesVia: *resolvesVia, Flags: flags,
			RequireVerdicts: *requireVerdicts, FinishExistingLanding: *finishExistingLanding,
		}, item.WaitingOn{Waiter: *waiter, Reason: *reason})
	})
}

// lifecycleFace records an explicit owner disposition with its reason and actor.
func lifecycleFace(verb string, args []string) (record.Envelope, int) {
	fs := newFlagSet(verb)
	path := fs.String("store", "", "the store to write")
	id := fs.String("item", "", "the item to "+verb)
	reason := fs.String("reason", "", "why the item is being "+verb+"d")
	by := fs.String("by", "", "who requests the transition")
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: wall clock)")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *id == "" {
		return record.Envelope{}, fail(item.Usagef("%s needs --store <path> and --item <id>", verb))
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = parsed.UTC()
	}
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		if verb == "close" {
			return item.Close(ctx, tx, *id, *reason, *by, at)
		}
		return item.Reopen(ctx, tx, *id, *reason, *by, at)
	})
}

// decisionFace dispatches the decision noun's four verbs.
func decisionFace(args []string) (record.Envelope, int) {
	if verbHelp("decision", "record, supersede, revoke or list", args) {
		return record.Envelope{}, record.ExitOK
	}
	if len(args) == 0 {
		return record.Envelope{}, fail(item.Usagef("decision needs a verb: record, supersede, revoke or list"))
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "record":
		return decisionRecordFace(rest)
	case "supersede":
		return decisionSupersedeFace(rest)
	case "revoke":
		return decisionRevokeFace(rest)
	case "list":
		return decisionListFace(rest)
	default:
		return record.Envelope{}, fail(item.Usagef("unknown decision verb %q: want record, supersede, revoke or list", verb))
	}
}

// decisionRecordFace writes one decision.
func decisionRecordFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("decision record")
	path := fs.String("store", "", "the store to write")
	input := fs.String("input", "", "the decision.record to write")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *input == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("decision record needs --store <path>, --input <record> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	env, err := readInput(*input, item.DecisionRecordKind)
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	env.CreatedAt = at.UTC()
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.RecordDecision(ctx, tx, env)
	})
}

// decisionSupersedeFace writes one decision and marks the one it replaces.
func decisionSupersedeFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("decision supersede")
	path := fs.String("store", "", "the store to write")
	input := fs.String("input", "", "the successor decision.record")
	supersedes := fs.String("decision", "", "the decision this one replaces")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *input == "" || *supersedes == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("decision supersede needs --store <path>, --decision <id>, --input <record> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	env, err := readInput(*input, item.DecisionRecordKind)
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	env.CreatedAt = at.UTC()
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.SupersedeDecision(ctx, tx, env, *supersedes)
	})
}

// decisionRevokeFace marks one decision revoked.
func decisionRevokeFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("decision revoke")
	path := fs.String("store", "", "the store to write")
	id := fs.String("decision", "", "the decision to revoke")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *id == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("decision revoke needs --store <path>, --decision <id> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.RevokeDecision(ctx, tx, *id, at)
	})
}

// decisionListFace answers the decisions in one status.
func decisionListFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("decision list")
	path := fs.String("store", "", "the store to read")
	status := fs.String("status", "", "the status to list: active, superseded or revoked")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" {
		return record.Envelope{}, fail(item.Usagef("decision list needs --store <path>"))
	}
	return readStore(*path, func(ctx context.Context, r *store.Reader) (record.Envelope, error) {
		return item.ListDecisions(ctx, r, *status)
	})
}

// mailFace dispatches the mail noun's three verbs.
func mailFace(args []string) (record.Envelope, int) {
	if verbHelp("mail", "send, list or ack", args) {
		return record.Envelope{}, record.ExitOK
	}
	if len(args) == 0 {
		return record.Envelope{}, fail(item.Usagef("mail needs a verb: send, list or ack"))
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "send":
		return mailSendFace(rest)
	case "list":
		return mailListFace(rest)
	case "ack":
		return mailAckFace(rest)
	default:
		return record.Envelope{}, fail(item.Usagef("unknown mail verb %q: want send, list or ack", verb))
	}
}

// mailSendFace records one delivery as intent.
func mailSendFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("mail send")
	path := fs.String("store", "", "the store to write")
	input := fs.String("input", "", "the mail.delivery to record")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *input == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("mail send needs --store <path>, --input <record> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	env, err := readInput(*input, item.MailRecordKind)
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	env.CreatedAt = at.UTC()
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.SendMail(ctx, tx, env)
	})
}

// mailListFace answers the deliveries to one target, or every delivery.
func mailListFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("mail list")
	path := fs.String("store", "", "the store to read")
	to := fs.String("to", "", "the target whose deliveries to list")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" {
		return record.Envelope{}, fail(item.Usagef("mail list needs --store <path>"))
	}
	return readStore(*path, func(ctx context.Context, r *store.Reader) (record.Envelope, error) {
		return item.ListMail(ctx, r, *to)
	})
}

// mailAckFace marks one delivery acknowledged.
func mailAckFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("mail ack")
	path := fs.String("store", "", "the store to write")
	id := fs.String("mail", "", "the delivery to acknowledge")
	by := fs.String("by", "", "the session acknowledging it")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *id == "" || *by == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("mail ack needs --store <path>, --mail <id>, --by <session> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		return item.AckMail(ctx, tx, *id, *by, at)
	})
}

// verdictFace dispatches the verdict noun's one verb.
func verdictFace(args []string) (record.Envelope, int) {
	if verbHelp("verdict", "record", args) {
		return record.Envelope{}, record.ExitOK
	}
	if len(args) == 0 {
		return record.Envelope{}, fail(item.Usagef("verdict needs a verb: record"))
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "record":
		return verdictRecordFace(rest)
	default:
		return record.Envelope{}, fail(item.Usagef("unknown verdict verb %q: want record", verb))
	}
}

// verdictRecordFace records one item.verdict, which is the only writer of a criterion's
// state.
func verdictRecordFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("verdict record")
	path := fs.String("store", "", "the store to write")
	input := fs.String("input", "", "the item.verdict to record")
	head := fs.String("head", "", "the exact candidate commit SHA (agent verdicts only)")
	workflow := fs.String("workflow", "", "the persisted workflow reference")
	node := fs.String("evaluator-node", "", "the persisted evaluator node")
	digest := fs.String("graph-sha256", "", "SHA-256 of the exact persisted expanded graph")

	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	if *path == "" || *input == "" || *now == "" {
		return record.Envelope{}, fail(item.Usagef("verdict record needs --store <path>, --input <record> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	env, err := readInput(*input, item.VerdictRecordKind)
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return record.Envelope{}, fail(item.Usagef("reading verdict payload: %v", err))
	}
	if payload == nil {
		return record.Envelope{}, fail(item.Usagef("verdict payload must be a JSON object"))
	}
	for key, value := range map[string]string{"workflow": *workflow, "evaluator_node": *node, "graph_sha256": *digest, "evaluated_head": *head} {
		if value == "" {
			continue
		}
		if prior, ok := payload[key]; ok && prior != "" && prior != value {
			return rejectionOrFailure(item.Rejectionf("evaluator-binding", fmt.Sprint(payload["item_id"]), "--%s differs from verdict payload", strings.ReplaceAll(key, "_", "-")), at)
		}
		payload[key] = value
	}
	if *head != "" {
		tree, err := judgeGitObject(*head + "^{tree}")
		if err != nil {
			return record.Envelope{}, fail(err)
		}
		if prior, ok := payload["evaluated_tree"]; ok && prior != "" && prior != tree {
			return rejectionOrFailure(item.Rejectionf("evaluated-tree", fmt.Sprint(payload["item_id"]), "candidate tree differs from verdict payload"), at)
		}
		payload["evaluated_tree"] = tree
	}
	env.Payload, err = json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, fail(item.Internalf("encoding verdict payload: %v", err))
	}

	env.CreatedAt = at.UTC()
	return writeStore(*path, at, func(ctx context.Context, tx *store.Tx) (record.Envelope, error) {
		stored, err := item.RecordVerdict(ctx, tx, env)
		if err != nil {
			return record.Envelope{}, err
		}
		if payload["actor_kind"] == item.ActorAgent {
			candidate, _ := payload["evaluated_head"].(string)
			resolved, err := judgeCommit(candidate)
			if err != nil {
				return record.Envelope{}, err
			}
			if resolved != candidate {
				return record.Envelope{}, item.Rejectionf("evaluated-head", fmt.Sprint(payload["item_id"]), "verdict head is not the exact candidate commit")
			}
			tree, err := judgeGitObject(candidate + "^{tree}")
			if err != nil {
				return record.Envelope{}, err
			}
			if tree != payload["evaluated_tree"] {
				return record.Envelope{}, item.Rejectionf("evaluated-tree", fmt.Sprint(payload["item_id"]), "verdict tree differs from the candidate tree")
			}
		}
		return stored, nil
	})
}

// readInput reads one input record and refuses a kind this face does not consume.
func readInput(path, kind string) (record.Envelope, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return record.Envelope{}, item.Usagef("reading %s: %v", path, err)
	}
	env, err := record.Decode(raw)
	if err != nil {
		return record.Envelope{}, item.Usagef("%v", err)
	}
	if env.Kind != kind {
		return record.Envelope{}, item.Usagef("this face consumes %s, not %q", kind, env.Kind)
	}
	return env, nil
}

// writeStore opens the store, runs one write face in a transaction and answers its
// record and exit code. The transaction is the face's: a refusal rolls back the rows
// and the event together.
func writeStore(path string, at time.Time, fn func(context.Context, *store.Tx) (record.Envelope, error)) (record.Envelope, int) {
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		return record.Envelope{}, fail(openFailure(err))
	}
	defer s.Close()

	var stored record.Envelope
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		stored, err = fn(ctx, tx)
		return err
	})
	if err != nil {
		return rejectionOrFailure(err, at)
	}
	return stored, record.ExitOK
}

// readStore opens the store read-only and runs one read face.
func readStore(path string, fn func(context.Context, *store.Reader) (record.Envelope, error)) (record.Envelope, int) {
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return record.Envelope{}, fail(openFailure(err))
	}
	defer s.Close()

	var stored record.Envelope
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		stored, err = fn(ctx, r)
		return err
	})
	if err != nil {
		return rejectionOrFailure(err, time.Now().UTC())
	}
	return stored, record.ExitOK
}

// rejectionOrFailure answers a face's error: a rejection is a negative decision and
// carries its record, anything else is a diagnostic and no record.
// encodeEnvelope encodes one record envelope without HTML-escaping angle brackets in
// command placeholders such as "--by <who> --why <reason>".
func encodeEnvelope(env record.Envelope) ([]byte, error) {
	env.CreatedAt = env.CreatedAt.UTC()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte{'\n'}), nil
}

func rejectionOrFailure(err error, at time.Time) (record.Envelope, int) {
	rej, ok := item.RejectionOf(err)
	if !ok {
		return record.Envelope{}, fail(err)
	}
	return item.RejectionEnvelope(rej, at), record.ExitNegative
}

// faceList is this binary's faces, in the order the usage lines print them. One home,
// so the refusal, the unknown-face line and `--help` cannot disagree.
const faceList = "apply, rank, dep, sync, reconcile, comments, list, show, state, place, idea, doc-triage, request, close, reopen, decision, mail, verdict or judge"

// errHelp is what parseFlags answers for a `--help`. A face's flags are the layer-1
// surface (docs/reference/tillr-distribution.md, "Documentation split"), so a help request is an answer
// and not a usage error: fail turns it into exit 0 and prints nothing.
var errHelp = errors.New("help requested")

func newFlagSet(face string) *flag.FlagSet {
	fs := flag.NewFlagSet("tillr-item "+face, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags turns a flag error into the usage exit, with the diagnostic on stderr.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(fs)
			return errHelp
		}
		return item.Usagef("%v", err)
	}
	if fs.NArg() > 0 {
		return item.Usagef("unexpected argument %q", fs.Arg(0))
	}
	return nil
}

// printHelp writes one face's flags to stdout, under the face's own name: a reader
// learns a flag from the binary and never from a document.
func printHelp(fs *flag.FlagSet) {
	fs.SetOutput(os.Stdout)
	fs.Usage()
}

// verbHelp answers `<noun> --help` with the verbs the noun accepts, and reports whether
// it answered. A noun's verbs are its surface, so the request is not a usage error.
func verbHelp(noun, verbs string, args []string) bool {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Printf("tillr-item %s: %s\nRun `tillr-item %s <verb> --help` for one verb's flags.\n", noun, verbs, noun)
		return true
	}
	return false
}
