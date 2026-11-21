package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// RemoteSyncItemPrefix identifies generated remote-sync repairs.
	RemoteSyncItemPrefix = "sync-remote-"
	// RemoteSyncCriterion is the one criterion carried by a remote-sync repair.
	RemoteSyncCriterion  = "remote-sync-resolved"
	remoteSyncRefPrefix  = "refs/tillr/sync-remote/"
	remoteSyncStagedKind = "drain.remote-sync-staged"
	pushKindConflict     = "remote-sync-conflict"
)

func init() {
	if err := event.Register(remoteSyncStagedKind, Utility); err != nil {
		panic(err)
	}
	if err := event.Register(remoteSyncReplayKind, Utility); err != nil {
		panic(err)
	}
}

// RemoteSyncItemID names the repair for a fetched remote tip.
func RemoteSyncItemID(remoteTip string) string {
	return RemoteSyncItemPrefix + strings.ToLower(shortSHA(strings.TrimSpace(remoteTip)))
}

// IsRemoteSyncItem reports whether an item is a generated remote-sync repair.
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

	FirstConflictParent string `json:"first_conflict_parent,omitempty"`
}

// parseMergeTreeConflict reads `merge-tree --write-tree --name-only -z`:
// tree OID, literal paths, an empty field, then (path count, paths, type, message)
// records. Only the messages are display text; paths must survive unchanged.
func parseMergeTreeConflict(stdout string) (paths []string, conflictText string) {
	_, rest, _ := strings.Cut(stdout, "\x00")
	seen := map[string]bool{}
	for {
		var path string
		path, rest, _ = strings.Cut(rest, "\x00")
		if path == "" {
			break
		}
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	var messages []string
	for fields := strings.Split(rest, "\x00"); len(fields) >= 3; {
		n, err := strconv.Atoi(fields[0])
		if err != nil || n < 0 || n > len(fields)-3 {
			break
		}
		messages = append(messages, strings.TrimSpace(fields[n+2]))
		fields = fields[n+3:]
	}
	return paths, strings.Join(messages, "\n")
}

// These conflicts still need the owner: a repair must not change its own gate,
// and recording a non-UTF-8 name in the store's JSON would lose its identity.
func remoteSyncProtectedPaths(paths []string) string {
	var gatePaths, invalidNames []string
	for _, p := range paths {
		if GateCode(p) {
			gatePaths = append(gatePaths, p)
		}
		if !utf8.ValidString(p) {
			invalidNames = append(invalidNames, strings.ToValidUTF8(p, "�"))
		}
	}
	var problems []string
	if len(gatePaths) != 0 {
		problems = append(problems, "protected gate code paths ("+strings.Join(gatePaths, ", ")+")")
	}
	if len(invalidNames) != 0 {
		problems = append(problems, "paths that cannot be recorded losslessly ("+strings.Join(invalidNames, ", ")+")")
	}
	return strings.Join(problems, " and ")
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
				var id, title string
				err := r.QueryRow(`SELECT l.item_id, COALESCE(i.title, '') FROM landings l LEFT JOIN items i USING(item_id) WHERE l.trunk_after = ? AND l.result = 'landed' LIMIT 1`, conflictCommit).Scan(&id, &title)
				if err == nil && id != "" {
					landingSummary = id
					if title != "" {
						landingSummary += ": " + title
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
	return readRemoteSyncStateAt(ctx, l.cfg.Store, itemID)
}

func readRemoteSyncStateAt(ctx context.Context, storePath, itemID string) (st remoteSyncState, found bool, err error) {
	if storePath == "" {
		return st, false, nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return st, false, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		st, found, err = readRemoteSyncState(r, itemID)
		return err
	})
	return st, found, err
}

func readRemoteSyncState(r *store.Reader, itemID string) (remoteSyncState, bool, error) {
	var st remoteSyncState
	var raw string
	err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, remoteSyncStagedKind, itemID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	err = json.Unmarshal([]byte(raw), &st)
	return st, err == nil, err
}

func remoteSyncDispatchBase(r *store.Reader, itemID string) (string, error) {
	st, ok, err := readRemoteSyncState(r, itemID)
	if err != nil {
		return "", err
	}
	if !ok || st.BaseSHA == "" {
		return "", Externalf("remote-sync item %s has no staged base SHA", itemID)
	}
	return st.BaseSHA, nil
}

// remoteSyncBriefFacts renders the context recorded when the repair was filed.
func remoteSyncBriefFacts(ctx context.Context, storePath, itemID string) (string, error) {
	st, ok, err := readRemoteSyncStateAt(ctx, storePath, itemID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", Externalf("remote-sync item %s has no staged conflict context", itemID)
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
	return strings.TrimSpace(b.String()), nil
}

// fileOrUpdateRemoteSyncItem files the first conflict for a remote tip. Push
// retries attach to the current stage without moving its pin or changing its fence.
func (l *Loop) fileOrUpdateRemoteSyncItem(ctx context.Context, st remoteSyncState) (*pushStateError, error) {
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return l.fileOrUpdateRemoteSyncItemLocked(ctx, st, "")
}

// Only landing may supply priorBase to advance a gated step. Keep the history
// lock across the original stage check, suffix replay and step publication.
func (l *Loop) fileOrUpdateRemoteSyncItemLocked(ctx context.Context, st remoteSyncState, priorBase string) (*pushStateError, error) {
	if _, err := l.recoverRemoteSyncReplayLocked(ctx); err != nil {
		return nil, err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, Incompatiblef("%v", err)
	}
	defer s.Close()

	var superseded []remoteSyncState
	if priorBase == "" {
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			open, err := openRemoteSyncStages(r)
			if err != nil {
				return err
			}
			for _, row := range open {
				old := row.stage
				if old.Remote == st.Remote && old.Ref == st.Ref && old.RemoteTip != st.RemoteTip {
					if older, _ := trunkAncestor(ctx, l.cfg.Root, old.RemoteTip, st.RemoteTip); older {
						superseded = append(superseded, old)
					}
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	var created, advanced bool
	var conflict *pushStateError
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		previous, found, err := readRemoteSyncState(tx.Reader(), st.ItemID)
		if err != nil {
			return err
		}
		if priorBase != "" {
			if !found || previous.BaseSHA != priorBase {
				return Externalf("remote-sync stage for %s changed before advancing", st.ItemID)
			}
			if err := refuseTerminalItem(tx.Reader(), st.ItemID, "advance remote-sync stage"); err != nil {
				return err
			}
			if err := l.advanceRemoteSyncStep(ctx, tx, st, priorBase); err != nil {
				return err
			}
			advanced = true
		} else if found {
			if previous.RemoteTip != st.RemoteTip {
				return Internalf("remote-sync item %s already names tip %s, not %s", st.ItemID, previous.RemoteTip, st.RemoteTip)
			}
			st = previous
		} else {
			if err := l.fileRemoteSyncStep(ctx, tx, st); err != nil {
				return err
			}
			created = true
		}
		var state string
		if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, st.ItemID).Scan(&state); err != nil {
			return err
		}
		for _, old := range superseded {
			if _, err := item.Close(ctx, tx, old.ItemID, "superseded by "+st.ItemID, Utility, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		conflict, err = readRemoteSyncConflict(tx.Reader(), st, state)
		return err
	})
	if err != nil {
		if advanced {
			// Restore the previous pin if the store did not publish the stage.
			if _, restoreErr := rawGit(context.WithoutCancel(ctx), l.cfg.Root, nil, "update-ref", remoteSyncPinRef(st.ItemID), priorBase, st.BaseSHA); restoreErr != nil {
				err = errors.Join(err, restoreErr)
			}
		}
		return nil, classifyWrite(err)
	}
	if created {
		l.reportf("loop: filed %s for rebase conflict onto %s on %s\n", st.ItemID, remoteSyncDestination(st), strings.Join(st.ConflictPaths, ", "))
	}
	return conflict, nil
}

func (l *Loop) fileRemoteSyncStep(ctx context.Context, tx *store.Tx, st remoteSyncState) error {
	problem := remoteSyncProtectedPaths(st.ConflictPaths)
	payload, err := json.Marshal(map[string]any{
		"item_id": st.ItemID, "title": "resolve rebase conflict onto " + remoteSyncDestination(st), "state": StateInQueue,
		"rank": TrunkFixerRank, "seat": TrunkFixerSeat, "express": true,
		"criteria": []map[string]string{{"criterion_id": RemoteSyncCriterion, "text": remoteSyncCriterionText(st), "state": "not-done"}},
	})
	if err != nil {
		return Internalf("encoding remote-sync item: %v", err)
	}
	at := l.cfg.Now().UTC()
	if _, err := item.Apply(ctx, tx, record.Envelope{
		RecordVersion: 1, Kind: item.RecordKind, ID: "item-" + st.ItemID,
		Producer: record.Producer{System: "tillr", Utility: Utility, Version: Version}, CreatedAt: at, Payload: payload,
	}); err != nil {
		return err
	}
	if problem != "" {
		if _, err := item.SetState(ctx, tx, st.ItemID, StateBlocked, at, item.WaitingOn{
			Waiter: "owner", Reason: "rebase conflict onto " + remoteSyncDestination(st) + " includes " + problem,
		}); err != nil {
			return err
		}
	}
	if err := appendEffect(tx, remoteSyncStagedKind, st.ItemID, st, at); err != nil {
		return err
	}
	// Write under the history lock before making the stage visible. If the store
	// commit fails, the sweep treats the pin as belonging to a missing item.
	_, err = rawGit(ctx, l.cfg.Root, nil, "update-ref", "--no-deref", remoteSyncPinRef(st.ItemID), st.BaseSHA)
	return err
}

func (l *Loop) advanceRemoteSyncStep(ctx context.Context, tx *store.Tx, st remoteSyncState, priorBase string) error {
	if _, err := tx.Exec(`UPDATE item_criteria SET text=? WHERE item_id=? AND criterion_id=?`, remoteSyncCriterionText(st), st.ItemID, RemoteSyncCriterion); err != nil {
		return err
	}
	at := l.cfg.Now().UTC()
	if problem := remoteSyncProtectedPaths(st.ConflictPaths); problem != "" {
		if _, err := item.SetState(ctx, tx, st.ItemID, StateBlocked, at, item.WaitingOn{
			Waiter: "owner", Reason: "rebase conflict onto " + remoteSyncDestination(st) + " includes " + problem,
		}); err != nil {
			return err
		}
	}
	if err := appendEffect(tx, remoteSyncStagedKind, st.ItemID, st, at); err != nil {
		return err
	}
	_, err := rawGit(ctx, l.cfg.Root, nil, "update-ref", "--no-deref", remoteSyncPinRef(st.ItemID), st.BaseSHA, priorBase)
	return err
}

func remoteSyncDestination(st remoteSyncState) string {
	return fmt.Sprintf("%s/%s (%s)", st.Remote, strings.TrimPrefix(st.Ref, "refs/heads/"), shortSHA(st.RemoteTip))
}

func remoteSyncConflictError(st remoteSyncState, who, reason string) *pushStateError {
	destination := remoteSyncDestination(st)
	action := "resolve rebase conflict onto " + destination
	mover, detail := st.ItemID+"; "+action, "; "+action+" on "
	if who != "" {
		mover = "operator; rebase " + TrunkRef + " onto " + destination
		if who == "owner" {
			mover = "owner; resolve " + st.ItemID + " conflict onto " + destination
		}
		detail = " escalated (" + reason + ") on "
	}
	return &pushStateError{
		kind: pushKindConflict, remoteTip: st.RemoteTip, escalated: who,
		detail: pushKindConflict + ": " + st.ItemID + detail + strings.Join(st.ConflictPaths, ", ") + "\nmover: " + mover,
	}
}
