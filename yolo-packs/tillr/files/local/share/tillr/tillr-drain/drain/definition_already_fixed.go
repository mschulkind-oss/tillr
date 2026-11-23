package drain

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

var (
	alreadyFixedPhrase = regexp.MustCompile(`(?i)\balready\s+(?:fixed|fixes|resolved|resolves|landed|implemented|implements|covered|covers|addressed|addresses|applied|patched|present|there|on\s+(?:the\s+)?(?:base|trunk|main))\b`)
	fixAlreadyPhrase   = regexp.MustCompile(`(?i)\b(?:fix|change|patch|work)\s+(?:is\s+)?already\s+(?:in|on|present|there|applied|landed|merged|committed)\b`)
	hexCommitSpan      = regexp.MustCompile(`[0-9a-fA-F]{7,40}`)
)

// evidenceClaimsAlreadyFixed reports whether a definition obstacle's EVIDENCE
// states that the requested fix is already present on the base. It excludes
// plan-wrong obstacles and unrelated tree-contradiction evidence (such as a
// commit that removed a flag).
func evidenceClaimsAlreadyFixed(evidence string) bool {
	trimmed := strings.TrimSpace(evidence)
	if trimmed == "" || strings.HasPrefix(strings.ToLower(trimmed), "plan-wrong:") {
		return false
	}
	return alreadyFixedPhrase.MatchString(trimmed) || fixAlreadyPhrase.MatchString(trimmed)
}

func isCommitBoundaryWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

// evidenceCommits extracts candidate git commit hashes (7-40 hex characters)
// from obstacle evidence text, skipping hex substrings embedded in hyphenated
// or underscored identifiers such as item-request-3a0ae4fd31.
func evidenceCommits(evidence string) []string {
	matches := hexCommitSpan.FindAllStringIndex(evidence, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var out []string
	for _, m := range matches {
		start, end := m[0], m[1]
		if start > 0 && isCommitBoundaryWordChar(evidence[start-1]) {
			continue
		}
		if end < len(evidence) && isCommitBoundaryWordChar(evidence[end]) {
			continue
		}
		sha := strings.ToLower(evidence[start:end])
		if seen[sha] {
			continue
		}
		seen[sha] = true
		out = append(out, sha)
	}
	return out
}

func (l *Loop) definitionObstacleBase(d Decision) (repo, baseSHA string) {
	if d.LaneID != "" && l.laneForkTarget(d.LaneID) != "" {
		baseSHA = l.laneBase(d.LaneID)
		if baseSHA == "" {
			baseSHA = d.NodeBaseSHA
		}
		return l.laneTarget(d.LaneID), baseSHA
	}
	baseSHA = d.NodeBaseSHA
	if baseSHA == "" && d.LaneID != "" {
		baseSHA = l.laneBase(d.LaneID)
	}
	return l.cfg.Root, baseSHA
}

// definitionAlreadyFixedCommit checks whether a definition obstacle's evidence
// claims the requested fix is already on the run's base and names a commit that
// is an ancestor of that base. When both the base commit and a strict ancestor
// commit are named, it prefers the strict ancestor.
func (l *Loop) definitionAlreadyFixedCommit(ctx context.Context, d Decision, obs Obstacle) (string, error) {
	if l.nonLanding {
		return "", nil
	}
	evidence := obs.Field("EVIDENCE")
	if !evidenceClaimsAlreadyFixed(evidence) {
		return "", nil
	}
	candidates := evidenceCommits(evidence)
	if len(candidates) == 0 {
		return "", nil
	}
	repo, baseSHA := l.definitionObstacleBase(d)
	if repo == "" || baseSHA == "" {
		return "", nil
	}
	resolvedBase, err := resolveCommit(ctx, repo, baseSHA)
	if err != nil || resolvedBase == "" {
		return "", err
	}
	fallback := ""
	for _, cand := range candidates {
		full, err := resolveCommit(ctx, repo, cand)
		if err != nil {
			return "", err
		}
		if full == "" {
			continue
		}
		ok, err := containsCommit(ctx, repo, full, resolvedBase)
		if err != nil {
			return "", err
		}
		if !ok {
			continue
		}
		if full != resolvedBase {
			return full, nil
		}
		if fallback == "" {
			fallback = full
		}
	}
	return fallback, nil
}

// resolveDefinitionAlreadyFixed resolves an item as resolved-elsewhere when a
// worker's definition obstacle names a commit that is an ancestor of the run's
// base and says the requested fix is already there, instead of holding the item
// for the design seat.
func (l *Loop) resolveDefinitionAlreadyFixed(ctx context.Context, d Decision, obs Obstacle) (bool, error) {
	commitSHA, err := l.definitionAlreadyFixedCommit(ctx, d, obs)
	if err != nil || commitSHA == "" {
		return false, err
	}
	runID := ""
	if d.RunRequest != nil {
		runID = d.RunRequest.RunID
	}
	reason := fmt.Sprintf("already fixed on base by commit %s", shortSHA(commitSHA))
	if runID != "" {
		reason = fmt.Sprintf("already fixed on base by commit %s (definition obstacle from %s)", shortSHA(commitSHA), runID)
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), d.ItemID, "blocked-report"); err != nil {
			return err
		}
		_, err := item.SetStateWithReceipt(ctx, tx, d.ItemID, StateResolvedElsewhere, at, item.MoveReceipt{
			ResolvesVia: "commit:" + commitSHA,
			Reason:      reason,
			By:          Utility,
		})
		if rejection, ok := item.RejectionOf(err); ok && rejection.Rule == "already-landed" {
			_, err = item.SetState(ctx, tx, d.ItemID, StateLanded, at)
		}
		if err != nil {
			return err
		}
		return endItemUnfinishedRunsInTx(ctx, tx, d.ItemID, runID, at)
	}); err != nil {
		return false, err
	}
	l.reportf("loop: %s resolved elsewhere via %s: %s\n", d.ItemID, shortSHA(commitSHA), reason)
	return true, nil
}
