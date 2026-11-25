package fixer

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// RewriteAcceptedKind records an operator accepting a trunk rewrite that kept the content.
const RewriteAcceptedKind = "drain.trunk-rewrite-accepted"

// AcceptedTrunkPrefix pins each accepted lost tip, so the history the operator replaced
// stays readable after the lost ref is cleared and reflogs expire.
const AcceptedTrunkPrefix = "refs/tillr/trunk-history/accepted/"

func init() {
	if err := event.Register(RewriteAcceptedKind, Utility); err != nil {
		panic(err)
	}
}

// AcceptRewriteRequest names the rewrite an operator accepts and why.
type AcceptRewriteRequest struct {
	Root, Store   string
	TrunkRef      string
	Lost, Current string
	By, Why       string
	At            time.Time
}

// RewriteAcceptance is what an accepted rewrite recorded.
type RewriteAcceptance struct {
	Lost    string `json:"lost_tip"`
	Current string `json:"current_tip"`
	Tree    string `json:"tree"`
	Commits int    `json:"commits"`
	// Rebased says the rewrite was a rebase onto a moved base: the trees differ, and each
	// of Commits lost commits has a commit with the same patch on the trunk.
	Rebased bool `json:"rebased,omitempty"`
	// Landing says an individual unmapped trunk landing commit (not the trunk tip) was
	// mapped to its patch-identical replacement on the trunk.
	Landing bool   `json:"landing,omitempty"`
	Pin     string `json:"pin"`
	// GreenFrom and GreenTo name the proof pointer's move, when the rewrite carried it.
	GreenFrom string `json:"green_from,omitempty"`
	GreenTo   string `json:"green_to,omitempty"`
	By        string `json:"by"`
	Why       string `json:"why"`
}

// AcceptRewrite clears a trunk-rewritten hold for a rewrite that kept the content.
func AcceptRewrite(ctx context.Context, req AcceptRewriteRequest) (RewriteAcceptance, error) {
	var a RewriteAcceptance
	if strings.TrimSpace(req.By) == "" || strings.TrimSpace(req.Why) == "" {
		return a, usagef("accept-rewrite needs --by <who> and --why <reason>")
	}
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", req.Root}, args...)...)
		cmd.Env = BoundaryGitEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", externalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit := func(flag, rev string) (string, error) {
		sha, err := git("rev-parse", "--verify", "--quiet", rev+"^{commit}")
		if err != nil || sha == "" {
			return "", usagef("%s %q is not a commit in %s", flag, rev, req.Root)
		}
		return sha, nil
	}
	lost, err := commit("--lost", req.Lost)
	if err != nil {
		return a, err
	}
	current, err := commit("--current", req.Current)
	if err != nil {
		return a, err
	}
	s, err := store.Open(ctx, req.Store, true)
	if err != nil {
		return a, err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		trunkRef := req.TrunkRef
		if trunkRef == "" {
			cfg, err := settings.Read(req.Root)
			if err != nil {
				return usagef("reading settings: %v", err)
			}
			trunkRef = cfg.TrunkBranch()
		}
		h, err := ReadTrunkHistory(tx.Reader())
		if err != nil {
			return err
		}
		tip, err := git("rev-parse", "refs/heads/"+trunkRef)
		if err != nil {
			return err
		}
		recorded := h.Lost
		if recorded == "" && h.Observed != "" {
			ancestor, err := TrunkAncestor(ctx, req.Root, h.Observed, tip)
			if err != nil {
				return err
			}
			if !ancestor {
				recorded = h.Observed // rewritten since the last pass, not yet recorded
			}
		}
		if recorded == "" {
			unmapped, err := isUnmappedTrunkLanding(ctx, tx.Reader(), req.Root, lost, tip)
			if err != nil {
				return err
			}
			if !unmapped {
				return usagef("no rewrite is recorded: the trunk %s contains the observed tip %q, so there is nothing to accept", tip, h.Observed)
			}
			if on, err := TrunkAncestor(ctx, req.Root, current, tip); err != nil {
				return err
			} else if !on {
				return usagef("--current %s is not on the trunk: the trunk %s does not contain it", current, tip)
			}
			lostPatchID, err := SingleCommitPatchID(ctx, req.Root, lost)
			if err != nil {
				return err
			}
			if lostPatchID == "" {
				return usagef("cannot verify patch identity: lost landing %s has no stable patch id (empty or merge commit)", lost)
			}
			currentPatchID, err := SingleCommitPatchID(ctx, req.Root, current)
			if err != nil {
				return err
			}
			if currentPatchID == "" {
				return usagef("cannot verify patch identity: --current %s has no stable patch id (empty or merge commit)", current)
			}
			if lostPatchID != currentPatchID {
				return usagef("the stable patch ids differ: lost landing %s has patch id %s, --current %s has patch id %s", lost, lostPatchID, current, currentPatchID)
			}
			currentTree, err := git("rev-parse", current+"^{tree}")
			if err != nil {
				return err
			}
			a = RewriteAcceptance{
				Lost: lost, Current: current, Tree: currentTree, Commits: 1,
				Landing: true, Pin: AcceptedTrunkPrefix + lost, By: req.By, Why: req.Why,
			}
			if _, err := git("update-ref", a.Pin, lost); err != nil {
				return err
			}
			if err := UpdateLandedMarkers(ctx, req.Root, map[string]string{lost: current}); err != nil {
				return err
			}
			if _, err := tx.Exec(heavy.RewritesDDL); err != nil {
				return err
			}
			at := req.At.Format(time.RFC3339)
			if _, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at,evidence) VALUES(?,?,?,?) ON CONFLICT(old_sha) DO UPDATE SET new_sha=excluded.new_sha,recorded_at=excluded.recorded_at,evidence=excluded.evidence`, lost, current, at, "accept-rewrite:landing"); err != nil {
				return err
			}
			body, err := json.Marshal(a)
			if err != nil {
				return err
			}
			_, err = tx.Append(event.Event{Kind: RewriteAcceptedKind, ObjectType: "trunk", ObjectID: &lost, Payload: body, At: req.At.UTC()})
			return err
		}
		oneForOnePairs, rebasedPairs, err := acceptedRewritePairs(ctx, req.Root, lost, current)
		if err != nil {
			return err
		}
		if recorded != lost {
			return usagef("the recorded lost tip is %s, not %s: name the tip the hold reports (trunk-rewritten: <lost> -> <current>)", recorded, lost)
		}
		if on, err := TrunkAncestor(ctx, req.Root, current, tip); err != nil {
			return err
		} else if !on {
			return usagef("--current %s is not on the trunk: the trunk %s does not contain it", current, tip)
		}
		lostTree, err := git("rev-parse", lost+"^{tree}")
		if err != nil {
			return err
		}
		currentTree, err := git("rev-parse", current+"^{tree}")
		if err != nil {
			return err
		}
		rebased := 0
		if lostTree != currentTree {
			matched, unmatched, err := patchMatches(git, lost, current)
			if err != nil {
				return err
			}
			if matched == 0 || len(unmatched) > 0 {
				paths, err := git("diff", "--name-only", lost, current)
				if err != nil {
					return err
				}
				names := strings.Fields(paths)
				if len(names) > 10 {
					names = append(names[:10], "...")
				}
				if len(unmatched) > 10 {
					unmatched = append(unmatched[:10], "...")
				}
				return usagef("the trees differ: lost %s has tree %s, current %s has tree %s; the rewrite changed %s, and it is not a rebase that kept every commit either: %d lost commits have a commit with the same patch on the trunk, and these have none: %s", lost, lostTree, current, currentTree, strings.Join(names, ", "), matched, strings.Join(unmatched, ", "))
			}
			rebased = matched
		}
		sides := []string{strconv.Itoa(rebased), strconv.Itoa(rebased)}
		if rebased == 0 {
			counts, err := git("rev-list", "--left-right", "--count", lost+"..."+current)
			if err != nil {
				return err
			}
			sides = strings.Fields(counts)
			if len(sides) != 2 {
				return externalf("git rev-list --left-right --count answered %q", counts)
			}
			if sides[0] != sides[1] {
				return usagef("the commit counts differ: %s commits only the lost tip %s has, %s only the current %s has; the trees match, but commits were added, dropped or squashed", sides[0], lost, sides[1], current)
			}
		}
		a = RewriteAcceptance{Lost: lost, Current: current, Tree: currentTree, Pin: AcceptedTrunkPrefix + lost, By: req.By, Why: req.Why}
		a.Rebased = rebased > 0
		if a.Commits, err = strconv.Atoi(sides[0]); err != nil {
			return externalf("git rev-list --left-right --count answered %q", sides[0])
		}
		green, err := git("for-each-ref", "--format=%(objectname)", heavy.GreenRef)
		if err != nil {
			return err
		}
		if green != "" {
			if on, err := TrunkAncestor(ctx, req.Root, green, tip); err != nil {
				return err
			} else if !on {
				if a.Rebased {
					return usagef("%s %s is not on the trunk, and a rebase onto a moved base has no commit with the same tree to carry it to: prove the trunk again (the loop's after-merge run does) or move the pointer, then accept", heavy.GreenRef, green)
				}
				carried, err := carryGreen(git, ctx, req.Root, green, lost, current)
				if err != nil {
					return err
				}
				a.GreenFrom, a.GreenTo = green, carried
			}
		}
		if _, err := git("update-ref", a.Pin, lost); err != nil {
			return err
		}
		if a.GreenTo != "" {
			if _, err := git("update-ref", heavy.GreenRef, a.GreenTo, a.GreenFrom); err != nil {
				return err
			}
		}
		if _, err := git("update-ref", ObservedTrunkRef, current); err != nil {
			return err
		}
		if _, err := git("update-ref", "-d", LostTrunkRef); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,?,'') ON CONFLICT(id) DO UPDATE SET observed_tip=excluded.observed_tip,lost_tip=excluded.lost_tip`, current); err != nil {
			return err
		}
		if _, err := tx.Exec(heavy.RewritesDDL); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM trunk_rewrite_unmatched WHERE lost_tip = ?`, lost); err != nil {
			return err
		}
		pairs := oneForOnePairs
		evidence := "accept-rewrite:same-tree"
		if a.Rebased {
			pairs = rebasedPairs
			evidence = "accept-rewrite:patch-id"
		}
		at := req.At.Format(time.RFC3339)
		for _, pair := range pairs {
			if _, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at,evidence) VALUES(?,?,?,?) ON CONFLICT(old_sha) DO UPDATE SET new_sha=excluded.new_sha,recorded_at=excluded.recorded_at,evidence=excluded.evidence`, pair[0], pair[1], at, evidence); err != nil {
				return err
			}
		}
		body, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{Kind: RewriteAcceptedKind, ObjectType: "trunk", ObjectID: &lost, Payload: body, At: req.At.UTC()})
		return err
	})
	if err == nil {
		RegisterRewriteStore(req.Root, req.Store)
	}
	return a, err
}

func isUnmappedTrunkLanding(ctx context.Context, r *store.Reader, root, lost, tip string) (bool, error) {
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings')`).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	rows, err := r.Query(`SELECT DISTINCT l.trunk_after
		FROM landings l LEFT JOIN events e ON e.seq=l.event_seq AND e.kind='land.landing'
		WHERE l.result='landed' AND l.trunk_after != ''
		  AND COALESCE(json_extract(e.payload, '$.base_ref'), '') NOT LIKE 'refs/heads/patch/%'`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return false, err
		}
		shas = append(shas, sha)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	matched := false
	for _, sha := range shas {
		resolved, err := heavy.RewriteResolve(r, sha)
		if err != nil {
			return false, err
		}
		if resolved == lost {
			matched = true
			break
		}
	}
	if !matched {
		return false, nil
	}
	onTrunk, err := TrunkAncestor(ctx, root, lost, tip)
	if err != nil {
		return false, err
	}
	return !onTrunk, nil
}

func acceptedRewritePairs(ctx context.Context, root, lost, current string) (oneForOne, rebased [][2]string, err error) {
	oldCommits, oldIDs, err := PatchIDs(ctx, root, current+".."+lost)
	if err != nil {
		return nil, nil, err
	}
	newCommits, newIDs, err := PatchIDs(ctx, root, lost+".."+current)
	if err != nil {
		return nil, nil, err
	}
	if len(oldCommits) == len(newCommits) {
		for i := range oldCommits {
			oneForOne = append(oneForOne, [2]string{oldCommits[i], newCommits[i]})
		}
	}
	taken := map[int]bool{}
	cursor := 0
	for _, old := range oldCommits {
		id := oldIDs[old]
		if id == "" {
			continue
		}
		matched := -1
		for j := cursor; j < len(newCommits); j++ {
			if !taken[j] && newIDs[newCommits[j]] == id {
				matched, cursor = j, j+1
				break
			}
		}
		if matched < 0 {
			for j := 0; j < len(newCommits); j++ {
				if !taken[j] && newIDs[newCommits[j]] == id {
					matched = j
					break
				}
			}
		}
		if matched >= 0 {
			taken[matched] = true
			rebased = append(rebased, [2]string{old, newCommits[matched]})
		}
	}
	return oneForOne, rebased, nil
}

func patchMatches(git func(...string) (string, error), lost, current string) (int, []string, error) {
	out, err := git("cherry", current, lost)
	if err != nil {
		return 0, nil, err
	}
	matched := 0
	var unmatched []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		if f[0] == "-" {
			matched++
		} else {
			unmatched = append(unmatched, f[1])
		}
	}
	return matched, unmatched, nil
}

func carryGreen(git func(...string) (string, error), ctx context.Context, root, green, lost, current string) (string, error) {
	under, err := TrunkAncestor(ctx, root, green, lost)
	if err != nil {
		return "", err
	}
	if !under {
		return "", usagef("%s %s is on neither the trunk nor the lost history, so the rewrite cannot carry it", heavy.GreenRef, green)
	}
	depth, err := git("rev-list", "--count", "--first-parent", green+".."+lost)
	if err != nil {
		return "", err
	}
	carried, err := git("rev-parse", "--verify", current+"~"+depth)
	if err != nil {
		return "", usagef("%s %s is %s commits below the lost tip, and the current %s has no commit that far below it", heavy.GreenRef, green, depth, current)
	}
	trees, err := git("rev-parse", green+"^{tree}", carried+"^{tree}")
	if err != nil {
		return "", err
	}
	if t := strings.Fields(trees); len(t) != 2 || t[0] != t[1] {
		return "", usagef("%s %s and the commit that replaced it, %s, have different trees, so the proof does not carry", heavy.GreenRef, green, carried)
	}
	counts, err := git("rev-list", "--left-right", "--count", green+"..."+carried)
	if err != nil {
		return "", err
	}
	if c := strings.Fields(counts); len(c) != 2 || c[0] != c[1] {
		return "", usagef("%s %s and the commit that replaced it, %s, have histories of different lengths (%s), so the proof does not carry", heavy.GreenRef, green, carried, counts)
	}
	return carried, nil
}
