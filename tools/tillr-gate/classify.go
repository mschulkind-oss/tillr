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

// classifyFace reports raw-result classifications, not a gate verdict. It
// takes only observations; it does not load or run the judged tree's checks.
func classifyFace(args []string) int {
	flags := flag.NewFlagSet("classify", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: tillr-gate classify\nRead a JSON array of check observations from stdin and write their classifications to stdout, in order.")
	}
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return record.ExitUsage
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
	if err := json.NewEncoder(os.Stdout).Encode(answers); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate classify: writing classifications: %v\n", err)
		return record.ExitInternal
	}
	return record.ExitOK
}
