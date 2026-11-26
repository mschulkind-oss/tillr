package drainmodel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// MaxRefComponent is the longest one path component of a ref name the file system stores.
const MaxRefComponent = 255

// PinTimeout bounds the fetch and ref write that survive the loop's stop.
const PinTimeout = 30 * time.Second

// PinRef returns the Git ref path that pins a run's candidate commit.
func PinRef(runID string) string {
	if len(runID) <= MaxRefComponent {
		return "refs/tillr/runs/" + runID
	}
	return fmt.Sprintf("refs/tillr/runs-long/%x", sha256.Sum256([]byte(runID)))
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
