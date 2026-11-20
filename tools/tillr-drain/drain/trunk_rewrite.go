package drain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A `git pull` whose pull.rebase is true replays the commits main holds and the remote
// lacks, and gives each a new sha. The store still names the old ones: landing rows, run
// pins, lane bases. Nothing was lost, so the loop maps each old sha to the commit that
// replaced it (trunk_rewrites) and goes on, rather than holding for an operator. It does
// so only when it can prove every commit survived: each commit between the point the
// two histories diverge and the lost tip must have a commit of the same patch on the new
// trunk, matched by `git patch-id --stable`. A commit with no match stops the automatic
// path, and the existing trunk-rewritten hold names it.

// rewriteAbsorbedKind records the loop absorbing a rewrite.
const rewriteAbsorbedKind = "drain.trunk-rewrite-absorbed"

func init() {
	if err := event.Register(rewriteAbsorbedKind, Utility); err != nil {
		panic(err)
	}
}

// rewriteStores names, per repository root, the store whose trunk_rewrites map
// containsCommit consults. A loop registers its own on every observation. Ancestry is asked
// from many places that have a root and no store, and the mapping is a fact about the
// repository's history, so it is looked up by the root.
var rewriteStores sync.Map

func registerRewriteStore(root, storePath string) {
	if root != "" && storePath != "" {
		rewriteStores.Store(filepath.Clean(root), storePath)
	}
}

// rewriteTranslate answers ancestor and tip as the trunk now spells them: each follows the
// recorded rewrites to the commit that replaced it. Unchanged when no map is registered or
// readable, so ancestry stays what git says.
func rewriteTranslate(ctx context.Context, root, ancestor, tip string) (string, string) {
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

// landingShas answers sha and every earlier sha a rewrite replaced with it, the shas a
// landing row may name for one trunk commit.
func landingShas(r *store.Reader, sha string) ([]string, error) {
	return heavy.RewriteAliases(r, sha)
}

// rewriteReason is h.reason() with the commits that had no match, when the drain tried to
// map this rewrite and could not.
func rewriteReason(q heavy.Querier, h trunkHistory) string {
	reason := h.reason()
	if h.Lost == "" {
		return reason
	}
	if unmatched, err := heavy.RewriteUnmatched(q, h.Lost); err == nil && len(unmatched) > 0 {
		reason += " (no match on the trunk for " + strings.Join(unmatched, ", ") + ")"
	}
	return reason
}

// rewriteAbsorption is a rewrite the loop proved: old sha -> new sha for each replaced
// commit, and the evidence. Or the commits it could not match.
type rewriteAbsorption struct {
	Lost, Tip string
	Pairs     map[string]string
	Order     []string // old shas, oldest first
	Unmatched []string
	Evidence  string
}

func (a *rewriteAbsorption) proved() bool { return a != nil && len(a.Unmatched) == 0 }

// rawGit runs git in dir and answers its stdout.
func rawGit(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = boundaryGitEnv()
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", Externalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// patchIDs answers each commit of a range with its stable patch id. A commit with no
// diff (a merge, an empty commit) has none, and maps to the empty string.
func patchIDs(ctx context.Context, root, span string) ([]string, map[string]string, error) {
	listed, err := rawGit(ctx, root, nil, "rev-list", "--reverse", span)
	if err != nil {
		return nil, nil, err
	}
	commits := strings.Fields(listed)
	if len(commits) == 0 {
		return nil, map[string]string{}, nil
	}
	patches, err := rawGit(ctx, root, nil, "log", "-p", "--reverse", "--format=%H", span)
	if err != nil {
		return nil, nil, err
	}
	ids, err := rawGit(ctx, root, []byte(patches), "patch-id", "--stable")
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
	reflog, _ := rawGit(ctx, root, nil, "reflog", "show", "-n", "3", "--format=%gd: %gs", TrunkRef)
	evidence := "git patch-id --stable matched " + strconv.Itoa(len(a.Pairs)) + " commits between " + base + " and " + lost + " to the trunk " + tip
	if r := strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(reflog), "\n", " | ")), " "); r != "" {
		evidence += "; reflog: " + r
	}
	a.Evidence = evidence
	return a, nil
}

// recordRewrite writes the absorbed mapping, or the unmatched commits, in tx.
func (a *rewriteAbsorption) record(tx *store.Tx, at time.Time) error {
	if _, err := tx.Exec(`DELETE FROM trunk_rewrite_unmatched WHERE lost_tip = ?`, a.Lost); err != nil {
		return err
	}
	if !a.proved() {
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
	_, err = tx.Append(event.Event{Kind: rewriteAbsorbedKind, ObjectType: "trunk", ObjectID: &a.Lost, Payload: body, At: at.UTC()})
	return err
}
