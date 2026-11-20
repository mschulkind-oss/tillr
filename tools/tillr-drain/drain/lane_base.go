package drain

import (
	"context"
	"database/sql"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// ReasonTrunkUnproved means a finished blocker's landing is not yet contained in
// refs/tillr/green. The blocker is done; proof of the trunk is what is pending.
const ReasonTrunkUnproved = "trunk-unproved"

func containsCommit(ctx context.Context, root, ancestor, tip string) (bool, error) {
	ancestry := func(ctx context.Context, a, t string) (bool, error) {
		cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", a, t)
		raw, err := cmd.CombinedOutput()
		if err == nil {
			return true, nil
		}
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, Externalf("checking lane base ancestry: %v: %s", err, raw)
	}
	out, err := gather(ctx, gitKey("ancestry", root, []string{"merge-base", "--is-ancestor", ancestor, tip}), func(ctx context.Context) (string, error) {
		yes, err := ancestry(ctx, ancestor, tip)
		if err != nil {
			return "", err
		}
		if !yes {
			// A commit the trunk's rewrite replaced is contained as the commit that
			// replaced it (trunk_rewrites); git itself no longer knows the old sha as
			// ancestry.
			if a, t := rewriteTranslate(ctx, root, ancestor, tip); a != ancestor || t != tip {
				if yes, err = ancestry(ctx, a, t); err != nil {
					return "", err
				}
			}
		}
		if yes {
			return "yes", nil
		}
		return "no", nil
	})
	return out == "yes", err
}

// gitKey names one git command in one checkout, the key gathered I/O is answered by. The
// form names what the wrapper answers (stdout as written, output trimmed, an ancestry
// verdict, a blob), because two wrappers can run the same command and answer it
// differently: a key without its form let a trimmed rev-parse be answered with the
// newline-terminated stdout another wrapper had gathered.
func gitKey(form, root string, args []string) string {
	return form + ": git -C " + root + " " + strings.Join(args, " ")
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

// blockSource is a cache of document blobs read from commits, keyed by
// "<sha>:<path>". A missing path is cached as nil.
type blockSource map[string]*string

// blockBodyAt reads an item's definition as committed at sha, with the same values
// item.DefinitionBody gives a working tree for a missing source. A body reference
// outside the repository answers ok=false: no commit carries it.
func blockBodyAt(ctx context.Context, root, sha, ref, itemID string, cache blockSource) (body string, ok bool, err error) {
	if ref == "" {
		return "body source not named", true, nil
	}
	file, _, _ := strings.Cut(ref, "#")
	if filepath.IsAbs(file) || strings.HasPrefix(filepath.Clean(file), "..") {
		return "", false, nil
	}
	key := sha + ":" + filepath.ToSlash(filepath.Clean(file))
	raw, seen := cache[key]
	if !seen {
		if _, err := heavyGitOutput(ctx, root, "rev-parse", "--verify", "--quiet", key); err == nil {
			text, err := gather(ctx, gitKey("blob", root, []string{"cat-file", "blob", key}), func(ctx context.Context) (string, error) {
				out, err := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", key).Output()
				if err != nil {
					return "", Externalf("reading %s: %v", key, err)
				}
				return string(out), nil
			})
			if err != nil {
				return "", false, err
			}
			raw = &text
		} else if isGatherMiss(err) {
			// Not an absent path: the answer has not been fetched yet.
			return "", false, err
		} else if _, err := heavyGitOutput(ctx, root, "rev-parse", "--verify", "--quiet", sha+"^{commit}"); err != nil {
			if isGatherMiss(err) {
				return "", false, err
			}
			return "", false, Externalf("reading %s: %s is not a commit: %v", key, sha, err)
		}
		cache[key] = raw
	}
	if raw == nil {
		return "body source missing: " + ref, true, nil
	}
	return item.DefinitionBodyContent(ref, itemID, []byte(*raw)), true, nil
}

// blockBase answers the commit a new lane for itemID is cut from when the proved
// commit is base: base itself when it carries the trunk tip's version of the item's
// block, else the oldest trunk commit after base that does. Sync reads blocks at the
// tip, so a lane cut from base alone would brief its worker on a definition its tree
// does not hold. Advancing only as far as that commit keeps the rest of the unproved
// range out of the lane, and the item never waits on a proof for its own block.
func blockBase(ctx context.Context, r *store.Reader, root, base, itemID string) (string, error) {
	if base == "" {
		return base, nil
	}
	tip, err := heavyGitOutput(ctx, root, "rev-parse", TrunkRef)
	if err != nil || tip == base {
		return base, err
	}
	var ref string
	if err := r.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id=?`, itemID).Scan(&ref); err != nil {
		return "", err
	}
	if ref == "" {
		return base, nil
	}
	cache := blockSource{}
	at, ok, err := blockBodyAt(ctx, root, base, ref, itemID, cache)
	if err != nil || !ok {
		return base, err
	}
	want, _, err := blockBodyAt(ctx, root, tip, ref, itemID, cache)
	if err != nil || at == want {
		return base, err
	}
	file, _, _ := strings.Cut(ref, "#")
	list, err := heavyGitOutput(ctx, root, "rev-list", "--reverse", "--ancestry-path", base+".."+tip, "--", file)
	if err != nil {
		return "", err
	}
	for _, sha := range strings.Fields(list) {
		body, _, err := blockBodyAt(ctx, root, sha, ref, itemID, cache)
		if err != nil {
			return "", err
		}
		if body == want {
			return sha, nil
		}
	}
	return tip, nil
}
