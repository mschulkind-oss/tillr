package land

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mschulkind-oss/tillr/record"
)

const patchPrefix = "refs/heads/patch/"

// forkRebase carries only an already verified old -> new transition. A later
// delivery can be the cache's target on recovery, but never the CAS base.
type forkRebase struct{ old, new string }

func (r forkRebase) verified() bool { return r.old != "" }

func patchHistoryRef(ref, old string) string {
	return "refs/tillr/patch-history/" + strings.TrimPrefix(ref, patchPrefix) + "/" + old
}

// Rewrites require the retained fork gate artifacts for this run, not just a
// verdict passed by the caller that happens to bind the same tree and base.
func (t *Trunk) hasRebaseVerdict(req LandingRequest) bool {
	dir := filepath.Join(t.repo, ".tillr", "loop", req.RunID)
	body, err := os.ReadFile(filepath.Join(dir, "gate.verdict.json"))
	if err != nil {
		return false
	}
	verdict, err := DecodeVerdict(body)
	if err != nil || verdict.Result != VerdictPass || !reflect.DeepEqual(verdict, req.Verdict) {
		return false
	}
	body, err = os.ReadFile(filepath.Join(dir, "gate.change.json"))
	if err != nil {
		return false
	}
	change, err := record.Decode(body)
	var candidate struct {
		Base string `json:"base_sha"`
		Head string `json:"head_sha"`
		Tree string `json:"tree_sha"`
	}
	if err != nil || change.Kind != "gate.change" || change.ID != "change-"+req.RunID ||
		json.Unmarshal(change.Payload, &candidate) != nil ||
		candidate.Base != req.BaseSHA || candidate.Head != req.HeadSHA || candidate.Tree != verdict.TreeSHA {
		return false
	}
	body, err = os.ReadFile(filepath.Join(dir, "fork.registry.json"))
	var registry struct {
		Digest       string          `json:"digest"`
		Declarations json.RawMessage `json:"declarations"`
		InProcess    json.RawMessage `json:"in_process"`
	}
	if err != nil || json.Unmarshal(body, &registry) != nil || registry.Digest != verdict.RegistryDigest {
		return false
	}
	// The gate hashes the two raw registry arrays in this order. Checking the
	// contents as well as the declared digest binds the fork label to the verdict.
	hash := sha256.New()
	hash.Write(registry.Declarations)
	hash.Write(registry.InProcess)
	var declarations []struct{ ID, Kind, Label string }
	if fmt.Sprintf("%x", hash.Sum(nil)) != registry.Digest || json.Unmarshal(registry.Declarations, &declarations) != nil {
		return false
	}
	fork := strings.TrimPrefix(PatchRef(req.BaseRef), patchPrefix)
	for _, check := range declarations {
		if check.ID == "fork-build" && check.Kind == "fork-build" && check.Label == fork+": pack build" {
			return true
		}
	}
	return false
}

// releaseRebase checks the launch's selected release, not a tag supplied by the
// candidate. Resolve before taking land.lock: fetching upstream can block.
func (t *Trunk) releaseRebase(req LandingRequest) (forkRebase, error) {
	fork := strings.TrimPrefix(PatchRef(req.BaseRef), patchPrefix)
	root, err := filepath.Abs(t.repo)
	if err != nil {
		return forkRebase{}, Externalf("resolving delivery repository %s: %v", t.repo, err)
	}
	cmd := exec.CommandContext(t.ctx, filepath.Join(root, "bin", "pi-forks"), "patch-branch", "target", "--fetch", "--fork", fork)
	cmd.Dir = root
	cmd.Env = gitEnvWith([]string{"PI_FORKS_ROOT=" + root, "PI_FORKS_SERIES_ROOT=" + root})
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return forkRebase{}, Externalf("resolving the followed release for %s: %v", fork, err)
	}
	line := strings.TrimSuffix(string(out), "\n")
	fields := strings.Split(line, "\t")
	if len(fields) != 4 || strings.Contains(line, "\n") || !strings.HasSuffix(fields[0], "/"+fork) {
		return forkRebase{}, Externalf("invalid followed target for %s: %q", fork, out)
	}
	release := fields[3]
	if fields[1] != "release" || !t.IsAncestor(release, req.HeadSHA) || t.IsAncestor(release, req.BaseSHA) {
		return forkRebase{}, nil
	}
	merges, err := t.git("rev-list", "--merges", "--max-count=1", release+".."+req.HeadSHA)
	if err != nil {
		return forkRebase{}, Externalf("checking the release rebase range: %v", err)
	}
	if strings.TrimSpace(merges) != "" {
		return forkRebase{}, nil
	}
	return forkRebase{old: req.BaseSHA, new: req.HeadSHA}, nil
}

// moveForkRebase installs the recovery marker and history pin in the same ref
// transaction as the guarded delivery. An identical pin is reused, not moved.
func (t *Trunk) moveForkRebase(ref, run string, r forkRebase) error {
	ref = PatchRef(ref)
	worktree, err := t.WorktreeFor(ref)
	if err != nil {
		return err
	}
	if worktree != "" {
		return Externalf("cannot rebase %s while it is checked out in %s", ref, worktree)
	}
	pin := patchHistoryRef(ref, r.old)
	pinCommand := fmt.Sprintf("create %s %s", pin, r.old)
	if tip, _ := t.git("rev-parse", "--verify", "--quiet", pin); strings.TrimSpace(tip) == r.old {
		pinCommand = fmt.Sprintf("verify %s %s", pin, r.old)
	}
	txn := fmt.Sprintf("start\noption no-deref\n%s\noption no-deref\nupdate %s %s %s\noption no-deref\ncreate %s %s\nprepare\ncommit\n",
		pinCommand, ref, r.new, r.old, landedMarkerRef(run), r.new)
	if _, stderr, err := t.runGitStdin(t.repo, []byte(txn), nil, "update-ref", "-m", "tillr-land: release rebase", "--stdin"); err != nil {
		return Externalf("delivering release rebase of %s: %s", ref, failureText(stderr, err))
	}
	return nil
}
