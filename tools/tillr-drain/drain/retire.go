// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// prepareCandidate adds loop-owned maintenance after the worker's fence check. Document
// retirement and mirror regeneration share one bound commit, so the gate and the lander
// see the same tree while recovery can still check the original worker's diff.
func (l *Loop) prepareCandidate(ctx context.Context, candidate Candidate, retireBlock bool) (Candidate, error) {
	if candidate.BaseRef != "" {
		// A patch-branch candidate was already exported to the delivery repository.
		// A new commit here would be undeliverable, and its docs are not trunk docs.
		l.reportf("loop: %s patch-branch candidate: trunk maintenance does not apply\n", candidate.ItemID)
		return candidate, nil
	}
	if candidate.WorkerHeadSHA != "" {
		return candidate, nil // recoverRetirement already validated the prepared tree.
	}
	head, err := l.git(ctx, candidate.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return candidate, err
	}
	if strings.TrimSpace(head) != candidate.HeadSHA {
		return candidate, Externalf("candidate %s HEAD differs from its recorded head; refusing unbound maintenance", candidate.ItemID)
	}
	mirrored, err := l.stagePackMirror(ctx, candidate)
	if err != nil {
		if ctx.Err() != nil {
			return candidate, ctx.Err()
		}
		if err := l.holdItemFault(ctx, candidate, ReasonPackSyncFailed, err.Error()); err != nil {
			return candidate, err
		}
		return candidate, errItemFaultHeld
	}
	var retired bool
	if retireBlock {
		retired, err = l.retireDocumentBlock(ctx, candidate)
		if err != nil {
			return candidate, err
		}
	}
	if !mirrored && !retired {
		return candidate, nil
	}
	kind, message := candidatePreparedKind, "chore: regenerate tillr pack mirror"
	if retired {
		// Only a real block retirement is evidence for retiredOriginal.
		kind, message = candidateRetirementKind, "chore: retire landed document block"
	}
	tree, err := l.git(ctx, candidate.Worktree, "write-tree")
	if err != nil {
		return candidate, err
	}
	candidate.WorkerHeadSHA = candidate.HeadSHA
	candidate.RetirementTreeSHA = strings.TrimSpace(tree)
	// Persist the tree before the ref-changing commit. Recovery validates HEAD's parent
	// and tree against this intent; an interruption before the commit fails explicitly,
	// rather than silently landing the old worker tree.
	if err := appendJudgment(ctx, l.cfg.Store, kind, "run", candidate.RunID, candidate, l.cfg.Now().UTC()); err != nil {
		return candidate, err
	}
	if _, err := l.git(ctx, candidate.Worktree, "commit", "-m", message); err != nil {
		return candidate, err
	}
	candidate, err = l.recoverRetirement(ctx, candidate)
	if err == nil {
		l.reportf("loop: prepared %s (pack mirror: %t, document retirement: %t) at %s\n", candidate.ItemID, mirrored, retired, candidate.HeadSHA)
	}
	return candidate, err
}

func (l *Loop) retireDocumentBlock(ctx context.Context, candidate Candidate) (bool, error) {
	docs, err := item.ReadSyncDocuments(candidate.Worktree)
	if err != nil {
		return false, Externalf("reading candidate documents for retirement: %v", err)
	}
	type change struct {
		path, text string
		mode       os.FileMode
	}
	var changes []change
	found := false
	for _, doc := range docs {
		text, present := item.RetireItemBlock(string(doc.Content), candidate.ItemID)
		found = found || present
		if text == string(doc.Content) {
			continue
		}
		path := filepath.Join(candidate.Worktree, filepath.FromSlash(doc.Path))
		info, err := os.Lstat(path)
		if err != nil {
			return false, Externalf("reading retirement document %s: %v", doc.Path, err)
		}
		if !info.Mode().IsRegular() {
			return false, Externalf("retirement document %s is not a regular file", doc.Path)
		}
		changes = append(changes, change{path: doc.Path, text: text, mode: info.Mode().Perm()})
	}
	if !found {
		l.reportf("loop: %s document block not found; continuing candidate\n", candidate.ItemID)
	}
	if len(changes) == 0 {
		return false, nil
	}
	paths := []string{"add", "--"}
	for _, c := range changes {
		if err := os.WriteFile(filepath.Join(candidate.Worktree, filepath.FromSlash(c.path)), []byte(c.text), c.mode); err != nil {
			return false, Externalf("retiring document %s: %v", c.path, err)
		}
		paths = append(paths, c.path)
	}
	_, err = l.git(ctx, candidate.Worktree, paths...)
	return err == nil, err
}

// recoverRetirement accepts only the one maintenance commit whose parent and tree the
// durable intent binds. No missing or partial intent is read as permission to skip scope.
func (l *Loop) recoverRetirement(ctx context.Context, candidate Candidate) (Candidate, error) {
	if candidate.WorkerHeadSHA == "" && candidate.RetirementTreeSHA == "" {
		return candidate, nil
	}
	if candidate.WorkerHeadSHA == "" || candidate.RetirementTreeSHA == "" || candidate.BaseRef != "" {
		return candidate, Externalf("candidate %s has an incomplete or inapplicable retirement binding", candidate.ItemID)
	}
	head, err := l.git(ctx, candidate.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return candidate, err
	}
	head = strings.TrimSpace(head)
	if head == candidate.WorkerHeadSHA {
		return candidate, Externalf("candidate %s retirement intent has no completed commit; worker tree must not land", candidate.ItemID)
	}
	parent, err := l.git(ctx, candidate.Worktree, "rev-parse", head+"^1")
	if err != nil {
		return candidate, err
	}
	tree, err := l.git(ctx, candidate.Worktree, "rev-parse", head+"^{tree}")
	if err != nil {
		return candidate, err
	}
	parents, err := l.git(ctx, candidate.Worktree, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return candidate, err
	}
	if strings.TrimSpace(parent) != candidate.WorkerHeadSHA || strings.TrimSpace(tree) != candidate.RetirementTreeSHA || len(strings.Fields(parents)) != 2 {
		return candidate, Externalf("candidate %s retirement HEAD does not match the recorded worker parent and maintenance tree", candidate.ItemID)
	}
	candidate.HeadSHA = head
	return candidate, nil
}

// retiredOriginal answers whether the loop recorded a completed retirement binding for
// runID: its newest drain.retirement names both the worker head and the maintenance tree.
// Only that record excuses a conflict-fix run's block removal, so a run the loop never
// retired is judged exactly as before.
func (l *Loop) retiredOriginal(ctx context.Context, runID string) (bool, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var raw []byte
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, candidateRetirementKind, runID)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			if err := rows.Scan(&raw); err != nil {
				return err
			}
		}
		return rows.Err()
	})
	if err != nil || raw == nil {
		return false, err
	}
	var c Candidate
	if err := json.Unmarshal(raw, &c); err != nil {
		return false, err
	}
	return c.WorkerHeadSHA != "" && c.RetirementTreeSHA != "", nil
}
