package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// checksFace judges declaration-file change checks using only the caller's range.
// It opens no store, and tree checks remain the responsibility of the other faces.
func checksFace(args []string) int {
	flags := flag.NewFlagSet("checks", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	base := flags.String("base", "", "base commit (required)")
	head := flags.String("head", "", "head commit (required)")
	root := flags.String("worktree", ".", "checkout containing the change")
	directory := flags.String("checks", "tools/gate/checks", "declaration directory")
	logs := flags.String("logs", "", "check log directory (required)")
	if code, answered := parseFlags(flags, args); answered {
		return code
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
		if check.Scope == "change" {
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
	verdict, runErr := Run(plan, *root, *logs)
	if verdict.Kind != "" {
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
