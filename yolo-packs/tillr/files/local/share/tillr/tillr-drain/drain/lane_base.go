package drain

import (
	"context"
	"database/sql"
	"os/exec"

	"github.com/mschulkind-oss/tillr/store"
)

// ReasonTrunkUnproved means a finished blocker's landing is not yet contained in
// refs/tillr/green. The blocker is done; proof of the trunk is what is pending.
const ReasonTrunkUnproved = "trunk-unproved"

func containsCommit(ctx context.Context, root, ancestor, tip string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", ancestor, tip)
	raw, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, Externalf("checking lane base ancestry: %v: %s", err, raw)
}

// provedLaneBase returns empty only for the documented bootstrap case or tip
// policy. A malformed or rewritten pointer is an error, never a silent fallback.
func provedLaneBase(ctx context.Context, root, mode string) (string, error) {
	if mode == "tip" || root == "" {
		return "", nil
	}
	green, err := heavyGreen(ctx, root)
	if err != nil || green == "" {
		return green, err
	}
	tip, err := heavyGitOutput(ctx, root, "rev-parse", TrunkRef)
	if err != nil {
		return "", err
	}
	ok, err := containsCommit(ctx, root, green, tip)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", Externalf("%s is not an ancestor of trunk; trunk rewritten", GreenRef)
	}
	return green, nil
}

func hasLandedBlocker(states map[string]string, edges map[string][]string) bool {
	for id, blockers := range edges {
		if IsTrunkFixer(id) {
			continue
		}
		for _, blocker := range blockers {
			if states[blocker] == StateLanded {
				return true
			}
		}
	}
	return false
}

func unprovedBlockers(ctx context.Context, r *store.Reader, root, green string, states map[string]string, edges map[string][]string) (map[string]bool, error) {
	waits := map[string]bool{}
	if green == "" {
		return waits, nil
	}
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')`).Scan(&exists); err != nil {
		return nil, err
	}
	proved := map[string]bool{}
	for id, blockers := range edges {
		if IsTrunkFixer(id) {
			continue
		}
		for _, blocker := range blockers {
			if states[blocker] != StateLanded {
				continue
			}
			ok, seen := proved[blocker]
			if !seen {
				var sha string
				if exists {
					err := r.QueryRow(`SELECT trunk_after FROM landings WHERE item_id=? AND result='landed' ORDER BY event_seq DESC LIMIT 1`, blocker).Scan(&sha)
					if err != nil && err != sql.ErrNoRows {
						return nil, err
					}
				}
				if sha != "" {
					// A fork landing delivers an already judged commit to its patch
					// branch, not to main. Its recorded trunk_after must be retained
					// by that delivery branch; main's green pointer cannot contain it.
					var fence string
					if err := r.QueryRow(`SELECT COALESCE(allowed_paths, '') FROM items WHERE item_id=?`, blocker).Scan(&fence); err != nil {
						return nil, err
					}
					allowed, err := decodeFence(fence)
					if err != nil {
						return nil, err
					}
					target := green
					if fork := forkOf(allowed); fork != "" {
						target = "refs/heads/patch/" + fork
					}
					ok, err = containsCommit(ctx, root, sha, target)
					if err != nil {
						return nil, err
					}
				}
				proved[blocker] = ok
			}
			if !ok {
				waits[id] = true
				break
			}
		}
	}
	return waits, nil
}
