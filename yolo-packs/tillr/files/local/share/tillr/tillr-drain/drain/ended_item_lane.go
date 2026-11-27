package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// endedWorkPrefix names commits holding uncommitted work left in a lane when its
	// item ended for good (closed, resolved-elsewhere, landed, or another finished state).
	// Unlike refs/tillr/preserved/, sweepRefs never deletes these refs automatically.
	endedWorkPrefix = "refs/tillr/ended/"
	// endedWorkSavedKind records uncommitted work saved from an ended item's lane before
	// restoring the lane's worktree.
	endedWorkSavedKind = "drain.ended-work-saved"
	// endedWorktreeCollectedKind records deletion of an ended item's worktree and/or
	// scratch (or an ended run's scratch under a pool lane).
	endedWorktreeCollectedKind = "drain.ended-worktree-collected"
	// endedWorktreeCollectFailedKind records a directory that could not be deleted during
	// ended-item collection and was left in place to retry on a later pass.
	endedWorktreeCollectFailedKind = "drain.ended-worktree-collect-failed"
)

// preservedScratchFiles names files that a drain reader may read from a run's scratch
// directory and that are copied beside the run's log (RunDir) before scratch is deleted:
//   - graph-exit.json: workflow no-commit exit record (also written to RunDir directly)
//   - landing-offer.json: worker landing offer
//   - eval.output: evaluator / judge verdict output
//   - replies.jsonl: comment batch replies
var preservedScratchFiles = []string{
	graphExitFile,
	landingOfferFile,
	evalOutputFile,
	commentRepliesFile,
}

func init() {
	for _, kind := range []string{endedWorkSavedKind, endedWorktreeCollectedKind, endedWorktreeCollectFailedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

// EndedWorktreeCollected is drain.ended-worktree-collected's payload.
type EndedWorktreeCollected struct {
	ItemID    string   `json:"item_id"`
	ItemState string   `json:"item_state"`
	LaneID    string   `json:"lane_id"`
	Deleted   []string `json:"deleted"`
}

// EndedWorktreeCollectFailed is drain.ended-worktree-collect-failed's payload.
type EndedWorktreeCollectFailed struct {
	ItemID    string `json:"item_id"`
	ItemState string `json:"item_state"`
	LaneID    string `json:"lane_id"`
	Directory string `json:"directory"`
	Reason    string `json:"reason"`
}

// EndedWorkSaved is drain.ended-work-saved's payload.
type EndedWorkSaved struct {
	RunID      string          `json:"run_id"`
	ItemID     string          `json:"item_id"`
	ItemState  string          `json:"item_state"`
	LaneID     string          `json:"lane_id"`
	Ref        string          `json:"ref"`
	IndexRef   string          `json:"index_ref,omitempty"`
	Repository string          `json:"repository"`
	Reason     string          `json:"reason"`
	KeptFiles  int64           `json:"kept_files"`
	KeptBytes  int64           `json:"kept_bytes"`
	Files      []PreservedFile `json:"files"`
	IndexFiles []PreservedFile `json:"index_files,omitempty"`
	Unmerged   []string        `json:"unmerged,omitempty"`
	Summary    string          `json:"summary"`
}

// terminalItemState answers the item's state and whether it has ended for good (any
// finished state in the store's definition, or removed from items).
func (l *Loop) terminalItemState(ctx context.Context, itemID string) (string, bool, error) {
	if l.cfg.Store == "" || itemID == "" {
		return "", false, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", false, err
	}
	defer s.Close()
	var state string
	var ended bool
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		err = r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&state)
		if err == sql.ErrNoRows {
			state, ended = "removed", true
			return nil
		}
		if err != nil {
			return err
		}
		ended = finishedState(declared.Parsed, state)
		return nil
	})
	return state, ended, err
}

// runItemTerminalState answers the item, its state, and whether it has ended for good.
func (l *Loop) runItemTerminalState(ctx context.Context, runID string) (itemID, state string, ended bool, err error) {
	if l.cfg.Store == "" || runID == "" {
		return "", "", false, nil
	}
	itemID, err = l.preservationItem(ctx, runID)
	if err != nil {
		return "", "", false, nil
	}
	state, ended, err = l.terminalItemState(ctx, itemID)
	return itemID, state, ended, err
}

// runItemTerminal answers whether the item owning runID is already in a finished state.
func (l *Loop) runItemTerminal(ctx context.Context, runID string) (bool, error) {
	_, _, ended, err := l.runItemTerminalState(ctx, runID)
	return ended, err
}

// saveEndedItemWork snapshots uncommitted files left in lane by an ended item's run
// under refs/tillr/ended/<runID> and records drain.ended-work-saved. It never discards
// uncommitted work: if saving fails, it returns the error before any caller resets the lane.
func (l *Loop) saveEndedItemWork(ctx context.Context, lane, runID, itemID, itemState, reason string) (bool, error) {
	if l.nonLanding || runID == "" {
		return false, nil
	}
	if err := checkRunID(runID); err != nil {
		return false, err
	}
	target := l.laneTarget(lane)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinTimeout)
	defer cancel()
	if err := l.clearStaleIndexLock(ctx, lane, runID); err != nil {
		return false, err
	}
	status, err := l.git(ctx, target, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) == "" {
		return false, nil
	}
	snapshot, err := l.snapshotWork(ctx, lane, runID)
	if err != nil {
		return false, err
	}
	if len(snapshot.files) == 0 && len(snapshot.indexFiles) == 0 {
		return false, nil
	}
	ref := endedWorkPrefix + runID
	refs, err := l.git(ctx, target, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return false, err
	}
	exists := false
	for _, name := range strings.Split(refs, "\n") {
		if name == ref {
			exists = true
		}
	}
	var p EndedWorkSaved
	if exists {
		body, err := l.git(ctx, target, "show", "-s", "--format=%B", ref)
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			return false, fmt.Errorf("reading saved ended work %s: %w", ref, err)
		}
		if p.RunID != runID || p.Ref != ref {
			return false, Externalf("saved ended work ref %s names a different run", ref)
		}
		if err := l.snapshotCovered(ctx, target, ref, snapshot.tree, snapshot.files); err != nil {
			return false, err
		}
		indexRef := p.IndexRef
		if indexRef == "" {
			indexRef = ref
		}
		if err := l.snapshotCovered(ctx, target, indexRef, snapshot.indexTree, snapshot.indexFiles); err != nil {
			return false, err
		}
		if p.KeptFiles == 0 && (len(p.Files) > 0 || len(p.IndexFiles) > 0) {
			keptFiles, keptBytes, err := l.measureEndedWorkKept(ctx, target, snapshot, p.IndexRef != "")
			if err != nil {
				return false, err
			}
			p.KeptFiles = keptFiles
			p.KeptBytes = keptBytes
		}
	} else {
		if itemState == "" {
			itemState = "closed"
		}
		hasSeparateIndex := snapshot.indexTree != snapshot.head && snapshot.indexTree != snapshot.tree
		keptFiles, keptBytes, err := l.measureEndedWorkKept(ctx, target, snapshot, hasSeparateIndex)
		if err != nil {
			return false, err
		}
		p = EndedWorkSaved{
			RunID:      runID,
			ItemID:     itemID,
			ItemState:  itemState,
			LaneID:     lane,
			Ref:        ref,
			Repository: target,
			Reason:     reason,
			KeptFiles:  keptFiles,
			KeptBytes:  keptBytes,
			Files:      snapshot.files,
			Summary:    snapshot.summary,
			Unmerged:   snapshot.unmerged,
		}
		if same, ok := l.sharesRepository(ctx, target); ok && same {
			p.Repository = l.cfg.Root
		}
		args := []string{"commit-tree", snapshot.tree, "-p", snapshot.head}
		if hasSeparateIndex {
			staged, err := l.git(ctx, target, "commit-tree", snapshot.indexTree, "-p", snapshot.head, "-m", "Staged contents of ended item run "+runID)
			if err != nil {
				return false, err
			}
			args = append(args, "-p", strings.TrimSpace(staged))
			p.IndexRef = ref + "^2"
			p.IndexFiles = snapshot.indexFiles
			p.Summary += "; staged index: " + snapshot.indexSummary
		}
		if len(snapshot.unmerged) > 0 {
			p.Summary += "; unmerged in the lane's index: " + strings.Join(snapshot.unmerged, ", ") + " (kept as the working copy has them)"
		}
		if snapshot.indexNote != "" {
			p.Summary += "; " + snapshot.indexNote
		}
		if err := l.writeEndedWorkRef(ctx, target, p, args...); err != nil {
			return false, err
		}
	}
	recorded, err := l.endedWorkRecorded(ctx, runID, ref)
	if err != nil {
		return false, err
	}
	if !recorded {
		if err := appendJudgment(ctx, l.cfg.Store, endedWorkSavedKind, "run", runID, p, l.cfg.Now().UTC()); err != nil {
			return false, err
		}
		l.reportf("loop: saved uncommitted work from ended item %s (%s) on %s at %s: %s\n", p.ItemID, p.ItemState, lane, p.Ref, p.Summary)
	}
	return true, nil
}

func (l *Loop) writeEndedWorkRef(ctx context.Context, target string, p EndedWorkSaved, args ...string) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", target}, args...)...)
	cmd.Stdin = bytes.NewReader(body)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	sha, err := cmd.Output()
	if err != nil {
		return Externalf("writing %s (%d files, %d staged files): git commit-tree in %s: %v: %s", p.Ref, len(p.Files), len(p.IndexFiles), target, err, strings.TrimSpace(stderr.String()))
	}
	_, err = l.git(ctx, target, "update-ref", p.Ref, strings.TrimSpace(string(sha)), "")
	return err
}

func (l *Loop) measureEndedWorkKept(ctx context.Context, target string, snapshot workSnapshot, includeIndex bool) (int64, int64, error) {
	seenPaths := map[string]struct{}{}
	workBlobs := map[string]string{}
	var blobRefs []string

	collectTree := func(tree string, isIndex bool) error {
		out, err := l.git(ctx, target, "--no-optional-locks", "diff", "--raw", "--full-index", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", snapshot.head, tree)
		if err != nil {
			return err
		}
		for out != "" {
			meta, rest, ok := strings.Cut(out, "\x00")
			if !ok {
				return fmt.Errorf("ended work size: incomplete raw diff metadata")
			}
			path, rest, ok := strings.Cut(rest, "\x00")
			if !ok {
				return fmt.Errorf("ended work size: incomplete raw diff path")
			}
			out = rest
			fields := strings.Fields(strings.TrimPrefix(meta, ":"))
			if len(fields) < 5 {
				return fmt.Errorf("ended work size: malformed raw diff entry %q", meta)
			}
			dstMode, dstSHA := fields[1], fields[3]
			seenPaths[path] = struct{}{}
			if dstMode == "000000" || dstMode == "160000" || strings.Trim(dstSHA, "0") == "" {
				continue
			}
			if !isIndex {
				workBlobs[path] = dstSHA
				blobRefs = append(blobRefs, dstSHA)
			} else if workBlobs[path] != dstSHA {
				blobRefs = append(blobRefs, dstSHA)
			}
		}
		return nil
	}

	if err := collectTree(snapshot.tree, false); err != nil {
		return 0, 0, err
	}
	if includeIndex && snapshot.indexTree != "" && snapshot.indexTree != snapshot.head && snapshot.indexTree != snapshot.tree {
		if err := collectTree(snapshot.indexTree, true); err != nil {
			return 0, 0, err
		}
	}

	keptFiles := int64(len(seenPaths))
	if len(blobRefs) == 0 {
		return keptFiles, 0, nil
	}

	uniqueSHAs := make([]string, 0, len(blobRefs))
	seenSHA := make(map[string]struct{}, len(blobRefs))
	for _, sha := range blobRefs {
		if _, ok := seenSHA[sha]; !ok {
			seenSHA[sha] = struct{}{}
			uniqueSHAs = append(uniqueSHAs, sha)
		}
	}

	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-C", target, "cat-file", "--batch-check=%(objectsize)")
	cmd.Stdin = strings.NewReader(strings.Join(uniqueSHAs, "\n") + "\n")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, Externalf("measuring ended work blobs in %s: git cat-file: %v: %s", target, err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != len(uniqueSHAs) {
		return 0, 0, fmt.Errorf("measuring ended work blobs: got %d sizes for %d blobs", len(lines), len(uniqueSHAs))
	}
	sizes := make(map[string]int64, len(uniqueSHAs))
	for i, line := range lines {
		n, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if err != nil || n < 0 {
			return 0, 0, fmt.Errorf("measuring ended work blob %s: invalid size %q", uniqueSHAs[i], line)
		}
		sizes[uniqueSHAs[i]] = n
	}
	var keptBytes int64
	for _, sha := range blobRefs {
		keptBytes += sizes[sha]
	}
	return keptFiles, keptBytes, nil
}

func (l *Loop) endedWorkRecorded(ctx context.Context, runID, ref string) (bool, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var exists bool
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? AND json_extract(payload, '$.ref') = ?)`, endedWorkSavedKind, runID, ref).Scan(&exists)
	})
	return exists, err
}

var endedLaneQueryHook func(query string, args []any)

func queryEndedLaneRows(r *store.Reader, query string, args ...any) (*sql.Rows, error) {
	if endedLaneQueryHook != nil {
		endedLaneQueryHook(query, args)
	}
	return r.Query(query, args...)
}

func queryEndedLaneRow(r *store.Reader, query string, args ...any) *sql.Row {
	if endedLaneQueryHook != nil {
		endedLaneQueryHook(query, args)
	}
	return r.QueryRow(query, args...)
}

type endedLaneOwner struct {
	runID      string
	itemID     string
	seq        int64
	fromRuns   bool
	finishedAt int64
	startedAt  int64
}

func (c endedLaneOwner) newerOrEqual(prev endedLaneOwner) bool {
	if c.seq != prev.seq {
		return c.seq > prev.seq
	}
	if c.fromRuns != prev.fromRuns {
		return c.fromRuns
	}
	if !c.fromRuns {
		return true
	}
	if c.finishedAt != prev.finishedAt {
		return c.finishedAt > prev.finishedAt
	}
	if c.startedAt != prev.startedAt {
		return c.startedAt > prev.startedAt
	}
	return c.runID >= prev.runID
}

type reclaimedCleanLane struct {
	runID string
	epoch int
}

type endedLaneHistory struct {
	storePath   string
	lastSeq     int64
	eventOwners map[string]endedLaneOwner
	cleanLanes  map[string]reclaimedCleanLane
}

type endedLaneEventPayload struct {
	ParentRunID string `json:"parent_run_id"`
	Decision    struct {
		ItemID string `json:"item_id"`
		LaneID string `json:"lane_id"`
	} `json:"decision"`
}

type endedLaneCandidate struct {
	lane       string
	runID      string
	itemID     string
	itemState  string
	trunkAfter string
}

type poolScratchEntry struct {
	lane  string
	sub   string
	runID string
}

type poolScratchCandidate struct {
	lane       string
	sub        string
	runID      string
	itemID     string
	itemState  string
	trunkAfter string
}

// removeEndedDirHook is a test seam for simulating a directory that cannot be removed.
var removeEndedDirHook func(path string) error

func removeEndedDir(path string) error {
	if removeEndedDirHook != nil {
		return removeEndedDirHook(path)
	}
	return os.RemoveAll(path)
}

func (l *Loop) hasCollectableDir(lane string) bool {
	if _, err := os.Stat(l.laneTarget(lane)); err == nil {
		return true
	}
	dir := l.laneDirPath(lane)
	for _, sub := range []string{"scratch", "durable"} {
		if _, err := os.Stat(filepath.Join(dir, sub)); err == nil {
			return true
		}
	}
	return false
}

func (l *Loop) scanPoolScratchEntries(lane string) []poolScratchEntry {
	dir := l.laneDirPath(lane)
	var out []poolScratchEntry
	for _, sub := range []string{"scratch", "durable"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			out = append(out, poolScratchEntry{lane: lane, sub: sub, runID: e.Name()})
		}
	}
	return out
}

func (l *Loop) landingProven(ctx context.Context, trunkAfter string) (bool, error) {
	if trunkAfter == "" {
		return false, nil
	}
	greenOut, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(objectname)", GreenRef)
	if err != nil {
		return false, err
	}
	green := strings.TrimSpace(greenOut)
	if green == "" {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", l.cfg.Root, "merge-base", "--is-ancestor", trunkAfter, green)
	if err := cmd.Run(); err == nil {
		return true, nil
	} else if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	} else {
		return false, err
	}
}

func latestLandedTrunkAfter(r *store.Reader, itemID string) (string, error) {
	rows, err := queryEndedLaneRows(r, `SELECT trunk_after, event_seq FROM landings WHERE item_id = ? AND result = 'landed'`, itemID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var bestTrunk string
	var bestSeq int64
	found := false
	for rows.Next() {
		var trunk string
		var seq int64
		if err := rows.Scan(&trunk, &seq); err != nil {
			return "", err
		}
		if !found || seq >= bestSeq {
			bestTrunk = trunk
			bestSeq = seq
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if bestTrunk != "" {
		if resolved, rErr := heavy.RewriteResolve(r, bestTrunk); rErr == nil && resolved != "" {
			bestTrunk = resolved
		}
	}
	return bestTrunk, nil
}

func itemHasLiveOrInterruptedNode(r *store.Reader, itemID string, leasedRunIDs, activeRunIDs map[string]bool) (bool, error) {
	rows, err := queryEndedLaneRows(r, `SELECT run_id, outcome FROM node_runs WHERE item_id = ?`, itemID)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var runID, outcome string
		if err := rows.Scan(&runID, &outcome); err != nil {
			rows.Close()
			return false, err
		}
		if outcome == "" || outcome == "running" || outcome == "interrupted" || leasedRunIDs[runID] || activeRunIDs[runID] {
			rows.Close()
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()

	runRows, err := queryEndedLaneRows(r, `SELECT run_id, state FROM runs WHERE item_id = ?`, itemID)
	if err != nil {
		return false, err
	}
	for runRows.Next() {
		var runID, state string
		if err := runRows.Scan(&runID, &state); err != nil {
			runRows.Close()
			return false, err
		}
		if state == run.StatePaused || leasedRunIDs[runID] || activeRunIDs[runID] {
			runRows.Close()
			return true, nil
		}
	}
	if err := runRows.Err(); err != nil {
		runRows.Close()
		return false, err
	}
	runRows.Close()
	return false, nil
}

func preserveRunScratchFiles(root, scratchRunDir, runID string) error {
	if root == "" || runID == "" {
		return nil
	}
	dstDir := RunDir(root, runID)
	for _, name := range preservedScratchFiles {
		src := filepath.Join(scratchRunDir, name)
		info, err := os.Stat(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			continue
		}
		dst := filepath.Join(dstDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return err
		}
		pending := dst + ".pending"
		if err := os.WriteFile(pending, data, 0o644); err != nil {
			return err
		}
		if err := os.Rename(pending, dst); err != nil {
			return err
		}
	}
	return nil
}

func preserveLaneScratchFiles(root, subDir string) error {
	entries, err := os.ReadDir(subDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if err := preserveRunScratchFiles(root, filepath.Join(subDir, e.Name()), e.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) dirHasLiveChildProcess(dir string) bool {
	if l.cfg.Store == "" || dir == "" {
		return false
	}
	here, err := currentLoopOwner()
	if err != nil {
		return false
	}
	marked, err := liveRunWorkers(l.cfg.Store, "", here)
	if err != nil {
		return false
	}
	for _, p := range marked {
		if inside, err := processInCheckout(p, dir); err == nil && inside {
			return true
		}
	}
	return false
}

func (l *Loop) recordCollectionCompleted(ctx context.Context, itemID, itemState, laneID string, deleted []string) {
	p := EndedWorktreeCollected{
		ItemID:    itemID,
		ItemState: itemState,
		LaneID:    laneID,
		Deleted:   deleted,
	}
	if err := appendJudgment(ctx, l.cfg.Store, endedWorktreeCollectedKind, "item", itemID, p, l.cfg.Now().UTC()); err != nil {
		l.reportf("loop: recording ended-item collection for %s: %v\n", itemID, err)
	}
	l.reportf("loop: collected ended item %s (%s) on %s: deleted %s\n", itemID, itemState, laneID, strings.Join(deleted, ", "))
}

func (l *Loop) recordCollectionFailed(ctx context.Context, itemID, itemState, laneID, dir string, err error) {
	p := EndedWorktreeCollectFailed{
		ItemID:    itemID,
		ItemState: itemState,
		LaneID:    laneID,
		Directory: dir,
		Reason:    err.Error(),
	}
	if recErr := appendJudgment(ctx, l.cfg.Store, endedWorktreeCollectFailedKind, "item", itemID, p, l.cfg.Now().UTC()); recErr != nil {
		l.reportf("loop: recording ended-item collection failure for %s: %v\n", itemID, recErr)
	}
	l.reportf("loop: ended item %s (%s) directory %s could not be deleted: %v\n", itemID, itemState, dir, err)
}

func (l *Loop) deleteIdleItemLaneRow(ctx context.Context, lane string) (bool, error) {
	if l.cfg.Store == "" || lane == "" || drainmodel.IsPoolLaneName(lane) {
		return false, nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var deleted bool
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		res, err := tx.Exec(`DELETE FROM lanes WHERE lane_id = ? AND state != ?`, lane, run.LaneStateLeased)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		deleted = n > 0
		return nil
	})
	return deleted, err
}

func (l *Loop) deleteEndedItemWorktree(ctx context.Context, c endedLaneCandidate) error {
	end := l.beginLaneStaging(c.lane)
	defer end()
	if err := l.restoreForkDelivery(ctx, c.lane, c.runID); err != nil {
		return fmt.Errorf("restoring fork checkout before collection: %w", err)
	}
	worktree := l.laneTarget(c.lane)
	if removeEndedDirHook != nil {
		if err := removeEndedDirHook(worktree); err != nil {
			return err
		}
	}
	return withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
		if _, err := l.git(ctx, l.cfg.Root, "worktree", "remove", "--force", worktree); err != nil {
			if rmErr := os.RemoveAll(worktree); rmErr != nil {
				return fmt.Errorf("git worktree remove: %v; remove dir: %w", err, rmErr)
			}
		}
		if _, err := l.git(ctx, l.cfg.Root, "worktree", "prune"); err != nil {
			return err
		}
		return nil
	})
}

func (l *Loop) recordCleanEndedLane(lane, runID string, epochBefore int, restored bool) {
	l.mu.Lock()
	staging := l.stagingLanes[lane] > 0
	epochNow := l.laneChanges[lane]
	l.mu.Unlock()
	if staging || (!restored && epochNow != epochBefore) {
		return
	}
	if l.endedLanes.cleanLanes == nil {
		l.endedLanes.cleanLanes = map[string]reclaimedCleanLane{}
	}
	l.endedLanes.cleanLanes[lane] = reclaimedCleanLane{runID: runID, epoch: epochNow}
}

// reclaimEndedItemLanes saves uncommitted work left in idle lanes whose most recent run
// belonged to an item that has ended for good (closed, resolved-elsewhere, landed, or
// removed), records drain.ended-work-saved, and restores each pool lane's worktree so the
// next item can stage onto it without hitting lane-holds-uncommitted-work.
//
// For an item directory whose item has ended (closed, removed, resolved elsewhere, or
// landed and contained in refs/tillr/green), it deletes the worktree (via Git and pruned
// under withWorktreeRegistryLock), deletes scratch and any older durable directory, and
// deletes the item's idle row in lanes. For a pool lane, it deletes the scratch of any
// ended run whose item has ended without touching the pool lane's worktree.
func (l *Loop) reclaimEndedItemLanes(ctx context.Context) error {
	if l.nonLanding || l.cfg.Store == "" || l.cfg.LaneRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(l.cfg.LaneRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var onDisk []string
	var poolScratchOnDisk []poolScratchEntry
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		lane := entry.Name()
		if l.hasLaneCondition(lane, ReasonLaneChildren) {
			continue
		}
		if drainmodel.IsPoolLaneName(lane) {
			if _, err := os.Stat(l.laneTarget(lane)); err == nil {
				onDisk = append(onDisk, lane)
			}
			poolScratchOnDisk = append(poolScratchOnDisk, l.scanPoolScratchEntries(lane)...)
		} else if l.hasCollectableDir(lane) {
			onDisk = append(onDisk, lane)
		}
	}
	if len(onDisk) == 0 && len(poolScratchOnDisk) == 0 {
		return nil
	}
	sort.Strings(onDisk)
	sort.Slice(poolScratchOnDisk, func(i, j int) bool {
		if poolScratchOnDisk[i].lane != poolScratchOnDisk[j].lane {
			return poolScratchOnDisk[i].lane < poolScratchOnDisk[j].lane
		}
		if poolScratchOnDisk[i].sub != poolScratchOnDisk[j].sub {
			return poolScratchOnDisk[i].sub < poolScratchOnDisk[j].sub
		}
		return poolScratchOnDisk[i].runID < poolScratchOnDisk[j].runID
	})

	l.endedLaneMu.Lock()
	defer l.endedLaneMu.Unlock()

	epochsBefore := make(map[string]int, len(onDisk))
	activeRunIDs := map[string]bool{}
	l.mu.Lock()
	filtered := onDisk[:0]
	for _, lane := range onDisk {
		if l.stagingLanes[lane] > 0 {
			continue
		}
		epochsBefore[lane] = l.laneChanges[lane]
		filtered = append(filtered, lane)
	}
	onDisk = filtered
	filteredScratch := poolScratchOnDisk[:0]
	for _, ps := range poolScratchOnDisk {
		if l.stagingLanes[ps.lane] > 0 {
			continue
		}
		filteredScratch = append(filteredScratch, ps)
	}
	poolScratchOnDisk = filteredScratch
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
	if len(onDisk) == 0 && len(poolScratchOnDisk) == 0 {
		return nil
	}

	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var candidates []endedLaneCandidate
	var poolScratchCandidates []poolScratchCandidate
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		busy := map[string]bool{}
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
			busy[lane] = true
			if runID != "" {
				leasedRunIDs[runID] = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		idleLanes := make([]string, 0, len(onDisk))
		for _, lane := range onDisk {
			if !busy[lane] {
				idleLanes = append(idleLanes, lane)
			}
		}
		if len(idleLanes) == 0 && len(poolScratchOnDisk) == 0 {
			return nil
		}

		var declared definition.Record
		declaredLoaded := false
		loadDeclared := func() error {
			if declaredLoaded {
				return nil
			}
			d, err := definition.ForReader(r)
			if err != nil {
				return err
			}
			declared = d
			declaredLoaded = true
			return nil
		}

		states := map[string]string{}
		existsByItem := map[string]bool{}
		trunkAfterByItem := map[string]string{}
		lookupItem := func(itemID string) (state string, exists bool, err error) {
			if st, seen := states[itemID]; seen {
				return st, existsByItem[itemID], nil
			}
			err = queryEndedLaneRow(r, `SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&state)
			if err == sql.ErrNoRows {
				exists = false
			} else if err != nil {
				return "", false, err
			} else {
				exists = true
			}
			states[itemID], existsByItem[itemID] = state, exists
			return state, exists, nil
		}
		lookupTrunkAfter := func(itemID string) (string, error) {
			if ta, ok := trunkAfterByItem[itemID]; ok {
				return ta, nil
			}
			ta, err := latestLandedTrunkAfter(r, itemID)
			if err != nil {
				return "", err
			}
			trunkAfterByItem[itemID] = ta
			return ta, nil
		}

		var endedIdle []endedLaneCandidate
		if len(idleLanes) > 0 {
			head, err := r.EventHeadSeq()
			if err != nil {
				return err
			}
			since := l.endedLanes.lastSeq
			var nextEventOwners map[string]endedLaneOwner
			if l.endedLanes.storePath != l.cfg.Store || head < since || l.endedLanes.eventOwners == nil {
				since = 0
				nextEventOwners = map[string]endedLaneOwner{}
				l.endedLanes.cleanLanes = nil
			} else {
				nextEventOwners = maps.Clone(l.endedLanes.eventOwners)
			}
			if head > since {
				for _, kind := range []string{dispatchStartedKind, nodeAdvancedKind} {
					rows, err = queryEndedLaneRows(r, `SELECT seq, object_id, payload FROM events WHERE kind = ? AND seq > ? AND object_type = 'run'`, kind, since)
					if err != nil {
						return err
					}
					for rows.Next() {
						var seq int64
						var runID string
						var raw []byte
						if err := rows.Scan(&seq, &runID, &raw); err != nil {
							rows.Close()
							return err
						}
						var p endedLaneEventPayload
						if err := json.Unmarshal(raw, &p); err != nil {
							continue
						}
						lane, itemID := p.Decision.LaneID, p.Decision.ItemID
						if lane == "" || itemID == "" {
							continue
						}
						if kind == nodeAdvancedKind && p.ParentRunID != "" {
							runID = p.ParentRunID
						}
						cand := endedLaneOwner{runID: runID, itemID: itemID, seq: seq}
						if prev, ok := nextEventOwners[lane]; !ok || cand.newerOrEqual(prev) {
							nextEventOwners[lane] = cand
						}
					}
					if err := rows.Err(); err != nil {
						rows.Close()
						return err
					}
					rows.Close()
				}
			}

			owners := make(map[string]endedLaneOwner, len(idleLanes))
			args := make([]any, len(idleLanes))
			conds := make([]string, len(idleLanes))
			for i, lane := range idleLanes {
				args[i] = lane
				conds[i] = "lane_id = ?"
				if owner, ok := nextEventOwners[lane]; ok {
					owners[lane] = owner
				}
			}
			rows, err = queryEndedLaneRows(r, `SELECT run_id, COALESCE(item_id, ''), lane_id, event_seq, finished_at, started_at FROM runs WHERE `+strings.Join(conds, " OR "), args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var runID, itemID, lane string
				var seq, finishedAt, startedAt int64
				if err := rows.Scan(&runID, &itemID, &lane, &seq, &finishedAt, &startedAt); err != nil {
					rows.Close()
					return err
				}
				if lane == "" || itemID == "" || runID == "" {
					continue
				}
				cand := endedLaneOwner{
					runID:      runID,
					itemID:     itemID,
					seq:        seq,
					fromRuns:   true,
					finishedAt: finishedAt,
					startedAt:  startedAt,
				}
				if prev, ok := owners[lane]; !ok || cand.newerOrEqual(prev) {
					owners[lane] = cand
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()

			l.endedLanes.storePath = l.cfg.Store
			l.endedLanes.lastSeq = head
			l.endedLanes.eventOwners = nextEventOwners

			unchecked := make([]string, 0, len(idleLanes))
			for _, lane := range idleLanes {
				owner, ok := owners[lane]
				if !ok {
					if drainmodel.IsPoolLaneName(lane) {
						continue
					}
					owner = endedLaneOwner{itemID: lane}
					owners[lane] = owner
				}
				if clean, ok := l.endedLanes.cleanLanes[lane]; ok && clean.runID == owner.runID && clean.epoch == epochsBefore[lane] {
					continue
				}
				unchecked = append(unchecked, lane)
			}
			if len(unchecked) > 0 {
				if err := loadDeclared(); err != nil {
					return err
				}
				for _, lane := range unchecked {
					owner := owners[lane]
					state, exists, err := lookupItem(owner.itemID)
					if err != nil {
						return err
					}
					if exists && !finishedState(declared.Parsed, state) {
						continue
					}
					if !exists {
						state = "removed"
					}
					var trunkAfter string
					if state == StateLanded {
						trunkAfter, err = lookupTrunkAfter(owner.itemID)
						if err != nil {
							return err
						}
					}
					endedIdle = append(endedIdle, endedLaneCandidate{
						lane:       lane,
						runID:      owner.runID,
						itemID:     owner.itemID,
						itemState:  state,
						trunkAfter: trunkAfter,
					})
				}
			}
		}

		var poolScratchEnded []poolScratchCandidate
		for _, ps := range poolScratchOnDisk {
			if leasedRunIDs[ps.runID] || activeRunIDs[ps.runID] {
				continue
			}
			var itemID, runState string
			err := queryEndedLaneRow(r, `SELECT COALESCE(item_id, ''), state FROM runs WHERE run_id = ?`, ps.runID).Scan(&itemID, &runState)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return err
			}
			if itemID == "" || runState == run.StatePaused {
				continue
			}
			if err := loadDeclared(); err != nil {
				return err
			}
			state, exists, err := lookupItem(itemID)
			if err != nil {
				return err
			}
			if exists && !finishedState(declared.Parsed, state) {
				continue
			}
			if !exists {
				state = "removed"
			}
			var trunkAfter string
			if state == StateLanded {
				trunkAfter, err = lookupTrunkAfter(itemID)
				if err != nil {
					return err
				}
			}
			poolScratchEnded = append(poolScratchEnded, poolScratchCandidate{
				lane:       ps.lane,
				sub:        ps.sub,
				runID:      ps.runID,
				itemID:     itemID,
				itemState:  state,
				trunkAfter: trunkAfter,
			})
		}

		if len(endedIdle) == 0 && len(poolScratchEnded) == 0 {
			return nil
		}

		busyItems := map[string]bool{}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, w := range works {
			if !w.Done && !w.LaneReleased && w.Decision.LaneID != "" {
				busy[w.Decision.LaneID] = true
			}
			if !w.Done && w.Decision.ItemID != "" {
				busyItems[w.Decision.ItemID] = true
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
		}
		if err := qpRows.Err(); err != nil {
			qpRows.Close()
			return err
		}
		qpRows.Close()

		checkedItemBusy := map[string]bool{}
		isItemBusy := func(itemID string) (bool, error) {
			if busyItems[itemID] {
				return true, nil
			}
			if checkedItemBusy[itemID] {
				return false, nil
			}
			checkedItemBusy[itemID] = true
			live, err := itemHasLiveOrInterruptedNode(r, itemID, leasedRunIDs, activeRunIDs)
			if err != nil {
				return false, err
			}
			if live {
				busyItems[itemID] = true
			}
			return live, nil
		}

		for _, c := range endedIdle {
			if busy[c.lane] {
				continue
			}
			if !drainmodel.IsPoolLaneName(c.lane) {
				live, err := isItemBusy(c.itemID)
				if err != nil {
					return err
				}
				if live {
					continue
				}
			}
			candidates = append(candidates, c)
		}
		for _, ps := range poolScratchEnded {
			live, err := isItemBusy(ps.itemID)
			if err != nil {
				return err
			}
			if live {
				continue
			}
			poolScratchCandidates = append(poolScratchCandidates, ps)
		}
		return nil
	})
	s.Close()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if l.hasLaneCondition(c.lane, ReasonLaneChildren) {
			continue
		}
		if !drainmodel.IsPoolLaneName(c.lane) {
			l.collectEndedItemDir(ctx, c, epochsBefore[c.lane])
			continue
		}
		target := l.laneTarget(c.lane)
		if _, err := os.Stat(target); err != nil {
			continue
		}
		saved, err := l.saveEndedItemWork(ctx, c.lane, c.runID, c.itemID, c.itemState, "item ended ("+c.itemState+")")
		if err != nil {
			refusal := asLaneRefusal(laneRefusalf(c.lane, ReasonLaneUncommittedWork,
				"commit or remove the files, or reset the lane deliberately",
				"lane %s holds uncommitted work from ended item %s (%s) that could not be saved: %v",
				c.lane, c.itemID, c.itemState, err))
			refusal.LeftBy = formatLeftBy(c.runID, c.itemID)
			if rotErr := l.takeLaneOutOfRotation(ctx, refusal); rotErr != nil {
				l.reportf("loop: lane %s holds uncommitted work from ended item %s (%s) that could not be saved: %v\n", c.lane, c.itemID, c.itemState, err)
			}
			continue
		}
		if saved {
			if head, err := l.revParse(ctx, target, "HEAD"); err == nil && head != "" {
				if err := l.pinLane(ctx, c.lane, c.itemID, c.runID, head); err != nil {
					l.reportf("loop: lane %s head could not be pinned before restore: %v\n", c.lane, err)
					continue
				}
			}
			if !l.restoreReclaimedLane(ctx, c) {
				continue
			}
			if l.hasLaneCondition(c.lane, ReasonLaneUncommittedWork) {
				_ = l.restoreLane(ctx, c.lane)
			}
		} else if l.hasLaneCondition(c.lane, ReasonLaneUncommittedWork) && l.laneWorktreeClean(ctx, c.lane) {
			_ = l.restoreLane(ctx, c.lane)
		}
		l.recordCleanEndedLane(c.lane, c.runID, epochsBefore[c.lane], saved)
	}
	for _, ps := range poolScratchCandidates {
		l.collectPoolRunScratch(ctx, ps)
	}
	return nil
}

func (l *Loop) collectEndedItemDir(ctx context.Context, c endedLaneCandidate, epochBefore int) {
	if c.itemState == StateLanded {
		proven, err := l.landingProven(ctx, c.trunkAfter)
		if err != nil {
			l.reportf("loop: checking proof status for landed item %s: %v\n", c.itemID, err)
			return
		}
		if !proven {
			// Landed items not yet contained in refs/tillr/green keep their directory.
			return
		}
	}
	itemDir := l.laneDirPath(c.lane)
	if l.dirHasLiveChildProcess(itemDir) {
		return
	}
	var deleted []string
	target := l.laneTarget(c.lane)
	if _, statErr := os.Stat(target); statErr == nil {
		saved, err := l.saveEndedItemWork(ctx, c.lane, c.runID, c.itemID, c.itemState, "item ended ("+c.itemState+")")
		if err != nil {
			refusal := asLaneRefusal(laneRefusalf(c.lane, ReasonLaneUncommittedWork,
				"commit or remove the files, or reset the lane deliberately",
				"lane %s holds uncommitted work from ended item %s (%s) that could not be saved: %v",
				c.lane, c.itemID, c.itemState, err))
			refusal.LeftBy = formatLeftBy(c.runID, c.itemID)
			if rotErr := l.takeLaneOutOfRotation(ctx, refusal); rotErr != nil {
				l.reportf("loop: lane %s holds uncommitted work from ended item %s (%s) that could not be saved: %v\n", c.lane, c.itemID, c.itemState, err)
			}
			return
		}
		if head, err := l.revParse(ctx, target, "HEAD"); err == nil && head != "" {
			repo := l.lanePinRepository(c.lane)
			if err := writeItemPin(ctx, l.git, repo, c.itemID, head); err != nil {
				l.reportf("loop: item %s branch head could not be pinned before collection: %v\n", c.itemID, err)
				return
			}
			if saved && c.runID != "" {
				if err := l.pinLane(ctx, c.lane, c.itemID, c.runID, head); err != nil {
					l.reportf("loop: lane %s head could not be pinned before collection: %v\n", c.lane, err)
					return
				}
			}
		}
		if err := l.deleteEndedItemWorktree(ctx, c); err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.lane, target, err)
			return
		}
		deleted = append(deleted, "worktree")
		if l.hasLaneCondition(c.lane, ReasonLaneUncommittedWork) {
			_ = l.restoreLane(ctx, c.lane)
		}
	}
	for _, sub := range []string{"scratch", "durable"} {
		subPath := filepath.Join(itemDir, sub)
		if _, err := os.Stat(subPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.lane, subPath, err)
			return
		}
		if err := preserveLaneScratchFiles(l.cfg.Root, subPath); err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.lane, subPath, err)
			return
		}
		if err := removeEndedDir(subPath); err != nil {
			l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.lane, subPath, err)
			return
		}
		deleted = append(deleted, sub)
	}
	removedRow, err := l.deleteIdleItemLaneRow(ctx, c.lane)
	if err != nil {
		l.recordCollectionFailed(ctx, c.itemID, c.itemState, c.lane, itemDir, fmt.Errorf("deleting idle lane row: %w", err))
		return
	}
	if removedRow {
		deleted = append(deleted, "lane-row")
	}
	if len(deleted) > 0 {
		l.recordCollectionCompleted(ctx, c.itemID, c.itemState, c.lane, deleted)
	}
	l.recordCleanEndedLane(c.lane, c.runID, epochBefore, true)
}

func (l *Loop) collectPoolRunScratch(ctx context.Context, ps poolScratchCandidate) {
	if l.hasLaneCondition(ps.lane, ReasonLaneChildren) {
		return
	}
	if ps.itemState == StateLanded {
		proven, err := l.landingProven(ctx, ps.trunkAfter)
		if err != nil {
			l.reportf("loop: checking proof status for landed item %s: %v\n", ps.itemID, err)
			return
		}
		if !proven {
			return
		}
	}
	runScratchPath := filepath.Join(l.laneDirPath(ps.lane), ps.sub, ps.runID)
	if _, err := os.Stat(runScratchPath); os.IsNotExist(err) {
		return
	}
	if l.dirHasLiveChildProcess(runScratchPath) {
		return
	}
	if err := preserveRunScratchFiles(l.cfg.Root, runScratchPath, ps.runID); err != nil {
		l.recordCollectionFailed(ctx, ps.itemID, ps.itemState, ps.lane, runScratchPath, err)
		return
	}
	if err := removeEndedDir(runScratchPath); err != nil {
		l.recordCollectionFailed(ctx, ps.itemID, ps.itemState, ps.lane, runScratchPath, err)
		return
	}
	l.recordCollectionCompleted(ctx, ps.itemID, ps.itemState, ps.lane, []string{ps.sub + "/" + ps.runID})
}

func (l *Loop) restoreReclaimedLane(ctx context.Context, c endedLaneCandidate) bool {
	// Like staging, restoration is a loop-owned checkout write. Invalidate sibling
	// snapshots even if it only partly completes. Clean lanes never enter this bracket.
	end := l.beginLaneStaging(c.lane)
	defer end()
	if err := l.restoreForkDelivery(ctx, c.lane, c.runID); err != nil {
		l.reportf("loop: lane %s fork checkout could not be restored after saving ended item %s: %v\n", c.lane, c.itemID, err)
		return false
	}
	if err := l.resetLane(ctx, c.lane); err != nil {
		l.reportf("loop: lane %s could not be restored after saving ended item %s: %v\n", c.lane, c.itemID, err)
		return false
	}
	return true
}
