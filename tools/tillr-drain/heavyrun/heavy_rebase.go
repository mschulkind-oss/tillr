package heavyrun

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/store"
)

// rebasedLandingMatch is a landing record whose commit left the trunk when main was
// rewritten, and whose content reappears on the trunk as another sha.
type rebasedLandingMatch struct {
	PreSHA, ItemID, LostTip string
}

// rebasedLanding answers whether sha, which has no landing record, is a rebased copy of
// a recorded landing: a landing row whose trunk_after is no longer on tip's first-parent
// line but whose commit object is still in the repository, and which names the same
// change (same patch-id, or same author, author date and subject) as sha. A rebase of
// main rewrites every commit above its base, so the record keeps naming the sha from
// before the rewrite. ok is false when no recorded landing matches: that commit is a
// genuinely unrecorded one.
func RebasedLanding(ctx context.Context, root, storePath, sha, tip string) (m rebasedLandingMatch, ok bool, err error) {
	if storePath == "" || sha == "" || tip == "" {
		return m, false, nil
	}
	raw, err := heavy.GitOutput(ctx, root, "rev-list", "--first-parent", tip)
	if err != nil {
		return m, false, err
	}
	onTrunk := map[string]bool{}
	for _, c := range strings.Fields(raw) {
		onTrunk[c] = true
	}
	type row struct{ item, after string }
	var rows []row
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return m, false, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		q, err := r.Query(`SELECT item_id, trunk_after FROM landings WHERE result = 'landed' ORDER BY event_seq DESC`)
		if err != nil {
			if strings.Contains(err.Error(), "no such table") {
				return nil
			}
			return err
		}
		defer q.Close()
		for q.Next() {
			var x row
			if err := q.Scan(&x.item, &x.after); err != nil {
				return err
			}
			rows = append(rows, x)
		}
		if err := q.Err(); err != nil {
			return err
		}
		h, err := fixer.ReadTrunkHistory(r)
		m.LostTip = h.Lost
		return err
	})
	if err != nil {
		return rebasedLandingMatch{}, false, err
	}
	var wantPatch, wantMeta string
	for _, x := range rows {
		if x.after == "" || onTrunk[x.after] || x.after == sha {
			continue
		}
		if _, err := heavy.GitOutput(ctx, root, "cat-file", "-e", x.after+"^{commit}"); err != nil {
			if ctx.Err() != nil {
				return rebasedLandingMatch{}, false, ctx.Err()
			}
			continue // the pre-rebase object is gone; it cannot be compared
		}
		if wantMeta == "" {
			if wantMeta, err = commitIdentity(ctx, root, sha); err != nil {
				return rebasedLandingMatch{}, false, err
			}
			wantPatch = commitPatchID(ctx, root, sha)
		}
		meta, err := commitIdentity(ctx, root, x.after)
		if err != nil {
			continue
		}
		if meta == wantMeta || wantPatch != "" && commitPatchID(ctx, root, x.after) == wantPatch {
			m.PreSHA, m.ItemID = x.after, x.item
			return m, true, nil
		}
	}
	return rebasedLandingMatch{}, false, nil
}

func commitIdentity(ctx context.Context, root, sha string) (string, error) {
	return heavy.GitOutput(ctx, root, "show", "-s", "--format=%an%x00%ae%x00%aI%x00%s", sha)
}

// commitPatchID is the stable patch-id of a commit, or "" when it has none (an empty
// diff) or git could not compute it.
func commitPatchID(ctx context.Context, root, sha string) string {
	diff, err := heavy.GitOutput(ctx, root, "diff-tree", "-p", "--no-color", sha)
	if err != nil || diff == "" {
		return ""
	}
	out, err := heavyPatchID(ctx, root, diff)
	if err != nil {
		return ""
	}
	return out
}

// RebaseNote names the rebase of the trunk as the reason sha has no landing record, and
// what the operator does about it.
func RebaseNote(sha string, m rebasedLandingMatch) string {
	restore := ""
	if m.LostTip != "" {
		restore = ", or restore the pre-rebase history " + ShortSHA(m.LostTip) + " onto the trunk so the landing records match"
	}
	return fmt.Sprintf("a rebase of the trunk changed the landed commit's sha, so no landing record matches: commit %s landed as %s (item %s) before the rebase; not reverted automatically; operator: run git revert %s on the trunk%s, and fixer-main-%s repairs it otherwise",
		ShortSHA(sha), ShortSHA(m.PreSHA), m.ItemID, ShortSHA(sha), restore, ShortSHA(sha))
}

func heavyPatchID(ctx context.Context, root, diff string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "patch-id", "--stable")
	cmd.Stdin = strings.NewReader(diff + "\n")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}
