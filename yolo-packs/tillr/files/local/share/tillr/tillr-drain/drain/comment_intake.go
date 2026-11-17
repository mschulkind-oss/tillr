package drain

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) commentServer() (string, string, error) {
	base, repo := l.cfg.VantageURL, l.cfg.VantageRepo
	if c := l.cfg.Comments; c != nil {
		base, repo = c.VantageURL, c.Repo
	}
	if repo == "" {
		root, err := filepath.Abs(l.cfg.Root)
		if err != nil {
			return "", "", err
		}
		repo = filepath.Base(root)
	}
	return base, repo, nil
}

func (l *Loop) intakeComments(ctx context.Context) error {
	c := l.cfg.Comments
	if c == nil {
		return nil
	}
	now := l.cfg.Now().UTC()
	if now.Before(l.nextCommentPoll) {
		return nil
	}
	l.nextCommentPoll = now.Add(time.Duration(c.PollSeconds) * time.Second)
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var first time.Time
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec("INSERT INTO comment_intake(id,first_intake) VALUES(1,?) ON CONFLICT(id) DO NOTHING", now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		var stamp string
		if err := tx.QueryRow("SELECT first_intake FROM comment_intake WHERE id=1").Scan(&stamp); err != nil {
			return err
		}
		first, err = time.Parse(time.RFC3339Nano, stamp)
		return err
	})
	if err != nil {
		return err
	}
	// Enumerate only configured local documents. WalkDir never follows symlink
	// directories; resolved paths must also remain inside the repository.
	docs := map[string]bool{}
	for _, prefix := range c.Paths {
		start := filepath.Join(l.cfg.Root, filepath.FromSlash(prefix))
		err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
			if os.IsNotExist(walkErr) {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".md" || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			real, e := filepath.EvalSymlinks(path)
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(l.cfg.Root, real)
			if e != nil {
				return e
			}
			if !filepath.IsLocal(rel) {
				return fmt.Errorf("comments.paths escapes the repository: %s", prefix)
			}
			rel, e = filepath.Rel(l.cfg.Root, path)
			if e != nil {
				return e
			}
			docs[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			return err
		}
	}
	ordered := []string{}
	for doc := range docs {
		ordered = append(ordered, doc)
	}
	sort.Strings(ordered)
	reviews := map[string][]item.ReviewComment{}
	base, repo, err := l.commentServer()
	if err != nil {
		return err
	}
	// Fetch the entire intake before writing any batch: an outage cannot produce
	// a partial sitting or lose a comment's history.
	for _, doc := range ordered {
		review, e := item.ReadReview(ctx, base, repo, doc)
		if e != nil {
			return s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Exec("UPDATE comment_intake SET unavailable=1 WHERE id=1")
				return err
			})
		}
		reviews[doc] = review
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec("UPDATE comment_intake SET unavailable=0,last_answer=? WHERE id=1", now.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM comment_intake_documents"); err != nil {
			return err
		}
		for _, doc := range ordered {
			pending, _ := item.QuietComments(reviews[doc], first, now, time.Duration(c.QuietSeconds)*time.Second, false)
			older := 0
			for _, c := range reviews[doc] {
				if !item.PendingReview(c) || item.ReviewChanged(c) >= float64(first.UnixNano())/1e9 {
					continue
				}
				var taken bool
				if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM comment_taken WHERE document=? AND comment_id=? AND turns=? AND changed=?)", doc, c.ID, len(c.Reactions), item.ReviewChanged(c)).Scan(&taken); err != nil {
					return err
				}
				if !taken {
					older++
				}
			}
			if _, err := tx.Exec("INSERT INTO comment_intake_documents(document,older) VALUES(?,?)", doc, older); err != nil {
				return err
			}
			if len(pending) != 0 {
				if _, err := item.TakeComments(ctx, tx, l.cfg.Root, doc, pending, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// checkCommentDispatch reads Vantage immediately before the item is claimed. It
// returns false on an outage; it closes only when every captured id is gone or resolved.
func (l *Loop) checkCommentDispatch(ctx context.Context, p plannedApplication, at time.Time) (bool, bool, error) {
	base, repo, err := l.commentServer()
	if err != nil {
		return false, false, err
	}
	if base == "" {
		return true, false, nil
	} // explicit hand intake without automatic settings
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, false, err
	}
	defer s.Close()
	var batch *item.CommentBatch
	var state string
	if err = s.ReadTx(ctx, func(r *store.Reader) error {
		if err = r.QueryRow("SELECT state FROM items WHERE item_id=?", p.decision.ItemID).Scan(&state); err != nil {
			return err
		}
		if state != "in-queue" && state != "blocked" {
			return nil
		}
		batch, err = item.ReadCommentBatch(r, p.decision.ItemID)
		return err
	}); err != nil {
		return false, false, err
	}
	if state != "in-queue" && state != "blocked" {
		return true, false, nil
	}
	if batch == nil {
		return false, false, fmt.Errorf("comment item %s has no batch", p.decision.ItemID)
	}
	review, err := item.ReadReview(ctx, base, repo, batch.Document)
	if err != nil {
		return false, false, nil
	}
	current := map[string]item.ReviewComment{}
	for _, c := range review {
		current[c.ID] = c
	}
	for _, c := range batch.Comments {
		if live, ok := current[c.ID]; ok && !live.Resolved {
			return true, false, nil
		}
	}
	reason := "review comments all resolved or deleted before dispatch"
	closed := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		version, e := inputVersion(tx.Reader(), p.decision)
		if e != nil {
			return e
		}
		if version != p.version {
			return nil
		}
		_, e = item.Close(ctx, tx, p.decision.ItemID, reason, "drain", at)
		closed = e == nil
		return e
	})
	return false, closed, err
}
