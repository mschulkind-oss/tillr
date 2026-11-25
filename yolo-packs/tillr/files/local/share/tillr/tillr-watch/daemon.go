package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mschulkind-oss/tillr/watch"
)

func daemon(ctx context.Context, e watch.Evaluator, stderr io.Writer, ticks <-chan time.Time) error {
	if ctx.Err() != nil {
		return nil
	}
	wake, err := watchStore(e.Store, ticks)
	if err != nil {
		return err
	}
	defer wake.close()
	var diagnostics ruleDiagnostics
	var previous map[string]int
	for ctx.Err() == nil {
		if err := wake.beginPass(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		diagnostics.report(stderr, results)
		previous = reportSkipped(stderr, results, previous)
		if _, err := wake.next(ctx, nil); err != nil && ctx.Err() == nil {
			return err
		}
	}
	return nil
}

// ruleDiagnostics reports a reason once until it changes or clears.
type ruleDiagnostics struct{ previous map[string]string }

func (d *ruleDiagnostics) report(stderr io.Writer, results []watch.RuleResult) {
	current := make(map[string]string)
	for _, r := range results {
		if reason := r.Diagnostic(); reason != "" {
			if d.previous[r.ID] != reason {
				fmt.Fprintf(stderr, "tillr watch: rule %s not evaluated: %s\n", r.ID, reason)
			}
			current[r.ID] = reason
		}
	}
	d.previous = current
}

func reportSkipped(stderr io.Writer, results []watch.RuleResult, previous map[string]int) map[string]int {
	current := make(map[string]int)
	for _, r := range results {
		if r.Skipped > 0 {
			if r.Skipped != previous[r.ID] {
				fmt.Fprintf(stderr, "tillr watch: rule %s skipped %d events: group_by %s missing or not a string\n", r.ID, r.Skipped, r.GroupBy)
			}
			current[r.ID] = r.Skipped
		}
	}
	return current
}
