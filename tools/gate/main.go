package main

import (
	"fmt"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "repo-gate: missing subcommand")
		return 2
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "design-citations":
		return cmdDesignCitations(rest)
	case "scratch":
		return cmdScratch(rest)
	case "excused-as":
		return cmdExcusedAs(rest)
	case "citations",
		"commit-identity",
		"documents",
		"file-size":
		return runChangeCheck(sub, rest)
	case "elapsed-time":
		return cmdElapsedTime(rest)
	case "gate-budget":
		return cmdGateBudget(rest)
	case "go-build":
		return cmdGoBuild(rest)
	case "workflows":
		return cmdWorkflows(rest)
	case "change-gate":
		return cmdChangeGate(rest)
	case "tree-check":
		return cmdTreeCheck(rest)
	case "done":
		return cmdDone(rest)
	case "benchmark-fast-checks":
		return cmdBenchmarkFastChecks(rest)
	default:
		fmt.Fprintf(os.Stderr, "repo-gate: unknown subcommand %q\n", sub)
		return 2
	}
}
