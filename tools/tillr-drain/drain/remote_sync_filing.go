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

func remoteSyncSplitFence(all []string) (allowed, gateOnly, unfenceable []string) {
	for _, p := range all {
		switch {
		case p == "":
			continue
		case GateCode(p):
			gateOnly = append(gateOnly, p)
		case strings.ContainsAny(p, "*?\n") || !utf8.ValidString(p):
			unfenceable = append(unfenceable, p)
		default:
			allowed = append(allowed, p)
		}
	}
	return allowed, gateOnly, unfenceable
}

func remoteSyncFenceProblem(gateOnly, unfenceable []string) string {
	var parts []string
	if len(gateOnly) > 0 {
		parts = append(parts, "protected gate code paths ("+strings.Join(gateOnly, ", ")+")")
	}
	if len(unfenceable) > 0 {
		parts = append(parts, "literal paths the fence cannot express ("+strings.Join(unfenceable, ", ")+")")
	}
	return strings.Join(parts, " and ")
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

// fileOrUpdateRemoteSyncItem files the first conflict for a remote tip. Retries
// attach to the original staging facts without moving its pin or changing its fence.
func (l *Loop) fileOrUpdateRemoteSyncItem(ctx context.Context, st remoteSyncState) (*pushStateError, error) {
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, Incompatiblef("%v", err)
	}
	defer s.Close()

	var created bool
	var who, reason string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		previous, found, err := readRemoteSyncState(tx.Reader(), st.ItemID)
		if err != nil {
			return err
		}
		if found {
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
		wait, _, err := item.ReadWaitingOn(tx.Reader(), st.ItemID)
		if wait != nil && wait.Waiter == "owner" {
			who, reason = wait.Waiter, wait.Reason
		}
		return err
	})
	if err != nil {
		return nil, classifyWrite(err)
	}
	if created {
		l.reportf("loop: filed %s for rebase conflict onto %s on %s\n", st.ItemID, remoteSyncDestination(st), strings.Join(st.ConflictPaths, ", "))
	}
	return remoteSyncConflictError(st, who, reason), nil
}

func (l *Loop) fileRemoteSyncStep(ctx context.Context, tx *store.Tx, st remoteSyncState) error {
	allowed, gateOnly, unfenceable := remoteSyncSplitFence(st.ConflictPaths)
	problem := remoteSyncFenceProblem(gateOnly, unfenceable)
	payload, err := json.Marshal(map[string]any{
		"item_id": st.ItemID, "title": "resolve rebase conflict onto " + remoteSyncDestination(st), "state": StateInQueue,
		"allowed_paths": allowed, "rank": TrunkFixerRank, "seat": TrunkFixerSeat, "express": true,
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
