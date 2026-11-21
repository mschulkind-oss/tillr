package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// refreshForkCheckout makes a reused writer start at the delivered tip. It never
// resets unlanded commits or dirty work; either would destroy another candidate.
func (s *LaneStager) refreshForkCheckout(ctx context.Context, fork ForkTarget) error {
	tip, err := s.git(ctx, s.Root, "rev-parse", "--verify", fork.Ref+"^{commit}")
	if err != nil {
		return err
	}
	tip = strings.TrimSpace(tip)
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
	if strings.TrimSpace(head) == tip {
		return nil
	}
	if _, err := s.git(ctx, fork.Checkout, "fetch", "--quiet", "--no-tags", s.Root, tip); err != nil {
		return err
	}
	if _, err := s.git(ctx, fork.Checkout, "merge-base", "--is-ancestor", "HEAD", tip); err != nil {
		return Externalf("fork checkout %s has unlanded or divergent commits; staging will not reset it to %s", fork.Checkout, tip)
	}
	_, err = s.git(ctx, fork.Checkout, "-c", "merge.autoStash=false", "merge", "--ff-only", "--no-edit", tip)
	return err
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
