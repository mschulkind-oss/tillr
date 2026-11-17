package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func ideaFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("idea")
	path := fs.String("store", "", "the store to write")
	root := fs.String("root", ".", "repository containing .tillr.toml")
	to := fs.String("to", "", "first or last")
	before := fs.String("before", "", "place before this item")
	after := fs.String("after", "", "place after this item")
	by := fs.String("by", "owner", "actor saving the idea")
	via := fs.String("via", "cli", "face saving the idea")
	now := fs.String("now", "", "injected RFC 3339 clock")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	dest := item.Destination{To: *to, Before: *before, After: *after}
	if *path == "" || !dest.Valid() {
		return record.Envelope{}, fail(item.Usagef("idea needs --store and exactly one of --to first|last, --before <item>, --after <item>; words come from standard input"))
	}
	at := time.Now().UTC()
	if *now != "" {
		var err error
		at, err = time.Parse(time.RFC3339, *now)
		if err != nil {
			return record.Envelope{}, fail(item.Usagef("invalid --now: %v", err))
		}
	}
	words, err := io.ReadAll(io.LimitReader(os.Stdin, item.MaxIdeaWords+1))
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	hash, err := item.IdeaHash(words)
	if err != nil {
		return rejectionOrFailure(err, at)
	}
	config, err := settings.Read(*root)
	if err != nil {
		return record.Envelope{}, fail(item.Usagef("%v", err))
	}
	abs, err := filepath.Abs(*path)
	if err != nil {
		return record.Envelope{}, fail(err)
	}
	intakeDir := filepath.Join(filepath.Dir(abs), "intake")
	ctx := context.Background()
	// Do not create a store implicitly. An open or lock-acquisition refusal writes
	// nothing. After acquisition, a failed store write preserves and names the words.
	preserveFailure := func(err error) (record.Envelope, int) {
		file, saveErr := item.PreserveUnassignedIdea(intakeDir, hash, words)
		if saveErr != nil {
			return rejectionOrFailure(item.Rejectionf("intake-store-failed", "", "could not preserve words at %s: %v; store error: %v", file, saveErr, err), at)
		}
		return rejectionOrFailure(item.Rejectionf("intake-store-failed", "", "words saved at %s; item was not saved: %v", file, err), at)
	}
	if _, err := os.Stat(abs); err != nil {
		return record.Envelope{}, fail(openFailure(err))
	}
	s, err := store.Open(ctx, abs, true)
	if err != nil {
		if store.IsBusy(err) {
			return rejectionOrFailure(item.Rejectionf("store-busy", "", "the store's writer lock did not become available; no idea was saved"), at)
		}
		return record.Envelope{}, fail(openFailure(err))
	}
	defer s.Close()
	var result record.Envelope
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		result, err = item.Idea(ctx, tx, words, hash, intakeDir, config.Intake, dest, at, *by, *via)
		return err
	})
	if err != nil {
		if store.IsBusy(err) {
			return rejectionOrFailure(item.Rejectionf("store-busy", "", "the store's writer lock did not become available; no idea was saved"), at)
		}
		if _, ok := item.RejectionOf(err); ok {
			return rejectionOrFailure(err, at)
		}
		return preserveFailure(err)
	}
	return result, record.ExitOK
}
