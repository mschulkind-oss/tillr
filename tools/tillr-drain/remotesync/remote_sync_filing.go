package remotesync

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

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// RemoteSyncItemPrefix identifies generated remote-sync repairs.
	RemoteSyncItemPrefix = "sync-remote-"
	// RemoteSyncCriterion is the one criterion carried by a remote-sync repair.
	RemoteSyncCriterion = "remote-sync-resolved"
	// RemoteSyncRefPrefix is the pin ref namespace for staged remote-sync repairs.
	RemoteSyncRefPrefix = "refs/tillr/sync-remote/"
	// RemoteSyncStagedKind is the drain event kind for staged remote-sync state.
	RemoteSyncStagedKind = "drain.remote-sync-staged"
	// PushKindConflict is the push event kind for active remote-sync conflicts.
	PushKindConflict = "remote-sync-conflict"
)

func init() {
	if err := event.Register(RemoteSyncStagedKind, Utility); err != nil {
		panic(err)
	}
	if err := event.Register(RemoteSyncReplayKind, Utility); err != nil {
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

// RemoteSyncPinRef returns the pin ref path for a remote-sync item.
func RemoteSyncPinRef(itemID string) string {
	return RemoteSyncRefPrefix + itemID
}

// RemoteSyncState records the staged conflict state for a remote-sync repair item.
type RemoteSyncState struct {
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

// ParseMergeTreeConflict reads `merge-tree --write-tree --name-only -z`:
// tree OID, literal paths, an empty field, then (path count, paths, type, message)
// records. Only the messages are display text; paths must survive unchanged.
func ParseMergeTreeConflict(stdout string) (paths []string, conflictText string) {
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

// RemoteSyncProtectedPaths reports conflicts that still need the owner: a repair
// must not change its own gate, and recording a non-UTF-8 name in the store's JSON
// would lose its identity.
func RemoteSyncProtectedPaths(paths []string, isGateCode func(string) bool) string {
	var gatePaths, invalidNames []string
	for _, p := range paths {
		if isGateCode != nil && isGateCode(p) {
			gatePaths = append(gatePaths, p)
		}
		if !utf8.ValidString(p) {
			invalidNames = append(invalidNames, strings.ToValidUTF8(p, "\ufffd"))
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

// RemoteSyncCriterionText formats the criterion description for a staged conflict.
func RemoteSyncCriterionText(st RemoteSyncState) string {
	branch := strings.TrimPrefix(st.Ref, "refs/heads/")
	return fmt.Sprintf("resolve rebase conflict of %s onto %s/%s (%s) atop %s on %s with one linear commit (zero merge parents) leaving all non-conflicting paths identical to the three-way merge",
		shortSHA(st.ConflictCommit), st.Remote, branch, shortSHA(st.RemoteTip), shortSHA(st.BaseSHA), strings.Join(st.ConflictPaths, ", "))
}

// ReadRemoteSyncStateAt reads the latest staged state for itemID from storePath.
func ReadRemoteSyncStateAt(ctx context.Context, storePath, itemID string) (st RemoteSyncState, found bool, err error) {
	if storePath == "" {
		return st, false, nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return st, false, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		st, found, err = ReadRemoteSyncState(r, itemID)
		return err
	})
	return st, found, err
}

// ReadRemoteSyncState reads the latest staged state for itemID within a read tx.
func ReadRemoteSyncState(r *store.Reader, itemID string) (RemoteSyncState, bool, error) {
	var st RemoteSyncState
	var raw string
	err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, RemoteSyncStagedKind, itemID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	err = json.Unmarshal([]byte(raw), &st)
	return st, err == nil, err
}

// RemoteSyncDispatchBase returns the staged base SHA for itemID.
func RemoteSyncDispatchBase(r *store.Reader, itemID string) (string, error) {
	st, ok, err := ReadRemoteSyncState(r, itemID)
	if err != nil {
		return "", err
	}
	if !ok || st.BaseSHA == "" {
		return "", fmt.Errorf("remote-sync item %s has no staged base SHA", itemID)
	}
	return st.BaseSHA, nil
}

// RemoteSyncBriefFacts renders the context recorded when the repair was filed.
func RemoteSyncBriefFacts(ctx context.Context, storePath, itemID string) (string, error) {
	st, ok, err := ReadRemoteSyncStateAt(ctx, storePath, itemID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("remote-sync item %s has no staged conflict context", itemID)
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

// RemoteSyncDestination formats the destination remote/branch (short-sha) string.
func RemoteSyncDestination(st RemoteSyncState) string {
	return fmt.Sprintf("%s/%s (%s)", st.Remote, strings.TrimPrefix(st.Ref, "refs/heads/"), shortSHA(st.RemoteTip))
}

// RemoteSyncConflictError formats the conflict detail string for push state reporting.
func RemoteSyncConflictError(st RemoteSyncState, trunkRef, who, reason string) string {
	destination := RemoteSyncDestination(st)
	action := "resolve rebase conflict onto " + destination
	mover, detail := st.ItemID+"; "+action, "; "+action+" on "
	if who != "" {
		mover = "operator; rebase " + trunkRef + " onto " + destination
		if who == "owner" {
			mover = "owner; resolve " + st.ItemID + " conflict onto " + destination
		}
		detail = " escalated (" + reason + ") on "
	}
	return PushKindConflict + ": " + st.ItemID + detail + strings.Join(st.ConflictPaths, ", ") + "\nmover: " + mover
}
