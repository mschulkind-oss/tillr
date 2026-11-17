package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/settings"
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
	older := fs.Bool("older", false, "include pending comments predating the first intake")
	if err := parseFlags(fs, args[1:]); err != nil {
		return fail(err)
	}
	config, err := settings.Read(*root)
	if err != nil {
		return fail(item.Usagef("%v", err))
	}
	if c := config.Comments; c != nil {
		if *vantage == "" {
			*vantage = c.VantageURL
		}
		if *repo == "" {
			*repo = c.Repo
		}
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if *doc == "" || *vantage == "" {
		return fail(item.Usagef("comments take needs --doc and --vantage (or comments.vantage_url in .tillr.toml)"))
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
		selected := comments
		if c := config.Comments; c != nil {
			if _, e = tx.Exec("INSERT INTO comment_intake(id,first_intake) VALUES(1,?) ON CONFLICT(id) DO NOTHING", at.Format(time.RFC3339Nano)); e != nil {
				return e
			}
			var stamp string
			if e = tx.QueryRow("SELECT first_intake FROM comment_intake WHERE id=1").Scan(&stamp); e != nil {
				return e
			}
			first, e := time.Parse(time.RFC3339Nano, stamp)
			if e != nil {
				return e
			}
			selected, _ = item.QuietComments(comments, first, at, time.Duration(c.QuietSeconds)*time.Second, *older)
		}
		id, e = item.TakeComments(ctx, tx, *root, *doc, selected, at)
		if e == nil && id != "" && *older {
			_, e = tx.Exec("UPDATE comment_intake_documents SET older=0 WHERE document=?", *doc)
		}
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
