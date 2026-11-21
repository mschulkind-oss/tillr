package drain

import (
	"context"
	"fmt"
)

// A run-pin fault belongs to the run that failed, not to the loop. Preserve
// the underlying Git error for diagnosis without classifying unrelated Git
// failures as pin faults.
type runPinError struct {
	ref, sha string
	err      error
}

func (e *runPinError) Error() string { return fmt.Sprintf("pinning %s at %s: %v", e.ref, e.sha, e.err) }
func (e *runPinError) Unwrap() error { return e.err }

// writeRunPin retains the commit in the repository that produced it.
// Staging cleanup can create a missing pin but cannot replace one.
func writeRunPin(ctx context.Context, git func(context.Context, string, ...string) (string, error), repo, runID, sha string, createOnly bool) error {
	if runID == "" || sha == "" {
		return nil
	}
	// A stop must not interrupt preservation; a wedged git still has a bound.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinTimeout)
	defer cancel()
	ref := pinRef(runID)
	args := []string{"update-ref", "--no-deref", ref, sha}
	if createOnly {
		args = append(args, "")
	}
	if _, err := git(ctx, repo, args...); err != nil {
		return &runPinError{ref, sha, err}
	}
	return nil
}
