package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// settleOwnerAnswers distinguishes publication from a ruling. An absent
// question needs positive evidence of compaction in the index's ledger.
func (l *Loop) settleOwnerAnswers(ctx context.Context, sha string) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var conflicts map[string]item.Conflict
	err = s.ReadTx(ctx, func(r *store.Reader) error { var e error; conflicts, e = item.ReadConflicts(r); return e })
	s.Close()
	if err != nil {
		return err
	}
	if l.ownerAnswerBlobs == nil {
		l.ownerAnswerBlobs = make(map[string]string)
	}
	var committed *VantageQuestions
	ids := make([]string, 0, len(conflicts))
	for id := range conflicts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := conflicts[id]
		if c.QuestionRef == "" || c.Status != "question" {
			continue
		}
		file, id, _ := strings.Cut(c.QuestionRef, "#")
		blob, e := l.revParse(ctx, l.cfg.Root, sha+":"+file)
		if e != nil || blob == "" || l.ownerAnswerBlobs[c.ConflictID] == blob {
			continue
		}
		if committed == nil {
			tmp, e := os.MkdirTemp("", "tillr-owner-index-*")
			if e != nil {
				return e
			}
			defer os.RemoveAll(tmp)
			root := filepath.Join(tmp, "tree")
			if e := os.Mkdir(root, 0700); e != nil {
				return e
			}
			archive := filepath.Join(tmp, "tree.tar")
			if _, e := l.git(ctx, l.cfg.Root, "archive", "--format=tar", "--output="+archive, sha); e != nil {
				return e
			}
			if out, e := exec.CommandContext(ctx, "tar", "-xf", archive, "-C", root).CombinedOutput(); e != nil {
				return fmt.Errorf("extract trunk archive: %w: %s", e, out)
			}
			committed = &VantageQuestions{Root: root, Command: l.cfg.QuestionsCmd, Context: ctx}
		}
		doc, e := committed.Document(file)
		if e != nil || !doc.Present {
			continue
		} // Planner reports unknown or missing; never answer on either.
		found, answered := false, false
		for _, q := range doc.Questions {
			if q.ID == id {
				found = true
				answered = q.State == QuestionAnswered
				break
			}
		}
		if !found {
			answered = slices.Contains(doc.LedgerIDs, id)
		}
		if !answered {
			l.ownerAnswerBlobs[c.ConflictID] = blob
			continue
		}
		// A moved trunk is refreshed on the next pass rather than joining its answer
		// to the revision whose index was just read.
		tip, e := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if e != nil {
			return e
		}
		if tip != sha {
			return nil
		}
		writer, e := store.Open(ctx, l.cfg.Store, true)
		if e != nil {
			return e
		}
		e = writer.WriteTx(ctx, func(tx *store.Tx) error {
			current, e := item.ReadConflicts(tx.Reader())
			if e != nil {
				return e
			}
			latest := current[c.ItemID]
			if latest.ConflictID != c.ConflictID || latest.Status != c.Status || latest.QuestionRef != c.QuestionRef || latest.AnswerHead != c.AnswerHead {
				return nil
			}
			var state string
			if e := tx.Reader().QueryRow(`SELECT state FROM items WHERE item_id=?`, c.ItemID).Scan(&state); e != nil {
				return e
			}
			if state != StateInQueue && state != StateBlocked {
				return nil
			} // An explicit disposition is not undone.
			latest.Status, latest.WaitingOn, latest.AnswerHead = "answered", "build", sha
			return item.RecordConflict(tx, latest, l.cfg.Now().UTC())
		})
		writer.Close()
		if e != nil {
			return e
		}
		l.ownerAnswerBlobs[c.ConflictID] = blob
		l.reportf("loop: %s owner answered %s at %s; conflict %s is answered\n", c.ItemID, c.QuestionRef, sha, c.ConflictID)
	}
	return nil
}
