package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// RemoteSyncItemPrefix is the item-id prefix of a generated remote-sync conflict repair item.
	RemoteSyncItemPrefix = "sync-remote-"
	// RemoteSyncCriterion is the one criterion a remote-sync conflict repair item carries.
	RemoteSyncCriterion = "remote-sync-resolved"
	// remoteSyncRefPrefix pins the base commit P_{k-1} of an active remote-sync repair item.
	remoteSyncRefPrefix = "refs/tillr/sync-remote/"
	// remoteSyncStagedKind records the staging parameters (P_{k-1}, C_k, C_k^1, R, pairs) for sync-remote-*.
	remoteSyncStagedKind = "drain.remote-sync-staged"
)

func init() {
	if err := event.Register(remoteSyncStagedKind, Utility); err != nil {
		panic(err)
	}
}

// RemoteSyncItemID answers the conflict-repair item id for a fetched remote tip R:
// `sync-remote-<r12>`.
func RemoteSyncItemID(remoteTip string) string {
	return RemoteSyncItemPrefix + strings.ToLower(shortSHA(strings.TrimSpace(remoteTip)))
}

// IsRemoteSyncItem answers whether an item is a generated remote-sync conflict repair item.
func IsRemoteSyncItem(itemID string) bool {
	return strings.HasPrefix(itemID, RemoteSyncItemPrefix)
}

func remoteSyncPinRef(itemID string) string {
	return remoteSyncRefPrefix + itemID
}

type remoteSyncState struct {
	ItemID         string            `json:"item_id"`
	Remote         string            `json:"remote"`
	Ref            string            `json:"ref"`
	RemoteTip      string            `json:"remote_tip"`
	MergeBase      string            `json:"merge_base,omitempty"`
	BaseSHA        string            `json:"base_sha"`
	ConflictCommit string            `json:"conflict_commit"`
	ConflictParent string            `json:"conflict_parent"`
	ConflictPaths  []string          `json:"conflict_paths"`
	ConflictOutput string            `json:"conflict_output"`
	RemoteMessages string            `json:"remote_messages,omitempty"`
	CommitMessage  string            `json:"commit_message,omitempty"`
	LandingSummary string            `json:"landing_summary,omitempty"`
	OldTrunk       string            `json:"old_trunk"`
	ReplayedPairs  map[string]string `json:"replayed_pairs,omitempty"`
	ReplayedOrder  []string          `json:"replayed_order,omitempty"`
}

// parseMergeTreeConflict extracts the conflicting repo-relative paths and human-readable
// CONFLICT lines from `git merge-tree --write-tree` stdout.
func parseMergeTreeConflict(stdout string) (paths []string, conflictText string) {
	seen := map[string]bool{}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	inMessages := false
	var msgLines []string
	for i, line := range lines {
		if i == 0 && !strings.Contains(line, "\t") && !strings.HasPrefix(line, "CONFLICT") {
			continue
		}
		if line == "" {
			inMessages = true
			continue
		}
		if !inMessages {
			if _, pathPart, ok := strings.Cut(line, "\t"); ok {
				p := filepath.ToSlash(strings.TrimSpace(pathPart))
				if p != "" && !seen[p] {
					seen[p] = true
					paths = append(paths, p)
				}
				continue
			}
		}
		if strings.HasPrefix(line, "CONFLICT") || inMessages {
			msgLines = append(msgLines, line)
		}
	}
	sort.Strings(paths)
	return paths, strings.TrimSpace(strings.Join(msgLines, "\n"))
}

func remoteSyncSplitFence(all []string) (allowed, gateOnly []string) {
	for _, p := range all {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if GateCode(p) {
			gateOnly = append(gateOnly, p)
		} else {
			allowed = append(allowed, p)
		}
	}
	return allowed, gateOnly
}

func (l *Loop) collectRemoteSyncContext(ctx context.Context, mergeBase, remoteTip, conflictCommit string) (remoteMsgs, commitMsg, landingSummary string) {
	if mergeBase != "" && remoteTip != "" {
		if out, err := rawGit(ctx, l.cfg.Root, nil, "log", "--format=%h %s", mergeBase+".."+remoteTip); err == nil {
			remoteMsgs = clip(out, 1500)
		}
	}
	if conflictCommit != "" {
		if out, err := rawGit(ctx, l.cfg.Root, nil, "log", "-1", "--format=%B", conflictCommit); err == nil {
			commitMsg = clip(out, 1500)
		}
	}
	if l.cfg.Store != "" && conflictCommit != "" {
		if s, err := store.OpenRead(ctx, l.cfg.Store); err == nil {
			defer s.Close()
			_ = s.ReadTx(ctx, func(r *store.Reader) error {
				var itemID, title string
				err := r.QueryRow(`SELECT l.item_id, COALESCE(i.title, '') FROM landings l LEFT JOIN items i USING(item_id) WHERE l.trunk_after = ? AND l.result = 'landed' LIMIT 1`, conflictCommit).Scan(&itemID, &title)
				if err == nil && itemID != "" {
					if title != "" {
						landingSummary = fmt.Sprintf("%s: %s", itemID, title)
					} else {
						landingSummary = itemID
					}
				}
				return nil
			})
		}
	}
	return remoteMsgs, commitMsg, landingSummary
}

func remoteSyncCriterionText(st remoteSyncState) string {
	branch := strings.TrimPrefix(st.Ref, "refs/heads/")
	return fmt.Sprintf("resolve rebase conflict of %s onto %s/%s (%s) atop %s on %s with one linear commit (zero merge parents) leaving all non-conflicting paths identical to the three-way merge",
		shortSHA(st.ConflictCommit), st.Remote, branch, shortSHA(st.RemoteTip), shortSHA(st.BaseSHA), strings.Join(st.ConflictPaths, ", "))
}

func (l *Loop) readRemoteSyncState(ctx context.Context, itemID string) (remoteSyncState, bool, error) {
	var st remoteSyncState
	if l.cfg.Store == "" {
		return st, false, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return st, false, err
	}
	defer s.Close()
	var found bool
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var raw string
		qerr := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, remoteSyncStagedKind, itemID).Scan(&raw)
		if errors.Is(qerr, sql.ErrNoRows) {
			return nil
		}
		if qerr != nil {
			return qerr
		}
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			return err
		}
		found = true
		return nil
	})
	return st, found, err
}

func (l *Loop) fileOrUpdateRemoteSyncItem(ctx context.Context, st remoteSyncState) (escalatedOwner bool, err error) {
	if _, err := rawGit(ctx, l.cfg.Root, nil, "update-ref", remoteSyncPinRef(st.ItemID), st.BaseSHA); err != nil {
		return false, err
	}
	allowed, gateOnly := remoteSyncSplitFence(st.ConflictPaths)
	onlyGate := len(allowed) == 0 && len(gateOnly) > 0
	if onlyGate {
		escalatedOwner = true
	}
	branch := strings.TrimPrefix(st.Ref, "refs/heads/")
	title := fmt.Sprintf("resolve rebase conflict onto %s/%s (%s)", st.Remote, branch, shortSHA(st.RemoteTip))
	criteriaText := remoteSyncCriterionText(st)
	initialState := StateInQueue
	itemPayload := map[string]any{
		"item_id":       st.ItemID,
		"title":         title,
		"state":         initialState,
		"blocked_by":    []string{},
		"rank":          TrunkFixerRank,
		"allowed_paths": allowed,
		"seat":          TrunkFixerSeat,
		"express":       true,
		"criteria": []map[string]string{{
			"criterion_id": RemoteSyncCriterion,
			"text":         criteriaText,
			"state":        "not-done",
		}},
	}
	if onlyGate {
		itemPayload["state"] = StateBlocked
		itemPayload["waiting_on"] = map[string]string{
			"waiter": "owner",
			"reason": fmt.Sprintf("rebase conflict onto %s/%s (%s) only touches gate code paths (%s)", st.Remote, branch, shortSHA(st.RemoteTip), strings.Join(gateOnly, ", ")),
		}
	}
	payload, err := json.Marshal(itemPayload)
	if err != nil {
		return false, Internalf("encoding remote-sync item: %v", err)
	}
	env := record.Envelope{
		RecordVersion: 1,
		Kind:          item.RecordKind,
		ID:            "item-" + st.ItemID,
		Producer:      record.Producer{System: "tillr", Utility: Utility, Version: Version},
		CreatedAt:     l.cfg.Now().UTC(),
		Payload:       payload,
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, Incompatiblef("%v", err)
	}
	defer s.Close()

	var toClose []string
	var created bool
	var existingState string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		declared, err := definition.ForReader(tx.Reader())
		if err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT item_id, state FROM items WHERE item_id LIKE ?`, RemoteSyncItemPrefix+"%")
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				rows.Close()
				return err
			}
			if id == st.ItemID {
				existingState = state
				continue
			}
			if !finishedState(declared.Parsed, state) {
				toClose = append(toClose, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		if existingState == "" {
			if _, err := item.Apply(ctx, tx, env); err != nil {
				return err
			}
			if onlyGate {
				waitingJSON, err := json.Marshal(item.WaitingOn{
					Waiter: "owner",
					Reason: fmt.Sprintf("rebase conflict onto %s/%s (%s) only touches gate code paths (%s)", st.Remote, branch, shortSHA(st.RemoteTip), strings.Join(gateOnly, ", ")),
				})
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE items SET state=?, waiting_on=? WHERE item_id=?`, StateBlocked, string(waitingJSON), st.ItemID); err != nil {
					return err
				}
			}
			created = true
		} else {
			if !onlyGate && len(allowed) > 0 {
				var oldFence string
				if err := tx.QueryRow(`SELECT COALESCE(allowed_paths, '[]') FROM items WHERE item_id=?`, st.ItemID).Scan(&oldFence); err == nil {
					var old []string
					_ = json.Unmarshal([]byte(oldFence), &old)
					merged := TrunkFixerFence(old, allowed)
					if raw, err := json.Marshal(merged); err == nil {
						if _, err := tx.Exec(`UPDATE items SET allowed_paths=? WHERE item_id=?`, string(raw), st.ItemID); err != nil {
							return err
						}
					}
				}
			}
			if _, err := tx.Exec(`UPDATE item_criteria SET text=? WHERE item_id=? AND criterion_id=?`, criteriaText, st.ItemID, RemoteSyncCriterion); err != nil {
				return err
			}
			if finishedState(declared.Parsed, existingState) {
				if _, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, StateInQueue, st.ItemID); err != nil {
					return err
				}
			}
		}
		return appendEffect(tx, remoteSyncStagedKind, st.ItemID, st, l.cfg.Now().UTC())
	})
	if err != nil {
		return false, classifyWrite(err)
	}
	for _, staleID := range toClose {
		_ = l.closeRemoteSyncItem(ctx, staleID, "superseded by "+st.ItemID)
	}
	if created {
		l.reportf("loop: filed %s for rebase conflict onto %s/%s (%s) on %s\n", st.ItemID, st.Remote, branch, shortSHA(st.RemoteTip), strings.Join(st.ConflictPaths, ", "))
	}
	return escalatedOwner, nil
}

func (l *Loop) closeRemoteSyncItem(ctx context.Context, itemID, reason string) error {
	if l.cfg.Steps != nil {
		_ = l.cfg.Steps.ItemState(ctx, itemID, "", "closed", item.WaitingOn{Reason: reason})
	}
	if l.cfg.Store != "" {
		if s, err := store.Open(ctx, l.cfg.Store, true); err == nil {
			defer s.Close()
			_ = s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id=? AND state != 'closed'`, itemID)
				return err
			})
		}
	}
	_, _ = rawGit(ctx, l.cfg.Root, nil, "update-ref", "-d", remoteSyncPinRef(itemID))
	return nil
}

// closeStaleRemoteSyncItems closes any open sync-remote-* item whose remote tip R is now
// an ancestor of refs/heads/main (e.g., reconciled manually or superseded by another landing).
func (l *Loop) closeStaleRemoteSyncItems(ctx context.Context) error {
	if l.cfg.Store == "" {
		return nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	type openSync struct {
		id string
	}
	var open []openSync
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		rows, err := r.Query(`SELECT item_id, state FROM items WHERE item_id LIKE ?`, RemoteSyncItemPrefix+"%")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				return err
			}
			if !finishedState(declared.Parsed, state) {
				open = append(open, openSync{id: id})
			}
		}
		return rows.Err()
	})
	s.Close()
	if err != nil {
		return err
	}
	if len(open) == 0 {
		return nil
	}
	tip, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return nil
	}
	for _, item := range open {
		st, ok, err := l.readRemoteSyncState(ctx, item.id)
		if err != nil || !ok || st.RemoteTip == "" {
			continue
		}
		if anc, err := trunkAncestor(ctx, l.cfg.Root, st.RemoteTip, tip); err == nil && anc {
			if err := l.closeRemoteSyncItem(ctx, item.id, "remote tip "+shortSHA(st.RemoteTip)+" is now an ancestor of "+TrunkRef); err != nil {
				return err
			}
			l.reportf("loop: closed stale %s (%s is now on %s)\n", item.id, shortSHA(st.RemoteTip), TrunkRef)
		}
	}
	return nil
}

// remoteSyncDispatchBase returns the staged BaseSHA (P_{k-1}) for a sync-remote-* item.
func (l *Loop) remoteSyncDispatchBase(ctx context.Context, itemID string) (string, error) {
	st, ok, err := l.readRemoteSyncState(ctx, itemID)
	if err != nil {
		return "", err
	}
	if !ok || st.BaseSHA == "" {
		return "", Externalf("remote-sync item %s has no staged base SHA", itemID)
	}
	return st.BaseSHA, nil
}

// remoteSyncBriefFacts returns the conflict context for stage.go to include in the brief.
func remoteSyncBriefFacts(ctx context.Context, storePath, itemID string) string {
	if storePath == "" {
		return ""
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return ""
	}
	defer s.Close()
	var raw string
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, remoteSyncStagedKind, itemID).Scan(&raw)
	})
	if raw == "" {
		return ""
	}
	var st remoteSyncState
	if json.Unmarshal([]byte(raw), &st) != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Remote-sync rebase conflict onto %s/%s (%s) from base %s:\n", st.Remote, strings.TrimPrefix(st.Ref, "refs/heads/"), shortSHA(st.RemoteTip), shortSHA(st.BaseSHA))
	fmt.Fprintf(&b, "- Conflicting commit: %s (parent %s)\n", shortSHA(st.ConflictCommit), shortSHA(st.ConflictParent))
	if st.LandingSummary != "" {
		fmt.Fprintf(&b, "- Landing summary for %s: %s\n", shortSHA(st.ConflictCommit), st.LandingSummary)
	}
	fmt.Fprintf(&b, "- Conflicting paths: %s\n", strings.Join(st.ConflictPaths, ", "))
	if st.ConflictOutput != "" {
		fmt.Fprintf(&b, "\nMerge-tree conflict output:\n%s\n", st.ConflictOutput)
	}
	if st.RemoteMessages != "" {
		fmt.Fprintf(&b, "\nRemote commits (%s..%s):\n%s\n", shortSHA(st.MergeBase), shortSHA(st.RemoteTip), st.RemoteMessages)
	}
	if st.CommitMessage != "" {
		fmt.Fprintf(&b, "\nLocal commit message (%s):\n%s\n", shortSHA(st.ConflictCommit), st.CommitMessage)
	}
	return strings.TrimSpace(b.String())
}

// checkRemoteSyncTree verifies D-RP5 before gating sync-remote-<r12>:
//  1. C_k' has single parent P_{k-1} (no merge commits);
//  2. Every path outside the item's derived fencePaths matches the exact blob OID, mode,
//     and presence of the mechanical three-way merge of (P_{k-1}, C_k) over merge-base C_k^1.
func (l *Loop) checkRemoteSyncTree(ctx context.Context, candidate Candidate) (string, error) {
	st, ok, err := l.readRemoteSyncState(ctx, candidate.ItemID)
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("%s: no remote-sync staging record found for %s", ScopeEscape, candidate.ItemID), nil
	}
	if candidate.BaseSHA != st.BaseSHA {
		return fmt.Sprintf("%s: candidate base %s does not match staged P_{k-1} %s", ScopeEscape, shortSHA(candidate.BaseSHA), shortSHA(st.BaseSHA)), nil
	}
	parentsOut, err := rawGit(ctx, candidate.Worktree, nil, "rev-list", "--parents", "-n", "1", candidate.HeadSHA)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(strings.TrimSpace(parentsOut))
	if len(fields) != 2 || fields[1] != st.BaseSHA {
		return fmt.Sprintf("%s: %s must be a single linear commit atop %s with zero merge parents (got %s)",
			ScopeEscape, shortSHA(candidate.HeadSHA), shortSHA(st.BaseSHA), strings.TrimSpace(parentsOut)), nil
	}
	mt := exec.CommandContext(ctx, "git", "-C", l.cfg.Root, "merge-tree", "--write-tree", "--merge-base="+st.ConflictParent, st.BaseSHA, st.ConflictCommit)
	mt.Env = boundaryGitEnv()
	var mtErr bytes.Buffer
	mt.Stderr = &mtErr
	mtOut, mtRunErr := mt.Output()
	mergeTreeOID, _, _ := strings.Cut(string(mtOut), "\n")
	mergeTreeOID = strings.TrimSpace(mergeTreeOID)
	if mergeTreeOID == "" {
		return "", Externalf("mechanical merge-tree for %s failed (%v): %s", candidate.ItemID, mtRunErr, strings.TrimSpace(mtErr.String()))
	}
	allowed, err := l.readItemAllowedPaths(ctx, candidate.ItemID)
	if err != nil {
		return "", err
	}
	fence := fencePaths(allowed)
	diffOut, err := rawGit(ctx, l.cfg.Root, nil, "diff-tree", "-r", "--no-renames", "--name-only", "-z", mergeTreeOID, candidate.HeadSHA+"^{tree}")
	if err != nil {
		return "", err
	}
	var outOfFence []string
	for _, p := range strings.Split(diffOut, "\x00") {
		if p == "" {
			continue
		}
		if !pathInFence(p, fence) {
			outOfFence = append(outOfFence, p)
		}
	}
	if len(outOfFence) > 0 {
		sort.Strings(outOfFence)
		return fmt.Sprintf("%s: %s altered unfenced path(s) relative to the mechanical three-way merge of (%s, %s) over %s: %s",
			ScopeEscape, candidate.ItemID, shortSHA(st.BaseSHA), shortSHA(st.ConflictCommit), shortSHA(st.ConflictParent), strings.Join(outOfFence, ", ")), nil
	}
	return "", nil
}

func (l *Loop) readItemAllowedPaths(ctx context.Context, itemID string) ([]string, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var raw string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COALESCE(allowed_paths, '[]') FROM items WHERE item_id=?`, itemID).Scan(&raw)
	})
	if err != nil {
		return nil, err
	}
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil {
		return nil, err
	}
	return paths, nil
}

func pathInFence(path string, fence []string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, entry := range fence {
		entry = filepath.ToSlash(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		trimmed := strings.TrimSuffix(entry, "/")
		if path == trimmed || strings.HasPrefix(path, trimmed+"/") {
			return true
		}
	}
	return false
}

// landRemoteSync completes a gated sync-remote-* conflict repair under land.lock:
// replays C_{k+1}..T_now linearly atop C_k', resets main via git reset --keep, records
// trunk_rewrites, holds refs/tillr/green strictly behind C_k', completes the item, and
// triggers push (or remote-sync-proving).
func (l *Loop) landRemoteSync(ctx context.Context, candidate Candidate, result *LoopResult, timing *PassTiming) (string, bool, error) {
	root := l.cfg.Root
	st, ok, err := l.readRemoteSyncState(ctx, candidate.ItemID)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, Externalf("remote-sync item %s has no staging record", candidate.ItemID)
	}
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return "", false, err
	}
	tNowOut, err := rawGit(ctx, root, nil, "rev-parse", "--verify", "refs/heads/"+TrunkRef+"^{commit}")
	if err != nil {
		release()
		return "", false, err
	}
	tNow := strings.TrimSpace(tNowOut)
	if anc, err := trunkAncestor(ctx, root, st.RemoteTip, tNow); err == nil && anc {
		release()
		if err := l.closeRemoteSyncItem(ctx, candidate.ItemID, "remote tip "+shortSHA(st.RemoteTip)+" is already an ancestor of "+TrunkRef); err != nil {
			return "", false, err
		}
		l.mu.Lock()
		result.Landed++
		l.mu.Unlock()
		return tNow, true, nil
	}
	if anc, err := trunkAncestor(ctx, root, st.ConflictCommit, tNow); err != nil || !anc {
		release()
		if err != nil {
			return "", false, err
		}
		candidate.RefusalDetail = fmt.Sprintf("conflicting commit %s is no longer an ancestor of %s (%s)", shortSHA(st.ConflictCommit), TrunkRef, shortSHA(tNow))
		if err := l.refuse(ctx, candidate, result, RefusalLand); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	listed, err := rawGit(ctx, root, nil, "rev-list", "--reverse", "--topo-order", "--parents", st.ConflictCommit+".."+tNow)
	if err != nil {
		release()
		return "", false, err
	}
	known := &rewriteAbsorption{
		Lost:     tNow,
		Pairs:    map[string]string{},
		Order:    append([]string{}, st.ReplayedOrder...),
		Evidence: "replayed onto " + st.Remote + " " + shortSHA(st.RemoteTip) + " with conflict repair " + candidate.ItemID + " under the landing lock",
	}
	for k, v := range st.ReplayedPairs {
		known.Pairs[k] = v
	}
	known.Order = append(known.Order, st.ConflictCommit)
	known.Pairs[st.ConflictCommit] = candidate.HeadSHA
	cur := candidate.HeadSHA

	for _, line := range strings.Split(strings.TrimSpace(listed), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			release()
			return "", false, Externalf("commit %s is a merge or root commit in %s..%s", shortSHA(f[0]), shortSHA(st.ConflictCommit), shortSHA(tNow))
		}
		c, parent := f[0], f[1]
		mt := exec.CommandContext(ctx, "git", "-C", root, "merge-tree", "--write-tree", "--merge-base="+parent, cur, c)
		mt.Env = boundaryGitEnv()
		var mtErr bytes.Buffer
		mt.Stderr = &mtErr
		tree, mtErrRun := mt.Output()
		if mtErrRun != nil {
			release()
			// A later commit C_m conflicted: advance sync-remote-<r12>'s base to C_{m-1}' (cur)
			// without moving main.
			conflictPaths, conflictOut := parseMergeTreeConflict(string(tree))
			if len(conflictPaths) == 0 {
				return "", false, Externalf("replaying %s onto %s failed: %v: %s", shortSHA(c), shortSHA(cur), mtErrRun, strings.TrimSpace(mtErr.String()))
			}
			_, commitMsg, landingSummary := l.collectRemoteSyncContext(ctx, "", "", c)
			nextSt := remoteSyncState{
				ItemID:         st.ItemID,
				Remote:         st.Remote,
				Ref:            st.Ref,
				RemoteTip:      st.RemoteTip,
				MergeBase:      st.MergeBase,
				BaseSHA:        cur,
				ConflictCommit: c,
				ConflictParent: parent,
				ConflictPaths:  conflictPaths,
				ConflictOutput: conflictOut,
				RemoteMessages: st.RemoteMessages,
				CommitMessage:  commitMsg,
				LandingSummary: landingSummary,
				OldTrunk:       tNow,
				ReplayedPairs:  known.Pairs,
				ReplayedOrder:  known.Order,
			}
			escalatedOwner, ferr := l.fileOrUpdateRemoteSyncItem(ctx, nextSt)
			if ferr != nil {
				return "", false, ferr
			}
			branch := strings.TrimPrefix(st.Ref, "refs/heads/")
			if escalatedOwner {
				l.recordPush(ctx, l.cfg.PostLanding.Push.What, tNow, &pushStateError{
					kind:      pushKindConflict,
					remoteTip: st.RemoteTip,
					escalated: "owner",
					detail:    fmt.Sprintf("remote-sync-conflict: %s conflicted on gate code paths while advancing to %s\nmover: owner; resolve gate-code conflict onto %s/%s (%s)", st.ItemID, shortSHA(c), st.Remote, branch, shortSHA(st.RemoteTip)),
				})
			} else {
				l.recordPush(ctx, l.cfg.PostLanding.Push.What, tNow, &pushStateError{
					kind:      pushKindConflict,
					remoteTip: st.RemoteTip,
					detail:    fmt.Sprintf("remote-sync-conflict: %s; resolve rebase conflict onto %s/%s (%s) on %s\nmover: %s; resolve rebase conflict onto %s/%s (%s)", st.ItemID, st.Remote, branch, shortSHA(st.RemoteTip), strings.Join(conflictPaths, ", "), st.ItemID, st.Remote, branch, shortSHA(st.RemoteTip)),
				})
			}
			candidate.RefusalDetail = fmt.Sprintf("resolved %s as %s, and later commit %s conflicted on %s; %s advanced to base %s",
				shortSHA(st.ConflictCommit), shortSHA(candidate.HeadSHA), shortSHA(c), strings.Join(conflictPaths, ", "), st.ItemID, shortSHA(cur))
			if err := l.refuse(ctx, candidate, result, RefusalLand); err != nil {
				return "", false, err
			}
			return "", false, nil
		}
		treeID, _, _ := strings.Cut(string(tree), "\n")
		known.Order = append(known.Order, c)
		if curTree, err := rawGit(ctx, root, nil, "rev-parse", cur+"^{tree}"); err != nil {
			release()
			return "", false, err
		} else if strings.TrimSpace(curTree) == treeID {
			known.Pairs[c] = cur
			continue
		}
		meta, err := rawGit(ctx, root, nil, "log", "-1", "--date=raw", "--format=%an%n%ae%n%ad", c)
		if err != nil {
			release()
			return "", false, err
		}
		m := strings.Split(strings.TrimRight(meta, "\n"), "\n")
		if len(m) != 3 {
			release()
			return "", false, Externalf("commit %s has unreadable author data", shortSHA(c))
		}
		msg, err := rawGit(ctx, root, nil, "log", "-1", "--format=%B", c)
		if err != nil {
			release()
			return "", false, err
		}
		ct := exec.CommandContext(ctx, "git", "-C", root, "commit-tree", treeID, "-p", cur)
		ct.Env = append(boundaryGitEnv(), "GIT_AUTHOR_NAME="+m[0], "GIT_AUTHOR_EMAIL="+m[1], "GIT_AUTHOR_DATE="+m[2])
		ct.Stdin = strings.NewReader(msg)
		var ctErr bytes.Buffer
		ct.Stderr = &ctErr
		made, err := ct.Output()
		if err != nil {
			release()
			return "", false, Externalf("recreating %s failed: %v: %s", shortSHA(c), err, strings.TrimSpace(ctErr.String()))
		}
		cur = strings.TrimSpace(string(made))
		known.Pairs[c] = cur
	}
	known.Tip = cur

	if _, err := rawGit(ctx, root, nil, "update-ref", publishedRef(st.Remote, st.Ref), st.RemoteTip); err != nil {
		release()
		return "", false, err
	}
	head, _ := rawGit(ctx, root, nil, "symbolic-ref", "-q", "HEAD")
	if strings.TrimSpace(head) != "refs/heads/"+TrunkRef {
		release()
		l.recordPush(ctx, l.cfg.PostLanding.Push.What, tNow, &pushStateError{
			kind:      pushKindWorktreeDirty,
			remoteTip: st.RemoteTip,
			escalated: "operator",
			detail:    fmt.Sprintf("remote-sync-worktree-dirty: the main checkout is not on %s\nmover: operator; check out %s or commit, stash, or restore clashing paths in the main checkout", TrunkRef, TrunkRef),
		})
		return "", false, l.holdDirty(ctx, candidate, nil)
	}
	if _, err := rawGit(ctx, root, nil, "reset", "--keep", "-q", cur); err != nil {
		release()
		l.recordPush(ctx, l.cfg.PostLanding.Push.What, tNow, &pushStateError{
			kind:      pushKindWorktreeDirty,
			remoteTip: st.RemoteTip,
			escalated: "operator",
			detail:    fmt.Sprintf("remote-sync-worktree-dirty: the main checkout is dirty on a path %s changes: %v\nmover: operator; check out %s or commit, stash, or restore clashing paths in the main checkout", shortSHA(cur), err, TrunkRef),
		})
		return "", false, l.holdDirty(ctx, candidate, nil)
	}
	release()

	// Hold refs/tillr/green strictly behind C_k': at mapped G' if G preceded C_k, else min(G, B).
	green := optionalCommit(ctx, root, GreenRef)
	if green != "" {
		var targetGreen string
		precededCk := false
		if st.ConflictParent != "" {
			if anc, err := trunkAncestor(ctx, root, green, st.ConflictParent); err == nil && anc {
				precededCk = true
			}
		}
		if precededCk {
			if mapped := known.Pairs[green]; mapped != "" {
				targetGreen = mapped
			} else {
				targetGreen = green
			}
		} else if st.MergeBase != "" {
			if mb, err := rawGit(ctx, root, nil, "merge-base", green, st.MergeBase); err == nil {
				targetGreen = strings.TrimSpace(mb)
			}
		}
		if targetGreen != "" {
			known.GreenOverride = targetGreen
		}
	}

	if _, err := l.observeTrunkWith(ctx, "", known); err != nil {
		return "", false, fmt.Errorf("trunk rebased onto %s via %s, recording rewrite failed: %w", shortSHA(st.RemoteTip), candidate.ItemID, err)
	}
	_, _ = rawGit(ctx, root, nil, "update-ref", "-d", remoteSyncPinRef(candidate.ItemID))

	if err := l.completeItem(ctx, candidate); err != nil {
		return "", false, err
	}
	l.mu.Lock()
	result.Landed++
	l.mu.Unlock()

	res := healResult{replayed: len(known.Order), remoteTip: st.RemoteTip, base: st.MergeBase, newTip: cur}
	p := l.cfg.PostLanding.Push
	switch p.What {
	case settings.PushTip:
		l.rollAfterRemoteSync(ctx, res)
		l.pushAction.submit(cur, func(tip string) {
			err := pushTrunk(ctx, l.cfg.Root, p)
			var refused *refusedPushError
			if errors.As(err, &refused) {
				r2, herr := l.healRefusedPushResult(ctx, p, err)
				if herr == nil {
					l.rollAfterRemoteSync(ctx, r2)
					err = pushTrunk(ctx, l.cfg.Root, p)
				} else {
					err = herr
				}
			}
			l.recordPush(ctx, settings.PushTip, tip, err)
		})
	case settings.PushGreen:
		l.maybeRollAfterRemoteSync(ctx, res)
		l.recordPush(ctx, settings.PushGreen, cur, &pushStateError{
			kind:      pushKindProving,
			remoteTip: st.RemoteTip,
			detail:    fmt.Sprintf("rebased %d local commit(s) onto remote %s at %s via %s; awaiting heavy proof before pushing", len(known.Order), shortSHA(st.RemoteTip), shortSHA(cur), candidate.ItemID),
		})
	}
	return cur, true, nil
}

// escalateRemoteSyncPushState transitions an active remote-sync-conflict push state to
// needs you when the repair item escalates to owner or operator.
func (l *Loop) escalateRemoteSyncPushState(ctx context.Context, itemID, who, reason string) {
	if !IsRemoteSyncItem(itemID) || l.cfg.Store == "" {
		return
	}
	st, ok, err := l.readRemoteSyncState(ctx, itemID)
	if err != nil || !ok {
		return
	}
	branch := strings.TrimPrefix(st.Ref, "refs/heads/")
	mover := fmt.Sprintf("operator; rebase %s onto %s/%s (%s)", TrunkRef, st.Remote, branch, shortSHA(st.RemoteTip))
	if who == "owner" {
		mover = fmt.Sprintf("owner; resolve %s conflict onto %s/%s (%s)", itemID, st.Remote, branch, shortSHA(st.RemoteTip))
	}
	l.recordPush(ctx, l.cfg.PostLanding.Push.What, st.OldTrunk, &pushStateError{
		kind:      pushKindConflict,
		remoteTip: st.RemoteTip,
		escalated: who,
		detail:    fmt.Sprintf("remote-sync-conflict: %s escalated (%s) on %s\nmover: %s", itemID, reason, strings.Join(st.ConflictPaths, ", "), mover),
	})
}

// maybeRecoverClearedPushHold triggers a push when a blocking push hold
// (push-patch-move-held or remote-sync-worktree-dirty) has cleared and unpublished commits
// remain ahead of refs/tillr/published/<remote>/<branch>.
func (l *Loop) maybeRecoverClearedPushHold(ctx context.Context) {
	p := l.cfg.PostLanding.Push
	if p.What != settings.PushGreen && p.What != settings.PushTip {
		return
	}
	if l.pushAction.busy() || l.cfg.Store == "" {
		return
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return
	}
	var lastKind string
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		var raw string
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`, postLandingKind).Scan(&raw); err != nil {
			return nil
		}
		var prev struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal([]byte(raw), &prev) == nil {
			lastKind = prev.Kind
		}
		return nil
	})
	s.Close()
	switch lastKind {
	case pushKindPatchMoveHeld:
		if l.patchMoveHeld(ctx) {
			return
		}
	case pushKindWorktreeDirty:
		head, _ := rawGit(ctx, l.cfg.Root, nil, "symbolic-ref", "-q", "HEAD")
		if strings.TrimSpace(head) != "refs/heads/"+TrunkRef {
			return
		}
		status, err := rawGit(ctx, l.cfg.Root, nil, "status", "--porcelain")
		if err != nil || strings.TrimSpace(status) != "" {
			return
		}
	default:
		return
	}
	l.triggerPushNow(ctx)
}

// PushOutcome is the result of one synchronous `tillr drain push` turn.
type PushOutcome struct {
	Action string `json:"action"`
	What   string `json:"what"`
	Kind   string `json:"kind"`
	Tip    string `json:"tip,omitempty"`
	Detail string `json:"detail,omitempty"`
	OK     bool   `json:"ok"`
}

// PushOnce runs one synchronous push/heal turn using `.tillr.toml` `[push]` and records
// `drain.post-landing-action`.
func PushOnce(ctx context.Context, root, storePath string) (PushOutcome, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return PushOutcome{}, err
	}
	repositorySettings, err := settings.Read(absRoot)
	if err != nil {
		return PushOutcome{}, err
	}
	l := &Loop{cfg: LoopConfig{
		Root:        absRoot,
		Store:       storePath,
		Now:         time.Now,
		Report:      io.Discard,
		PostLanding: repositorySettings,
	}}
	p := repositorySettings.Push
	if p.What == "" || p.What == settings.PushNone {
		return PushOutcome{Action: "push", What: settings.PushNone, Kind: pushKindOK, OK: true}, nil
	}
	var tip string
	var pushErr error
	switch p.What {
	case settings.PushTip:
		tip = optionalCommit(ctx, absRoot, "refs/heads/"+TrunkRef)
		pushErr = pushTrunk(ctx, absRoot, p)
		var refused *refusedPushError
		if errors.As(pushErr, &refused) {
			res, herr := l.healRefusedPushResult(ctx, p, pushErr)
			if herr == nil {
				l.rollAfterRemoteSync(ctx, res)
				tip = res.newTip
				pushErr = pushTrunk(ctx, absRoot, p)
			} else {
				pushErr = herr
			}
		}
	case settings.PushGreen:
		tip, pushErr = pushGreen(ctx, absRoot, p)
		if tip == "" && pushErr == nil {
			return PushOutcome{Action: "push", What: p.What, Kind: pushKindOK, OK: true}, nil
		}
		var refused *refusedPushError
		if errors.As(pushErr, &refused) {
			res, herr := l.healRefusedPushResult(ctx, p, pushErr)
			if herr == nil {
				l.maybeRollAfterRemoteSync(ctx, res)
				carriedGreen := optionalCommit(ctx, absRoot, GreenRef)
				if carriedGreen != "" && carriedGreen == res.newTip {
					tip, pushErr = pushGreen(ctx, absRoot, p)
				} else {
					pushErr = &pushStateError{
						kind:       pushKindProving,
						remoteTip:  res.remoteTip,
						rebasedTip: res.newTip,
						detail:     fmt.Sprintf("the remote had moved: %d unpublished commits were rebased onto it; the new tip is proved, then pushed at the next green advance", res.replayed),
					}
				}
			} else {
				pushErr = herr
			}
		} else if pushErr == nil || errors.Is(pushErr, errAlreadyPublished) {
			l.flushPendingRemoteRoll(ctx, tip)
		}
	default:
		return PushOutcome{}, Usagef("unknown push mode %q", p.What)
	}
	l.recordPush(ctx, p.What, tip, pushErr)
	if errors.Is(pushErr, errAlreadyPublished) {
		pushErr = nil
	}
	kind := pushKindOK
	detail := ""
	if pushErr != nil {
		kind, _ = l.classifyPushFailure(ctx, pushErr)
		detail = pushErr.Error()
	}
	return PushOutcome{
		Action: "push",
		What:   p.What,
		Kind:   kind,
		Tip:    tip,
		Detail: detail,
		OK:     pushErr == nil,
	}, pushErr
}

// HasUnpublishedCommits reports whether green (or main in tip mode) has commits ahead of
// refs/tillr/published/<remote>/<branch>.
func HasUnpublishedCommits(ctx context.Context, root string, p settings.Push) bool {
	if p.What != settings.PushGreen && p.What != settings.PushTip {
		return false
	}
	remote, ref, err := pushDestination(ctx, root, p)
	if err != nil {
		return false
	}
	pub := priorPublished(ctx, root, remote, ref)
	g, m := pushCounts(ctx, root, pub)
	if p.What == settings.PushGreen {
		return g > 0
	}
	return m > 0
}

// triggerPushNow runs one push turn through pushAction (used by hold clearance and CLI).
func (l *Loop) triggerPushNow(ctx context.Context) {
	p := l.cfg.PostLanding.Push
	switch p.What {
	case settings.PushGreen:
		l.afterGreen(ctx, "hold-cleared")
	case settings.PushTip:
		tip := optionalCommit(ctx, l.cfg.Root, "refs/heads/"+TrunkRef)
		if tip != "" {
			l.afterLanding(ctx, tip)
		}
	}
}
