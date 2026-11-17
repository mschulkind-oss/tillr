package land

import (
	"os"
	"path/filepath"
	"strings"
)

func (t *Trunk) forkCache(ref string) string {
	return filepath.Join(t.repo, ".forks", strings.TrimPrefix(PatchRef(ref), "refs/heads/patch/"))
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
func (t *Trunk) prepareForkCache(ref, tip string) error {
	cache := t.forkCache(ref)
	branch, err := t.forkGit(cache, "symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branch) != "main" {
		return Externalf("fork cache %s must have main checked out before delivery", cache)
	}
	paths, err := t.UncommittedPaths(cache)
	if err != nil {
		return err
	}
	if len(paths) != 0 {
		return Externalf("fork cache %s has uncommitted work; delivery will not overwrite it", cache)
	}
	if _, err := t.forkGit(cache, "fetch", "--quiet", "--no-tags", t.repo, tip); err != nil {
		return err
	}
	if _, err := t.forkGit(cache, "merge-base", "--is-ancestor", "HEAD", tip); err != nil {
		return Externalf("fork cache %s has unlanded or divergent commits; delivery will not reset it", cache)
	}
	return nil
}

// RefreshForkCache repairs both main and the cache's copy of the delivery ref. It runs
// before the receipt, including crash recovery, so a recorded landing has a usable cache.
func (t *Trunk) RefreshForkCache(ref, tip string) error {
	ref = PatchRef(ref)
	cache := t.forkCache(ref)
	actual, err := t.Head(ref)
	if err != nil {
		return err
	}
	if actual != tip {
		return Externalf("fork delivery %s moved during cache refresh: expected %s, got %s", ref, tip, actual)
	}
	if err := t.prepareForkCache(ref, tip); err != nil {
		return err
	}
	if _, err := t.forkGit(cache, "-c", "merge.autoStash=false", "merge", "--ff-only", "--no-edit", tip); err != nil {
		return err
	}
	_, err = t.forkGit(cache, "fetch", "--quiet", "--no-tags", t.repo, "+"+ref+":"+ref)
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
