package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
)

func splitFace(args []string) int {
	fs := newFlagSet("split")
	root := fs.String("root", ".", "the repository holding the build workflow")
	path := fs.String("store", "", "the store (default <root>/.tillr/state.db)")
	parent := fs.String("item", "", "the parent request's item ID (required); read the design seat's JSON statement from stdin")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *parent == "" {
		return fail(drain.Usagef("split needs --item <parent-request> and a JSON statement on stdin"))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	parts, err := drain.FileSplit(context.Background(), *root, *path, *parent, os.Stdin, time.Now().UTC())
	if err != nil {
		if e, ok := item.RejectionOf(err); ok {
			fmt.Fprintln(os.Stderr, e.Msg)
			return e.Code
		}
		return fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(parts); err != nil {
		return fail(drain.Externalf("writing filed part IDs: %v", err))
	}
	return record.ExitOK
}
