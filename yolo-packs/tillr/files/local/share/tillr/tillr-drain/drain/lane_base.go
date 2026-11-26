package drain

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// ReasonTrunkUnproved means a finished blocker's landing is not yet contained in
// refs/tillr/green. The blocker is done; proof of the trunk is what is pending.
const ReasonTrunkUnproved = "trunk-unproved"

// ReasonBlockerLandingNotOnTrunk means a proof of the current history cannot
// contain the blocker's recorded landing, even after following known rewrites.
const ReasonBlockerLandingNotOnTrunk = "blocker-landing-not-on-trunk"

func checkAncestry(ctx context.Context, root, a, t string) (bool, error) {
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

func isFullHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func notContainedCommits(ctx context.Context, root string, ancestors []string, tip string) (map[string]bool, error) {
	var stdin strings.Builder
	stdin.WriteString("--end-of-options\n^")
	stdin.WriteString(tip)
	stdin.WriteByte('\n')
	for _, a := range ancestors {
		stdin.WriteString(a)
		stdin.WriteByte('\n')
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-list", "--stdin")
	cmd.Stdin = strings.NewReader(stdin.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, Externalf("checking lane base ancestry: %v: %s", err, stderr.Bytes())
	}
	missing := make(map[string]bool)
	for _, sha := range strings.Fields(string(raw)) {
		missing[sha] = true
	}
	return missing, nil
}

func containsCommit(ctx context.Context, root, ancestor, tip string) (bool, error) {
	out, err := gather(ctx, gitKey("ancestry", root, []string{"merge-base", "--is-ancestor", ancestor, tip}), func(ctx context.Context) (string, error) {
		yes, err := checkAncestry(ctx, root, ancestor, tip)
		if err != nil {
			return "", err
		}
		if !yes {
			// A commit the trunk's rewrite replaced is contained as the commit that
			// replaced it (trunk_rewrites); git itself no longer knows the old sha as
			// ancestry.
			if a, t := rewriteTranslate(ctx, root, ancestor, tip); a != ancestor || t != tip {
				if yes, err = checkAncestry(ctx, root, a, t); err != nil {
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

// containsCommits answers, for each commit in ancestors, whether tip contains it
// (directly or via a recorded trunk rewrite). Multiple full-SHA commits for the same
// tip are checked with one git process rather than one per commit.
func containsCommits(ctx context.Context, root string, ancestors []string, tip string) (map[string]bool, error) {
	result := make(map[string]bool, len(ancestors))
	if len(ancestors) == 0 {
		return result, nil
	}
	var fullSHAs, other []string
	seen := make(map[string]bool, len(ancestors))
	for _, a := range ancestors {
		if seen[a] {
			continue
		}
		seen[a] = true
		if isFullHexSHA(a) {
			fullSHAs = append(fullSHAs, a)
		} else {
			other = append(other, a)
		}
	}
	slices.Sort(other)
	for _, a := range other {
		ok, err := containsCommit(ctx, root, a, tip)
		if err != nil {
			return nil, err
		}
		result[a] = ok
	}
	if len(fullSHAs) == 0 {
		return result, nil
	}
	if len(fullSHAs) == 1 {
		ok, err := containsCommit(ctx, root, fullSHAs[0], tip)
		if err != nil {
			return nil, err
		}
		result[fullSHAs[0]] = ok
		return result, nil
	}
	slices.Sort(fullSHAs)
	fullSet := make(map[string]bool, len(fullSHAs))
	for _, a := range fullSHAs {
		fullSet[a] = true
	}
	keyArgs := make([]string, 0, len(fullSHAs)+2)
	keyArgs = append(keyArgs, "rev-list", "^"+tip)
	keyArgs = append(keyArgs, fullSHAs...)
	out, err := gather(ctx, gitKey("ancestry-batch", root, keyArgs), func(ctx context.Context) (string, error) {
		origMissing, err := notContainedCommits(ctx, root, fullSHAs, tip)
		if err != nil {
			return "", err
		}
		stillMissing := make(map[string]bool)
		type translatedCheck struct {
			orig string
			a    string
		}
		byTip := map[string][]translatedCheck{}
		for _, a := range fullSHAs {
			if !origMissing[a] {
				continue
			}
			stillMissing[a] = true
			// A commit the trunk's rewrite replaced is contained as the commit that
			// replaced it (trunk_rewrites); git itself no longer knows the old sha as
			// ancestry.
			if ta, tt := rewriteTranslate(ctx, root, a, tip); ta != a || tt != tip {
				if tt == tip && fullSet[ta] {
					if !origMissing[ta] {
						delete(stillMissing, a)
					}
					continue
				}
				byTip[tt] = append(byTip[tt], translatedCheck{orig: a, a: ta})
			}
		}
		for _, tt := range slices.Sorted(maps.Keys(byTip)) {
			checks := byTip[tt]
			var tFull, tOther []string
			tSeen := make(map[string]bool, len(checks))
			for _, c := range checks {
				if tSeen[c.a] {
					continue
				}
				tSeen[c.a] = true
				if isFullHexSHA(c.a) {
					tFull = append(tFull, c.a)
				} else {
					tOther = append(tOther, c.a)
				}
			}
			var tMissing map[string]bool
			if len(tFull) > 0 {
				tMissing, err = notContainedCommits(ctx, root, tFull, tt)
				if err != nil {
					return "", err
				}
			}
			otherContained := map[string]bool{}
			for _, a := range tOther {
				ok, err := checkAncestry(ctx, root, a, tt)
				if err != nil {
					return "", err
				}
				otherContained[a] = ok
			}
			for _, c := range checks {
				var ok bool
				if isFullHexSHA(c.a) {
					ok = !tMissing[c.a]
				} else {
					ok = otherContained[c.a]
				}
				if ok {
					delete(stillMissing, c.orig)
				}
			}
		}
		var uncontained []string
		for _, a := range fullSHAs {
			if stillMissing[a] {
				uncontained = append(uncontained, a)
			}
		}
		return strings.Join(uncontained, "\n"), nil
	})
	if err != nil {
		return nil, err
	}
	uncontained := make(map[string]bool)
	for _, sha := range strings.Fields(out) {
		uncontained[sha] = true
	}
	for _, a := range fullSHAs {
		result[a] = !uncontained[a]
	}
	return result, nil
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
	return provedLaneBaseAt(ctx, root, mode, "")
}

func provedLaneBaseAt(ctx context.Context, root, mode, tip string) (string, error) {
	if mode == "tip" || root == "" {
		return "", nil
	}
	green, err := heavyGreen(ctx, root)
	if err != nil || green == "" {
		return green, err
	}
	if tip == "" {
		tip, err = heavyGitOutput(ctx, root, "rev-parse", trunkBranchFor(root))
		if err != nil {
			return "", err
		}
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

func hasLandedBlocker(states map[string]string, edges map[string][]itemEdge) bool {
	for id, blockers := range edges {
		if IsTrunkFixer(id) {
			continue
		}
		for _, edge := range blockers {
			if states[edge.Target] == StateLanded {
				return true
			}
		}
	}
	return false
}

type landedBlockerCommit struct {
	sha string
	ref string
}

func readLandedBlockerCommits(r *store.Reader, states map[string]string, edges map[string][]itemEdge) (map[string]landedBlockerCommit, error) {
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')`).Scan(&exists); err != nil {
		return nil, err
	}
	commits := map[string]landedBlockerCommit{}
	for id, blockers := range edges {
		if IsTrunkFixer(id) {
			continue
		}
		for _, edge := range blockers {
			blocker := edge.Target
			if states[blocker] != StateLanded {
				continue
			}
			if _, seen := commits[blocker]; seen {
				continue
			}
			var sha, ref string
			if exists {
				err := r.QueryRow(`SELECT l.trunk_after, COALESCE(json_extract(e.payload, '$.base_ref'), '')
					FROM landings l LEFT JOIN events e ON e.seq=l.event_seq AND e.kind='land.landing'
					WHERE l.item_id=? AND l.result='landed' ORDER BY l.event_seq DESC LIMIT 1`, blocker).Scan(&sha, &ref)
				if err != nil && err != sql.ErrNoRows {
					return nil, err
				}
			}
			if sha != "" {
				if resolved, err := heavy.RewriteResolve(r, sha); err != nil {
					return nil, err
				} else if resolved != "" {
					sha = resolved
				}
			}
			commits[blocker] = landedBlockerCommit{sha: sha, ref: ref}
		}
	}
	return commits, nil
}

func unprovedBlockersFromCommits(ctx context.Context, root, green, tip string, states map[string]string, edges map[string][]itemEdge, commits map[string]landedBlockerCommit) (map[string]string, error) {
	waits := map[string]string{}
	if green == "" {
		return waits, nil
	}
	byTarget := map[string][]string{}
	seenTargetSHA := map[string]map[string]bool{}
	seenBlocker := map[string]bool{}
	for id, blockers := range edges {
		if IsTrunkFixer(id) {
			continue
		}
		for _, edge := range blockers {
			blocker := edge.Target
			if states[blocker] != StateLanded || seenBlocker[blocker] {
				continue
			}
			seenBlocker[blocker] = true
			landing := commits[blocker]
			if landing.sha == "" {
				continue
			}
			// A fork landing delivers an already judged commit to its patch
			// branch, not to main. Its recorded trunk_after must be retained
			// by that delivery branch; main's green pointer cannot contain it.
			target := green
			if strings.HasPrefix(landing.ref, "refs/heads/patch/") {
				target = landing.ref
			}
			if seenTargetSHA[target] == nil {
				seenTargetSHA[target] = map[string]bool{}
			}
			if !seenTargetSHA[target][landing.sha] {
				seenTargetSHA[target][landing.sha] = true
				byTarget[target] = append(byTarget[target], landing.sha)
			}
		}
	}
	containedByTarget := make(map[string]map[string]bool, len(byTarget))
	for _, target := range slices.Sorted(maps.Keys(byTarget)) {
		contained, err := containsCommits(ctx, root, byTarget[target], target)
		if err != nil {
			return nil, err
		}
		containedByTarget[target] = contained
	}
	var onTrunk map[string]bool
	if tip != "" && tip != green {
		var unprovedGreen []string
		for _, sha := range byTarget[green] {
			if !containedByTarget[green][sha] {
				unprovedGreen = append(unprovedGreen, sha)
			}
		}
		if len(unprovedGreen) > 0 {
			var err error
			onTrunk, err = containsCommits(ctx, root, unprovedGreen, tip)
			if err != nil {
				return nil, err
			}
		}
	}
	reasons := map[string]string{}
	for id, blockers := range edges {
		if IsTrunkFixer(id) {
			continue
		}
		for _, edge := range blockers {
			blocker := edge.Target
			if states[blocker] != StateLanded {
				continue
			}
			reason, seen := reasons[blocker]
			if !seen {
				reason = ReasonTrunkUnproved
				landing := commits[blocker]
				if landing.sha != "" {
					target := green
					if strings.HasPrefix(landing.ref, "refs/heads/patch/") {
						target = landing.ref
					}
					if containedByTarget[target][landing.sha] {
						reason = ""
					} else if tip != "" && target == green && !onTrunk[landing.sha] {
						reason = fmt.Sprintf("%s: %s at %s", ReasonBlockerLandingNotOnTrunk, blocker, landing.sha)
					}
				}
				reasons[blocker] = reason
			}
			if reason != "" {
				waits[id] = reason
				break
			}
		}
	}
	return waits, nil
}

// A nonempty tip adds planner diagnostics. Claim and release checks only need
// to know whether the dependency is absent from the selected base.
func unprovedBlockers(ctx context.Context, r *store.Reader, root, green, tip string, states map[string]string, edges map[string][]itemEdge) (map[string]string, error) {
	if green == "" {
		return map[string]string{}, nil
	}
	commits, err := readLandedBlockerCommits(r, states, edges)
	if err != nil {
		return nil, err
	}
	return unprovedBlockersFromCommits(ctx, root, green, tip, states, edges, commits)
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
	tip, err := heavyGitOutput(ctx, root, "rev-parse", trunkBranchFor(root))
	if err != nil {
		return base, err
	}
	return blockBaseAt(ctx, r, root, base, tip, itemID)
}

func blockBaseAt(ctx context.Context, r *store.Reader, root, base, tip, itemID string) (string, error) {
	if base == "" || tip == base {
		return base, nil
	}
	var ref string
	if err := r.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id=?`, itemID).Scan(&ref); err != nil {
		return "", err
	}
	if landed, err := designAnswerLanded(ctx, r, root, tip, itemID); err != nil {
		return "", err
	} else if landed != "" && landed != base {
		// A design run's landed answer (a decision ledger under docs/, with no item block
		// to compare) is part of what the returned worker's base must contain, even when
		// the green pointer the base came from lags the trunk.
		if ok, err := containsCommit(ctx, root, base, landed); err != nil {
			return "", err
		} else if ok {
			base = landed
		}
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

// designAnswerLanded answers the trunk commit at which an item's answered (or closed)
// design conflict landed, or "" when it has none. It is the landing's trunk_after when the
// tip contains it, else the answer's head when the tip contains that, else the tip itself
// for a landed row whose commits were squashed away.
func designAnswerLanded(ctx context.Context, r *store.Reader, root, tip, itemID string) (string, error) {
	conflicts, err := item.ReadConflicts(r)
	if err != nil {
		return "", err
	}
	c, ok := conflicts[itemID]
	if !ok || (c.Status != "answered" && c.Status != "closed") || c.AnswerHead == "" || c.DesignRunID == "" {
		return "", nil
	}
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')`).Scan(&exists); err != nil || !exists {
		return "", err
	}
	// AnswerHead records the candidate that actually answered the conflict, even when
	// a conflict-fix run landed it. DesignRunID still names the original dispatch.
	// For an owner answer, keep looking up the design run that landed the question:
	// AnswerHead in that case is the owner's commit, not a candidate head.
	var after string
	if c.QuestionRef != "" {
		err = r.QueryRow(`SELECT trunk_after FROM landings WHERE item_id=? AND run_id=? AND result='landed' ORDER BY event_seq DESC LIMIT 1`, itemID, c.DesignRunID).Scan(&after)
	} else {
		err = r.QueryRow(`SELECT trunk_after FROM landings WHERE item_id=? AND head_sha=? AND result='landed' ORDER BY event_seq DESC LIMIT 1`, itemID, c.AnswerHead).Scan(&after)
	}
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, sha := range []string{after, c.AnswerHead} {
		if sha == "" {
			continue
		}
		if ok, err := containsCommit(ctx, root, sha, tip); err != nil {
			return "", err
		} else if ok {
			return sha, nil
		}
	}
	return tip, nil
}
