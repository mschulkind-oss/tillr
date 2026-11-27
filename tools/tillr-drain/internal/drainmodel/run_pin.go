package drainmodel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// MaxRefComponent is the longest one path component of a ref name the file system stores.
const MaxRefComponent = 255

const (
	// ItemPinPrefix is the ref namespace that pins each item's newest branch head.
	ItemPinPrefix = "refs/tillr/items/"
	// ItemPinLongPrefix is the fallback ref namespace for item IDs too long or unsafe for a ref component.
	ItemPinLongPrefix = "refs/tillr/items-long/"
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
