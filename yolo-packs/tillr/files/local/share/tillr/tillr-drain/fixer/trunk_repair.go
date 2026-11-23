package fixer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
)

const (
	// TrunkRepairFile stores the latest recorded trunk repair commit.
	TrunkRepairFile       = "trunk-repair.json"
	repairDeferredLeadIn  = "repair deferred to proof of "
	trunkRewrittenMessage = "trunk was rewritten"
)

// TrunkRepairRecord is the JSON payload stored in TrunkRepairFile.
type TrunkRepairRecord struct {
	Commit string `json:"commit"`
	Reason string `json:"reason,omitempty"`
}

// RecordTrunkRepair records that the trunk was repaired at sha so candidates cut from
// older bases are rebased onto the current trunk before their landing gate starts
// (landing principle 3: gate what will be the tip, once). Never moves the recorded
// repair backward along trunk ancestry.
func RecordTrunkRepair(ctx context.Context, root, sha, reason string) error {
	if root == "" || sha == "" {
		return nil
	}
	resolved, err := heavy.GitOutput(ctx, root, "rev-parse", "--verify", sha+"^{commit}")
	if err != nil {
		return err
	}
	dir, err := heavy.Directory(ctx, root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if raw, err := os.ReadFile(filepath.Join(dir, TrunkRepairFile)); err == nil {
		var existing TrunkRepairRecord
		if json.Unmarshal(raw, &existing) == nil && existing.Commit != "" {
			if existing.Commit == resolved {
				return nil
			}
			if covered, err := TrunkAncestor(ctx, root, resolved, existing.Commit); err == nil && covered {
				return nil
			}
		}
	}
	return PublishHeavyJSON(dir, TrunkRepairFile, TrunkRepairRecord{Commit: resolved, Reason: reason})
}

// DeferredRepairPrefixes extracts commit prefixes from heavy check details written by
// retainRedRepairs ("repair deferred to proof of <sha>: <reason>"), ignoring rewrites.
func DeferredRepairPrefixes(detail string) []string {
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

// RecordedTrunkRepair returns the newest trunk repair commit recorded by the drain
// that is an ancestor of mainSHA, or "" when none is recorded on mainSHA.
func RecordedTrunkRepair(ctx context.Context, root, mainSHA string) (string, error) {
	if root == "" || mainSHA == "" {
		return "", nil
	}
	dir, err := heavy.Directory(ctx, root)
	if err != nil {
		return "", err
	}
	var candidates []string
	if raw, err := os.ReadFile(filepath.Join(dir, TrunkRepairFile)); err == nil {
		var rec TrunkRepairRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Commit != "" {
			candidates = append(candidates, rec.Commit)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "heavy.json")); err == nil {
		var run heavy.HeavyRun
		if json.Unmarshal(raw, &run) == nil {
			for _, check := range run.Checks {
				if check.RevertedBy != "" {
					candidates = append(candidates, check.RevertedBy)
				}
				candidates = append(candidates, DeferredRepairPrefixes(check.Detail)...)
			}
		}
	}
	newest := ""
	for _, raw := range candidates {
		resolved, err := heavy.GitOutput(ctx, root, "rev-parse", "--verify", raw+"^{commit}")
		if err != nil {
			continue
		}
		onTrunk, err := TrunkAncestor(ctx, root, resolved, mainSHA)
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
		newer, err := TrunkAncestor(ctx, root, newest, resolved)
		if err == nil && newer {
			newest = resolved
		}
	}
	return newest, nil
}
