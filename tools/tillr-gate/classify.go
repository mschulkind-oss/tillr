package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

type classifyRunOutput struct {
	Outcome  checkresult.Outcome `json:"outcome"`
	Detail   string              `json:"detail"`
	ExitCode int                 `json:"exit_code"`
}

type classifyAggregateOutput struct {
	Checks []checkresult.Result `json:"checks"`
	Run    classifyRunOutput    `json:"run"`
}

func parseAggregateOrder(raw string) (checkresult.AggregateOrder, bool) {
	switch checkresult.AggregateOrder(raw) {
	case checkresult.RunnerOrder, checkresult.ShadowOrder, checkresult.LegacyGateOrder, checkresult.LegacyReplayOrder:
		return checkresult.AggregateOrder(raw), true
	default:
		return "", false
	}
}

// classifyFace reports raw-result classifications, not a gate verdict. It
// takes only observations; it does not load or run the judged tree's checks.
func classifyFace(args []string) int {
	flags := flag.NewFlagSet("classify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	aggregate := flags.String("aggregate", "", "aggregate classified checks under `order`: runner, shadow, legacy-gate, or legacy-replay")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: tillr-gate classify [--aggregate <order>]\nRead a JSON array of check observations from stdin and write their classifications to stdout, in order.")
		flags.PrintDefaults()
	}
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return record.ExitUsage
	}
	aggregateSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "aggregate" {
			aggregateSet = true
		}
	})
	var order checkresult.AggregateOrder
	if aggregateSet {
		var ok bool
		order, ok = parseAggregateOrder(*aggregate)
		if !ok {
			fmt.Fprintf(os.Stderr, "tillr-gate classify: %q is not an aggregate order; name one of: runner, shadow, legacy-gate, legacy-replay\n", *aggregate)
			return record.ExitUsage
		}
	}
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	var batch []*checkresult.Observation
	if err := decoder.Decode(&batch); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate classify: reading observations: %v\n", err)
		return record.ExitUsage
	}
	if batch == nil || decoder.Decode(new(any)) != io.EOF {
		fmt.Fprintln(os.Stderr, "tillr-gate classify: expected exactly one JSON array")
		return record.ExitUsage
	}
	answers := make([]checkresult.Result, len(batch))
	for i, observation := range batch {
		if observation == nil {
			fmt.Fprintf(os.Stderr, "tillr-gate classify: null observation at index %d\n", i)
			return record.ExitUsage
		}
		answers[i] = checkresult.Classify(*observation)
	}
	var output any = answers
	if aggregateSet {
		runResult := checkresult.Aggregate(order, answers)
		output = classifyAggregateOutput{
			Checks: answers,
			Run: classifyRunOutput{
				Outcome:  runResult.Outcome,
				Detail:   runResult.Detail,
				ExitCode: runResult.ExitCode,
			},
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate classify: writing classifications: %v\n", err)
		return record.ExitInternal
	}
	return record.ExitOK
}
