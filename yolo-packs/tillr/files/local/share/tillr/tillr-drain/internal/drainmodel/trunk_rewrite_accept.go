package drainmodel

import (
	"context"

	"github.com/mschulkind-oss/tillr-drain/fixer"
)

// RewriteAcceptedKind records an operator accepting a trunk rewrite that kept the content.
const RewriteAcceptedKind = fixer.RewriteAcceptedKind

// AcceptedTrunkPrefix pins each accepted lost tip, so the history the operator replaced
// stays readable after the lost ref is cleared and reflogs expire.
const AcceptedTrunkPrefix = fixer.AcceptedTrunkPrefix

// AcceptRewriteRequest names the rewrite an operator accepts and why.
type AcceptRewriteRequest = fixer.AcceptRewriteRequest

// RewriteAcceptance is what an accepted rewrite recorded.
type RewriteAcceptance = fixer.RewriteAcceptance

// AcceptRewrite clears a trunk-rewritten hold for a rewrite that kept the content.
func AcceptRewrite(ctx context.Context, req AcceptRewriteRequest) (RewriteAcceptance, error) {
	a, err := fixer.AcceptRewrite(ctx, req)
	return a, WrapFixerError(err)
}
