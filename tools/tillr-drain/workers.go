package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func workersFace(args []string) int {
	fs := newFlagSet("workers")
	root := fs.String("root", ".", "the repository whose workers to list")
	path := fs.String("store", "", "the store to read (default: <root>/.tillr/state.db)")
	asJSON := fs.Bool("json", false, "print the workers as JSON")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if fs.NArg() != 0 {
		return fail(drain.Usagef("workers takes no positional arguments"))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	loopDir := filepath.Join(filepath.Dir(*path), "loop")
	var workers []drain.Worker
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var rerr error
		workers, rerr = drain.Workers(r, *path, loopDir)
		return rerr
	}); err != nil {
		return fail(err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(workers); err != nil {
			return fail(drain.Externalf("writing workers: %v", err))
		}
		return record.ExitOK
	}
	for _, w := range workers {
		pid := "-"
		if w.PID > 0 {
			pid = strconv.Itoa(w.PID)
		}
		status := "dead"
		if w.Alive {
			status = "alive"
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\n", w.Lane, w.Item, w.Run, pid, status); err != nil {
			return fail(drain.Externalf("writing workers: %v", err))
		}
	}
	return record.ExitOK
}
