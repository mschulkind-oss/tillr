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

// writeRunPin retains the commit in the root even if its source checkout is
// reset or removed. Import the exact object, without updating delivery refs,
// tags, or FETCH_HEAD. Cleanup can create a missing pin but cannot replace one.
func writeRunPin(ctx context.Context, git func(context.Context, string, ...string) (string, error), root, checkout, runID, sha string, createOnly bool) error {
	if runID == "" || sha == "" {
		return nil
	}
	// A stop must not interrupt preservation; a wedged git still has a bound.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pinTimeout)
	defer cancel()
	ref := pinRef(runID)
	if checkout != "" && checkout != root {
		if _, err := git(ctx, root, "cat-file", "-e", sha+"^{commit}"); err != nil {
			if _, err := git(ctx, root, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--", checkout, sha); err != nil {
				return &runPinError{ref, sha, err}
			}
		}
	}
	args := []string{"update-ref", "--no-deref", ref, sha}
	if createOnly {
		args = append(args, "")
	}
	if _, err := git(ctx, root, args...); err != nil {
		return &runPinError{ref, sha, err}
	}
	return nil
}
