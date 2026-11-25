package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// rewriteAbsorbedKind records the loop absorbing a rewrite.
const rewriteAbsorbedKind = fixer.RewriteAbsorbedKind

func registerRewriteStore(root, storePath string) {
	fixer.RegisterRewriteStore(root, storePath)
}

func rewriteTranslate(ctx context.Context, root, ancestor, tip string) (string, string) {
	return fixer.RewriteTranslate(ctx, root, ancestor, tip)
}

func landingShas(r *store.Reader, sha string) ([]string, error) {
	return fixer.LandingShas(r, sha)
}

func rewriteReason(q heavy.Querier, h trunkHistory) string {
	return fixer.RewriteReason(q, h)
}

type rewriteAbsorption = fixer.RewriteAbsorption

func rawGit(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	out, err := fixer.RawGit(ctx, dir, stdin, args...)
	return out, wrapFixerError(err)
}

func patchIDs(ctx context.Context, root, span string) ([]string, map[string]string, error) {
	commits, byCommit, err := fixer.PatchIDs(ctx, root, span)
	return commits, byCommit, wrapFixerError(err)
}

// proveRewrite matches every commit between the point the lost history diverged from the
// trunk and the lost tip to a commit of the same patch on the trunk since that point. The
// matching is one to one and keeps order, so a duplicated, dropped or changed commit is
// unmatched. It reads git only.
func (l *Loop) proveRewrite(ctx context.Context, lost, tip string) (*rewriteAbsorption, error) {
	root := l.cfg.Root
	a := &rewriteAbsorption{Lost: lost, Tip: tip, Pairs: map[string]string{}}
	base, err := rawGit(ctx, root, nil, "merge-base", lost, tip)
	if err != nil {
		// No common ancestor: nothing can be proved.
		listed, lerr := rawGit(ctx, root, nil, "rev-list", "--reverse", lost)
		if lerr != nil {
			return nil, err
		}
		a.Order = strings.Fields(listed)
		a.Unmatched = a.Order
		return a, nil
	}
	base = strings.TrimSpace(base)
	oldCommits, oldIDs, err := patchIDs(ctx, root, base+".."+lost)
	if err != nil {
		return nil, err
	}
	newCommits, newIDs, err := patchIDs(ctx, root, base+".."+tip)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, old := range oldCommits {
		a.Order = append(a.Order, old)
		id := oldIDs[old]
		match := ""
		if id != "" {
			for _, c := range newCommits {
				if !used[c] && newIDs[c] == id {
					match = c
					break
				}
			}
		}
		if match == "" {
			a.Unmatched = append(a.Unmatched, old)
			continue
		}
		used[match] = true
		a.Pairs[old] = match
	}
	reflog, _ := rawGit(ctx, root, nil, "reflog", "show", "-n", "3", "--format=%gd: %gs", trunkBranchFor(root))
	evidence := "git patch-id --stable matched " + strconv.Itoa(len(a.Pairs)) + " commits between " + base + " and " + lost + " to the trunk " + tip
	if r := strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(reflog), "\n", " | ")), " "); r != "" {
		evidence += "; reflog: " + r
	}
	a.Evidence = evidence
	return a, nil
}

func singleCommitPatchID(ctx context.Context, root, sha string) (string, error) {
	id, err := fixer.SingleCommitPatchID(ctx, root, sha)
	return id, wrapFixerError(err)
}

func nonMergePatchIDs(ctx context.Context, root, span string) ([]string, map[string]string, error) {
	commits, byCommit, err := fixer.NonMergePatchIDs(ctx, root, span)
	return commits, byCommit, wrapFixerError(err)
}

func tryMergeBase(ctx context.Context, root, a, b string) (string, bool, error) {
	base, ok, err := fixer.TryMergeBase(ctx, root, a, b)
	return base, ok, wrapFixerError(err)
}

func updateLandedMarkers(ctx context.Context, root string, mapped map[string]string) error {
	return wrapFixerError(fixer.UpdateLandedMarkers(ctx, root, mapped))
}

func (l *Loop) landingRewriteIsChecked(old, tip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.landingRewriteChecked[old] == tip
}

func (l *Loop) markLandingRewriteChecked(old, tip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.landingRewriteChecked == nil {
		l.landingRewriteChecked = map[string]string{}
	}
	l.landingRewriteChecked[old] = tip
}

// reconcileRewrittenLandings maps unmapped trunk landings whose recorded trunk_after is no
// longer an ancestor of main to their unique git patch-id --stable match in
// merge-base(old, main)..main. It opens the store writer only when at least one pair is
// matched, and caches checked (old_sha, main_tip) pairs in memory so unmatchable commits
// do not re-scan git history on unchanged tips.
func (l *Loop) reconcileRewrittenLandings(ctx context.Context) error {
	if l.nonLanding || l.cfg.Root == "" || l.cfg.Store == "" {
		return nil
	}
	registerRewriteStore(l.cfg.Root, l.cfg.Store)
	tip, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
	if err != nil {
		return err
	}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var resolvedShas []string
	var activeLost string
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		h, err := readTrunkHistory(tx)
		if err != nil {
			return err
		}
		activeLost = h.Lost
		if activeLost != "" {
			return nil
		}
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')`).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return nil
		}
		rows, err := tx.Query(`SELECT DISTINCT l.trunk_after
			FROM landings l LEFT JOIN events e ON e.seq=l.event_seq AND e.kind='land.landing'
			WHERE l.result='landed' AND l.trunk_after != ''
			  AND COALESCE(json_extract(e.payload, '$.base_ref'), '') NOT LIKE 'refs/heads/patch/%'
			ORDER BY l.trunk_after`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var rawShas []string
		for rows.Next() {
			var sha string
			if err := rows.Scan(&sha); err != nil {
				return err
			}
			rawShas = append(rawShas, sha)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, sha := range rawShas {
			resolved, err := heavy.RewriteResolve(tx, sha)
			if err != nil {
				return err
			}
			if resolved != "" && !seen[resolved] {
				seen[resolved] = true
				resolvedShas = append(resolvedShas, resolved)
			}
		}
		return nil
	})
	r.Close()
	if err != nil || activeLost != "" || len(resolvedShas) == 0 {
		return err
	}

	type landingCandidate struct {
		old  string
		base string
	}
	var candidates []landingCandidate
	for _, old := range resolvedShas {
		if l.landingRewriteIsChecked(old, tip) {
			continue
		}
		if _, err := rawGit(ctx, l.cfg.Root, nil, "rev-parse", "--verify", "--quiet", old+"^{commit}"); err != nil {
			l.markLandingRewriteChecked(old, tip)
			continue
		}
		onTrunk, err := trunkAncestor(ctx, l.cfg.Root, old, tip)
		if err != nil {
			return err
		}
		if onTrunk {
			l.markLandingRewriteChecked(old, tip)
			continue
		}
		base, ok, err := tryMergeBase(ctx, l.cfg.Root, old, tip)
		if err != nil {
			return err
		}
		if !ok || base == "" {
			l.markLandingRewriteChecked(old, tip)
			continue
		}
		candidates = append(candidates, landingCandidate{old: old, base: base})
	}
	if len(candidates) == 0 {
		return nil
	}

	oldestBase := candidates[0].base
	uniqueBases := map[string]bool{oldestBase: true}
	for _, c := range candidates[1:] {
		if uniqueBases[c.base] {
			continue
		}
		uniqueBases[c.base] = true
		mb, ok, err := tryMergeBase(ctx, l.cfg.Root, oldestBase, c.base)
		if err != nil {
			return err
		}
		if ok && mb != "" {
			oldestBase = mb
		}
	}

	_, trunkIDs, err := nonMergePatchIDs(ctx, l.cfg.Root, oldestBase+".."+tip)
	if err != nil {
		return err
	}
	byPatchID := make(map[string]map[string][]string, len(uniqueBases))
	for b := range uniqueBases {
		listed, err := rawGit(ctx, l.cfg.Root, nil, "rev-list", "--no-merges", "--reverse", b+".."+tip)
		if err != nil {
			return err
		}
		index := map[string][]string{}
		for _, c := range strings.Fields(listed) {
			if id := trunkIDs[c]; id != "" {
				index[id] = append(index[id], c)
			}
		}
		byPatchID[b] = index
	}

	type pair struct{ old, replacement string }
	var tentative []pair
	newCounts := map[string]int{}
	for _, c := range candidates {
		oldID, err := singleCommitPatchID(ctx, l.cfg.Root, c.old)
		if err != nil {
			return err
		}
		if oldID == "" {
			l.markLandingRewriteChecked(c.old, tip)
			continue
		}
		hits := byPatchID[c.base][oldID]
		if len(hits) != 1 {
			l.markLandingRewriteChecked(c.old, tip)
			continue
		}
		tentative = append(tentative, pair{old: c.old, replacement: hits[0]})
		newCounts[hits[0]]++
	}

	var matched []pair
	matchedMap := map[string]string{}
	for _, p := range tentative {
		if newCounts[p.replacement] != 1 {
			l.markLandingRewriteChecked(p.old, tip)
			continue
		}
		matched = append(matched, p)
		matchedMap[p.old] = p.replacement
	}
	if len(matched) == 0 {
		return nil
	}

	if err := updateLandedMarkers(ctx, l.cfg.Root, matchedMap); err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	stamp := at.Format(time.RFC3339)
	evidence := fmt.Sprintf("git patch-id --stable matched %d rewritten trunk landings to %s", len(matched), tip)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(heavy.RewritesDDL); err != nil {
			return err
		}
		for _, p := range matched {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO trunk_rewrites(old_sha, new_sha, recorded_at, evidence) VALUES(?, ?, ?, ?)`, p.old, p.replacement, stamp, evidence); err != nil {
				return err
			}
		}
		body, err := json.Marshal(map[string]any{
			"tip":      tip,
			"commits":  len(matched),
			"evidence": evidence,
			"landings": matchedMap,
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{Kind: rewriteAbsorbedKind, ObjectType: "trunk", ObjectID: &tip, Payload: body, At: at})
		return err
	}); err != nil {
		return err
	}
	for _, p := range matched {
		l.markLandingRewriteChecked(p.old, tip)
	}
	return nil
}
