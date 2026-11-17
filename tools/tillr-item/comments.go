package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func commentsFace(args []string) int {
	if len(args) == 0 || args[0] != "take" {
		return fail(item.Usagef("comments needs take: run tillr item comments take --help"))
	}
	fs := newFlagSet("comments take")
	path := fs.String("store", "", "the store to write")
	root := fs.String("root", ".", "the repository root")
	repo := fs.String("repo", "", "the Vantage repository name (default: root directory's name)")
	doc := fs.String("doc", "", "the repository-relative document")
	vantage := fs.String("vantage", "", "the Vantage HTTP server URL")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args[1:]); err != nil {
		return fail(err)
	}
	if *path == "" || *doc == "" || *vantage == "" {
		return fail(item.Usagef("comments take needs --store, --doc and --vantage"))
	}
	at := time.Now().UTC()
	if *now != "" {
		var err error
		at, err = time.Parse(time.RFC3339, *now)
		if err != nil {
			return fail(item.Usagef("--now: %v", err))
		}
	}
	if *repo == "" {
		abs, err := filepath.Abs(*root)
		if err != nil {
			return fail(err)
		}
		*repo = filepath.Base(abs)
	}
	ctx := context.Background()
	comments, err := item.ReadReview(ctx, *vantage, *repo, *doc)
	if err != nil {
		return fail(err)
	}
	pending := false
	for _, c := range comments {
		if item.PendingReview(c) {
			pending = true
		}
	}
	if !pending {
		return 0
	}
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	var id string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var e error
		id, e = item.TakeComments(ctx, tx, *root, *doc, comments, at)
		return e
	})
	if err != nil {
		return fail(err)
	}
	if id != "" {
		fmt.Println(id)
	}
	return 0
}
