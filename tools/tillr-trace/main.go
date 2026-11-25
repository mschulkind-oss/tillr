package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const mainUsage = `usage: tillr-trace [-h] [--trace-root TRACE_ROOT] [--store STORE] [--loop-dir LOOP_DIR]
                   {tick,audit,run,cost,discrepancies} ...

Answers over recorded run artifacts, read on demand. Never a source of truth,
and keeps nothing: every command reads the artifacts it needs and writes no file.

positional arguments:
  {tick,audit,run,cost,discrepancies}
    tick                one tick, in order
    audit               one run: asked, ran, changed, cost
    run                 one run's row, events and claims, as JSON (what tillr-view reads)
    cost                per lane, per model, per route
    discrepancies       claims the record does not support

options:
  -h, --help            show this help message and exit
  --trace-root TRACE_ROOT
                        where the recorder's runs/<run-id>/ directories are (default: $TILLR_TRACE_ROOT or the state home)
  --store STORE
  --loop-dir LOOP_DIR   the drain's per-run record directory (default: the store's sibling loop)
`

func runsHelp() string {
	return fmt.Sprintf(
		"read the N most recent runs, by the store's start time (default %d; 0 reads every run, which parses every recorded artifact and takes minutes on a large corpus)",
		DefaultRuns,
	)
}

func subHelp(cmd string) string {
	switch cmd {
	case "tick":
		return "usage: tillr-trace tick [-h] [--last] [tick_id]\n\noptions:\n  -h, --help  show this help message and exit\n  --last      the tick named by the most recent run that names one\n"
	case "audit":
		return "usage: tillr-trace audit [-h] run_id\n\noptions:\n  -h, --help  show this help message and exit\n"
	case "run":
		return "usage: tillr-trace run [-h] run_id\n\noptions:\n  -h, --help  show this help message and exit\n"
	case "cost":
		return fmt.Sprintf("usage: tillr-trace cost [-h] [--runs N]\n\noptions:\n  -h, --help  show this help message and exit\n  --runs N    %s\n", runsHelp())
	case "discrepancies":
		return fmt.Sprintf("usage: tillr-trace discrepancies [-h] [--since SINCE] [--all] [--runs N]\n\noptions:\n  -h, --help     show this help message and exit\n  --since SINCE  only claims of runs that finished at or after this UTC time (ISO 8601, e.g. 2026-11-18T00:00:00Z)\n  --all          include unverified rows\n  --runs N       %s\n", runsHelp())
	default:
		return mainUsage
	}
}

func dieUsage(msg string) int {
	fmt.Fprintf(os.Stderr, "tillr-trace: error: %s\n", msg)
	return 2
}

func splitFlagEq(arg string) (string, string, bool) {
	if strings.HasPrefix(arg, "--") {
		if idx := strings.IndexByte(arg, '='); idx != -1 {
			return arg[:idx], arg[idx+1:], true
		}
	}
	return arg, "", false
}

func runCLI(args []string) int {
	traceRoot := traceRootDefault()
	storePath := ".tillr/state.db"
	loopDir := ""

	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			fmt.Print(mainUsage)
			return 0
		}
		if !strings.HasPrefix(arg, "-") {
			break
		}
		name, val, hasEq := splitFlagEq(arg)
		needVal := func() (string, bool) {
			if hasEq {
				return val, true
			}
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch name {
		case "--trace-root":
			v, ok := needVal()
			if !ok {
				return dieUsage("argument --trace-root: expected one argument")
			}
			traceRoot = v
		case "--store":
			v, ok := needVal()
			if !ok {
				return dieUsage("argument --store: expected one argument")
			}
			storePath = v
		case "--loop-dir":
			v, ok := needVal()
			if !ok {
				return dieUsage("argument --loop-dir: expected one argument")
			}
			loopDir = v
		default:
			return dieUsage(fmt.Sprintf("unrecognized arguments: %s", arg))
		}
		i++
	}

	if i >= len(args) {
		return dieUsage("the following arguments are required: command")
	}

	cmd := args[i]
	rest := args[i+1:]

	switch cmd {
	case "tick", "audit", "run", "cost", "discrepancies":
	default:
		return dieUsage(fmt.Sprintf("argument command: invalid choice: '%s' (choose from 'tick', 'audit', 'run', 'cost', 'discrepancies')", cmd))
	}

	// Parse subcommand arguments.
	var (
		tickLast  bool
		tickID    string
		runID     string
		runsN     = DefaultRuns
		since     string
		allClaims bool
	)

	var pos []string
	for j := 0; j < len(rest); j++ {
		arg := rest[j]
		if arg == "-h" || arg == "--help" {
			fmt.Print(subHelp(cmd))
			return 0
		}
		if !strings.HasPrefix(arg, "-") {
			pos = append(pos, arg)
			continue
		}
		name, val, hasEq := splitFlagEq(arg)
		needVal := func(flagName string) (string, bool) {
			if hasEq {
				return val, true
			}
			if j+1 >= len(rest) {
				return "", false
			}
			j++
			return rest[j], true
		}
		switch cmd {
		case "tick":
			if name == "--last" && !hasEq {
				tickLast = true
			} else {
				return dieUsage(fmt.Sprintf("unrecognized arguments: %s", arg))
			}
		case "cost":
			if name == "--runs" {
				v, ok := needVal("--runs")
				if !ok {
					return dieUsage("argument --runs: expected one argument")
				}
				n, err := strconv.Atoi(v)
				if err != nil {
					return dieUsage(fmt.Sprintf("argument --runs: invalid int value: '%s'", v))
				}
				runsN = n
			} else {
				return dieUsage(fmt.Sprintf("unrecognized arguments: %s", arg))
			}
		case "discrepancies":
			switch name {
			case "--since":
				v, ok := needVal("--since")
				if !ok {
					return dieUsage("argument --since: expected one argument")
				}
				since = v
			case "--all":
				if hasEq {
					return dieUsage(fmt.Sprintf("unrecognized arguments: %s", arg))
				}
				allClaims = true
			case "--runs":
				v, ok := needVal("--runs")
				if !ok {
					return dieUsage("argument --runs: expected one argument")
				}
				n, err := strconv.Atoi(v)
				if err != nil {
					return dieUsage(fmt.Sprintf("argument --runs: invalid int value: '%s'", v))
				}
				runsN = n
			default:
				return dieUsage(fmt.Sprintf("unrecognized arguments: %s", arg))
			}
		default:
			return dieUsage(fmt.Sprintf("unrecognized arguments: %s", arg))
		}
	}

	switch cmd {
	case "tick":
		if len(pos) > 1 {
			return dieUsage(fmt.Sprintf("unrecognized arguments: %s", strings.Join(pos[1:], " ")))
		}
		if len(pos) == 1 {
			tickID = pos[0]
		}
	case "audit", "run":
		if len(pos) == 0 {
			return dieUsage("the following arguments are required: run_id")
		}
		if len(pos) > 1 {
			return dieUsage(fmt.Sprintf("unrecognized arguments: %s", strings.Join(pos[1:], " ")))
		}
		runID = pos[0]
	case "cost", "discrepancies":
		if len(pos) > 0 {
			return dieUsage(fmt.Sprintf("unrecognized arguments: %s", strings.Join(pos, " ")))
		}
	}

	corpus, err := newCorpus(traceRoot, storePath, loopDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-trace: error: %v\n", err)
		return 1
	}
	defer corpus.Close()

	switch cmd {
	case "tick":
		allRuns := corpus.runIDs(0)
		ticks := make(map[string]string, len(allRuns))
		for _, rid := range allRuns {
			t := corpus.tickOf(rid)
			ticks[rid] = t
			if tickLast && tickID == "" && t != "" {
				tickID = t
			}
		}
		var matching []string
		for _, rid := range allRuns {
			if t := ticks[rid]; t != "" && t == tickID {
				matching = append(matching, rid)
			}
		}
		corpus.load(matching, nil, false)
		fmt.Println(renderTick(qTick(corpus.DB, tickID)))
	case "audit":
		if corpus.known(runID) {
			corpus.load([]string{runID}, nil, true)
		}
		fmt.Println(renderAudit(qAudit(corpus.DB, runID)))
	case "run":
		if corpus.known(runID) {
			corpus.load([]string{runID}, nil, true)
		}
		if err := writeRun(qRun(corpus.DB, runID), os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "tillr-trace: error: %v\n", err)
			return 1
		}
	case "cost":
		corpus.load(corpus.runIDs(runsN), map[string]bool{"llm.turn": true}, false)
		fmt.Println(renderCost(qCost(corpus.DB)))
	case "discrepancies":
		corpus.load(corpus.runIDs(runsN), map[string]bool{}, true)
		fmt.Println(renderDiscrepancies(qDiscrepancies(corpus.DB, since, allClaims), allClaims))
	}
	return 0
}

func main() {
	os.Exit(runCLI(os.Args[1:]))
}
