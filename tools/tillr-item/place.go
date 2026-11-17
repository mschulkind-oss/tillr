package main

import (
	"context"
	"os"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func placeFace(args []string) (record.Envelope, int) {
	fs := newFlagSet("place")
	path := fs.String("store", "", "the store to write")
	id := fs.String("item", "", "the item to move")
	to := fs.String("to", "", "first or last")
	before := fs.String("before", "", "place immediately before this item")
	after := fs.String("after", "", "place immediately after this item")
	by := fs.String("by", "owner", "the actor making the move")
	via := fs.String("via", "cli", "the face making the move")
	now := fs.String("now", "", "injected RFC 3339 clock (default: now)")
	if err := parseFlags(fs, args); err != nil {
		return record.Envelope{}, fail(err)
	}
	d := item.Destination{To: *to, Before: *before, After: *after}
	if *path == "" || *id == "" || !d.Valid() {
		return record.Envelope{}, fail(item.Usagef("place needs --store, --item and exactly one of --to first|last, --before <item>, --after <item>"))
	}
	at := time.Now().UTC()
	if *now != "" {
		var err error
		at, err = time.Parse(time.RFC3339, *now)
		if err != nil {
			return record.Envelope{}, fail(item.Usagef("invalid --now: %v", err))
		}
	}
	ctx := context.Background()
	if _, err := os.Stat(*path); err != nil {
		return record.Envelope{}, fail(openFailure(err))
	}
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		if store.IsBusy(err) {
			return rejectionOrFailure(item.Rejectionf("store-busy", *id, "the store's writer lock did not become available"), at)
		}
		return record.Envelope{}, fail(openFailure(err))
	}
	defer s.Close()
	var result record.Envelope
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		result, err = item.Place(ctx, tx, *id, d, at, *by, *via)
		return err
	})
	if store.IsBusy(err) {
		err = item.Rejectionf("store-busy", *id, "the store's writer lock did not become available")
	}
	if err != nil {
		return rejectionOrFailure(err, at)
	}
	return result, record.ExitOK
}
