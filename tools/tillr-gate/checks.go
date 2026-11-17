package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// checksFace judges declaration-file change checks using only the caller's range.
// The adapter can include fast tree declarations in the same verdict; heavy tree
// checks remain the responsibility of the heavy runner.
func checksFace(args []string) int {
	flags := flag.NewFlagSet("checks", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	base := flags.String("base", "", "base commit (required)")
	head := flags.String("head", "", "head commit (required)")
	root := flags.String("worktree", ".", "checkout containing the change")
	directory := flags.String("checks", "tools/gate/checks", "declaration directory")
	fastTree := flags.Bool("fast-tree", false, "include fast tree declarations alongside change checks")
	logs := flags.String("logs", "", "check log directory (required)")
	treeOverrun := flags.String("tree-overrun", TreeOverrunKeep, "keep a working check's own verdict; only keep is supported")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	options, err := treeOverrunFlag(*treeOverrun)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tillr-gate checks:", err)
		return record.ExitUsage
	}
	for _, arg := range []struct{ name, value string }{{"--base", *base}, {"--head", *head}, {"--logs", *logs}} {
		if arg.value == "" {
			fmt.Fprintf(os.Stderr, "tillr-gate checks: could not judge: missing %s argument\n", arg.name)
			return record.ExitExternal
		}
	}
	if _, err := changeGit(*root, "merge-base", "--is-ancestor", *base, *head); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate checks: could not judge: --base must resolve to an ancestor of --head: %v\n", err)
		return record.ExitExternal
	}
	changeRecord, err := DeriveChange(ChangeCandidate{Run: "checks", Worktree: *root, Base: *base, Head: *head}, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "tillr-gate checks: could not judge:", err)
		return record.ExitExternal
	}
	raw, err := record.Encode(changeRecord)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return record.ExitInternal
	}
	change, err := decodeChange(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitCode(err)
	}
	checkDirectory := *directory
	if !filepath.IsAbs(checkDirectory) {
		checkDirectory = filepath.Join(*root, checkDirectory)
	}
	registry, err := LoadRegistry(checkDirectory)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tillr-gate checks: could not judge:", err)
		return record.ExitExternal
	}
	changes := registry.Declarations[:0]
	for _, check := range registry.Declarations {
		if check.Scope == "change" || *fastTree && check.Scope == "tree" && check.Tier == "fast" {
			// --fast-tree asks for a verdict on every admitted fast tree check,
			// including a remembered pass when none of its inputs changed. Reads
			// bounds the fingerprint, not this face's explicitly requested set.
			if check.Scope == "tree" && check.Forced == "" {
				check.Forced = "fast-tree"
			}
			changes = append(changes, check)
		}
	}
	registry.Declarations = changes
	registry.InProcess = nil
	plan, err := Plan(registry, change, *root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tillr-gate checks: could not judge:", err)
		return exitCode(err)
	}
	verdict, runErr := RunWith(plan, *root, *logs, options)
	if verdict.Kind != "" {
		var payload VerdictPayload
		if err := json.Unmarshal(verdict.Payload, &payload); err == nil {
			// Prefixed with the face's name: the run face's triage keeps the lines a gate
			// program marks as its own, so an overrun survives into the verdict's log_ref.
			for _, note := range budgetNotes(payload) {
				fmt.Fprintln(os.Stderr, "tillr-gate checks:", note)
			}
		}
		body, err := encodeVerdictRecord(verdict)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitCode(err)
		}
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return record.ExitInternal
		}
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "tillr-gate checks:", runErr)
		return exitCode(runErr)
	}
	return record.ExitOK
}
