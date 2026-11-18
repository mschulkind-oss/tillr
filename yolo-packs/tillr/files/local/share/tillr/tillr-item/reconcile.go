package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// reconcileFace reports what documents owe the queue without creating any items.
// It prints only a diagnostic on refusal, never a partial report.
func reconcileFace(args []string) int {
	fs := newFlagSet("reconcile")
	root := fs.String("root", ".", "the repository root to reconcile")
	path := fs.String("store", "", "optional store to read carrying and landed items from (never created)")
	command := fs.String("questions-cmd", "", "the Vantage index command as one command line (default: "+vantage.IndexCommandName(nil)+")")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	ctx := context.Background()
	in := item.ReconcileInput{Root: *root, Command: strings.Fields(*command)}
	var report item.ReconcileReport
	var err error
	if *path == "" {
		report, err = item.Reconcile(ctx, nil, in)
	} else {
		s, openErr := store.OpenRead(ctx, *path)
		if openErr != nil {
			return fail(openFailure(openErr))
		}
		defer s.Close()
		err = s.ReadTx(ctx, func(r *store.Reader) error { report, err = item.Reconcile(ctx, r, in); return err })
	}
	if err != nil {
		return fail(err)
	}
	fmt.Print(item.FormatReconcileReport(report))
	return record.ExitOK
}
