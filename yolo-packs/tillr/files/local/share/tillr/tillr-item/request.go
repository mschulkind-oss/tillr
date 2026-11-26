package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// requestFace files one request: the words on standard input become an item that runs an
// existing workflow with no document block.
func requestFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("request")
	path := fs.String("store", "", "the store to write")
	root := fs.String("root", ".", "the repository holding config/tillr/workflows")
	title := fs.String("title", "", "the item's one-line title")
	seat := fs.String("seat", "build", "the item's seat: build or design (any declared seat with --workflow)")
	workflow := fs.String("workflow", "", "the workflow to run (default planned-build@v1 for build, scouted-design@v1 for design; single-worker@v1 is one run)")
	var paths, criteria, blockedBy, afterItems, alerts []string
	fs.Func("path", "deprecated and ignored: items no longer use an allow-list", func(p string) error {
		paths = append(paths, p)
		return nil
	})
	fs.Func("criterion", "an extra criterion, judged with the fixed four; repeat for each (at most four)", func(c string) error {
		criteria = append(criteria, c)
		return nil
	})
	fs.Func("blocked-by", "an item id this request waits on; repeat for each", func(id string) error {
		blockedBy = append(blockedBy, id)
		return nil
	})
	fs.Func("after-item", "an item id this request runs after (ordering-only, regardless of how it finishes); repeat for each", func(id string) error {
		afterItems = append(afterItems, id)
		return nil
	})
	fs.Func("alert", "an alert id to link to this request; repeat for each", func(id string) error {
		alerts = append(alerts, id)
		return nil
	})
	to := fs.String("to", "", "first or last (default last when no other destination is given)")
	before := fs.String("before", "", "place before this item")
	after := fs.String("after", "", "place after this item")
	ordinary := fs.Bool("ordinary", false, "file for the ordinary lanes only, not the express lanes")
	by := fs.String("by", "owner", "actor filing the request")
	filedBy := fs.String("filed-by", "", "provenance actor filing the request (e.g. tillr-triage); defaults to --by")
	dedupKey := fs.String("dedup-key", "", "idempotence key (<rule_id>:<cause_key>) to deduplicate open requests for one cause")
	via := fs.String("via", "cli", "face filing the request")
	now := fs.String("now", "", "injected RFC 3339 clock")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	byExplicit := false
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "by" {
			byExplicit = true
		}
	})
	actor := *by
	if *filedBy != "" && !byExplicit {
		actor = *filedBy
	}
	if len(paths) > 0 {
		fmt.Fprintln(os.Stderr, "notice: --path is deprecated and ignored; items no longer carry a list of allowed paths")
		paths = nil
	}
	dest := item.Destination{To: *to, Before: *before, After: *after}
	if dest == (item.Destination{}) {
		dest.To = "last"
	}
	if *path == "" || !dest.Valid() || fs.NArg() != 0 {
		return record.Envelope{}, fail(item.Usagef("request needs --store, --title, and at most one of --to first|last, --before <item>, --after <item>; the words come from standard input"))
	}
	at := time.Now()
	if *now != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, *now); err != nil {
			return record.Envelope{}, fail(item.Usagef("invalid --now: %v", err))
		}
	}
	words, err := io.ReadAll(io.LimitReader(os.Stdin, item.MaxIdeaWords+1))
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	filing := item.RequestFiling{Words: words, Title: *title, Seat: *seat, Workflow: *workflow, Paths: paths, Criteria: criteria, BlockedBy: blockedBy, AfterItems: afterItems, Alerts: alerts, FiledBy: *filedBy, DedupKey: *dedupKey, Ordinary: *ordinary, Destination: dest, At: at, By: actor, Via: *via}
	if filing.Workflow == "" {
		filing.Workflow = item.RequestDefaultWorkflow[filing.Seat]
	}
	// Every file this face reads is read here, before the write transaction opens.
	if filing.Workflow != "" {
		if err := item.RequestWorkflowCheck(*root, filing.Workflow); err != nil {
			return rejectionOrFailure(err, at)
		}
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	if _, err := os.Stat(abs); err != nil {
		return record.Envelope{}, fail(openFailure(err))
	}
	ctx := context.Background()
	s, err := store.Open(ctx, abs, true)
	if err != nil {
		if store.IsBusy(err) {
			return rejectionOrFailure(item.Rejectionf("store-busy", "", "the store's writer lock did not become available; no request was filed"), at)
		}
		return record.Envelope{}, fail(openFailure(err))
	}
	defer s.Close()
	var result record.Envelope
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		result, err = item.FileRequest(ctx, tx, filing)
		return err
	}); err != nil {
		if store.IsBusy(err) {
			return rejectionOrFailure(item.Rejectionf("store-busy", "", "the store's writer lock did not become available; no request was filed"), at)
		}
		return rejectionOrFailure(err, at)
	}
	return result, record.ExitOK
}
