package land

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// forkPinUpdate holds a prepared trunk commit that updates yolo-packs/fork-pins/<fork>
// alongside a fork patch-branch delivery.
type forkPinUpdate struct {
	ref         string
	trunkBefore string
	commit      string
	plan        *OwnerPlan
}

// prepareForkPin builds a trunk commit updating yolo-packs/fork-pins/<fork> when the
// trunk tracks a pin for this fork and its recorded tip or upstream merge base differs
// from the delivered commit. When the trunk tracks no pin for the fork (or the pin
// already matches), it returns the zero value and no trunk commit is made.
func (t *Trunk) prepareForkPin(req LandingRequest, tip string, rebase forkRebase) (forkPinUpdate, error) {
	fork := strings.TrimPrefix(PatchRef(req.BaseRef), patchPrefix)
	pinRel := "yolo-packs/fork-pins/" + fork

	trunkRef, err := t.Branch()
	if err != nil {
		// A bare or detached-HEAD repository with no tracked pin has nothing to update.
		if _, _, existsErr := t.runGit(nil, "cat-file", "-e", "HEAD:"+pinRel); existsErr != nil {
			return forkPinUpdate{}, nil
		}
		return forkPinUpdate{}, err
	}
	trunkTip, err := t.Head(trunkRef)
	if err != nil {
		return forkPinUpdate{}, err
	}

	raw, stderr, err := t.runGit(nil, "cat-file", "-p", trunkTip+":"+pinRel)
	if err != nil {
		if _, _, existsErr := t.runGit(nil, "cat-file", "-e", trunkTip+":"+pinRel); existsErr != nil {
			return forkPinUpdate{}, nil
		}
		return forkPinUpdate{}, Externalf("reading %s at %s: %s", pinRel, trunkTip, failureText(stderr, err))
	}
	lines := strings.Split(strings.TrimRight(raw, "\r\n"), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[1]) == "" {
		return forkPinUpdate{}, Externalf("invalid fork pin %s at %s: expected 3 lines (tip, remote, base)", pinRel, trunkTip)
	}
	remote := lines[1]

	base, err := t.forkPinMergeBase(req.BaseRef, fork, tip, rebase)
	if err != nil {
		return forkPinUpdate{}, err
	}
	newContent := fmt.Sprintf("%s\n%s\n%s\n", tip, remote, base)
	if newContent == raw {
		return forkPinUpdate{}, nil
	}

	identity, err := t.LandingIdentity()
	if err != nil {
		return forkPinUpdate{}, err
	}
	blob, stderr, err := t.runGitStdin(t.repo, []byte(newContent), nil, "hash-object", "-w", "--stdin")
	if err != nil {
		return forkPinUpdate{}, Externalf("writing blob for %s: %s", pinRel, failureText(stderr, err))
	}
	blob = strings.TrimSpace(blob)

	tmpDir, err := os.MkdirTemp("", "tillr-fork-pin-")
	if err != nil {
		return forkPinUpdate{}, Externalf("creating temporary index dir for %s: %v", pinRel, err)
	}
	defer os.RemoveAll(tmpDir)
	idxEnv := []string{"GIT_INDEX_FILE=" + filepath.Join(tmpDir, "index")}
	if _, stderr, err := t.runGit(idxEnv, "read-tree", trunkTip); err != nil {
		return forkPinUpdate{}, Externalf("reading trunk tree %s for %s: %s", trunkTip, pinRel, failureText(stderr, err))
	}
	if _, stderr, err := t.runGit(idxEnv, "update-index", "--cacheinfo", "100644,"+blob+","+pinRel); err != nil {
		return forkPinUpdate{}, Externalf("updating index entry %s: %s", pinRel, failureText(stderr, err))
	}
	treeOut, stderr, err := t.runGit(idxEnv, "write-tree")
	if err != nil {
		return forkPinUpdate{}, Externalf("writing tree for %s: %s", pinRel, failureText(stderr, err))
	}
	newTree := strings.TrimSpace(treeOut)

	shortTip := tip
	if len(shortTip) > 10 {
		shortTip = shortTip[:10]
	}
	message := fmt.Sprintf("chore(fork-pins): update %s pin to %s\n", fork, shortTip)
	authorDate := req.At.Local().Format(time.RFC3339)
	commitOut, stderr, err := t.runGit(stampedCommitEnv(identity, authorDate, req.At),
		"commit-tree", newTree, "-p", trunkTip, "-m", message)
	if err != nil {
		return forkPinUpdate{}, Externalf("writing pin commit for %s: %s", pinRel, failureText(stderr, err))
	}
	commit := strings.TrimSpace(commitOut)

	plan, err := t.PlanOwner(trunkRef, req.RunID, trunkTip, commit)
	if err != nil {
		return forkPinUpdate{}, err
	}
	return forkPinUpdate{
		ref:         trunkRef,
		trunkBefore: trunkTip,
		commit:      commit,
		plan:        plan,
	}, nil
}

// forkPinMergeBase resolves the followed upstream ref in .forks/<fork> (matching
// bin/pi-forks's followed_ref) and computes git merge-base <tip> <upstreamRef>.
func (t *Trunk) forkPinMergeBase(baseRef, fork, tip string, rebase forkRebase) (string, error) {
	cache := t.forkCache(baseRef)
	sourceRef := t.forkSourceRef(fork)
	upstreamRef := t.resolveForkUpstreamRef(cache, sourceRef)
	if upstreamRef != "" {
		out, err := t.forkGit(cache, "merge-base", tip, upstreamRef)
		if err != nil {
			return "", err
		}
		base := strings.TrimSpace(out)
		if rebase.release != "" && base != rebase.release &&
			t.isForkAncestor(cache, base, rebase.release) &&
			t.isForkAncestor(cache, rebase.release, tip) {
			return rebase.release, nil
		}
		return base, nil
	}
	if rebase.release != "" && t.isForkAncestor(cache, rebase.release, tip) {
		return rebase.release, nil
	}
	return "", Externalf("resolving upstream merge base for fork %s in %s: no followed upstream ref found", fork, cache)
}

func (t *Trunk) isForkAncestor(dir, ancestor, descendant string) bool {
	_, _, err := t.runGitIn(dir, nil, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

// forkSourceRef reads the ?ref= query parameter from the pack contribution declaring
// patch_branch for patch/<fork>, or "" if no pack manifest declares it.
func (t *Trunk) forkSourceRef(fork string) string {
	wantBranch := "patch/" + fork
	matches, err := filepath.Glob(filepath.Join(t.repo, "yolo-packs", "*", "pack.json"))
	if err != nil {
		return ""
	}
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var manifest struct {
			Contributes []struct {
				Source      string `json:"source"`
				PatchBranch string `json:"patch_branch"`
			} `json:"contributes"`
		}
		if json.Unmarshal(body, &manifest) != nil {
			continue
		}
		for _, c := range manifest.Contributes {
			if !patchBranchMatches(c.PatchBranch, wantBranch) {
				continue
			}
			if u, err := url.Parse(c.Source); err == nil {
				if ref := u.Query().Get("ref"); ref != "" {
					return ref
				}
			}
		}
	}
	return ""
}

func patchBranchMatches(locator, wantBranch string) bool {
	if locator == "" {
		return false
	}
	if locator == wantBranch {
		return true
	}
	if u, err := url.Parse(locator); err == nil {
		return u.Query().Get("ref") == wantBranch
	}
	return false
}

func (t *Trunk) resolveForkUpstreamRef(cache, ref string) string {
	if ref == "" {
		out, _, err := t.runGitIn(cache, nil, "symbolic-ref", "--quiet", "refs/remotes/upstream/HEAD")
		candidate := strings.TrimSpace(out)
		if err != nil || candidate == "" {
			candidate = "refs/remotes/upstream/main"
		}
		if _, _, err := t.runGitIn(cache, nil, "rev-parse", "--verify", "--quiet", candidate+"^{commit}"); err == nil {
			return candidate
		}
		return ""
	}
	if isHexCommit(ref) {
		if _, _, err := t.runGitIn(cache, nil, "cat-file", "-e", ref+"^{commit}"); err == nil {
			return ref
		}
		return ""
	}
	if _, _, err := t.runGitIn(cache, nil, "show-ref", "--verify", "--quiet", "refs/remotes/upstream/"+ref); err == nil {
		return "refs/remotes/upstream/" + ref
	}
	if _, _, err := t.runGitIn(cache, nil, "show-ref", "--verify", "--quiet", "refs/tags/"+ref); err == nil {
		return "refs/tags/" + ref
	}
	return ""
}

func isHexCommit(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
