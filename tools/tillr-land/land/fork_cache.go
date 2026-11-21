package land

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func (t *Trunk) forkCache(ref string) string {
	return filepath.Join(t.repo, ".forks", strings.TrimPrefix(PatchRef(ref), patchPrefix))
}

// ensureForkCache publishes a complete cache atomically. Its source is the exact gated
// fork copy, whose upstream metadata the fork gate retained. The initial main is the
// currently delivered tip, never an unpublished candidate that a final close may refuse.
func (t *Trunk) ensureForkCache(req LandingRequest, delivered string) error {
	cache := t.forkCache(req.BaseRef)
	if _, err := os.Stat(cache); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return Externalf("reading fork cache %s: %v", cache, err)
	}
	record := filepath.Join(t.repo, ".tillr", "loop", req.RunID, "fork-cache-source")
	body, err := os.ReadFile(record)
	if err != nil {
		return Externalf("fork cache %s is missing and its gated source %s is unreadable: %v; gate the fork candidate before delivery", cache, record, err)
	}
	source := strings.TrimSpace(string(body))
	head, err := t.forkGit(source, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != req.HeadSHA {
		return Externalf("fork cache source %s is not the gated head %s", source, req.HeadSHA)
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0755); err != nil {
		return Externalf("creating fork cache parent: %v", err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(cache), ".tillr-cache-")
	if err != nil {
		return Externalf("creating private fork cache stage: %v", err)
	}
	defer os.RemoveAll(stage)
	prepared := filepath.Join(stage, "checkout")
	if _, err := t.forkGit(t.repo, "clone", "--quiet", "--shared", "--no-checkout", source, prepared); err != nil {
		return err
	}
	// The delivered tip may be newer than this run's head during receipt recovery.
	if _, err := t.forkGit(prepared, "fetch", "--quiet", "--no-tags", t.repo, delivered); err != nil {
		return err
	}
	if _, err := t.forkGit(prepared, "checkout", "--quiet", "-B", "main", delivered); err != nil {
		return err
	}
	if _, err := t.forkGit(prepared, "fetch", "--quiet", "--no-tags", source,
		"+refs/remotes/upstream/*:refs/remotes/upstream/*", "+refs/tags/*:refs/tags/*"); err != nil {
		return err
	}
	upstream, _, upstreamErr := t.runGitIn(source, nil, "remote", "get-url", "upstream")
	if upstreamErr == nil {
		if _, err := t.forkGit(prepared, "remote", "add", "upstream", strings.TrimSpace(upstream)); err != nil {
			return err
		}
	}
	if _, err := t.forkGit(prepared, "fetch", "--quiet", "--no-tags", t.repo, "+"+PatchRef(req.BaseRef)+":"+PatchRef(req.BaseRef)); err != nil {
		return err
	}
	// Shared clone alternates must not depend on the retained run's lifetime. Root has
	// imported the candidate; copy every other source object before removing alternates.
	if _, err := t.forkGit(prepared, "repack", "-a", "-d"); err != nil {
		return err
	}
	common, err := t.forkGit(prepared, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	alternates := filepath.Join(prepared, strings.TrimSpace(common), "objects/info/alternates")
	if err := os.Remove(alternates); err != nil && !os.IsNotExist(err) {
		return Externalf("dissociating fork cache: %v", err)
	}
	if err := os.Rename(prepared, cache); err != nil {
		return Externalf("publishing prepared fork cache %s: %v", cache, err)
	}
	return nil
}

// prepareForkCache checks that delivery can refresh the shared cache without dropping
// work. Importing objects does not move main or the delivery branch.
func (t *Trunk) prepareForkCache(ref, tip string, rebase forkRebase, recovering bool) error {
	_, err := t.checkForkCache(ref, tip, rebase, recovering)
	return err
}

const cacheRefreshPrefix = "refs/tillr/cache-refresh/"

// A journal ref names the verified transition and detached source, and points at
// the intended main. It pins both command boundaries of an interrupted refresh.
type cacheRefresh struct {
	ref, source, target string
}

type forkCacheState struct {
	main, patch string
	detached    bool
	refresh     cacheRefresh
}

// checkForkCache also captures the exact local tips refresh may replace.
func (t *Trunk) checkForkCache(ref, tip string, rebase forkRebase, recovering bool) (forkCacheState, error) {
	var state forkCacheState
	ref = PatchRef(ref)
	cache := t.forkCache(ref)
	if _, err := t.forkGit(cache, "fetch", "--quiet", "--no-tags", t.repo, tip); err != nil {
		return state, err
	}
	branch, stderr, err := t.runGitIn(cache, nil, "symbolic-ref", "--quiet", "HEAD")
	var exit *exec.ExitError
	state.detached = errors.As(err, &exit) && exit.ExitCode() == 1 && strings.TrimSpace(stderr) == ""
	inDeliveredRange := func(commit string) bool {
		return recovering && t.IsAncestor(rebase.new, commit) && t.IsAncestor(commit, tip)
	}
	acceptMain := func(commit string) bool { return commit == rebase.old || inDeliveredRange(commit) }
	if !state.detached && (err != nil || strings.TrimSpace(branch) != "refs/heads/main") {
		return state, Externalf("fork cache %s must have main checked out before delivery", cache)
	}
	journal, err := t.forkGit(cache, "for-each-ref", "--format=%(refname) %(objectname)", cacheRefreshPrefix)
	if err != nil {
		return state, err
	}
	if fields := strings.Fields(journal); len(fields) != 0 {
		prefix := cacheRefreshPrefix + rebase.new + "/"
		if len(fields) != 2 || !rebase.verified() || !recovering || !strings.HasPrefix(fields[0], prefix) {
			return state, Externalf("fork cache %s has another unfinished cache refresh", cache)
		}
		state.refresh = cacheRefresh{fields[0], strings.TrimPrefix(fields[0], prefix), fields[1]}
		if !acceptMain(state.refresh.source) || !inDeliveredRange(state.refresh.target) {
			return state, Externalf("fork cache %s has an invalid cache refresh journal", cache)
		}
	} else if state.detached {
		return state, Externalf("fork cache %s must have main checked out before delivery", cache)
	}
	status, err := t.forkGit(cache, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return state, err
	}
	if strings.TrimSpace(status) != "" {
		return state, Externalf("fork cache %s has uncommitted work; delivery will not overwrite it", cache)
	}
	if !rebase.verified() {
		if _, err := t.forkGit(cache, "merge-base", "--is-ancestor", "HEAD", tip); err != nil {
			return state, Externalf("fork cache %s has unlanded or divergent commits; delivery will not reset it", cache)
		}
		return state, nil
	}
	out, err := t.forkGit(cache, "rev-parse", "refs/heads/main")
	if err != nil {
		return state, err
	}
	state.main = strings.TrimSpace(out)
	if !acceptMain(state.main) {
		return state, Externalf("fork cache %s has unlanded or divergent commits; delivery will not reset it", cache)
	}
	if move := state.refresh; move.ref != "" {
		if state.main != move.source && state.main != move.target && (state.detached || state.main != tip) {
			return state, Externalf("fork cache %s main moved independently during cache refresh", cache)
		}
		if state.detached {
			head, err := t.forkGit(cache, "rev-parse", "HEAD")
			if err != nil || strings.TrimSpace(head) != move.source {
				return state, Externalf("fork cache %s detached HEAD does not match the cache refresh source", cache)
			}
		}
	}
	out, stderr, err = t.runGitIn(cache, nil, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || strings.TrimSpace(stderr) != "" {
			return state, Externalf("reading cache delivery ref %s: %s", ref, failureText(stderr, err))
		}
		state.patch = strings.Repeat("0", len(tip))
		return state, nil
	}
	state.patch = strings.TrimSpace(out)
	if state.patch != rebase.old && !t.IsAncestor(state.patch, rebase.old) && !inDeliveredRange(state.patch) {
		return state, Externalf("fork cache %s has an independent %s at %s; delivery will not reset it", cache, ref, state.patch)
	}
	return state, nil
}

// retainForkInput puts the delivered object where a candidate's pin is read, even
// before the remote serves it. Share the gate reader's lock and immutable ref naming.
func (t *Trunk) retainForkInput(ref, tip string) error {
	name := strings.TrimPrefix(ref, patchPrefix)
	parent := filepath.Join(t.repo, ".forks", "inputs")
	cache := filepath.Join(parent, name)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return Externalf("creating fork input parent: %v", err)
	}
	lock, err := os.OpenFile(filepath.Join(parent, "."+name+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Externalf("opening fork input lock: %v", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return Externalf("locking fork input %s: %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(cache, ".git")); os.IsNotExist(err) {
		if _, err := os.Stat(filepath.Join(cache, "HEAD")); os.IsNotExist(err) {
			if _, err := t.forkGit(t.repo, "init", "--quiet", cache); err != nil {
				return err
			}
		}
	}
	inputRef := "refs/fork-inputs/" + tip
	if _, err := t.forkGit(cache, "cat-file", "-e", tip+"^{commit}"); err != nil {
		_, err = t.forkGit(cache, "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", t.repo, tip+":"+inputRef)
		return err
	}
	_, err = t.forkGit(cache, "update-ref", "--no-deref", inputRef, tip)
	return err
}

// RefreshForkCache repairs both main and the cache's copy of the delivery ref. It runs
// before the receipt, including crash recovery, so a recorded landing has a usable cache.
func (t *Trunk) RefreshForkCache(ref, tip string, rebase forkRebase) error {
	ref = PatchRef(ref)
	cache := t.forkCache(ref)
	checkDelivery := func() error {
		actual, err := t.Head(ref)
		if err == nil && actual != tip {
			err = Externalf("fork delivery %s moved during cache refresh: expected %s, got %s", ref, tip, actual)
		}
		return err
	}
	if err := checkDelivery(); err != nil {
		return err
	}
	state, err := t.checkForkCache(ref, tip, rebase, true)
	if err != nil {
		return err
	}
	if rebase.verified() {
		if err := checkDelivery(); err != nil {
			return err
		}
		if move := state.refresh; move.ref != "" {
			if !state.detached && state.main == tip {
				if err := t.clearCacheRefresh(cache, move); err != nil {
					return err
				}
			} else if err := t.finishCacheRefresh(cache, state, move); err != nil {
				return err
			}
			// A later delivery can exist when we recover a receipt. Recheck the
			// now-attached checkout before starting another refresh toward that tip.
			return t.RefreshForkCache(ref, tip, rebase)
		}
		if state.main != tip {
			move := cacheRefresh{cacheRefreshPrefix + rebase.new + "/" + state.main, state.main, tip}
			if _, err := t.forkGit(cache, "update-ref", "--no-deref", move.ref, tip, strings.Repeat("0", len(tip))); err != nil {
				return err
			}
			if err := t.finishCacheRefresh(cache, state, move); err != nil {
				return err
			}
		}
		if state.patch != tip {
			_, err = t.forkGit(cache, "update-ref", "--no-deref", ref, tip, state.patch)
		}
		if err != nil {
			return err
		}
	} else {
		if _, err := t.forkGit(cache, "-c", "merge.autoStash=false", "merge", "--ff-only", "--no-edit", tip); err != nil {
			return err
		}
		if _, err := t.forkGit(cache, "fetch", "--quiet", "--no-tags", t.repo, "+"+ref+":"+ref); err != nil {
			return err
		}
	}
	return t.retainForkInput(ref, tip)
}

func (t *Trunk) finishCacheRefresh(cache string, state forkCacheState, move cacheRefresh) error {
	if state.main != move.target && !state.detached {
		// Detach without changing the checkout. The journal already identifies
		// this source if the process stops before main can be moved.
		if _, err := t.forkGit(cache, "checkout", "--quiet", "--detach"); err != nil {
			return err
		}
		head, err := t.forkGit(cache, "rev-parse", "HEAD")
		if err != nil || strings.TrimSpace(head) != move.source {
			return Externalf("fork cache %s HEAD moved before detaching for cache refresh", cache)
		}
		state.detached = true
	}
	if state.main != move.target {
		if _, err := t.forkGit(cache, "update-ref", "refs/heads/main", move.target, move.source); err != nil {
			// Reattach only if Git can do so without overwriting work.
			_, _ = t.forkGit(cache, "checkout", "--quiet", "--no-overwrite-ignore", "main")
			return err
		}
	}
	if state.detached {
		// Checkout protects changes made since preflight, including ignored files.
		if _, err := t.forkGit(cache, "checkout", "--quiet", "--no-overwrite-ignore", "main"); err != nil {
			// Roll back only our own update. If interrupted, the journal also
			// recognizes either side of the compare-and-swap on the next retry.
			if _, rollback := t.forkGit(cache, "update-ref", "refs/heads/main", move.source, move.target); rollback == nil {
				_, _ = t.forkGit(cache, "checkout", "--quiet", "main")
			}
			return err
		}
	}
	main, err := t.forkGit(cache, "rev-parse", "refs/heads/main")
	if err != nil || strings.TrimSpace(main) != move.target {
		return Externalf("fork cache %s main moved during cache refresh", cache)
	}
	return t.clearCacheRefresh(cache, move)
}

func (t *Trunk) clearCacheRefresh(cache string, move cacheRefresh) error {
	_, err := t.forkGit(cache, "update-ref", "--no-deref", "-d", move.ref, move.target)
	return err
}

// forkGit classifies a cache operation that could not complete as infrastructure,
// never as a compiler verdict about the candidate.
func (t *Trunk) forkGit(dir string, args ...string) (string, error) {
	out, err := t.gitIn(dir, args...)
	if err != nil {
		return "", Externalf("fork cache git %s in %s: %v", strings.Join(args, " "), dir, err)
	}
	return out, nil
}
