package drain

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// preservedLaneDirtyCovered permits discarding only dirty bytes already retained
// for this checkout's original run. Incoming item ownership is irrelevant: the
// committed head is admitted separately by refuseUnlandedCommit.
func (s *LaneStager) preservedLaneDirtyCovered(ctx context.Context, lane, worktree string) (bool, error) {
	if s.Store == "" {
		return false, nil
	}
	db, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return false, err
	}
	var saved []PreservedWork
	err = db.ReadTx(ctx, func(r *store.Reader) error { var err error; saved, err = ReadPreservedWork(r); return err })
	db.Close()
	if err != nil {
		return false, err
	}
	head, err := s.git(ctx, worktree, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	l := &Loop{cfg: LoopConfig{Root: s.Root, LaneRoot: s.LaneRoot, Store: s.Store}}
	if filepath.Clean(l.laneTarget(lane)) != filepath.Clean(worktree) {
		return false, nil
	}
	// clearWorktree removes ignored files too, but preservation never snapshots
	// them. Their presence cannot borrow a proof about the visible dirty paths.
	ignored, err := s.git(ctx, worktree, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return false, err
	}
	if ignored != "" {
		return false, nil
	}
	for _, p := range saved {
		if p.Pruned || p.LaneID != lane || p.RunID == "" || p.Ref != preservedPrefix+p.RunID || (filepath.Clean(p.Repository) != filepath.Clean(s.Root) && filepath.Clean(p.Repository) != filepath.Clean(worktree)) {
			continue
		}
		parent, err := s.git(ctx, worktree, "rev-parse", "--verify", p.Ref+"^1")
		if err != nil || strings.TrimSpace(parent) != strings.TrimSpace(head) {
			continue
		}
		snapshot, err := l.snapshotWork(ctx, lane, p.RunID)
		if err != nil {
			return false, err
		}
		if snapshot.indexNote != "" || len(snapshot.unmerged) != 0 || !preservedPathsCover(p.Files, snapshot.files) {
			continue
		}
		indexRef, indexFiles := p.IndexRef, p.IndexFiles
		if indexRef == "" {
			indexRef, indexFiles = p.Ref, p.Files
		}
		if indexRef != p.Ref && indexRef != p.Ref+"^2" {
			continue
		}
		if !preservedPathsCover(indexFiles, snapshot.indexFiles) {
			continue
		}
		if err := l.snapshotCovered(ctx, worktree, p.Ref, snapshot.tree, snapshot.files); err != nil {
			continue
		}
		if err := l.snapshotCovered(ctx, worktree, indexRef, snapshot.indexTree, snapshot.indexFiles); err != nil {
			continue
		}
		return true, nil
	}
	return false, nil
}

func preservedPathsCover(saved, current []PreservedFile) bool {
	paths := map[string]bool{}
	for _, file := range saved {
		paths[file.Path] = true
	}
	for _, file := range current {
		if !paths[file.Path] {
			return false
		}
	}
	return true
}
