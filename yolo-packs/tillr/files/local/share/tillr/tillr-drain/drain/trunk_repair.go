package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

const (
	trunkRepairFile       = "trunk-repair.json"
	repairDeferredLeadIn  = "repair deferred to proof of "
	trunkRewrittenMessage = "trunk was rewritten"
)

type trunkRepairRecord struct {
	Commit string `json:"commit"`
	Reason string `json:"reason,omitempty"`
}

// candidateRebaseConflict is an ordinary candidate whose pre-gate rebase onto a
// repaired trunk conflicted. The rebase is already aborted and the lane is back on
// the candidate's head, so downstream conflict handling never finds a mid-rebase tree.
type candidateRebaseConflict struct {
	ItemID, Tip string
	Reports     []string
	Cause       error
}

func (e *candidateRebaseConflict) Error() string {
	return fmt.Sprintf("rebasing %s onto %s before gate failed: %v", e.ItemID, shortSHA(e.Tip), e.Cause)
}

func asCandidateRebaseConflict(err error) *candidateRebaseConflict {
	var c *candidateRebaseConflict
	if errors.As(err, &c) {
		return c
	}
	return nil
}

// recordTrunkRepair records that the trunk was repaired at sha so candidates cut from
// older bases are rebased onto the current trunk before their landing gate starts
// (landing principle 3: gate what will be the tip, once). Never moves the recorded
// repair backward along trunk ancestry.
func recordTrunkRepair(ctx context.Context, root, sha, reason string) error {
	if root == "" || sha == "" {
		return nil
	}
	resolved, err := heavyGitOutput(ctx, root, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		return err
	}
	dir, err := heavyDirectory(ctx, root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if raw, err := os.ReadFile(filepath.Join(dir, trunkRepairFile)); err == nil {
		var existing trunkRepairRecord
		if json.Unmarshal(raw, &existing) == nil && existing.Commit != "" {
			if existing.Commit == resolved {
				return nil
			}
			if covered, err := trunkAncestor(ctx, root, resolved, existing.Commit); err == nil && covered {
				return nil
			}
		}
	}
	return publishHeavyJSON(dir, trunkRepairFile, trunkRepairRecord{Commit: resolved, Reason: reason})
}

// deferredRepairPrefixes extracts commit prefixes from heavy check details written by
// retainRedRepairs ("repair deferred to proof of <sha>: <reason>"), ignoring rewrites.
func deferredRepairPrefixes(detail string) []string {
	var out []string
	rest := detail
	for {
		_, after, ok := strings.Cut(rest, repairDeferredLeadIn)
		if !ok {
			return out
		}
		prefix, afterColon, hasColon := strings.Cut(after, ":")
		prefix = strings.TrimSpace(prefix)
		reason := strings.TrimSpace(afterColon)
		if hasColon && prefix != "" && !strings.HasPrefix(reason, trunkRewrittenMessage) {
			out = append(out, prefix)
		}
		rest = after
	}
}

// recordedTrunkRepair returns the newest trunk repair commit recorded by the drain
// that is an ancestor of mainSHA, or "" when none is recorded on mainSHA.
func recordedTrunkRepair(ctx context.Context, root, mainSHA string) (string, error) {
	if root == "" || mainSHA == "" {
		return "", nil
	}
	dir, err := heavyDirectory(ctx, root)
	if err != nil {
		return "", err
	}
	var candidates []string
	if raw, err := os.ReadFile(filepath.Join(dir, trunkRepairFile)); err == nil {
		var rec trunkRepairRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Commit != "" {
			candidates = append(candidates, rec.Commit)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "heavy.json")); err == nil {
		var run HeavyRun
		if json.Unmarshal(raw, &run) == nil {
			for _, check := range run.Checks {
				if check.RevertedBy != "" {
					candidates = append(candidates, check.RevertedBy)
				}
				candidates = append(candidates, deferredRepairPrefixes(check.Detail)...)
			}
		}
	}
	newest := ""
	for _, raw := range candidates {
		resolved, err := heavyGitOutput(ctx, root, "rev-parse", "--verify", raw+"^{commit}")
		if err != nil {
			continue
		}
		onTrunk, err := trunkAncestor(ctx, root, resolved, mainSHA)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue
		}
		if !onTrunk {
			continue
		}
		if newest == "" {
			newest = resolved
			continue
		}
		if resolved == newest {
			continue
		}
		newer, err := trunkAncestor(ctx, root, newest, resolved)
		if err == nil && newer {
			newest = resolved
		}
	}
	return newest, nil
}

// preRebaseRepairedTrunk rebases a non-fixer candidate onto the current trunk before
// its landing gate starts when the drain has already recorded that the trunk was
// repaired at a commit strictly newer than the candidate's base. Candidates whose base
// is at or after the repair are left untouched so throughput does not depend on
// unrelated trunk movement (landing principle 3).
func (l *Loop) preRebaseRepairedTrunk(ctx context.Context, candidate Candidate) (Candidate, error) {
	if l.nonLanding || l.cfg.Root == "" || candidate.Worktree == "" || candidate.BaseSHA == "" {
		return candidate, nil
	}
	if IsTrunkFixer(candidate.ItemID) || IsRemoteSyncItem(candidate.ItemID) {
		return candidate, nil
	}
	if candidate.BaseRef != "" || l.laneForkTarget(candidate.LaneID) != "" {
		return candidate, nil
	}
	mainSHA, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil || mainSHA == "" || mainSHA == candidate.BaseSHA {
		return candidate, err
	}
	repairedSHA, err := recordedTrunkRepair(ctx, l.cfg.Root, mainSHA)
	if err != nil || repairedSHA == "" || repairedSHA == candidate.BaseSHA {
		return candidate, err
	}
	older, err := trunkAncestor(ctx, l.cfg.Root, candidate.BaseSHA, repairedSHA)
	if err != nil || !older {
		return candidate, err
	}
	if _, err := l.git(ctx, candidate.Worktree, "rebase", "--onto", mainSHA, candidate.BaseSHA); err != nil {
		return candidate, l.abortCandidateRebase(ctx, candidate, mainSHA, err)
	}
	head, err := l.revParse(ctx, candidate.Worktree, "HEAD")
	if err != nil {
		return candidate, err
	}
	oldBase := candidate.BaseSHA
	candidate.BaseSHA = mainSHA
	candidate.HeadSHA = head
	if candidate.LaneID != "" && l.cfg.LaneRoot != "" {
		laneDir := l.laneDirPath(candidate.LaneID)
		for _, name := range []string{BaseSHAFile, ItemBaseSHAFile} {
			path := filepath.Join(laneDir, name)
			if _, statErr := os.Stat(path); statErr == nil {
				if err := os.WriteFile(path, []byte(mainSHA+"\n"), 0o644); err != nil {
					return candidate, Externalf("recording lane %s's base: %v", candidate.LaneID, err)
				}
			}
		}
	}
	if l.cfg.Store != "" {
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return candidate, err
		}
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			return appendEffect(tx, candidatePreparedKind, candidate.RunID, candidate, l.cfg.Now().UTC())
		})
		s.Close()
		if err != nil {
			return candidate, err
		}
	}
	l.reportf("loop: pre-rebased %s onto %s before gate (trunk repaired at %s, newer than base %s)\n",
		candidate.ItemID, shortSHA(mainSHA), shortSHA(repairedSHA), shortSHA(oldBase))
	return candidate, nil
}

func (l *Loop) abortCandidateRebase(ctx context.Context, candidate Candidate, tip string, cause error) error {
	unmerged, _ := l.git(ctx, candidate.Worktree, "diff", "--name-only", "--diff-filter=U")
	reports := []string{}
	for _, line := range strings.Split(unmerged, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			reports = append(reports, "CONFLICT (content): Merge conflict in "+path)
		}
	}
	_, _ = l.git(ctx, candidate.Worktree, "rebase", "--abort")
	if candidate.HeadSHA != "" {
		if _, err := l.git(ctx, candidate.Worktree, "reset", "-q", "--hard", candidate.HeadSHA); err != nil {
			return Externalf("restoring %s's lane after a failed pre-gate rebase: %v (rebase: %v)", candidate.ItemID, err, cause)
		}
	}
	return &candidateRebaseConflict{ItemID: candidate.ItemID, Tip: tip, Reports: reports, Cause: cause}
}
