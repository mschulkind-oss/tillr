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
	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr-drain/internal/worktreebudget"
	"github.com/mschulkind-oss/tillr-run/run"
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
	agentLimit := ordinaryLaneCount(l.lanes())
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
	poolSub       string // "scratch" or "durable" for per-run scratch under a pool lane
	waitReason    string // "wait-owner", "wait-item", "orphaned-pool-scratch", "ended-item", or ""
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

	endWait := startPassStep(ctx, "collect_lock_wait")
	l.endedLaneMu.Lock()
	endWait()
	defer l.endedLaneMu.Unlock()
	defer startPassStep(ctx, "collect_walk")()

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
	for id := range l.gateLand.Gate.Runs {
		activeRunIDs[id] = true
	}
	for id := range l.gateLand.Land.Runs {
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
	var poolScratchMeta []idleWorktreeCandidate
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
					if inUse[laneID] {
						continue
					}
					for _, ps := range l.scanPoolScratchEntries(laneID) {
						if leasedRunIDs[ps.runID] || activeRunIDs[ps.runID] {
							continue
						}
						runScratchPath := filepath.Join(l.laneDirPath(ps.lane), ps.sub, ps.runID)
						if l.dirHasLiveChildProcess(runScratchPath) {
							continue
						}
						var itemID, runState string
						var finishedAt, startedAt int64
						runErr := queryEndedLaneRow(r,
							`SELECT COALESCE(item_id, ''), state, COALESCE(finished_at, 0), COALESCE(started_at, 0) FROM runs WHERE run_id = ?`,
							ps.runID,
						).Scan(&itemID, &runState, &finishedAt, &startedAt)
						if runErr == sql.ErrNoRows {
							// Per docs/design/tillr-vocabulary.md §3.3 ("State that already exists"),
							// per-run scratch under a pool lane with no run record is collectable.
							poolScratchMeta = append(poolScratchMeta, idleWorktreeCandidate{
								laneID:      ps.lane,
								latestRunID: ps.runID,
								poolSub:     ps.sub,
								waitReason:  "orphaned-pool-scratch",
							})
							continue
						}
						if runErr != nil {
							return runErr
						}
						if runState != run.StateExited && runState != run.StateFailedToStart && runState != run.StateTimedOut {
							continue
						}
						lastActive := finishedAt
						if lastActive == 0 {
							lastActive = startedAt
						}
						if itemID == "" {
							poolScratchMeta = append(poolScratchMeta, idleWorktreeCandidate{
								laneID:       ps.lane,
								latestRunID:  ps.runID,
								poolSub:      ps.sub,
								waitReason:   "orphaned-pool-scratch",
								lastActiveAt: lastActive,
							})
							continue
						}
						var state, waitingOn string
						itemErr := queryEndedLaneRow(r, `SELECT state, COALESCE(waiting_on, '') FROM items WHERE item_id = ?`, itemID).Scan(&state, &waitingOn)
						if itemErr == sql.ErrNoRows {
							poolScratchMeta = append(poolScratchMeta, idleWorktreeCandidate{
								laneID:       ps.lane,
								itemID:       itemID,
								itemState:    "removed",
								latestRunID:  ps.runID,
								poolSub:      ps.sub,
								waitReason:   "ended-item",
								lastActiveAt: lastActive,
							})
							continue
						}
						if itemErr != nil {
							return itemErr
						}
						waitReason := classifyItemWaitForCollection(r, itemID, state, waitingOn, declared.Parsed)
						if waitReason == "" && finishedState(declared.Parsed, state) && state != StateLanded {
							waitReason = "ended-item"
						}
						poolScratchMeta = append(poolScratchMeta, idleWorktreeCandidate{
							laneID:       ps.lane,
							itemID:       itemID,
							itemState:    state,
							latestRunID:  ps.runID,
							poolSub:      ps.sub,
							waitReason:   waitReason,
							lastActiveAt: lastActive,
						})
					}
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

	if !l.cfg.ItemWorktrees || l.cfg.Store == "" {
		return snap, nil
	}

	if len(idleMeta) > 0 || len(poolScratchMeta) > 0 {
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
		for _, meta := range poolScratchMeta {
			runScratchPath := filepath.Join(l.laneDirPath(meta.laneID), meta.poolSub, meta.latestRunID)
			fi, err := os.Stat(runScratchPath)
			if err != nil {
				continue
			}
			meta.modTime = fi.ModTime()
			if usage, ok := snap.Items[meta.laneID]; ok {
				for _, sd := range usage.ScratchDirs {
					if sd.Path == runScratchPath {
						meta.bytes = sd.Bytes
						break
					}
				}
			}
			if meta.waitReason != "" {
				waitCands = append(waitCands, meta)
			} else {
				lruCands = append(lruCands, meta)
			}
		}

		sort.Slice(waitCands, func(i, j int) bool {
			if waitCands[i].laneID != waitCands[j].laneID {
				return waitCands[i].laneID < waitCands[j].laneID
			}
			if waitCands[i].poolSub != waitCands[j].poolSub {
				return waitCands[i].poolSub < waitCands[j].poolSub
			}
			return waitCands[i].latestRunID < waitCands[j].latestRunID
		})
		sort.Slice(lruCands, func(i, j int) bool {
			if lruCands[i].lastActiveAt != lruCands[j].lastActiveAt {
				return lruCands[i].lastActiveAt < lruCands[j].lastActiveAt
			}
			if !lruCands[i].modTime.Equal(lruCands[j].modTime) {
				return lruCands[i].modTime.Before(lruCands[j].modTime)
			}
			if lruCands[i].laneID != lruCands[j].laneID {
				return lruCands[i].laneID < lruCands[j].laneID
			}
			if lruCands[i].poolSub != lruCands[j].poolSub {
				return lruCands[i].poolSub < lruCands[j].poolSub
			}
			return lruCands[i].latestRunID < lruCands[j].latestRunID
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

		// Phase 3: other idle items (including landed-unproven) and ended pool-lane
		// scratch, LRU first, while over bound.
		for _, c := range lruCands {
			overCount := countLimit > 0 && snap.OnDisk > countLimit
			overDisk := diskBudgetBytes > 0 && snap.TotalBytes > diskBudgetBytes
			if !overCount && !overDisk {
				break
			}
			if c.poolSub != "" && !overDisk {
				continue
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
	}

	if diskBudgetBytes > 0 && snap.TotalBytes > diskBudgetBytes {
		l.reportOvershoot(ctx, snap)
	} else {
		l.endedLanes.lastOvershoot = ""
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
	if c.poolSub != "" {
		return l.collectOnePoolRunScratch(ctx, c)
	}
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

func (l *Loop) collectOnePoolRunScratch(ctx context.Context, c idleWorktreeCandidate) bool {
	runScratchPath := filepath.Join(l.laneDirPath(c.laneID), c.poolSub, c.latestRunID)
	if _, err := os.Stat(runScratchPath); os.IsNotExist(err) {
		return false
	} else if err != nil {
		l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, runScratchPath, err)
		return false
	}
	if l.dirHasLiveChildProcess(runScratchPath) {
		return false
	}
	if err := preserveRunScratchFiles(l.cfg.Root, runScratchPath, c.latestRunID); err != nil {
		l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, runScratchPath, err)
		return false
	}
	if err := removeEndedDir(runScratchPath); err != nil {
		l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.laneID, runScratchPath, err)
		return false
	}
	l.recordIdleWorktreeCollected(ctx, c.itemID, c.laneID, c.waitReason, []string{c.poolSub + "/" + c.latestRunID}, c.bytes)
	return true
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
	objType := "item"
	objID := &itemID
	if itemID == "" {
		objType = "lane"
		objID = &laneID
	}
	_ = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       worktreeCollectedKind,
			ObjectType: objType,
			ObjectID:   objID,
			Payload:    raw,
			At:         now,
		})
		return err
	})
}

func (l *Loop) reportOvershoot(ctx context.Context, snap worktreebudget.Snapshot) {
	raw, err := json.Marshal(WorktreeBudgetOvershoot{
		TotalBytes:      snap.TotalBytes,
		DiskBudgetBytes: snap.DiskBudgetBytes,
		LargestScratch:  snap.LargestScratch,
	})
	if err != nil {
		return
	}
	key := string(raw)
	if key == l.endedLanes.lastOvershoot {
		return
	}
	var parts []string
	for _, sc := range snap.LargestScratch {
		parts = append(parts, fmt.Sprintf("%s (%s)", sc.Path, worktreebudget.FormatBytes(sc.Bytes)))
	}
	detail := "none"
	if len(parts) > 0 {
		detail = strings.Join(parts, ", ")
	}
	if l.cfg.Store == "" {
		l.endedLanes.lastOvershoot = key
		l.reportf("loop: worktree disk budget overshoot: %s used > %s budget; largest scratch: %s\n",
			worktreebudget.FormatBytes(snap.TotalBytes), worktreebudget.FormatBytes(snap.DiskBudgetBytes), detail)
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
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       worktreeBudgetOvershootKind,
			ObjectType: "loop",
			Payload:    raw,
			At:         now,
		})
		return err
	}); err != nil {
		return
	}
	l.endedLanes.lastOvershoot = key
	l.reportf("loop: worktree disk budget overshoot: %s used > %s budget; largest scratch: %s\n",
		worktreebudget.FormatBytes(snap.TotalBytes), worktreebudget.FormatBytes(snap.DiskBudgetBytes), detail)
}

// startGCActor runs the lease, ref, and worktree collection background actor
// outside the scheduling pass (D-SN3, Section 5.7), woken by run.exited /
// item.ended / agent.slot-freed / resync and reconciled every StorePollInterval
// (15s default).
func (l *Loop) startGCActor(ctx context.Context, result *LoopResult) func() {
	actorCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.actors.Ensure()
	l.mu.Lock()
	l.actors.GC.Active = true
	timer := l.actors.GC.Timer
	l.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.actors.GC.Active = false
			l.mu.Unlock()
		}()
		wait := l.cfg.StorePollInterval
		if wait <= 0 {
			wait = DefaultFallbackPollInterval
		}
		gateland.RunLoop(actorCtx, wait, l.coordinatorFallbackTimer(timer), func() <-chan struct{} {
			return l.actors.GCWakeChan()
		}, func() {
			if actorCtx.Err() != nil || l.isRestartReplaced() {
				return
			}
			if stopped, err := l.stopRequested(actorCtx); stopped || err != nil {
				return
			}
			beforeSeq, _ := l.readEventHeadSeq(actorCtx)
			if err := l.runGCStep(actorCtx, result); err != nil {
				l.reportActorError(actorCtx, "gc", err)
				return
			}
			afterSeq, _ := l.readEventHeadSeq(actorCtx)
			if afterSeq != beforeSeq {
				l.actors.GCGen.Add(1)
				l.signalPassWake()
			}
		})
	}()

	return func() {
		cancel()
		<-done
	}
}

func (l *Loop) runGCStep(ctx context.Context, result *LoopResult) error {
	return l.runGCStepGeneration(ctx, result, nil)
}

func (l *Loop) runGCStepGeneration(ctx context.Context, result *LoopResult, generation *string) error {
	return l.runGCStepMode(ctx, result, generation, true)
}

// runGCMaintenanceStep runs GC store/lane maintenance without ended-item
// collection or item-directory walks, so startup seeding and pre-dispatch
// inline housekeeping do not block dispatch behind a collection backlog.
func (l *Loop) runGCMaintenanceStep(ctx context.Context, result *LoopResult, generation *string) error {
	return l.runGCStepMode(ctx, result, generation, false)
}

func (l *Loop) runGCStepMode(ctx context.Context, result *LoopResult, generation *string, collectWorktrees bool) error {
	l.actors.GCMu.Lock()
	defer l.actors.GCMu.Unlock()
	l.actors.GC.Runs.Add(1)

	if err := l.releaseUnlaunchedClaims(ctx); err != nil {
		return err
	}
	if err := l.repairStaleRunRecords(ctx); err != nil {
		return err
	}
	reapAt := l.cfg.Now().UTC()
	var localGen string
	reapGen := &localGen
	if generation != nil {
		reapGen = generation
	}
	beforeGen := *reapGen
	if reaped, err := l.reapDeadAndExpired(ctx, reapAt, reapGen); err != nil {
		return err
	} else if reaped {
		l.signalPassWake()
	}
	if generation == nil && *reapGen != "" && *reapGen != beforeGen {
		if err := l.recordReapPassTiming(ctx, *reapGen, reapAt); err != nil {
			return err
		}
		l.countGeneration(result, *reapGen)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		return err
	}
	if l.nonLanding {
		if err := l.recheckLaneChildren(ctx); err != nil {
			return err
		}
	} else {
		if err := l.recheckLanes(ctx); err != nil {
			return err
		}
	}
	if l.cfg.Store != "" {
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		err = l.releaseCountedVerifyHolds(ctx, s)
		s.Close()
		if err != nil {
			return err
		}
	}
	if !l.nonLanding {
		if err := l.supersedeRetriedCandidates(ctx); err != nil {
			return err
		}
	}
	if err := l.retireOrderWork(ctx); err != nil {
		return err
	}
	if !l.nonLanding {
		if collectWorktrees {
			l.runWorktreeCollection(ctx)
		}
		l.reportRefSweepFailure(l.pruneRunRefs(ctx))
	}
	l.recoverStaleTriageClaims(ctx)
	l.dispatchAlertTriage(ctx)
	return nil
}

// runWorktreeCollection reclaims ended item lanes and enforces worktree bounds
// without a time limit or count cap. Callers run this off the dispatch path
// (in the background GC actor, or after dispatch in inline mode).
func (l *Loop) runWorktreeCollection(ctx context.Context) {
	if l.nonLanding {
		return
	}
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		l.reportf("loop: ended-item lane cleanup failed: %v\n", err)
	}
	if l.cfg.ItemWorktrees {
		if snap, err := l.collectIdleWorktrees(ctx); err == nil {
			l.actors.SetCachedWorktreeSnapshot(snap)
		} else {
			l.reportf("loop: idle worktree collection failed: %v\n", err)
		}
	}
}

func (l *Loop) recordReapPassTiming(ctx context.Context, generation string, at time.Time) error {
	if generation == "" || l.cfg.Store == "" {
		return nil
	}
	timing := PassTiming{
		TickID:         generation,
		AtMS:           at.UnixMilli(),
		EndMS:          l.cfg.Now().UTC().UnixMilli(),
		GateBudgetMS:   l.gateBudget.Milliseconds(),
		DecisionCounts: map[string]int{},
	}
	if err := recordPassTiming(context.WithoutCancel(ctx), l.cfg.Store, timing); err != nil {
		return fmt.Errorf("recording pass %s: %w", generation, err)
	}
	return nil
}

// reapDeadAndExpired reaps dead loop runs and expired run leases outside the
// scheduling pass, preserving and pinning orphaned work when needed.
func (l *Loop) reapDeadAndExpired(ctx context.Context, at time.Time, generation *string) (bool, error) {
	if l.cfg.Store == "" {
		return false, nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()

	nextGeneration := ""
	if generation != nil {
		nextGeneration = *generation
	}
	var reapedSingle []*pendingWork
	reapedAny := false
	// The process-table evidence is gathered with no transaction open. The run-start
	// lock spans it and the write, so no run face starts between the scan and the reap.
	if err := withRunStartLock(ctx, l.cfg.Store, func() error {
		ev, err := l.gatherReapEvidence(ctx, s)
		if err != nil {
			return err
		}
		defer func() {
			for _, line := range *ev.Reclaimed {
				l.reportf("%s", line)
			}
		}()
		pendingBusy := l.takeBusyWrites()
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			var before, after int64
			if err := tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&before); err != nil {
				return err
			}
			if err := l.reapDeadLoopRuns(ctx, tx, at, ev); err != nil {
				return err
			}
			released, err := run.ReapExpiredLeases(ctx, tx, at)
			if err != nil {
				return err
			}
			if err := tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&after); err != nil {
				return err
			}
			reapedAny = len(released) > 0 || after != before
			if reapedAny && nextGeneration == "" {
				nextGeneration, err = allocateGeneration(tx)
			}
			if err != nil {
				return err
			}
			return l.appendBusyWrites(tx, pendingBusy)
		})
		if err != nil && len(pendingBusy) > 0 {
			l.restoreBusyWrites(pendingBusy)
		}
		if err == nil && ev.ReapedSingle != nil {
			reapedSingle = *ev.ReapedSingle
		}
		return err
	}); err != nil {
		return false, err
	}
	if generation != nil {
		*generation = nextGeneration
	}
	for _, work := range reapedSingle {
		d := work.Decision
		id := d.RunRequest.RunID
		_ = pipelineCloseGraph(l.cfg.Root, id)
		if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
			return reapedAny, err
		}
		var rec *run.Record
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			var readErr error
			rec, readErr = terminalRecord(r, id)
			return readErr
		}); err != nil {
			return reapedAny, err
		}
		if rec != nil {
			if err := l.recordSettlementSpend(ctx, nextGeneration, work.LoopID, *rec); err != nil {
				return reapedAny, err
			}
		}
		if !l.nonLanding {
			base := work.BaseSHA
			if base == "" {
				base = l.laneBase(d.LaneID)
			}
			if err := l.preserveWorkFromBase(ctx, d.LaneID, id, base, "orphaned by a loop stop"); err != nil {
				l.retainUnpreserved(ctx, d.LaneID, id, l.headOf(ctx, d.LaneID), err)
				continue
			}
			if err := l.pinLane(ctx, d.LaneID, d.ItemID, id, l.headOf(ctx, d.LaneID)); err != nil {
				if holdErr := l.holdItemFault(ctx, Candidate{ItemID: d.ItemID, RunID: id, LaneID: d.LaneID}, ReasonSettleFailed, err.Error()); holdErr != nil && asItemTerminal(holdErr) == nil {
					return reapedAny, holdErr
				}
				continue
			}
			if err := l.resetLane(ctx, d.LaneID); err != nil {
				return reapedAny, err
			}
		}
		if d.Reason == ReasonConflictFix && work.ParentRunID != "" {
			if restored, err := l.restoreConflictCandidate(ctx, work); err != nil {
				return reapedAny, err
			} else if restored {
				continue
			}
		}
		if err := l.markExamined(ctx, d); err != nil {
			return reapedAny, err
		}
		if d.BlockConflict != nil {
			if rec != nil {
				if err := l.recordDesigner(ctx, d, *rec); err != nil {
					return reapedAny, err
				}
			}
			if err := l.settleUnansweredDesignRuns(ctx); err != nil {
				return reapedAny, err
			}
		}
	}
	return reapedAny, nil
}
