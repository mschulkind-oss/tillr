package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// designBlockAnswer reads a design run's candidate against the item block its conflict
// names: whether the head still holds the block, and whether the run changed it.
//
// The block is found by its id, not only by the heading anchor its body_ref names. On
// 2026-11-17 two committed design answers went to the owner without a gate: one rewrite
// run retitled its section, and the settlement looked for a block with the rewrite item's
// own id, which no document holds (a rewrite item's block is its original item's); one ran
// on a base older than the trunk commit that renamed its heading, so the body_ref anchor
// resolved in neither the base nor the candidate and both read "body anchor missing".
func (l *Loop) designBlockAnswer(ctx context.Context, candidate Candidate, ref string) (exists, changed bool, err error) {
	blockID := candidate.ItemID
	if original := item.ProseRewriteOriginal(candidate.ItemID); original != "" {
		blockID = original
	}
	file, _, _ := strings.Cut(ref, "#")
	head, err := l.fileAt(ctx, candidate.Worktree, candidate.HeadSHA, file)
	if err != nil {
		return false, false, err
	}
	if !item.DefinitionBlockExists(file, blockID, []byte(head)) {
		return false, false, nil
	}
	byID := candidate
	byID.ItemID = blockID
	if changed, err = l.definitionChanged(ctx, byID, ref); err != nil || changed {
		return true, changed, err
	}
	base, err := l.fileAt(ctx, candidate.Worktree, candidate.BaseSHA, file)
	if err != nil {
		return false, false, err
	}
	if !strings.HasPrefix(item.DefinitionBodyContent(ref, blockID, []byte(base)), "body anchor missing") {
		return true, false, nil
	}
	before, okBefore := blockSection(base, blockID)
	after, okAfter := blockSection(head, blockID)
	// Triage creates the block: an absent base block and a present head block
	// is an amendment too, even when the stored request's anchor never existed.
	return true, okAfter && (!okBefore || before != after), nil
}

// fileAt answers one file's content at a commit, or "" when the commit does not hold it.
func (l *Loop) fileAt(ctx context.Context, dir, sha, file string) (string, error) {
	files, err := l.git(ctx, dir, "ls-tree", "--name-only", sha, "--", file)
	if err != nil || strings.TrimSpace(files) == "" {
		return "", err
	}
	return l.git(ctx, dir, "show", sha+":"+file)
}

var sectionHeading = regexp.MustCompile(`^(#{1,6})\s`)

// blockSection answers the section that holds the item block with id: from the nearest
// heading above its directive to the next heading of the same or a higher level, outside
// code fences.
func blockSection(raw, id string) (string, bool) {
	lines := strings.Split(raw, "\n")
	marker := `<!-- tillr: item id="` + id + `"`
	levels := make([]int, len(lines))
	at, inFence := -1, false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := sectionHeading.FindStringSubmatch(line); m != nil {
			levels[i] = len(m[1])
		}
		if at < 0 && strings.HasPrefix(trimmed, marker) {
			at = i
		}
	}
	if at < 0 {
		return "", false
	}
	start := -1
	for i := at; i >= 0; i-- {
		if levels[i] > 0 {
			start = i
			break
		}
	}
	if start < 0 {
		return "", false
	}
	end := len(lines)
	for i := at + 1; i < len(lines); i++ {
		if levels[i] > 0 && levels[i] <= levels[start] {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n"), true
}

type misreadAnswer struct {
	conflict item.Conflict
	head     string
}

// settleMisreadDesignAnswers re-reads the owner holds the settlement wrote before the
// owner's ruling of 2026-11-17, when a design commit that did not amend the item's block,
// or that the settlement could not find the block in, went to the owner without a gate. A
// conflict with the owner whose last line says its design run did not amend, or removed or
// renamed, the item block is raised again for the design seat, naming the commit that
// holds the earlier answer when the run left one. The candidate is not gated as it stands:
// it was cut from a base the trunk has since moved past in the same document, so the
// design seat carries it onto the current trunk. Each run is re-read once, recorded by a
// drain.design-unanswered record with misread set.
func (l *Loop) settleMisreadDesignAnswers(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	found := []misreadAnswer{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		marks, err := readDesignUnanswered(r)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(conflicts))
		for id := range conflicts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			c := conflicts[id]
			if c.Status != "owner" || c.DesignRunID == "" || c.TriageVersion != "" {
				continue
			}
			lines := strings.Split(strings.TrimRight(c.Text, "\n"), "\n")
			last := lines[len(lines)-1]
			prefix := "Design run " + c.DesignRunID + " "
			if !strings.HasPrefix(last, prefix+"did not amend the blocked item block ") && !strings.HasPrefix(last, prefix+"removed or renamed its item block ") {
				continue
			}
			seen := false
			for _, m := range marks[c.ConflictID] {
				seen = seen || m.RunID == c.DesignRunID
			}
			if seen {
				continue
			}
			var raw []byte
			err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq DESC LIMIT 1`, candidateReadyKind, c.DesignRunID).Scan(&raw)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			head := ""
			var cand Candidate
			if err == nil && json.Unmarshal(raw, &cand) == nil && cand.HeadSHA != "" && cand.HeadSHA != cand.BaseSHA {
				head = cand.HeadSHA
			}
			found = append(found, misreadAnswer{conflict: c, head: head})
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return err
	}
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		current, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return err
		}
		for _, f := range found {
			c := current[f.conflict.ItemID]
			if c.ConflictID != f.conflict.ConflictID || c.Status != "owner" || c.DesignRunID != f.conflict.DesignRunID {
				continue
			}
			if err := refuseTerminalItem(tx.Reader(), c.ItemID, "design-misread"); err != nil {
				continue
			}
			runID := c.DesignRunID
			if f.head != "" {
				c.Text += "\n\nDesign run " + runID + " committed an answer: commit " + shortSHA(f.head) + " holds it. The loop read it as no answer before 2026-11-17, so it never reached the gate. The conflict is raised again for the design seat, to carry that answer onto the current trunk."
			} else {
				c.Text += "\n\nDesign run " + runID + " was read as no answer before 2026-11-17 and sent to the owner without an objection. The conflict is raised again for the design seat."
			}
			c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
			if err := item.RecordConflict(tx, c, at); err != nil {
				return err
			}
			mark := designUnanswered{ConflictID: c.ConflictID, ItemID: c.ItemID, RunID: runID, Outcome: "answer misread", Changes: []string{}, Misread: true}
			if err := appendEffect(tx, designUnansweredKind, runID, mark, at); err != nil {
				return err
			}
			l.reportf("loop: %s conflict %s is raised again: design run %s was read as no answer before 2026-11-17\n", c.ItemID, c.ConflictID, runID)
		}
		return nil
	})
}
