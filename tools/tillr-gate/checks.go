package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// selectHeavyDeclarations selects the full heavy tree set even on an empty diff.
// Fingerprint memory, not diff selection, decides which of these actually executes.
func selectHeavyDeclarations(reg *Registry, only string) error {
	selected := []Declaration{}
	for _, check := range reg.Declarations {
		if check.Scope == "tree" && check.Tier == "heavy" && (only == "" || check.ID == only) {
			check.Forced = "heavy-tree"
			selected = append(selected, check)
		}
	}
	if only != "" && len(selected) != 1 {
		return externalf("heavy check %q is not declared at this revision", only)
	}
	reg.Declarations = selected
	reg.InProcess = nil
	return nil
}

// readsChanged compares declared reads (not writes or selection triggers) to the
// candidate. A dynamic read is the set of paths matched by the declaration's selects.
func readsChanged(check Declaration, paths []string, root string) bool {
	for _, read := range check.Watches {
		if read.Paths && len(selectedPaths(check.Selects, paths, root)) != 0 {
			return true
		}
		for _, path := range paths {
			if read.Path != "" && coversPath([]string{read.Path}, path) || read.Matches(path, root) {
				return true
			}
		}
	}
	return false
}

// checksFace judges declaration-file change checks using only the caller's range.
// The adapter can include fast tree declarations in the same verdict; heavy tree
// checks remain the responsibility of the heavy runner.
func checksFace(args []string) int {
	started := time.Now()
	flags := flag.NewFlagSet("checks", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	base := flags.String("base", "", "base commit (required)")
	head := flags.String("head", "", "head commit (required)")
	root := flags.String("worktree", ".", "checkout containing the change")
	directory := flags.String("checks", "tools/gate/checks", "declaration directory")
	fastTree := flags.Bool("fast-tree", false, "include fast tree declarations alongside change checks")
	scopeToChange := flags.Bool("scope-to-change", false, "skip checks whose declared reads are disjoint from the change")
	heavyTree := flags.Bool("heavy-tree", false, "judge every heavy tree declaration, without change or fast checks")
	only := flags.String("only", "", "with --heavy-tree, judge exactly this heavy check for attribution")
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
	options.measureLanding = *fastTree && os.Getenv("TILLR_FAST_TREE_TIMING") == "1"
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
	var skipped, undeclared []string
	if *heavyTree {
		if *scopeToChange {
			fmt.Fprintln(os.Stderr, "tillr-gate checks: --scope-to-change is not supported with --heavy-tree")
			return record.ExitUsage
		}
		if *fastTree {
			fmt.Fprintln(os.Stderr, "tillr-gate checks: --heavy-tree and --fast-tree are mutually exclusive")
			return record.ExitUsage
		}
		if err := selectHeavyDeclarations(registry, *only); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return record.ExitExternal
		}
	} else {
		if *only != "" {
			fmt.Fprintln(os.Stderr, "tillr-gate checks: --only requires --heavy-tree")
			return record.ExitUsage
		}
		changes := registry.Declarations[:0]
		for _, check := range registry.Declarations {
			if check.Scope == "change" || *fastTree && check.Scope == "tree" && check.Tier == "fast" {
				if *scopeToChange {
					if check.undeclaredReads || len(check.Watches) == 0 {
						undeclared = append(undeclared, check.ID)
						remember := false
						check.Remember = &remember
					} else if !readsChanged(check, change.ChangedPaths, *root) {
						skipped = append(skipped, check.ID)
						continue
					}
				}
				// The admitted set must not be filtered by the planner's selects.
				if (check.Scope == "tree" || *scopeToChange) && check.Forced == "" {
					check.Forced = "fast-tree"
				}
				changes = append(changes, check)
			}
		}
		registry.Declarations = changes
	}
	if len(skipped) != 0 {
		fmt.Fprintf(os.Stderr, "tillr-gate checks: %s: %s\n", ResultSkipped, strings.Join(skipped, ", "))
	}
	if len(undeclared) != 0 {
		fmt.Fprintf(os.Stderr, "tillr-gate checks: checks with no declared reads (always run): %s\n", strings.Join(undeclared, ", "))
	}
	registry.InProcess = nil
	plan, err := Plan(registry, change, *root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tillr-gate checks: could not judge:", err)
		return exitCode(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	options.Context = ctx
	verdict, runErr := RunWith(plan, *root, *logs, options)
	if verdict.Kind != "" {
		var payload VerdictPayload
		if err := json.Unmarshal(verdict.Payload, &payload); err != nil {
			fmt.Fprintln(os.Stderr, internalf("decoding the verdict payload: %v", err))
			return record.ExitInternal
		}
		if len(skipped) != 0 || options.measureLanding {
			for _, id := range skipped {
				payload.Checks = append(payload.Checks, notRunCheck(id, ResultSkipped, options.measureLanding))
			}
			if options.measureLanding {
				payload.WallMS = wallMS(started)
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				fmt.Fprintln(os.Stderr, internalf("encoding the verdict payload: %v", err))
				return record.ExitInternal
			}
			verdict.Payload = encoded
		}
		// Prefixed with the face's name: the run face's triage keeps the lines a gate
		// program marks as its own, so an overrun survives into the verdict's log_ref.
		for _, note := range budgetNotes(payload) {
			fmt.Fprintln(os.Stderr, "tillr-gate checks:", note)
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
