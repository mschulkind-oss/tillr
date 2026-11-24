package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/record"
)

func runNotify(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "tillr notify: a face is required; name one of: serve")
		return record.ExitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, "Usage: tillr notify <face> [flags]\n\nFaces:\n  serve   run the local Unix-socket wake-hint relay")
		return record.ExitOK
	case "serve":
		return runNotifyServe(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "tillr notify: %q is not a face; name one of: serve\n", args[0])
		return record.ExitUsage
	}
}

func runNotifyServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tillr notify serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	storePath := fs.String("store", "", "path to the tillr state database (required)")
	idleExit := fs.Duration("idle-exit", notify.DefaultIdleExit, "exit when zero connections remain for this duration")

	if parseErr := fs.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: tillr notify serve --store <path> [--idle-exit <duration>]")
			fs.SetOutput(stdout)
			fs.PrintDefaults()
			return record.ExitOK
		}
		fmt.Fprintf(stderr, "tillr notify serve: %v\n", parseErr)
		return record.ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tillr notify serve: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return record.ExitUsage
	}
	if strings.TrimSpace(*storePath) == "" {
		fmt.Fprintln(stderr, "tillr notify serve: --store is required")
		return record.ExitUsage
	}
	if *idleExit <= 0 {
		fmt.Fprintln(stderr, "tillr notify serve: --idle-exit must be positive")
		return record.ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := notify.Serve(ctx, notify.ServeConfig{
		StorePath: *storePath,
		IdleExit:  *idleExit,
	}); err != nil {
		fmt.Fprintf(stderr, "tillr notify serve: %v\n", err)
		if errors.Is(err, notify.ErrAlreadyRunning) {
			return record.ExitNegative
		}
		return record.ExitExternal
	}
	return record.ExitOK
}
