package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/preserved"
)

// forkCheckoutUnretainedError is a staging fault the operator can repair, not
// an infrastructure failure that another attempt could resolve.
type forkCheckoutUnretainedError struct {
	checkout, head, tip string
}

func (e *forkCheckoutUnretainedError) Error() string {
	return fmt.Sprintf("fork checkout %s has unretained commits at %s; staging will not reset it to %s. Preserve the commits on a branch and reset the checkout to the delivered tip", e.checkout, e.head, e.tip)
}

// refreshForkCheckout makes a reused writer start at the delivered tip. It can
// move a clean lane's main away from an earlier attempt only if the fork keeps it.
func (s *LaneStager) refreshForkCheckout(ctx context.Context, fork ForkTarget) error {
	tip, err := s.git(ctx, s.Root, "rev-parse", "--verify", fork.Ref+"^{commit}")
	if err != nil {
		return err
	}
	tip = strings.TrimSpace(tip)
	// Refuse Git's parent-repo discovery and shared Git directories before
	// fetching or moving any refs. This operation is for the lane clone only.
	checkout, err := filepath.Abs(fork.Checkout)
	if err != nil {
		return err
	}
	gitDir := filepath.Join(checkout, ".git")
	real, err := filepath.EvalSymlinks(gitDir)
	if err != nil || real != gitDir {
		return Externalf("fork checkout %s is missing its own Git directory or aliases another checkout", fork.Checkout)
	}
	layout, err := s.git(ctx, fork.Checkout, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return err
	}
	if strings.TrimSpace(layout) != checkout+"\n"+gitDir {
		return Externalf("fork checkout %s is not an isolated checkout", fork.Checkout)
	}
	status, err := s.git(ctx, fork.Checkout, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return Externalf("fork checkout %s has uncommitted work; staging will not overwrite it", fork.Checkout)
	}
	head, err := s.git(ctx, fork.Checkout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head = strings.TrimSpace(head)
	if head == tip {
		return nil
	}
	if _, err := s.git(ctx, fork.Checkout, "fetch", "--quiet", "--no-tags", s.Root, tip); err != nil {
		return err
	}
	undelivered, err := s.git(ctx, fork.Checkout, "rev-list", "--max-count=1", tip+".."+head)
	if err != nil {
		return err
	}
	if strings.TrimSpace(undelivered) == "" {
		_, err := s.git(ctx, fork.Checkout, "-c", "merge.autoStash=false", "merge", "--ff-only", "--no-edit", tip)
		return err
	}
	// A ref in this checkout that contains HEAD keeps its entire history. A
	// pin for just an ancestor is not enough, nor is a pin in another repo.
	refs, err := s.git(ctx, fork.Checkout, "for-each-ref", "--contains", head, "--count=1", "--format=%(refname)",
		"refs/tillr/runs/", "refs/tillr/runs-long/", preserved.Prefix)
	if err != nil {
		return err
	}
	oldSeries, err := s.wasDeliveredForkHead(ctx, fork.Ref, head)
	if err != nil {
		return err
	}
	if strings.TrimSpace(refs) == "" && !oldSeries {
		return &forkCheckoutUnretainedError{checkout: fork.Checkout, head: head, tip: tip}
	}
	// Do not replace a main we did not inspect (e.g. a detached rebase HEAD).
	branch, err := s.git(ctx, fork.Checkout, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(branch) != "refs/heads/main" {
		return Externalf("fork checkout %s is on %s, not main; staging will not reset it", fork.Checkout, strings.TrimSpace(branch))
	}
	if oldSeries {
		keep := "refs/heads/kept/pre-rebase-" + head
		if _, err := s.git(ctx, fork.Checkout, "update-ref", "--no-deref", keep, head, ""); err != nil {
			// An earlier attempt may have saved the branch but failed to switch.
			// Reuse only the same direct ref; never overwrite another branch.
			out, readErr := s.git(ctx, fork.Checkout, "for-each-ref", "--format=%(refname) %(objectname) %(symref)", keep)
			if readErr != nil || strings.TrimSpace(out) != keep+" "+head {
				return err
			}
		}
	}
	_, err = s.git(ctx, fork.Checkout, "switch", "--quiet", "--no-overwrite-ignore", "-C", "main", tip)
	return err
}

// The lander pins this fork's old tip in the same transaction that delivers
// a release rebase. If an old tip contains HEAD, it proves the entire history
// we would leave behind was delivered, even if this lane missed several moves.
func (s *LaneStager) wasDeliveredForkHead(ctx context.Context, ref, head string) (bool, error) {
	return wasDeliveredForkHead(ctx, s.git, s.Root, ref, head)
}

func wasDeliveredForkHead(ctx context.Context, git func(context.Context, string, ...string) (string, error), root, ref, head string) (bool, error) {
	name, ok := strings.CutPrefix(ref, forkPatchPrefix)
	if !ok || name == "" || strings.Contains(name, "/") {
		return false, nil
	}
	// A private lane commit need not exist in the delivery repository at all.
	if _, err := git(ctx, root, "--no-replace-objects", "cat-file", "-e", head+"^{commit}"); err != nil {
		return false, ctx.Err()
	}
	refs, err := git(ctx, root, "--no-replace-objects", "for-each-ref", "--contains", head,
		"--count=1", "--format=%(refname)", "refs/tillr/patch-history/"+name+"/")
	return strings.TrimSpace(refs) != "", err
}

// restageForkAtKeptHead cuts a fork checkout at the retained candidate rather than
// the delivery's tip.
// Uncommitted work is refused, as in refreshForkCheckout. A checkout whose own HEAD holds a
// commit no pin, landing or the delivery's tip contains is refused too, because resetting
// would delete the only copy of unjudged work.
func (s *LaneStager) restageForkAtKeptHead(ctx context.Context, spec StageSpec, fork ForkTarget) error {
	status, err := s.git(ctx, fork.Checkout, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return Externalf("fork checkout %s has uncommitted work; staging will not overwrite it", fork.Checkout)
	}
	headOut, err := s.git(ctx, fork.Checkout, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	head := strings.TrimSpace(headOut)
	if head != spec.KeptHeadSHA {
		tip, err := s.git(ctx, s.Root, "rev-parse", "--verify", fork.Ref+"^{commit}")
		if err != nil {
			return err
		}
		if _, err := s.git(ctx, fork.Checkout, "fetch", "--quiet", "--no-tags", s.Root, strings.TrimSpace(tip)); err != nil {
			return err
		}
		if _, err := s.git(ctx, fork.Checkout, "merge-base", "--is-ancestor", head, strings.TrimSpace(tip)); err != nil {
			clearable, cerr := s.clearableLaneCopy(ctx, spec.LaneID, spec.ItemID, head, fork.Checkout)
			if cerr != nil {
				return cerr
			}
			if !clearable {
				return Externalf("fork checkout %s has unlanded or divergent commits; staging will not reset it to %s", fork.Checkout, spec.KeptHeadSHA)
			}
		}
	}
	if _, err := s.git(ctx, fork.Checkout, "cat-file", "-e", spec.KeptHeadSHA+"^{commit}"); err != nil {
		source := s.keptForkSource(ctx, spec, fork)
		if _, err := s.git(ctx, fork.Checkout, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", source, spec.KeptHeadSHA); err != nil {
			return err
		}
	}
	if _, err := s.git(ctx, fork.Checkout, "checkout", "-q", "-B", "main", spec.KeptHeadSHA); err != nil {
		return err
	}
	_, err = s.git(ctx, fork.Checkout, "reset", "--hard", "-q", spec.KeptHeadSHA)
	return err
}

// A returned candidate may be restaged on a cold lane. The worker session
// names its original lane; its fork checkout retains the run pin even when
// that lane now runs another item.
func (s *LaneStager) keptForkSource(ctx context.Context, spec StageSpec, fork ForkTarget) string {
	var session *WorkerSession
	if spec.Returned != nil {
		session = spec.Returned.WorkerSession
	} else if spec.RetryCandidate != nil {
		session = spec.RetryCandidate.WorkerSession
	}
	if session != nil {
		if source, err := forkTargetAtLane(s.Root, fork.Ref, session.LaneID); err == nil {
			if _, err := s.git(ctx, source.Checkout, "cat-file", "-e", spec.KeptHeadSHA+"^{commit}"); err == nil {
				return source.Checkout
			}
		}
	}
	return s.Root // Also supports candidates pinned before fork-local pins.
}

// Only a settlement that already preserved the worker's state may call this.
// Staging itself still refuses unowned dirty or divergent checkouts.
func (l *Loop) restoreForkDelivery(ctx context.Context, lane, runID string) error {
	target := l.laneForkTarget(lane)
	if target == "" || runID == "" {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	if err := l.checkForkRepository(ctx, target); err != nil {
		return err
	}
	tip, err := l.revParse(ctx, l.cfg.Root, l.lanePatchRef(lane)+"^{commit}")
	if err != nil {
		return err
	}
	if _, err := l.git(ctx, target, "cat-file", "-e", tip+"^{commit}"); err != nil {
		if _, err := l.git(ctx, target, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", l.cfg.Root, tip); err != nil {
			return err
		}
	}
	return l.resetForkWork(ctx, target, runID, tip)
}

func (l *Loop) forkRebaseDir(ctx context.Context, target string) (string, error) {
	paths, err := l.git(ctx, target, "rev-parse", "--path-format=absolute", "--git-path", "rebase-merge", "--git-path", "rebase-apply")
	if err != nil {
		return "", err
	}
	for _, path := range strings.Split(strings.TrimSpace(paths), "\n") {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

func (l *Loop) resetForkWork(ctx context.Context, target, runID, revision string) error {
	if err := l.checkForkRepository(ctx, target); err != nil {
		return err
	}
	rebase, err := l.forkRebaseDir(ctx, target)
	if err != nil {
		return err
	}
	if rebase != "" {
		// HEAD is detached during a rebase. Pin both the replayed prefix and
		// the original tip before removing the sequencer state or moving main.
		head, err := l.revParse(ctx, target, "HEAD")
		if err != nil {
			return err
		}
		original, err := os.ReadFile(filepath.Join(rebase, "orig-head"))
		if err != nil {
			return err
		}
		if runID == "" {
			return Externalf("cannot clear the rebase in %s without an owning run", target)
		}
		if err := writeRunPin(ctx, l.git, target, runID, head, false); err != nil {
			return err
		}
		originalHead := strings.TrimSpace(string(original))
		if originalHead == "" {
			return Externalf("cannot clear the rebase in %s: orig-head is empty", target)
		}
		if err := writeRunPin(ctx, l.git, target, runID+"-pre-rebase", originalHead, false); err != nil {
			return err
		}
		// Unlike --abort, --quit does not apply a pre-rebase autostash. Git
		// keeps it in the stash list rather than mixing it into cleanup.
		if _, err := l.git(ctx, target, "rebase", "--quit"); err != nil {
			return err
		}
	}
	// Clean with the worker's ignore rules before restoring tracked files.
	if _, err := l.git(ctx, target, "clean", "-fd"); err != nil {
		return err
	}
	if _, err := l.git(ctx, target, "reset", "--hard", "HEAD"); err != nil {
		return err
	}
	_, err = l.git(ctx, target, "checkout", "--no-overwrite-ignore", "-B", "main", revision)
	return err
}
