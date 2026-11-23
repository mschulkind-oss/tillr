package remotesync

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ForkTarget is a checkout selected by the recorded delivery decision and the
// patch ref the lander publishes.
type ForkTarget struct {
	// Checkout is the absolute path of the fork's own checkout.
	Checkout string
	// Ref is the branch the delivery moves: refs/heads/patch/<fork>.
	Ref string
	// Path is the repo-relative path the fence names.
	Path string
	// Fork is the fork's name.
	Fork string
}

// HeldForkRefFormat is the git for-each-ref format used to snapshot held fork refs.
const HeldForkRefFormat = "--format=%(refname) %(objectname)"

// HeldForkInputs records the upstream URL and support refs for a held fork candidate.
type HeldForkInputs struct {
	Upstream string `json:"upstream,omitempty"`
	Refs     string `json:"refs,omitempty"`
}

// Prefix returns the ref namespace prefix for storing held-fork support refs.
func (s *HeldForkInputs) Prefix(pinRef string) string {
	binding := pinRef + "\x00" + s.Upstream + "\x00" + s.Refs
	return fmt.Sprintf("refs/tillr/held-fork-inputs/%x/", sha256.Sum256([]byte(binding)))
}

// ForkRefLines splits newline-delimited ref lines, returning nil for empty input.
func ForkRefLines(refs string) []string {
	if refs == "" {
		return nil
	}
	return strings.Split(refs, "\n")
}

// CandidatePinRef returns the pin ref binding a run to its delivery ref and base.
// When baseRef is empty, fallbackPinRef is returned unchanged.
func CandidatePinRef(runID, baseRef, baseSHA, fallbackPinRef string) string {
	if baseRef == "" {
		return fallbackPinRef
	}
	binding := runID + "\x00" + baseRef + "\x00" + baseSHA
	return fmt.Sprintf("refs/tillr/held-forks/%x", sha256.Sum256([]byte(binding)))
}

// ForkTargetAtLane resolves the ForkTarget for ref on lane under root.
func ForkTargetAtLane(root, ref, lane string) (ForkTarget, error) {
	name, ok := strings.CutPrefix(ref, ForkPatchPrefix)
	if !ok || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return ForkTarget{}, fmt.Errorf("invalid fork delivery ref %q", ref)
	}
	if lane == "" || lane == "." || lane == ".." || strings.ContainsAny(lane, "/\\") {
		return ForkTarget{}, fmt.Errorf("invalid held fork lane %q", lane)
	}
	path := ".forks/lanes/" + lane + "/" + name
	checkout, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(path)))
	return ForkTarget{Fork: name, Ref: ref, Path: path, Checkout: checkout}, err
}

// CheckHeldStagePath checks ancestors before mkdir: a missing leaf can still sit below
// a symlink into a sibling's checkout.
func CheckHeldStagePath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for path := abs; ; path = filepath.Dir(path) {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if real, err := filepath.EvalSymlinks(path); err != nil || real != path {
			return fmt.Errorf("lane staging path %s aliases another path", abs)
		}
		return nil
	}
}

// RebaseHeldPath rewrites path from prefix from to prefix to when it matches.
func RebaseHeldPath(path, from, to string) string {
	if path == from {
		return to
	}
	if suffix, ok := strings.CutPrefix(path, from+string(filepath.Separator)); ok {
		return filepath.Join(to, suffix)
	}
	return path
}

// RebindForkFence rewrites fence entries under from.Path to to.Path.
func RebindForkFence(body string, from, to ForkTarget) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		entry := strings.TrimSpace(line)
		if entry == from.Path || strings.HasPrefix(entry, from.Path+"/") {
			lines[i] = strings.Replace(line, from.Path, to.Path, 1)
		}
	}
	return strings.Join(lines, "\n")
}
