// Command tillr-gate derives a change record, plans it into the checks the gate would run,
// runs them, and measures the landing tier in shadow.
//
// `change`, `plan` and `shadow` are the no-store faces: `change` reads a candidate and a
// worktree and writes a `gate.change`, `plan` reads that record and a check registry and
// writes a `gate.plan`, and `shadow` reads that plan and a worktree, runs the plan's
// landing tier, and writes a `gate.shadow` -- the wall each tier would have cost, measured
// rather than estimated. They need no store, no candidate queue and no other utility's
// binary, which is what makes them adoptable on their own (design §5.7, §5.11).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-gate/goaffected"
	"github.com/mschulkind-oss/tillr-gate/shards"
	"github.com/mschulkind-oss/tillr/buildstamp"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// faces is the dispatch table. A face is one verb and one function, so adding one is a
// line here rather than a branch in `run`.
var faces = map[string]func([]string) int{
	"change":      changeFace,
	"change-gate": changeGateFace,
	"checks":      checksFace,
	"classify":    classifyFace,
	"enable":      enableFace,
	"go-affected": goAffectedFace,
	"plan":        planFace,
	"run":         runFace,
	"shadow":      shadowFace,
	"shards":      shards.RunCLI,
	"tree-check":  treeCheckFace,
}

// run dispatches one invocation and answers with its exit code. An unknown verb is a
// usage error: the face made no decision, so it emits no record.
func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "tillr-gate: name a face: %s\n", faceNames())
		return record.ExitUsage
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("tillr-gate: %s\nRun `tillr-gate <face> --help` for one face's flags.\n", faceNames())
		return record.ExitOK
	}
	if args[0] == "--version" {
		fmt.Println(buildstamp.Read().String())
		return record.ExitOK
	}
	face, ok := faces[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "tillr-gate: %q is not a face; name one of: %s\n", args[0], faceNames())
		return record.ExitUsage
	}
	return face(args[1:])
}

// faceNames lists the faces in a fixed order, so the usage line is stable.
func faceNames() string {
	return "change, change-gate, checks, classify, enable, go-affected, plan, run, shadow, shards, tree-check"
}

// parseFlags parses one face's flags and reports whether it has already answered the
// face. A `--help` writes the face's flags to stdout and answers 0 -- a face's flags are
// the layer-1 surface (docs/reference/tillr-distribution.md, "Documentation split"), so the request is an
// answer and not a usage error -- and any other flag error is the usage exit, exactly as
// before. A face runs only when the second result is false.
func parseFlags(flags *flag.FlagSet, args []string) (int, bool) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stdout)
			flags.Usage()
			return record.ExitOK, true
		}
		return record.ExitUsage, true
	}
	return record.ExitOK, false
}

// shadowFace is `tillr-gate shadow`: a plan and a worktree in, a `gate.shadow` out.
//
// The landing tier runs and is measured; nothing lands. The face answers 0 whenever it
// wrote its record, whatever the measured tier did: a shadow red is a measurement, and a
// caller that read this exit as a verdict would be landing on a proof this face never
// claimed to be. A check that could not start is `error` in the record for the same
// reason -- the record is the product, and it names the hole rather than hiding it. Only a
// plan this face cannot read or run at all is a non-zero exit, and then it writes no
// record.
func shadowFace(args []string) int {
	flags := flag.NewFlagSet("shadow", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	inputPath := flags.String("input", "", "the gate.plan record to measure (required)")
	worktree := flags.String("worktree", "", "the worktree to run the landing tier in (required)")
	outputPath := flags.String("output", "", "where to write the gate.shadow record (default: stdout)")
	logDir := flags.String("logs", "", "where to write each check's log (default: <shadow_id>.logs beside the output)")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate shadow: --input is required")
		return record.ExitUsage
	}
	if *worktree == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate shadow: --worktree is required")
		return record.ExitUsage
	}
	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate shadow: %v\n", usagef("reading the plan record: %v", err))
		return record.ExitUsage
	}
	plan, err := decodePlan(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate shadow: %v\n", err)
		return exitCode(err)
	}
	logs := *logDir
	if logs == "" {
		base := "."
		if *outputPath != "" {
			base = filepath.Dir(*outputPath)
		}
		payload, err := planPayload(plan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate shadow: %v\n", err)
			return exitCode(err)
		}
		logs = filepath.Join(base, shadowID(payload.PlanID, payload.TreeSHA, TierLanding)+".logs")
	}
	envelope, err := Shadow(plan, *worktree, logs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate shadow: %v\n", err)
		return exitCode(err)
	}
	body, err := encodeShadow(envelope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate shadow: %v\n", err)
		return exitCode(err)
	}
	if *outputPath == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate shadow: %v\n", internalf("writing the shadow record: %v", err))
			return record.ExitInternal
		}
		return record.ExitOK
	}
	if err := os.WriteFile(*outputPath, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate shadow: %v\n", internalf("writing %s: %v", *outputPath, err))
		return record.ExitInternal
	}
	return record.ExitOK
}

// stringList collects a flag given more than once, in the order it was given.
type stringList []string

func (list *stringList) String() string { return strings.Join(*list, ",") }

func (list *stringList) Set(value string) error {
	*list = append(*list, value)
	return nil
}

// runFace is `tillr-gate run`: a plan and a worktree in, a verdict record out.
//
// The verdict is written before the exit code is answered, because a verdict that could
// not judge is still a record: exit 4 with no record would leave the caller unable to
// tell a check that could not start from a face that never ran.
//
// With `--gate` the plan's own commands are not run: the named program is the gate, and
// its exit is the verdict (see `RunGate`). Without it the plan's checks are run one by
// one, which is the shape a test registry can spell.
func runFace(args []string) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	inputPath := flags.String("input", "", "the gate.plan record to run (required)")
	worktree := flags.String("worktree", "", "the worktree to run the plan in (required)")
	outputPath := flags.String("output", "", "where to write the gate.verdict record (default: stdout)")
	logDir := flags.String("logs", "", "where to write each check's log (default: <verdict_id>.logs beside the output)")
	gateProgram := flags.String("gate", "", "a gate program to run instead of the plan's checks, e.g. tools/tillr-gate/change-gate.py")
	gateTimeout := flags.Duration("gate-timeout", 30*time.Minute, "reporting target for the --gate program; only sampled stalls kill it (0 disables target reporting)")
	var gateArgs stringList
	flags.Var(&gateArgs, "gate-arg", "an argument to pass to the --gate program, in order (repeatable)")
	treeOverrun := flags.String("tree-overrun", TreeOverrunKeep, "keep a working check's own verdict; only keep is supported")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	options, err := treeOverrunFlag(*treeOverrun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", err)
		return record.ExitUsage
	}
	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate run: --input is required")
		return record.ExitUsage
	}
	if *worktree == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate run: --worktree is required")
		return record.ExitUsage
	}
	if *gateProgram == "" && len(gateArgs) > 0 {
		fmt.Fprintln(os.Stderr, "tillr-gate run: --gate-arg needs --gate")
		return record.ExitUsage
	}
	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", usagef("reading the plan record: %v", err))
		return record.ExitUsage
	}
	plan, err := decodePlan(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", err)
		return exitCode(err)
	}
	logs := *logDir
	if logs == "" {
		base := "."
		if *outputPath != "" {
			base = filepath.Dir(*outputPath)
		}
		payload, err := planPayload(plan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", err)
			return exitCode(err)
		}
		logs = filepath.Join(base, verdictID(payload.PlanID, payload.TreeSHA)+".logs")
	}
	var envelope record.Envelope
	var runErr error
	if *gateProgram != "" {
		program := GateProgram{Command: append([]string{*gateProgram}, gateArgs...), Timeout: *gateTimeout}
		envelope, runErr = RunGate(plan, *worktree, logs, program)
	} else {
		envelope, runErr = RunWith(plan, *worktree, logs, options)
	}
	if envelope.Kind != "" {
		var verdict VerdictPayload
		if err := json.Unmarshal(envelope.Payload, &verdict); err == nil {
			writeRunSummary(os.Stderr, verdict)
		}
	}
	if runErr != nil && envelope.Kind == "" {
		// The face refused the plan outright: no verdict exists to write.
		fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", runErr)
		return exitCode(runErr)
	}
	body, err := encodeVerdictRecord(envelope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", err)
		return exitCode(err)
	}
	if *outputPath == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", internalf("writing the verdict record: %v", err))
			return record.ExitInternal
		}
	} else if err := os.WriteFile(*outputPath, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", internalf("writing %s: %v", *outputPath, err))
		return record.ExitInternal
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate run: %v\n", runErr)
		return exitCode(runErr)
	}
	return record.ExitOK
}

func writeRunSummary(w io.Writer, verdict VerdictPayload) {
	passed, remembered, unavailable, inherited, skipped, disabled, failed := 0, 0, 0, 0, 0, 0, 0
	for _, check := range verdict.Checks {
		var mem checkresult.Result
		if check.Memory != "" {
			mem = classifyCheck(checkresult.Observation{
				Check:  check.CheckID,
				Commit: verdict.TreeSHA,
				Source: checkresult.ResultWord,
				Word:   check.Memory,
			})
		}
		if check.Result == ResultInherited {
			inherited++
		} else {
			res := classifyCheck(checkresult.Observation{
				Check:  check.CheckID,
				Commit: verdict.TreeSHA,
				Source: checkresult.ResultWord,
				Word:   check.Result,
			})
			switch {
			case res.Outcome == checkresult.Unavailable && res.Detail == checkresult.DetailUnavailable:
				unavailable++
				for _, need := range check.MissingNeeds {
					fmt.Fprintf(w, "%s: unavailable; needs %s; provide with: %s\n", check.CheckID, need.Need, need.Command)
				}
			case res.Outcome == checkresult.Pass && mem.Outcome == checkresult.Pass && mem.Detail == checkresult.DetailRemembered:
				remembered++
			case res.Outcome == checkresult.Pass:
				passed++
			case res.Outcome == checkresult.Unavailable && res.Detail == checkresult.DetailSkipped:
				skipped++
			case res.Outcome == checkresult.Disabled:
				disabled++
				writeDisabledSummaryLine(w, check)
			default:
				failed++
			}
		}
		switch {
		case mem.Outcome == checkresult.Pass && mem.Detail == checkresult.DetailRemembered:
			fmt.Fprintf(w, "%s: remembered pass at %s\n", check.CheckID, check.PassTime)
		case check.Memory == "flaky":
			fmt.Fprintf(w, "%s: flaky; pass at %s, fail at %s\n", check.CheckID, check.PassTime, check.FailTime)
		case check.Memory == "not remembered":
			fmt.Fprintf(w, "%s: not remembered; %s differs from judged tree\n", check.CheckID, check.UnboundPath)
		}
	}
	for _, note := range budgetNotes(verdict) {
		fmt.Fprintln(w, note)
	}
	summary := fmt.Sprintf("checks: %d passed, %d remembered, %d unavailable", passed, remembered, unavailable)
	if inherited > 0 {
		summary += fmt.Sprintf(", %d inherited", inherited)
	}
	if skipped > 0 {
		summary += fmt.Sprintf(", %d skipped", skipped)
	}
	if disabled > 0 {
		summary += fmt.Sprintf(", %d disabled", disabled)
	}
	fmt.Fprintf(w, "%s, %d failed\n", summary, failed)
}

func writeDisabledSummaryLine(w io.Writer, check VerdictCheck) {
	if check.PassTime != "" || check.FailTime != "" {
		passLoc := check.PassLogRef
		if strings.TrimSpace(passLoc) == "" {
			passLoc = "output location not recorded"
		}
		failLoc := check.FailLogRef
		if strings.TrimSpace(failLoc) == "" {
			failLoc = "output location not recorded"
		}
		line := fmt.Sprintf("%s: disabled; pass at %s (%s), fail at %s (%s)",
			check.CheckID, check.PassTime, passLoc, check.FailTime, failLoc)
		if check.RepairItem != "" {
			line += fmt.Sprintf("; repair item %s", check.RepairItem)
		}
		fmt.Fprintln(w, line)
		return
	}
	if check.RepairItem != "" {
		fmt.Fprintf(w, "%s: disabled; repair item %s\n", check.CheckID, check.RepairItem)
		return
	}
	fmt.Fprintf(w, "%s: disabled\n", check.CheckID)
}

// planFace is `tillr-gate plan`: a change and a registry in, a plan record out.
//
// It writes nothing but the output file. There is no `--store` flag and no store is
// opened: design §5.7's primary mode needs no store at all, and §5.11's strong acceptance
// for this face is that it runs with no other utility's binary present.
func planFace(args []string) int {
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	registryPath := flags.String("registry", "", "a directory of check declaration JSON files or a generated registry file (required)")
	inputPath := flags.String("input", "", "the gate.change record to plan (required)")
	outputPath := flags.String("output", "", "where to write the gate.plan record (default: stdout)")
	root := flags.String("root", "", "the worktree the change is against (default: the working directory)")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if *registryPath == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate plan: --registry is required")
		return record.ExitUsage
	}
	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate plan: --input is required")
		return record.ExitUsage
	}
	worktree := *root
	if worktree == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", err)
			return record.ExitInternal
		}
		worktree = cwd
	}
	raw, err := os.ReadFile(*inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", usagef("reading the change record: %v", err))
		return record.ExitUsage
	}
	change, err := decodeChange(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", err)
		return exitCode(err)
	}
	registry, err := LoadRegistry(*registryPath)
	if err != nil {
		var named *exitError
		if errors.As(err, &named) {
			fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", err)
			return exitCode(err)
		}
		fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", incompatiblef("%v", err))
		return record.ExitIncompatible
	}
	envelope, err := Plan(registry, change, worktree)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", err)
		return exitCode(err)
	}
	body, err := encodePlan(envelope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", err)
		return exitCode(err)
	}
	if *outputPath == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", internalf("writing the plan record: %v", err))
			return record.ExitInternal
		}
		return record.ExitOK
	}
	if err := os.WriteFile(*outputPath, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate plan: %v\n", internalf("writing %s: %v", *outputPath, err))
		return record.ExitInternal
	}
	return record.ExitOK
}

// goAffectedFace is `tillr-gate go-affected`: select and run affected Go packages.
func goAffectedFace(args []string) int {
	return goaffected.Run(args)
}
