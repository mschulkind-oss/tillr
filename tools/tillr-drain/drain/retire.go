package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
)

// retireDocumentBlock is loop-owned maintenance, after the worker's fence check and before
// the gate. Its separate commit may touch tracked documents outside the worker's fence;
// the worker's already-checked diff is never exempted from that fence.
func (l *Loop) retireDocumentBlock(ctx context.Context, candidate Candidate) (Candidate, error) {
	if candidate.BaseRef != "" {
		// A patch-branch candidate was already exported to the delivery repository.
		// A new commit here would be undeliverable, and its docs are not trunk docs.
		l.reportf("loop: %s patch-branch candidate: trunk document retirement does not apply\n", candidate.ItemID)
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
		return candidate, Externalf("candidate %s HEAD differs from its recorded head; refusing unbound document maintenance", candidate.ItemID)
	}
	docs, err := item.ReadSyncDocuments(candidate.Worktree)
	if err != nil {
		return candidate, Externalf("reading candidate documents for retirement: %v", err)
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
			return candidate, Externalf("reading retirement document %s: %v", doc.Path, err)
		}
		if !info.Mode().IsRegular() {
			return candidate, Externalf("retirement document %s is not a regular file", doc.Path)
		}
		changes = append(changes, change{path: doc.Path, text: text, mode: info.Mode().Perm()})
	}
	if !found {
		l.reportf("loop: %s document block not found; continuing candidate\n", candidate.ItemID)
	}
	if len(changes) == 0 {
		return candidate, nil
	}
	paths := []string{"add", "--"}
	for _, c := range changes {
		if err := os.WriteFile(filepath.Join(candidate.Worktree, filepath.FromSlash(c.path)), []byte(c.text), c.mode); err != nil {
			return candidate, Externalf("retiring document %s: %v", c.path, err)
		}
		paths = append(paths, c.path)
	}
	if _, err := l.git(ctx, candidate.Worktree, paths...); err != nil {
		return candidate, err
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
	if err := appendJudgment(ctx, l.cfg.Store, candidateRetirementKind, "run", candidate.RunID, candidate, l.cfg.Now().UTC()); err != nil {
		return candidate, err
	}
	if _, err := l.git(ctx, candidate.Worktree, "commit", "-m", "chore: retire landed document block"); err != nil {
		return candidate, err
	}
	candidate, err = l.recoverRetirement(ctx, candidate)
	if err != nil {
		return candidate, err
	}
	l.reportf("loop: retired %s document block and dependencies in %d document(s) (%s)\n", candidate.ItemID, len(changes), candidate.HeadSHA)
	return candidate, nil
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
