package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/worktreebudget"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	worktreeCollectedKind       = "drain.worktree-collected"
	worktreeBudgetOvershootKind = "drain.worktree-budget-overshoot"
)

func init() {
	for _, kind := range []string{worktreeCollectedKind, worktreeBudgetOvershootKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

// WorktreeCollected is drain.worktree-collected's payload.
type WorktreeCollected struct {
	ItemID     string   `json:"item_id"`
	LaneID     string   `json:"lane_id"`
	Reason     string   `json:"reason"`
	Deleted    []string `json:"deleted"`
	FreedBytes int64    `json:"freed_bytes"`
}

// WorktreeBudgetOvershoot is drain.worktree-budget-overshoot's payload.
type WorktreeBudgetOvershoot struct {
	TotalBytes      int64                     `json:"total_bytes"`
	DiskBudgetBytes int64                     `json:"disk_budget_bytes"`
	LargestScratch  []worktreebudget.DirBytes `json:"largest_scratch,omitempty"`
}

func (l *Loop) effectiveWorktreeBounds() (countLimit int, diskBudgetBytes int64) {
	w := l.postLandingSettings().Worktrees
	agentLimit := ordinaryLaneCount(l.cfg.Lanes)
	countLimit = w.EffectiveMaxCount(agentLimit)
	diskBudgetBytes = w.EffectiveDiskBudgetBytes(agentLimit)
	if l.cfg.WorktreeCountOverride > 0 {
		countLimit = l.cfg.WorktreeCountOverride
	}
	if l.cfg.WorktreeDiskBudgetOverride > 0 {
		diskBudgetBytes = l.cfg.WorktreeDiskBudgetOverride
	}
	return countLimit, diskBudgetBytes
}

type idleWorktreeCandidate struct {
	laneID        string
	itemID        string
	itemState     string
	latestRunID   string
	waitReason    string // "wait-owner", "wait-item", or ""
	lastActiveAt  int64
	modTime       time.Time
	hasWorktree   bool
	bytes         int64
	scratchSubdir bool
}

// collectIdleWorktrees enforces the worktree count and disk bounds from D-VO10
// without a timer: it collects idle item directories waiting on the owner or another
// item immediately, then collects remaining idle item directories LRU-first while
// OnDisk > countLimit or TotalBytes > diskBudgetBytes. Running or interrupted nodes
// are never stopped or collected; any remaining overshoot is reported with the largest
// scratch directories named.
func (l *Loop) collectIdleWorktrees(ctx context.Context) (worktreebudget.Snapshot, error) {
	countLimit, diskBudgetBytes := l.effectiveWorktreeBounds()
	if l.nonLanding || l.cfg.LaneRoot == "" {
		return worktreebudget.Snapshot{
			CountLimit:      countLimit,
			DiskBudgetBytes: diskBudgetBytes,
			Items:           map[string]worktreebudget.ItemUsage{},
		}, nil
	}

	l.endedLaneMu.Lock()
	defer l.endedLaneMu.Unlock()

	inUse := map[string]bool{}
	activeRunIDs := map[string]bool{}
	l.mu.Lock()
	for lane, n := range l.stagingLanes {
		if n > 0 {
			inUse[lane] = true
		}
	}
	for id := range l.active {
		activeRunIDs[id] = true
	}
	for id := range l.examiningRuns {
		activeRunIDs[id] = true
	}
	l.mu.Unlock()

	if entries, err := os.ReadDir(l.cfg.LaneRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				if l.hasLaneCondition(entry.Name(), ReasonLaneChildren) {
					inUse[entry.Name()] = true
				}
			}
		}
	}

	var idleMeta map[string]idleWorktreeCandidate
	if l.cfg.Store != "" {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return worktreebudget.Snapshot{}, err
		}
		idleMeta = map[string]idleWorktreeCandidate{}
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			leasedRunIDs := map[string]bool{}
			rows, err := queryEndedLaneRows(r, `SELECT lane_id, COALESCE(run_id, '') FROM lanes WHERE state = 'leased'`)
			if err != nil {
				return err
			}
			for rows.Next() {
				var lane, runID string
				if err := rows.Scan(&lane, &runID); err != nil {
					rows.Close()
					return err
				}
				inUse[lane] = true
				if runID != "" {
					leasedRunIDs[runID] = true
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()

			busyItems := map[string]bool{}
			works, err := readPending(r)
			if err != nil {
				return err
			}
			for _, w := range works {
				if !w.Done && !w.LaneReleased && w.Decision.LaneID != "" {
					inUse[w.Decision.LaneID] = true
				}
				if !w.Done && w.Decision.ItemID != "" {
					busyItems[w.Decision.ItemID] = true
					inUse[w.Decision.ItemID] = true
				}
			}

			roRows, err := queryEndedLaneRows(r, `SELECT item_id FROM read_only_runs WHERE settled = 0`)
			if err != nil {
				return err
			}
			for roRows.Next() {
				var id string
				if err := roRows.Scan(&id); err != nil {
					roRows.Close()
					return err
				}
				busyItems[id] = true
				inUse[id] = true
			}
			if err := roRows.Err(); err != nil {
				roRows.Close()
				return err
			}
			roRows.Close()

			qpRows, err := queryEndedLaneRows(r, `SELECT item_id FROM quota_pauses`)
			if err != nil {
				return err
			}
			for qpRows.Next() {
				var id string
				if err := qpRows.Scan(&id); err != nil {
					qpRows.Close()
					return err
				}
				busyItems[id] = true
				inUse[id] = true
			}
			if err := qpRows.Err(); err != nil {
				qpRows.Close()
				return err
			}
			qpRows.Close()

			declared, err := definition.ForReader(r)
			if err != nil {
				return err
			}

			// Scan directories under LaneRoot to check item liveness and wait metadata.
			entries, err := os.ReadDir(l.cfg.LaneRoot)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, entry := range entries {
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				laneID := entry.Name()
				if drainmodel.IsPoolLaneName(laneID) {
					continue
				}
				if l.dirHasLiveChildProcess(l.laneDirPath(laneID)) {
					inUse[laneID] = true
					continue
				}
				if inUse[laneID] {
					continue
				}
				itemID := laneID
				if !busyItems[itemID] {
					live, err := itemHasLiveOrInterruptedNode(r, itemID, leasedRunIDs, activeRunIDs)
					if err != nil {
						return err
					}
					if live {
						inUse[laneID] = true
						continue
					}
				}
				var state, waitingOn string
				err = queryEndedLaneRow(r, `SELECT state, COALESCE(waiting_on, '') FROM items WHERE item_id = ?`, itemID).Scan(&state, &waitingOn)
				if err == sql.ErrNoRows {
					continue // ended/removed item: handled by reclaimEndedItemLanes
				}
				if err != nil {
					return err
				}
				if finishedState(declared.Parsed, state) && state != StateLanded {
					continue // ended item: handled by reclaimEndedItemLanes
				}

				// Determine latest run and last activity timestamp for LRU ordering.
				var latestRunID string
				var finishedAt, startedAt int64
				runErr := queryEndedLaneRow(r,
					`SELECT run_id, COALESCE(finished_at, 0), COALESCE(started_at, 0) FROM runs WHERE item_id = ? ORDER BY event_seq DESC, rowid DESC LIMIT 1`,
					itemID,
				).Scan(&latestRunID, &finishedAt, &startedAt)
				if runErr != nil && runErr != sql.ErrNoRows {
					return runErr
				}
				lastActive := finishedAt
				if lastActive == 0 {
					lastActive = startedAt
				}

				waitReason := classifyItemWaitForCollection(r, itemID, state, waitingOn, declared.Parsed)
				idleMeta[laneID] = idleWorktreeCandidate{
					laneID:       laneID,
					itemID:       itemID,
					itemState:    state,
					latestRunID:  latestRunID,
					waitReason:   waitReason,
					lastActiveAt: lastActive,
				}
			}
			return nil
		})
		s.Close()
		if err != nil {
			return worktreebudget.Snapshot{}, err
		}
	}

	snap, err := worktreebudget.MeasureLaneRoot(l.cfg.LaneRoot, inUse)
	if err != nil {
		return worktreebudget.Snapshot{}, err
	}
	snap.CountLimit = countLimit
	snap.DiskBudgetBytes = diskBudgetBytes
	snap.RunLogBytes = worktreebudget.MeasureRunLogs(l.cfg.Root)

	if !l.cfg.ItemWorktrees || l.cfg.Store == "" || len(idleMeta) == 0 {
		return snap, nil
	}

	var waitCands []idleWorktreeCandidate
	var lruCands []idleWorktreeCandidate
	for laneID, meta := range idleMeta {
		usage, ok := snap.Items[laneID]
		if !ok || (!usage.HasWorktree && usage.ScratchBytes == 0) {
			continue
		}
		meta.hasWorktree = usage.HasWorktree
		meta.bytes = usage.TotalBytes
		meta.modTime = usage.ModTime
		if meta.waitReason != "" {
			waitCands = append(waitCands, meta)
		} else {
			lruCands = append(lruCands, meta)
		}
	}

	sort.Slice(waitCands, func(i, j int) bool {
		return waitCands[i].laneID < waitCands[j].laneID
	})
	sort.Slice(lruCands, func(i, j int) bool {
		if lruCands[i].lastActiveAt != lruCands[j].lastActiveAt {
			return lruCands[i].lastActiveAt < lruCands[j].lastActiveAt
		}
		if !lruCands[i].modTime.Equal(lruCands[j].modTime) {
			return lruCands[i].modTime.Before(lruCands[j].modTime)
		}
		return lruCands[i].laneID < lruCands[j].laneID
	})

	// Phase 2: idle items whose wait names the owner or another item — always, at once.
	for _, c := range waitCands {
		if l.collectOneIdleWorktree(ctx, c) {
			if c.hasWorktree {
				snap.OnDisk--
				snap.Idle--
			}
			snap.TotalBytes -= c.bytes
		}
	}

	// Phase 3: other idle items (including landed-unproven), LRU first, while over bound.
	overBound := func() bool {
		if countLimit > 0 && snap.OnDisk > countLimit {
			return true
		}
		if diskBudgetBytes > 0 && snap.TotalBytes > diskBudgetBytes {
			return true
		}
		return false
	}
	for _, c := range lruCands {
		if !overBound() {
			break
		}
		c.waitReason = "lru-bound"
		if l.collectOneIdleWorktree(ctx, c) {
			if c.hasWorktree {
				snap.OnDisk--
				snap.Idle--
			}
			snap.TotalBytes -= c.bytes
		}
	}

	// Re-measure after collections so returned snapshot is exact.
	snap, err = worktreebudget.MeasureLaneRoot(l.cfg.LaneRoot, inUse)
	if err != nil {
		return worktreebudget.Snapshot{}, err
	}
	snap.CountLimit = countLimit
	snap.DiskBudgetBytes = diskBudgetBytes
	snap.RunLogBytes = worktreebudget.MeasureRunLogs(l.cfg.Root)

	if diskBudgetBytes > 0 && snap.TotalBytes > diskBudgetBytes {
		l.reportOvershoot(ctx, snap)
	}
	return snap, nil
}

func classifyItemWaitForCollection(r *store.Reader, itemID, state, waitingOn string, def definition.Definition) string {
	if waitingOn != "" {
		var w struct {
			Waiter string `json:"waiter"`
		}
		if err := json.Unmarshal([]byte(waitingOn), &w); err == nil && w.Waiter != "" {
			if w.Waiter == "owner" || w.Waiter == "design" {
				return "wait-owner"
			}
			if strings.HasPrefix(w.Waiter, "item:") {
				return "wait-item"
			}
		}
	}
	if state == StateBlocked {
		return "wait-owner"
	}

	// Check open dependencies on another item.
	depRows, depErr := queryEndedLaneRows(r, `SELECT e.blocked_by, COALESCE(i.state, '') FROM item_edges e LEFT JOIN items i ON i.item_id = e.blocked_by WHERE e.item_id = ?`, itemID)
	if depErr == nil {
		hasOpenDep := false
		for depRows.Next() {
			var depID, depState string
			if err := depRows.Scan(&depID, &depState); err == nil {
				if depState == "" || !finishedState(def, depState) {
					hasOpenDep = true
				}
			}
		}
		depRows.Close()
		if hasOpenDep {
			return "wait-item"
		}
	}

	// Check latest recorded drain.decision for this item.
	var payload []byte
	evErr := queryEndedLaneRow(r,
		`SELECT payload FROM events WHERE kind = ? AND object_type = 'item' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
		DecisionKind, itemID,
	).Scan(&payload)
	if evErr == nil && len(payload) > 0 {
		var dec Decision
		if err := json.Unmarshal(payload, &dec); err == nil && dec.Action == ActionHold {
			switch dec.Reason {
			case ReasonToolingDefect, ReasonForkHeld, ReasonAwaitsRewrite:
				return "wait-item"
			}
			rule := holdrules.For(dec.Reason)
			if rule.Class == "needs you" {
				return "wait-owner"
			}
		}
	}
	return ""
}

func (l *Loop) collectOneIdleWorktree(ctx context.Context, c idleWorktreeCandidate) bool {
	itemDir := l.laneDirPath(c.laneID)
	if l.dirHasLiveChildProcess(itemDir) {
		return false
	}
	var deleted []string
	target := l.laneTarget(c.laneID)
	if _, statErr := os.Stat(target); statErr == nil {
		saved, err := l.saveEndedItemWork(ctx, c.laneID, c.latestRunID, c.itemID, c.itemState, "idle collection ("+c.waitReason+")")
		if err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, target, err)
			return false
		}
		if head, err := l.revParse(ctx, target, "HEAD"); err == nil && head != "" {
			repo := l.lanePinRepository(c.laneID)
			if err := writeItemPin(ctx, l.git, repo, c.itemID, head); err != nil {
				l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, target, fmt.Errorf("pinning item branch head: %w", err))
				return false
			}
			if saved && c.latestRunID != "" {
				if err := l.pinLane(ctx, c.laneID, c.itemID, c.latestRunID, head); err != nil {
					l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, target, fmt.Errorf("pinning lane head: %w", err))
					return false
				}
			}
		}
		if err := l.deleteEndedItemWorktree(ctx, endedLaneCandidate{
			lane:      c.laneID,
			itemID:    c.itemID,
			itemState: c.itemState,
		}); err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, target, err)
			return false
		}
		deleted = append(deleted, "worktree")
	}
	for _, sub := range []string{"scratch", "durable"} {
		subPath := filepath.Join(itemDir, sub)
		if _, err := os.Stat(subPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, subPath, err)
			return false
		}
		if err := preserveLaneScratchFiles(l.cfg.Root, subPath); err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, subPath, err)
			return false
		}
		if err := removeEndedDir(subPath); err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, subPath, err)
			return false
		}
		deleted = append(deleted, sub)
	}
	if _, err := l.deleteIdleItemLaneRow(ctx, c.laneID); err != nil {
		l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, itemDir, fmt.Errorf("deleting idle lane row: %w", err))
		return false
	}
	if len(deleted) > 0 {
		l.recordIdleWorktreeCollected(ctx, c.itemID, c.laneID, c.waitReason, deleted, c.bytes)
	}
	return len(deleted) > 0
}

func (l *Loop) recordIdleWorktreeCollected(ctx context.Context, itemID, laneID, reason string, deleted []string, freedBytes int64) {
	l.reportf("loop: collected idle worktree %s (%s: %s, freed %s)\n", laneID, reason, strings.Join(deleted, ", "), worktreebudget.FormatBytes(freedBytes))
	if l.cfg.Store == "" {
		return
	}
	raw, err := json.Marshal(WorktreeCollected{
		ItemID:     itemID,
		LaneID:     laneID,
		Reason:     reason,
		Deleted:    deleted,
		FreedBytes: freedBytes,
	})
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if l.cfg.Now != nil {
		now = l.cfg.Now().UTC()
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       worktreeCollectedKind,
			ObjectType: "item",
			ObjectID:   &itemID,
			Payload:    raw,
			At:         now,
		})
		return err
	})
}

func (l *Loop) reportOvershoot(ctx context.Context, snap worktreebudget.Snapshot) {
	var parts []string
	for _, sc := range snap.LargestScratch {
		parts = append(parts, fmt.Sprintf("%s (%s)", sc.Path, worktreebudget.FormatBytes(sc.Bytes)))
	}
	detail := "none"
	if len(parts) > 0 {
		detail = strings.Join(parts, ", ")
	}
	l.reportf("loop: worktree disk budget overshoot: %s used > %s budget; largest scratch: %s\n",
		worktreebudget.FormatBytes(snap.TotalBytes), worktreebudget.FormatBytes(snap.DiskBudgetBytes), detail)
	if l.cfg.Store == "" {
		return
	}
	raw, err := json.Marshal(WorktreeBudgetOvershoot{
		TotalBytes:      snap.TotalBytes,
		DiskBudgetBytes: snap.DiskBudgetBytes,
		LargestScratch:  snap.LargestScratch,
	})
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if l.cfg.Now != nil {
		now = l.cfg.Now().UTC()
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       worktreeBudgetOvershootKind,
			ObjectType: "loop",
			Payload:    raw,
			At:         now,
		})
		return err
	})
}
