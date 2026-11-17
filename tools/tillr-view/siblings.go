// The planner this face asks: which `tillr-drain` it runs, and whether that program is the
// build this server was made with.
//
// `/api/v1/why` and the `waiting` face answer with the planner's own decision, so they run
// `tillr-drain why` (why.go keeps that process boundary). Until 2026-11-17 the program was
// found on PATH by name alone. That failed two ways, both silently: a server started from a
// shell whose PATH held no tillr binaries answered every Waiting read with `exec:
// "tillr-drain": executable file not found in $PATH`, and a server whose PATH held a stale
// copy earlier than the fresh build ran the stale planner, whose rules are not the ones the
// server was built beside. Both are closed here:
//
//   - The program is resolved explicitly: `--tillr-drain <path>` when the caller names one,
//     else the `tillr-drain` in this executable's own directory (where `just observatory`
//     and `just tillr-build` put both), else the pack launcher's same-digest sibling, and
//     only then PATH.
//   - Whatever was found must answer with a build stamp that matches this server's. A
//     planner that names no build, or a different one, is refused with the sentence that
//     fixes it, rather than answering with another vintage's rules.
package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// plannerProgram is the `tillr-drain` this process runs, and how it was found. How is one of
// "flag", "beside" or "PATH", because "which planner is this" is the first question a wrong
// Waiting answer raises.
type plannerProgram struct {
	Path string
	How  string
}

func (p plannerProgram) String() string {
	switch p.How {
	case "flag":
		return p.Path + " (named by --tillr-drain)"
	case "beside":
		return p.Path + " (beside this server)"
	default:
		return p.Path + " (found on PATH)"
	}
}

// plannerError is a planner that could not be asked, or one whose answer is refused. Kind
// is plannerUnavailable or plannerMismatch, which are different API codes because they have
// different remedies; neither is a statement about the store or about the drain moving.
type plannerError struct {
	Kind string
	Msg  string
}

func (e *plannerError) Error() string { return e.Msg }

const (
	plannerUnavailable = "unavailable"
	plannerMismatch    = "mismatch"
)

// plannerRemedy is the one command that puts a matching planner beside the server.
const plannerRemedy = "run `just observatory` (or `just observatory-dev`) from the checkout, which builds both programs from it, or pass --tillr-drain <path> naming the tillr-drain built from the same tree"

// resolvePlanner finds the planner for this process: the named one, else the sibling beside
// os.Executable, else PATH.
func resolvePlanner(explicit string) (plannerProgram, error) {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}
	return resolvePlannerFrom(explicit, exe, exec.LookPath)
}

// resolvePlannerFrom is resolvePlanner with its two inputs named, so a test can say which
// executable is asking and what PATH would answer without replacing the test binary.
func resolvePlannerFrom(explicit, exe string, lookPath func(string) (string, error)) (plannerProgram, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err == nil {
			err = executableFile(path)
		}
		if err != nil {
			return plannerProgram{}, &plannerError{Kind: plannerUnavailable,
				Msg: fmt.Sprintf("--tillr-drain %s is not a program this server can run (%v): %s", explicit, err, plannerRemedy)}
		}
		return plannerProgram{Path: path, How: "flag"}, nil
	}
	for _, candidate := range siblingCandidates(exe, "tillr-drain") {
		if executableFile(candidate) == nil {
			return plannerProgram{Path: candidate, How: "beside"}, nil
		}
	}
	if path, err := lookPath("tillr-drain"); err == nil {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return plannerProgram{Path: path, How: "PATH"}, nil
	}
	dir := "this server's directory"
	if exe != "" {
		dir = filepath.Dir(exe)
	}
	return plannerProgram{}, &plannerError{Kind: plannerUnavailable,
		Msg: fmt.Sprintf("no tillr-drain beside this server (in %s) and none on PATH, so the planner cannot be asked: %s", dir, plannerRemedy)}
}

// siblingCandidates are the paths a sibling program of exe would have. A plain build
// directory holds `tillr-drain` beside `tillr-view`; the pack launcher's cache holds
// `tillr-drain-<digest>` beside `tillr-view-<digest>`, the digest being the whole source
// tree's, so the same digest is the same tree.
func siblingCandidates(exe, name string) []string {
	if exe == "" {
		return nil
	}
	dir := filepath.Dir(exe)
	out := []string{filepath.Join(dir, name)}
	if digest := launcherDigest(exe); digest != "" {
		out = append(out, filepath.Join(dir, name+"-"+digest))
	}
	return out
}

// launcherDigest is the source digest the pack launcher names its cache file with
// (`<program>-<64 hex>`), or "" for an executable named any other way. It is the same rule
// tillr-drain's buildDigest reads, spelled here because a reader must not link the writer.
func launcherDigest(exe string) string {
	base := filepath.Base(exe)
	i := strings.LastIndexByte(base, '-')
	if i < 0 {
		return ""
	}
	suffix := base[i+1:]
	if len(suffix) != sha256.Size*2 {
		return ""
	}
	for _, r := range suffix {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return suffix
}

func executableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("a directory")
	}
	if info.Mode()&0o111 == 0 {
		return errors.New("not executable")
	}
	return nil
}

// checkPlannerBuild refuses a planner that is not this server's build. The comparison is
// made on the identity both programs can state:
//
//   - both carry a VCS revision (a `go build` in a checkout): the revision and the dirty flag
//     must agree;
//   - otherwise both are pack-launcher builds: the source digest in their names must agree.
//
// A planner that names no build at all predates the stamp in its why answer, which is itself
// proof it is older than this server. A server that can state no identity of its own (a test
// binary, or `-buildvcs=false`) has nothing to compare against, and is not refused for it.
func checkPlannerBuild(self buildStamp, planner *drainBuild, program plannerProgram) error {
	selfDigest := launcherDigest(self.Executable)
	if self.Revision == "" && selfDigest == "" {
		return nil
	}
	if !planner.named() {
		return &plannerError{Kind: plannerMismatch,
			Msg: fmt.Sprintf("the planner %s names no build, so it is older than this server (%s): %s", program, self.String(), plannerRemedy)}
	}
	if self.Revision != "" && planner.Revision != "" {
		if self.Revision == planner.Revision && self.Modified == planner.Modified {
			return nil
		}
	} else if selfDigest != "" && planner.Digest == selfDigest {
		return nil
	}
	return &plannerError{Kind: plannerMismatch,
		Msg: fmt.Sprintf("the planner %s was built from %s, and this server from %s, so its answer would be another build's rules: %s",
			program, drainBuildLabel(planner), buildLabel(self.Revision, self.Modified, selfDigest), plannerRemedy)}
}

func drainBuildLabel(b *drainBuild) string {
	return buildLabel(b.Revision, b.Modified, launcherDigest(b.Executable))
}

func buildLabel(revision string, modified bool, digest string) string {
	label := "no revision"
	if revision != "" {
		label = revision
		if len(label) > 12 {
			label = label[:12]
		}
		if modified {
			label += "+dirty"
		}
	}
	if digest != "" {
		label += " (digest " + digest[:12] + ")"
	}
	return label
}
