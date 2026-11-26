package gatespans

import (
	"context"
	"os/exec"
	"strings"
)

// gateUsageSignatures are the phrases a program prints when it refused its own arguments
// (Go's flag package, argparse, and the loop adapter's "answered exit 2" line for a gate
// face that answered a usage refusal). A gate that says one of these before it judged
// anything is not "no verdict yet": the same program, the same arguments and the same
// installed binary answer the same words on every later pass.
var gateUsageSignatures = []string{
	"flag provided but not defined",
	"unrecognized arguments",
	"unknown flag",
	"unknown option",
	"unknown shorthand flag",
	"unexpected argument",
	"invalid choice:",
	"answered exit 2:",
}

// GateUsageError reports whether a gate's stderr tail names a usage refusal.
func GateUsageError(stderr string) bool {
	lower := strings.ToLower(stderr)
	for _, sig := range gateUsageSignatures {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// GateErrorLines is the first lines of the gate's own error, for a hold's reason.
func GateErrorLines(stderr string, max int) string {
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
			if len(lines) == max {
				break
			}
		}
	}
	return strings.Join(lines, " | ")
}

const (
	GateScriptDir = "tools/gate/"
	GateBinaryDir = "tools/tillr-gate/"
)

// GateBinaryDisagreement says plainly when a candidate changes both a gate script and the
// binary the script calls. The gate judges every candidate with the INSTALLED tillr-gate,
// built from the trunk, never with the candidate's own build: a candidate that built its own
// judge would certify itself. So the script in such a candidate must stay compatible with
// the installed binary, and the binary change must land first. Empty when the candidate
// touches only one side.
func GateBinaryDisagreement(paths []string) string {
	var script, binary bool
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, GateScriptDir):
			script = true
		case strings.HasPrefix(p, GateBinaryDir):
			binary = true
		}
	}
	if !script || !binary {
		return ""
	}
	return "this candidate changes both " + GateScriptDir + " (the gate script) and " + GateBinaryDir +
		" (the binary it calls), but the gate runs the installed tillr-gate built from the trunk, " +
		"not the candidate's own build: the script must stay compatible with the installed binary, " +
		"so land the binary change first and the script change after"
}

// CandidateChangedPaths lists base..head in the candidate's worktree; a failure reads as none.
func CandidateChangedPaths(ctx context.Context, worktree, baseSHA, headSHA string) []string {
	out, err := exec.CommandContext(ctx, "git", "-C", worktree, "diff", "--name-only", baseSHA, headSHA).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// GateErrorDetail is the hold's reason for a deterministic gate error: the gate's first
// error lines, then the binary disagreement when the candidate has one.
func GateErrorDetail(stderr string, paths []string) string {
	detail := "the gate program refused its own arguments and judged nothing; the same program and installed binary answer the same on every pass: " +
		GateErrorLines(stderr, 3)
	if d := GateBinaryDisagreement(paths); d != "" {
		detail += "; " + d
	}
	return detail
}
