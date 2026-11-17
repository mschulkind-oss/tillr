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
