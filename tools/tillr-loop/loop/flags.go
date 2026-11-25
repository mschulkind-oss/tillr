package loop

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

type flagDef struct {
	name     string
	required bool
	isBool   bool
	defVal   string
	choices  []string
	help     string
	group    string // mutually exclusive group name, if any
}

type parsedArgs struct {
	checkArgv     bool
	item          string
	lane          string
	run           string
	worktree      string
	base          string
	head          string
	root          string
	store         string
	workDir       string
	now           string
	tillrGate     string
	tillrLand     string
	tillrItem     string
	registry      string
	gateProgram   string
	baseRef       string
	messageFrom   string
	observedTrunk string
	verdict       string
	only          string
	all           bool
	since         string
}

func flagsFor(kind string) []flagDef {
	if kind == "describe" {
		return nil
	}
	defs := []flagDef{
		{name: "--check-argv", isBool: true, help: "parse arguments only; tillr-step-argv-check-v1 (see drain README)"},
	}
	if kind == "replay" {
		defs = append(defs, flagDef{name: "--only", required: true, help: "the failing check identifier to replay"})
		return defs
	}
	if kind == "rolling" {
		defs = append(defs,
			flagDef{name: "--all", isBool: true, group: "rolling-mode", help: "judge all rolling checks"},
			flagDef{name: "--since", group: "rolling-mode", help: "judge rolling checks since SHA"},
		)
		return defs
	}
	// Candidate flags
	defs = append(defs,
		flagDef{name: "--item", required: true, help: "the item the candidate is for"},
		flagDef{name: "--lane", defVal: "", help: "the lane the candidate was made in"},
		flagDef{name: "--run", required: true, help: "the run that produced the candidate"},
		flagDef{name: "--worktree", required: true, help: "the candidate worktree"},
		flagDef{name: "--base", required: true, help: "the base sha the candidate is on"},
		flagDef{name: "--head", required: true, help: "the candidate's head sha"},
	)
	// Common flags
	defs = append(defs,
		flagDef{name: "--root", help: "the repository (default: this checkout)"},
		flagDef{name: "--store", help: "the store (default: <root>/.tillr/state.db)"},
		flagDef{name: "--work-dir", help: "the loop's scratch (default: <root>/.tillr/loop)"},
		flagDef{name: "--now", help: "the clock, RFC 3339 (default: now)"},
	)
	// Binary flags
	defs = append(defs,
		flagDef{name: "--tillr-gate", defVal: "tillr-gate", help: "the tillr-gate program"},
		flagDef{name: "--tillr-land", defVal: "tillr-land", help: "the tillr-land program"},
		flagDef{name: "--tillr-item", defVal: "tillr-item", help: "the tillr-item program"},
	)
	if kind == "fixer-gate" {
		defs = append(defs, flagDef{name: "--only", required: true, help: "the proof's named check selector"})
	}
	if kind == "gate" || kind == "self-check" {
		defs = append(defs,
			flagDef{
				name: "--registry",
				help: fmt.Sprintf("the check registry tillr-gate plan reads (default: $%s, else the candidate's tools/gate/checks directory)", registryEnv),
			},
			flagDef{
				name: "--gate-program",
				help: "the program `tillr-gate run --gate` runs (default: python3 <tillr-gate>/change-gate.py --base {base} --head {head} --runner {runner} --logs {logs})",
			},
		)
	}
	if kind == "land" || kind == "self-check" {
		defs = append(defs,
			flagDef{name: "--base-ref", help: "the branch the base sha is on"},
			flagDef{name: "--message-from", help: "the commit in base..head whose message and author date to land"},
			flagDef{name: "--observed-trunk", help: "the loop's last observed trunk tip, handed to `tillr-land pass`, which refuses with trunk-rewritten if the trunk no longer contains it"},
		)
	}
	if kind == "verdict" {
		defs = append(defs, flagDef{
			name:     "--verdict",
			required: true,
			choices:  []string{"pass", "pass-inherited", "fail"},
			help:     "the verdict to record",
		})
	} else if kind == "self-check" {
		defs = append(defs, flagDef{
			name:    "--verdict",
			defVal:  "pass",
			choices: []string{"pass", "pass-inherited", "fail"},
			help:    "the verdict to show (default: pass)",
		})
	}
	return defs
}

func describeFlagNames(kind string) []string {
	var out []string
	for _, d := range flagsFor(kind) {
		if d.name == "--check-argv" || d.name == "--help" {
			continue
		}
		out = append(out, d.name)
	}
	return out
}

func writeHelp(w io.Writer, kind string) {
	fmt.Fprintf(w, "usage: tillr-loop %s [-h]", kind)
	defs := flagsFor(kind)
	for _, d := range defs {
		if d.isBool {
			fmt.Fprintf(w, " [%s]", d.name)
		} else {
			upper := strings.ToUpper(strings.ReplaceAll(strings.TrimPrefix(d.name, "--"), "-", "_"))
			if d.required {
				fmt.Fprintf(w, " %s %s", d.name, upper)
			} else {
				fmt.Fprintf(w, " [%s %s]", d.name, upper)
			}
		}
	}
	fmt.Fprintln(w)
	if len(defs) > 0 {
		fmt.Fprintln(w, "\noptions:")
		fmt.Fprintln(w, "  -h, --help            show this help message and exit")
		for _, d := range defs {
			fmt.Fprintf(w, "  %-22s %s\n", d.name, d.help)
		}
	}
}

// parseArgs parses argv for kind. Returns (parsed, helpShown, exitCode).
// If helpShown is true, exitCode is ExitPass (0). If exitCode != ExitPass, an error was written to stderr.
func parseArgs(kind string, args []string, stdout, stderr io.Writer) (parsedArgs, bool, int) {
	var out parsedArgs
	defs := flagsFor(kind)
	byName := make(map[string]flagDef, len(defs))
	values := make(map[string]string, len(defs))
	seen := make(map[string]bool, len(defs))
	for _, d := range defs {
		byName[d.name] = d
		if d.defVal != "" {
			values[d.name] = d.defVal
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			writeHelp(stdout, kind)
			return out, true, ExitPass
		}
		if !strings.HasPrefix(arg, "--") {
			fmt.Fprintf(stderr, "tillr-loop %s: error: unrecognized arguments: %s\n", kind, arg)
			return out, false, ExitUsage
		}
		name := arg
		hasEq := false
		eqVal := ""
		if idx := strings.IndexByte(arg, '='); idx >= 0 {
			name = arg[:idx]
			eqVal = arg[idx+1:]
			hasEq = true
		}
		def, ok := byName[name]
		if !ok {
			fmt.Fprintf(stderr, "tillr-loop %s: error: unrecognized arguments: %s\n", kind, arg)
			return out, false, ExitUsage
		}
		if def.isBool {
			if hasEq {
				fmt.Fprintf(stderr, "tillr-loop %s: error: argument %s: ignored explicit argument %q\n", kind, name, eqVal)
				return out, false, ExitUsage
			}
			seen[name] = true
			values[name] = "true"
			continue
		}
		var val string
		if hasEq {
			val = eqVal
		} else {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "tillr-loop %s: error: argument %s: expected one argument\n", kind, name)
				return out, false, ExitUsage
			}
			val = args[i+1]
			i++
		}
		if len(def.choices) > 0 && !slices.Contains(def.choices, val) {
			fmt.Fprintf(stderr, "tillr-loop %s: error: argument %s: invalid choice: %q (choose from %s)\n",
				kind, name, val, strings.Join(def.choices, ", "))
			return out, false, ExitUsage
		}
		seen[name] = true
		values[name] = val
	}

	var missing []string
	for _, d := range defs {
		if d.required && !seen[d.name] {
			missing = append(missing, d.name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "tillr-loop %s: error: the following arguments are required: %s\n",
			kind, strings.Join(missing, ", "))
		return out, false, ExitUsage
	}

	if kind == "rolling" {
		if seen["--all"] && seen["--since"] {
			fmt.Fprintf(stderr, "tillr-loop %s: error: argument --since: not allowed with argument --all\n", kind)
			return out, false, ExitUsage
		}
		if !seen["--all"] && !seen["--since"] {
			fmt.Fprintf(stderr, "tillr-loop %s: error: one of the arguments --all --since is required\n", kind)
			return out, false, ExitUsage
		}
	}

	out.checkArgv = seen["--check-argv"]
	out.item = values["--item"]
	out.lane = values["--lane"]
	out.run = values["--run"]
	out.worktree = values["--worktree"]
	out.base = values["--base"]
	out.head = values["--head"]
	out.root = values["--root"]
	out.store = values["--store"]
	out.workDir = values["--work-dir"]
	out.now = values["--now"]
	out.tillrGate = values["--tillr-gate"]
	out.tillrLand = values["--tillr-land"]
	out.tillrItem = values["--tillr-item"]
	out.registry = values["--registry"]
	out.gateProgram = values["--gate-program"]
	out.baseRef = values["--base-ref"]
	out.messageFrom = values["--message-from"]
	out.observedTrunk = values["--observed-trunk"]
	out.verdict = values["--verdict"]
	out.only = values["--only"]
	out.all = seen["--all"]
	out.since = values["--since"]
	return out, false, ExitPass
}
