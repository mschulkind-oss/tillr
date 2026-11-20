package drain

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// rewriteAcceptedKind records an operator accepting a trunk rewrite that kept the content.
const rewriteAcceptedKind = "drain.trunk-rewrite-accepted"

// acceptedTrunkPrefix pins each accepted lost tip, so the history the operator replaced
// stays readable after the lost ref is cleared and reflogs expire.
const acceptedTrunkPrefix = "refs/tillr/trunk-history/accepted/"

func init() {
	if err := event.Register(rewriteAcceptedKind, Utility); err != nil {
		panic(err)
	}
}

// AcceptRewriteRequest names the rewrite an operator accepts and why.
type AcceptRewriteRequest struct {
	Root, Store   string
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
	Rebased bool   `json:"rebased,omitempty"`
	Pin     string `json:"pin"`
	// GreenFrom and GreenTo name the proof pointer's move, when the rewrite carried it.
	GreenFrom string `json:"green_from,omitempty"`
	GreenTo   string `json:"green_to,omitempty"`
	By        string `json:"by"`
	Why       string `json:"why"`
}

// AcceptRewrite clears a trunk-rewritten hold for a rewrite that kept the content. Two
// shapes are accepted. The first is described below. The second is a rebase onto a moved
// base, which is what `git pull --rebase` does to unpushed landings: the trees differ
// because the new base brought commits of its own, and every commit only the lost tip
// reaches has a commit with the same patch on the trunk. For the first shape, the
// lost tip and the named current commit have the same tree, the current commit is on the
// trunk, and each side of lost...current has the same number of commits, so the rewrite
// replaced commits one for one (a re-authoring or a message edit) rather than adding,
// dropping or squashing any. It accepts a rewrite whether or not a pass has recorded it
// yet: an observed tip the trunk no longer contains is the same rewrite, unrecorded.
//
// The lost tip stays pinned under refs/tillr/trunk-history/accepted/, the observation
// becomes the named current commit, and the acceptance is an event. Anything else is a
// refusal that names what differs, and changes nothing.
func AcceptRewrite(ctx context.Context, req AcceptRewriteRequest) (RewriteAcceptance, error) {
	var a RewriteAcceptance
	if strings.TrimSpace(req.By) == "" || strings.TrimSpace(req.Why) == "" {
		return a, Usagef("accept-rewrite needs --by <who> and --why <reason>")
	}
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", req.Root}, args...)...)
		cmd.Env = boundaryGitEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", Externalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return strings.TrimSpace(string(out)), nil
	}
	commit := func(flag, rev string) (string, error) {
		sha, err := git("rev-parse", "--verify", "--quiet", rev+"^{commit}")
		if err != nil || sha == "" {
			return "", Usagef("%s %q is not a commit in %s", flag, rev, req.Root)
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
		h, err := readTrunkHistory(tx.Reader())
		if err != nil {
			return err
		}
		tip, err := git("rev-parse", TrunkRef)
		if err != nil {
			return err
		}
		recorded := h.Lost
		if recorded == "" && h.Observed != "" {
			ancestor, err := trunkAncestor(ctx, req.Root, h.Observed, tip)
			if err != nil {
				return err
			}
			if !ancestor {
				recorded = h.Observed // rewritten since the last pass, not yet recorded
			}
		}
		if recorded == "" {
			return Usagef("no rewrite is recorded: the trunk %s contains the observed tip %q, so there is nothing to accept", tip, h.Observed)
		}
		if recorded != lost {
			return Usagef("the recorded lost tip is %s, not %s: name the tip the hold reports (trunk-rewritten: <lost> -> <current>)", recorded, lost)
		}
		if on, err := trunkAncestor(ctx, req.Root, current, tip); err != nil {
			return err
		} else if !on {
			return Usagef("--current %s is not on the trunk: the trunk %s does not contain it", current, tip)
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
			// A rebase onto a moved base changes the tree, because the new base brought
			// commits of its own. It still kept the content when every commit only the
			// lost tip reaches has a commit with the same patch under the current one.
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
				return Usagef("the trees differ: lost %s has tree %s, current %s has tree %s; the rewrite changed %s, and it is not a rebase that kept every commit either: %d lost commits have a commit with the same patch on the trunk, and these have none: %s", lost, lostTree, current, currentTree, strings.Join(names, ", "), matched, strings.Join(unmatched, ", "))
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
				return Externalf("git rev-list --left-right --count answered %q", counts)
			}
			if sides[0] != sides[1] {
				return Usagef("the commit counts differ: %s commits only the lost tip %s has, %s only the current %s has; the trees match, but commits were added, dropped or squashed", sides[0], lost, sides[1], current)
			}
		}
		a = RewriteAcceptance{Lost: lost, Current: current, Tree: currentTree, Pin: acceptedTrunkPrefix + lost, By: req.By, Why: req.Why}
		a.Rebased = rebased > 0
		if a.Commits, err = strconv.Atoi(sides[0]); err != nil {
			return Externalf("git rev-list --left-right --count answered %q", sides[0])
		}
		// The proof pointer names a tree that passed. When the rewrite moved it off the
		// trunk, it moves to the commit that replaced it, which must have the same tree
		// and the same history length; otherwise the acceptance is refused before
		// anything moves, because lanes cannot stage from a proof the trunk lacks.
		green, err := git("for-each-ref", "--format=%(objectname)", GreenRef)
		if err != nil {
			return err
		}
		if green != "" {
			if on, err := trunkAncestor(ctx, req.Root, green, tip); err != nil {
				return err
			} else if !on {
				if a.Rebased {
					return Usagef("%s %s is not on the trunk, and a rebase onto a moved base has no commit with the same tree to carry it to: prove the trunk again (the loop's after-merge run does) or move the pointer, then accept", GreenRef, green)
				}
				carried, err := carryGreen(git, ctx, req.Root, green, lost, current)
				if err != nil {
					return err
				}
				a.GreenFrom, a.GreenTo = green, carried
			}
		}
		// Pin the lost tip before the lost ref goes, and move observed before the row,
		// in the order observeTrunk keeps: no stored tip is ever collectible.
		if _, err := git("update-ref", a.Pin, lost); err != nil {
			return err
		}
		if a.GreenTo != "" {
			if _, err := git("update-ref", GreenRef, a.GreenTo, a.GreenFrom); err != nil {
				return err
			}
		}
		if _, err := git("update-ref", observedTrunkRef, current); err != nil {
			return err
		}
		if _, err := git("update-ref", "-d", lostTrunkRef); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,?,'') ON CONFLICT(id) DO UPDATE SET observed_tip=excluded.observed_tip,lost_tip=excluded.lost_tip`, current); err != nil {
			return err
		}
		body, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{Kind: rewriteAcceptedKind, ObjectType: "trunk", ObjectID: &lost, Payload: body, At: req.At.UTC()})
		return err
	})
	return a, err
}

// patchMatches compares the commits only the lost tip reaches with the current commit's
// history by patch, as `git cherry` does: it answers how many lost commits have a commit
// with the same patch under current, and the lost commits that have none.
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

// carryGreen answers the commit of the rewritten history that replaced green: the one
// as far below current as green is below lost, with green's tree and as many commits
// beneath it.
func carryGreen(git func(...string) (string, error), ctx context.Context, root, green, lost, current string) (string, error) {
	under, err := trunkAncestor(ctx, root, green, lost)
	if err != nil {
		return "", err
	}
	if !under {
		return "", Usagef("%s %s is on neither the trunk nor the lost history, so the rewrite cannot carry it", GreenRef, green)
	}
	depth, err := git("rev-list", "--count", "--first-parent", green+".."+lost)
	if err != nil {
		return "", err
	}
	carried, err := git("rev-parse", "--verify", current+"~"+depth)
	if err != nil {
		return "", Usagef("%s %s is %s commits below the lost tip, and the current %s has no commit that far below it", GreenRef, green, depth, current)
	}
	trees, err := git("rev-parse", green+"^{tree}", carried+"^{tree}")
	if err != nil {
		return "", err
	}
	if t := strings.Fields(trees); len(t) != 2 || t[0] != t[1] {
		return "", Usagef("%s %s and the commit that replaced it, %s, have different trees, so the proof does not carry", GreenRef, green, carried)
	}
	counts, err := git("rev-list", "--left-right", "--count", green+"..."+carried)
	if err != nil {
		return "", err
	}
	if c := strings.Fields(counts); len(c) != 2 || c[0] != c[1] {
		return "", Usagef("%s %s and the commit that replaced it, %s, have histories of different lengths (%s), so the proof does not carry", GreenRef, green, carried, counts)
	}
	return carried, nil
}
