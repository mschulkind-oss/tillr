package drain

import (
	"context"
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
	if _, err := s.git(ctx, fork.Checkout, "fetch", "--quiet", "--no-tags", s.Root, spec.KeptHeadSHA); err != nil {
		return err
	}
	if _, err := s.git(ctx, fork.Checkout, "checkout", "-q", "-B", "main", spec.KeptHeadSHA); err != nil {
		return err
	}
	_, err = s.git(ctx, fork.Checkout, "reset", "--hard", "-q", spec.KeptHeadSHA)
	return err
}
