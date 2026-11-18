package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// docTriageFace files one document triage: an item that runs doc-triage@v1 over the
// documents named after the flags, or linked from --roadmap, and lands one report.
func docTriageFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("doc-triage")
	path := fs.String("store", "", "the store to write")
	root := fs.String("root", ".", "the repository holding the documents and config/tillr/workflows")
	title := fs.String("title", "", "the triage's title; its slug names the item and the default report")
	roadmap := fs.String("roadmap", "", "a roadmap whose links under docs/design/ are triaged, after any named documents")
	report := fs.String("report", "", "the report's path (default docs/triage/<date>-<slug>.md)")
	to := fs.String("to", "", "first or last (default last when no other destination is given)")
	before := fs.String("before", "", "place before this item")
	after := fs.String("after", "", "place after this item")
	by := fs.String("by", "owner", "actor filing the triage")
	via := fs.String("via", "cli", "face filing the triage")
	now := fs.String("now", "", "injected RFC 3339 clock")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(fs)
			return record.Envelope{}, fail(errHelp)
		}
		return record.Envelope{}, fail(item.Usagef("%v", err))
	}
	dest := item.Destination{To: *to, Before: *before, After: *after}
	if dest == (item.Destination{}) {
		dest.To = "last"
	}
	if *path == "" || !dest.Valid() {
		return record.Envelope{}, fail(item.Usagef("doc-triage needs --store, --title, documents as arguments or --roadmap, and at most one of --to first|last, --before <item>, --after <item>"))
	}
	at := time.Now()
	if *now != "" {
		var err error
		if at, err = time.Parse(time.RFC3339, *now); err != nil {
			return record.Envelope{}, fail(item.Usagef("invalid --now: %v", err))
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
			return rejectionOrFailure(item.Rejectionf("store-busy", "", "the store's writer lock did not become available; no triage was filed"), at)
		}
		return record.Envelope{}, fail(openFailure(err))
	}
	defer s.Close()
	req := item.DocTriageRequest{Root: *root, Title: *title, Docs: fs.Args(), Roadmap: *roadmap, Report: *report, Destination: dest, At: at, By: *by, Via: *via}
	var result record.Envelope
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		result, err = item.DocTriage(ctx, tx, req)
		return err
	}); err != nil {
		if store.IsBusy(err) {
			return rejectionOrFailure(item.Rejectionf("store-busy", "", "the store's writer lock did not become available; no triage was filed"), at)
		}
		return rejectionOrFailure(err, at)
	}
	return result, record.ExitOK
}
