package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func markCopiedUsageFace(args []string) int {
	fs := newFlagSet("mark-copied-usage")
	path := fs.String("store", "", "the store holding the recorded usage")
	id := fs.String("run", "", "the run whose usage was copied")
	source := fs.String("source-run", "", "the earlier distinct run with equal tokens and cost")
	reason := fs.String("reason", "", "why this usage is known to be copied (required)")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *id == "" || *source == "" || strings.TrimSpace(*reason) == "" {
		return fail(run.Usagef("mark-copied-usage needs --store <path>, --run <id>, --source-run <id> and --reason <text>"))
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	if err := run.MarkCopiedUsage(ctx, s, *id, *source, *reason, time.Now().UTC()); err != nil {
		return fail(err)
	}
	fmt.Printf("marked usage of %s as copied from %s\n", *id, *source)
	return record.ExitOK
}

func copiedUsageFace(args []string) int {
	fs := newFlagSet("copied-usage")
	path := fs.String("store", "", "the store to inspect")
	apply := fs.Bool("apply", false, "clear all matching usage and preserve it in events, in one transaction")
	reason := fs.String("reason", "", "why the matching usage is known to be copied (required with --apply)")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || (*apply && strings.TrimSpace(*reason) == "") || (!*apply && *reason != "") {
		return fail(run.Usagef("copied-usage needs --store <path>; correction needs --apply --reason <text> (reason is only used with --apply)"))
	}
	ctx := context.Background()
	var s *store.Store
	var err error
	if *apply {
		s, err = store.Open(ctx, *path, true)
	} else {
		s, err = store.OpenRead(ctx, *path)
	}
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	var matches []run.CopiedUsage
	if *apply {
		matches, err = run.CorrectCopiedUsage(ctx, s, *reason, time.Now().UTC())
	} else {
		matches, err = run.DetectCopiedUsage(ctx, s)
	}
	if err != nil {
		return fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(matches); err != nil {
		return fail(run.Internalf("printing copied usage: %v", err))
	}
	return record.ExitOK
}
