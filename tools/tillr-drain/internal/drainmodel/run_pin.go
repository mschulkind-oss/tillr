package drainmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// MaxRefComponent is the longest one path component of a ref name the file system stores.
const MaxRefComponent = 255

const (
	// ItemPinPrefix is the ref namespace that pins each item's newest branch head.
	ItemPinPrefix = "refs/tillr/items/"
	// ItemPinLongPrefix is the fallback ref namespace for item IDs too long or unsafe for a ref component.
	ItemPinLongPrefix = "refs/tillr/items-long/"
	// NodePinPrefix is the disjoint sibling ref namespace that pins each parallel writer's node commit.
	NodePinPrefix = "refs/tillr/item-nodes/"
	// NodePinLongPrefix is the fallback ref namespace for item IDs too long or unsafe for a ref component.
	NodePinLongPrefix = "refs/tillr/item-nodes-long/"
)

// PinTimeout bounds the fetch and ref write that survive the loop's stop.
const PinTimeout = 30 * time.Second

// PinRef returns the Git ref path that pins a run's candidate commit.
func PinRef(runID string) string {
	if len(runID) <= MaxRefComponent {
		return "refs/tillr/runs/" + runID
	}
	return fmt.Sprintf("refs/tillr/runs-long/%x", sha256.Sum256([]byte(runID)))
}

// ItemPinRef returns the Git ref path that pins an item's most recently pinned head.
// Item IDs that are too long or not safe as a single ref component fall back to
// refs/tillr/items-long/<sha256>, matching PinRef's fallback for long run IDs.
func ItemPinRef(itemID string) string {
	if safeRefComponent(itemID) {
		return ItemPinPrefix + itemID
	}
	return fmt.Sprintf("%s%x", ItemPinLongPrefix, sha256.Sum256([]byte(itemID)))
}

// NodeKey returns a single sanitized ref and path component <sanitized>-<sha256[:8]>
// within MaxRefComponent bytes so expanded node IDs (for example "work/piece-a") never
// introduce '/' or collide with parent item refs.
func NodeKey(nodeID string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(nodeID) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		sanitized = "node"
	}
	const hashSuffixLen = 9 // "-" + 8 hex chars
	if maxPrefix := MaxRefComponent - hashSuffixLen; len(sanitized) > maxPrefix {
		sanitized = strings.TrimRight(sanitized[:maxPrefix], "-")
		if sanitized == "" {
			sanitized = "node"
		}
	}
	sum := sha256.Sum256([]byte(nodeID))
	return sanitized + "-" + hex.EncodeToString(sum[:4])
}

// NodePinRef returns the disjoint Git ref path that pins a parallel writer's node commit.
func NodePinRef(itemID, nodeID string) string {
	if safeRefComponent(itemID) {
		return NodePinPrefix + itemID + "/" + NodeKey(nodeID)
	}
	return fmt.Sprintf("%s%x", NodePinLongPrefix, sha256.Sum256([]byte(itemID+"\x00"+nodeID)))
}

// NodeLaneBranch returns the isolated branch name for a parallel writer's node checkout.
func NodeLaneBranch(scope, itemID, nodeID string) string {
	itemKey := itemID
	if !safeRefComponent(itemKey) {
		itemKey = NodeKey(itemID)
	}
	return "tillr/" + scope + "-nodes/" + itemKey + "/" + NodeKey(nodeID)
}

// NodeDir returns the directory holding one parallel writer's checkout and scratch.
func NodeDir(laneRoot, itemID, nodeID string) string {
	return filepath.Join(laneRoot, itemID, "nodes", NodeKey(nodeID))
}

// NodeWorktreePath returns the isolated worktree path for one parallel writer.
func NodeWorktreePath(laneRoot, itemID, nodeID string) string {
	return filepath.Join(NodeDir(laneRoot, itemID, nodeID), "worktree")
}

// NodeScratchDir returns the node-scoped run scratch directory for one parallel writer.
func NodeScratchDir(laneRoot, itemID, nodeID, runID string) string {
	return filepath.Join(NodeDir(laneRoot, itemID, nodeID), "scratch", runID)
}

// safeRefComponent answers whether name is a valid, single-component Git ref name
// within MaxRefComponent bytes. Rejecting '/' prevents one item's ref from acting as a
// parent directory that collides with another item's ref.
func safeRefComponent(name string) bool {
	if len(name) == 0 || len(name) > MaxRefComponent || name == "@" {
		return false
	}
	if name[0] == '.' || name[len(name)-1] == '.' {
		return false
	}
	if len(name) >= 5 && name[len(name)-5:] == ".lock" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= 0x20 || c == 0x7f {
			return false
		}
		switch c {
		case '/', '~', '^', ':', '?', '*', '[', '\\':
			return false
		case '.':
			if i+1 < len(name) && name[i+1] == '.' {
				return false
			}
		case '@':
			if i+1 < len(name) && name[i+1] == '{' {
				return false
			}
		}
	}
	return true
}

// RunPinError records a pin failure without classifying unrelated Git failures as pin faults.
type RunPinError struct {
	Ref, SHA string
	Err      error
}

func (e *RunPinError) Error() string { return fmt.Sprintf("pinning %s at %s: %v", e.Ref, e.SHA, e.Err) }
func (e *RunPinError) Unwrap() error { return e.Err }

// WriteRunPin retains the commit in the repository that produced it.
// Staging cleanup can create a missing pin but cannot replace one.
func WriteRunPin(ctx context.Context, git func(context.Context, string, ...string) (string, error), repo, runID, sha string, createOnly bool) error {
	if runID == "" || sha == "" {
		return nil
	}
	// A stop must not interrupt preservation; a wedged git still has a bound.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PinTimeout)
	defer cancel()
	ref := PinRef(runID)
	args := []string{"update-ref", "--no-deref", ref, sha}
	if createOnly {
		args = append(args, "")
	}
	if _, err := git(ctx, repo, args...); err != nil {
		return &RunPinError{ref, sha, err}
	}
	return nil
}

// WriteItemPin moves the item's branch-head pin to sha in the repository holding the commit.
func WriteItemPin(ctx context.Context, git func(context.Context, string, ...string) (string, error), repo, itemID, sha string) error {
	if itemID == "" || sha == "" {
		return nil
	}
	// A stop must not interrupt preservation; a wedged git still has a bound.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PinTimeout)
	defer cancel()
	ref := ItemPinRef(itemID)
	if _, err := git(ctx, repo, "update-ref", "--no-deref", ref, sha); err != nil {
		return &RunPinError{ref, sha, err}
	}
	return nil
}

// WriteNodePin moves a parallel writer node's commit pin to sha.
func WriteNodePin(ctx context.Context, git func(context.Context, string, ...string) (string, error), repo, itemID, nodeID, sha string) error {
	if itemID == "" || nodeID == "" || sha == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PinTimeout)
	defer cancel()
	ref := NodePinRef(itemID, nodeID)
	if _, err := git(ctx, repo, "update-ref", "--no-deref", ref, sha); err != nil {
		return &RunPinError{ref, sha, err}
	}
	return nil
}
