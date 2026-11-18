package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
)

func probeBackendFace(args []string) int {
	fs := newFlagSet("probe-backend")
	path := fs.String("store", "", "store holding backend run history (required)")
	backend := fs.String("backend", "", "backend exercised by the smoke-check command (required)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(fs)
			return record.ExitOK
		}
		return fail(drain.Usagef("%v", err))
	}
	if *path == "" || *backend == "" || fs.NArg() == 0 {
		return fail(drain.Usagef("probe-backend needs --store <path>, --backend <name> -- <smoke-check command>"))
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	root, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := drain.ProbeBackend(ctx, *path, root, *backend, fs.Args()); err != nil {
		return fail(err)
	}
	fmt.Printf("backend %s: successful probe recorded\n", *backend)
	return record.ExitOK
}
