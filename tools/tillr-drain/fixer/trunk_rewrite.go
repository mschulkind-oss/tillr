package fixer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// RewriteAbsorbedKind records the loop absorbing a rewrite.
const RewriteAbsorbedKind = "drain.trunk-rewrite-absorbed"

func init() {
	if err := event.Register(RewriteAbsorbedKind, Utility); err != nil {
		panic(err)
	}
}

var rewriteStores sync.Map

// RegisterRewriteStore records the store path for a repository root so RewriteTranslate can consult it.
func RegisterRewriteStore(root, storePath string) {
	if root != "" && storePath != "" {
		rewriteStores.Store(filepath.Clean(root), storePath)
	}
}

// RewriteTranslate answers ancestor and tip as the trunk now spells them: each follows the
// recorded rewrites to the commit that replaced it. Unchanged when no map is registered or
// readable, so ancestry stays what git says.
func RewriteTranslate(ctx context.Context, root, ancestor, tip string) (string, string) {
	path, ok := rewriteStores.Load(filepath.Clean(root))
	if !ok {
		return ancestor, tip
	}
	s, err := store.OpenRead(ctx, path.(string))
	if err != nil {
		return ancestor, tip
	}
	defer s.Close()
	a, t := ancestor, tip
	_ = s.ReadTx(ctx, func(r *store.Reader) error {
		if x, err := heavy.RewriteResolve(r, ancestor); err == nil {
			a = x
		}
		if x, err := heavy.RewriteResolve(r, tip); err == nil {
			t = x
		}
		return nil
	})
	return a, t
}

// LandingShas answers sha and every earlier sha a rewrite replaced with it, the shas a
// landing row may name for one trunk commit.
func LandingShas(r *store.Reader, sha string) ([]string, error) {
	return heavy.RewriteAliases(r, sha)
}

// RewriteReason is h.Reason() with the commits that had no match, when the drain tried to
// map this rewrite and could not.
func RewriteReason(q heavy.Querier, h TrunkHistory) string {
	reason := h.Reason()
	if h.Lost == "" {
		return reason
	}
	if unmatched, err := heavy.RewriteUnmatched(q, h.Lost); err == nil && len(unmatched) > 0 {
		reason += " (no match on the trunk for " + strings.Join(unmatched, ", ") + ")"
	}
	return reason
}

// RewriteAbsorption is a rewrite the loop proved: old sha -> new sha for each replaced
// commit, and the evidence. Or the commits it could not match.
type RewriteAbsorption struct {
	Lost, Tip     string
	Pairs         map[string]string
	Order         []string // old shas, oldest first
	Unmatched     []string
	Evidence      string
	GreenOverride string // a repair must hold green strictly behind its gated commit
}

// Proved reports whether all lost commits were matched.
func (a *RewriteAbsorption) Proved() bool { return a != nil && len(a.Unmatched) == 0 }

// Record writes the absorbed mapping, or the unmatched commits, in tx.
func (a *RewriteAbsorption) Record(tx *store.Tx, at time.Time) error {
	if _, err := tx.Exec(`DELETE FROM trunk_rewrite_unmatched WHERE lost_tip = ?`, a.Lost); err != nil {
		return err
	}
	if !a.Proved() {
		for _, sha := range a.Unmatched {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO trunk_rewrite_unmatched(sha, lost_tip) VALUES(?, ?)`, sha, a.Lost); err != nil {
				return err
			}
		}
		return nil
	}
	stamp := at.Format(time.RFC3339)
	mapped := 0
	for _, old := range a.Order {
		now := a.Pairs[old]
		if now == "" || now == old {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO trunk_rewrites(old_sha, new_sha, recorded_at, evidence) VALUES(?, ?, ?, ?)`, old, now, stamp, a.Evidence); err != nil {
			return err
		}
		mapped++
	}
	body, err := json.Marshal(map[string]any{"lost_tip": a.Lost, "tip": a.Tip, "commits": mapped, "evidence": a.Evidence})
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: RewriteAbsorbedKind, ObjectType: "trunk", ObjectID: &a.Lost, Payload: body, At: at.UTC()})
	return err
}

// PatchIDs answers each commit of a range with its stable patch id. A commit with no
// diff (a merge, an empty commit) has none, and maps to the empty string.
func PatchIDs(ctx context.Context, root, span string) ([]string, map[string]string, error) {
	listed, err := RawGit(ctx, root, nil, "rev-list", "--reverse", span)
	if err != nil {
		return nil, nil, err
	}
	commits := strings.Fields(listed)
	if len(commits) == 0 {
		return nil, map[string]string{}, nil
	}
	patches, err := RawGit(ctx, root, nil, "log", "-p", "--reverse", "--format=%H", span)
	if err != nil {
		return nil, nil, err
	}
	ids, err := RawGit(ctx, root, []byte(patches), "patch-id", "--stable")
	if err != nil {
		return nil, nil, err
	}
	byCommit := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(ids))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			byCommit[f[1]] = f[0]
		}
	}
	return commits, byCommit, sc.Err()
}

// SingleCommitPatchID answers the stable patch id of one commit, or empty when the
// commit has no diff (for instance, an empty commit or a merge commit).
func SingleCommitPatchID(ctx context.Context, root, sha string) (string, error) {
	patch, err := RawGit(ctx, root, nil, "log", "-1", "--no-merges", "-p", "--format=%H", sha)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(patch) == "" {
		return "", nil
	}
	ids, err := RawGit(ctx, root, []byte(patch), "patch-id", "--stable")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(ids, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == sha {
			return f[0], nil
		}
	}
	return "", nil
}

// NonMergePatchIDs answers each non-merge commit of a range with its stable patch id.
func NonMergePatchIDs(ctx context.Context, root, span string) ([]string, map[string]string, error) {
	listed, err := RawGit(ctx, root, nil, "rev-list", "--no-merges", "--reverse", span)
	if err != nil {
		return nil, nil, err
	}
	commits := strings.Fields(listed)
	if len(commits) == 0 {
		return nil, map[string]string{}, nil
	}
	patches, err := RawGit(ctx, root, nil, "log", "--no-merges", "-p", "--reverse", "--format=%H", span)
	if err != nil {
		return nil, nil, err
	}
	ids, err := RawGit(ctx, root, []byte(patches), "patch-id", "--stable")
	if err != nil {
		return nil, nil, err
	}
	byCommit := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(ids))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 {
			byCommit[f[1]] = f[0]
		}
	}
	return commits, byCommit, sc.Err()
}

// TryMergeBase returns the merge base of a and b, or ("", false, nil) when they share no
// common ancestor (exit 1).
func TryMergeBase(ctx context.Context, root, a, b string) (string, bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", a, b)
	cmd.Env = BoundaryGitEnv()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", false, nil
	}
	return "", false, externalf("git merge-base %s %s in %s: %v: %s", a, b, root, err, strings.TrimSpace(stderr.String()))
}

// UpdateLandedMarkers moves any refs/tillr/landed/<run_id> pointing at an old sha in
// mapped to its replacement sha.
func UpdateLandedMarkers(ctx context.Context, root string, mapped map[string]string) error {
	if len(mapped) == 0 {
		return nil
	}
	out, err := RawGit(ctx, root, nil, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tillr/landed")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		ref, old := f[0], f[1]
		if replacement, ok := mapped[old]; ok && replacement != "" && replacement != old {
			if _, err := RawGit(ctx, root, nil, "update-ref", ref, replacement, old); err != nil {
				return err
			}
		}
	}
	return nil
}
