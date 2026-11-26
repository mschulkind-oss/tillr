package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/mschulkind-oss/tillr/watch"
)

func runLint(ctx context.Context, args []string, out, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintln(stderr, "tillr watch:", err)
		return 1
	}
	f := flag.NewFlagSet("lint", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var opts watch.LintOptions
	f.StringVar(&opts.CharterPath, "charter", "", "path to charter definition.json")
	f.StringVar(&opts.WorkflowsPath, "workflows", "", "path to workflow file or directory")
	f.Func("policy", "path to backend policy JSON (repeatable)", func(value string) error {
		if value == "" {
			return fmt.Errorf("must not be empty")
		}
		opts.PolicyPaths = append(opts.PolicyPaths, value)
		return nil
	})
	f.StringVar(&opts.RulesPath, "rules", "", "path to watch rule file or directory")
	f.BoolVar(&opts.NoShippedRules, "no-shipped-rules", false, "omit tillr's embedded shipped rules")
	opts.ShippedFS = shippedRulesFS

	operands, err := parseEpisodeArgs(f, args)
	if err == flag.ErrHelp {
		fmt.Fprint(out, help)
		return 0
	}
	if err != nil {
		return fail(err)
	}
	set := map[string]bool{}
	f.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	for _, check := range []struct {
		name string
		val  string
	}{
		{"charter", opts.CharterPath},
		{"workflows", opts.WorkflowsPath},
		{"rules", opts.RulesPath},
	} {
		if set[check.name] && check.val == "" {
			return fail(fmt.Errorf("--%s must not be empty", check.name))
		}
	}
	if len(operands) > 1 {
		return fail(fmt.Errorf("lint accepts at most one <repo-or-dir>; see --help"))
	}
	if len(operands) == 1 {
		if operands[0] == "" {
			return fail(fmt.Errorf("lint <repo-or-dir> must not be empty"))
		}
		opts.Root = operands[0]
	}

	report, err := watch.LintWithReport(ctx, opts)
	if err != nil {
		return fail(err)
	}
	if len(report.Findings) == 0 {
		for _, r := range report.Rules {
			fmt.Fprintf(out, "rule %s source=%s\n", r.ID, r.Source)
		}
		return 0
	}
	for _, finding := range report.Findings {
		fmt.Fprintln(out, finding.String())
	}
	return 1
}
