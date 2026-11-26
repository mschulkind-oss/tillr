package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return fmt.Sprintf("%v", *s) }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func runClaimTriage(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("claim-triage", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	runID := f.String("run", "", "triage run ID claiming the alert")

	operands, err := parseEpisodeArgs(f, args)
	if err == flag.ErrHelp {
		_, err = io.WriteString(out, help)
		return err
	}
	if err == nil && (len(operands) != 1 || operands[0] == "" || *path == "" || *runID == "") {
		err = fmt.Errorf("claim-triage requires --store, --run, and one alert ID; see --help")
	}
	if err != nil {
		if *path != "" && len(operands) > 0 {
			return refuseEpisodeOptions(ctx, *path, operands, err)
		}
		return err
	}
	id := operands[0]

	err = store.EditExisting(ctx, *path, func(s *store.Store) error {
		e := watch.Evaluator{Store: s}
		needMigrate, err := e.NeedsClaimTriageMigration(ctx, id, *runID)
		if err != nil {
			return err
		}
		if needMigrate {
			return errNeedSchemaMigration
		}
		return e.ClaimTriage(ctx, id, *runID)
	})
	if errors.Is(err, errNeedSchemaMigration) {
		ms, openErr := store.OpenMigrated(ctx, *path)
		if openErr != nil {
			err = openErr
		} else {
			err = watch.Evaluator{Store: ms}.ClaimTriage(ctx, id, *runID)
			if closeErr := ms.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "alert %s triage claimed (run %s)\n", id, *runID)
	return err
}

func runRecordTriage(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("record-triage", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	runID := f.String("run", "", "triage run ID recording the outcome")
	outcome := f.String("outcome", "", "triage outcome (filed, linked, or escalated)")
	outcomeKind := f.String("outcome-kind", "", "item outcome kind (fix-fault or fix-rule)")
	itemID := f.String("item", "", "queue item ID")
	finding := f.String("finding", "", "evidence-backed triage finding")
	escalateTo := f.String("escalate-to", "", "escalation target (operator-session or owner)")
	actionNeeded := f.String("action-needed", "", "action needed when escalating to operator-session")
	question := f.String("question", "", "question when escalating to owner")
	var options stringSliceFlag
	f.Var(&options, "option", "option when escalating to owner (repeatable)")

	operands, err := parseEpisodeArgs(f, args)
	if err == flag.ErrHelp {
		_, err = io.WriteString(out, help)
		return err
	}
	if err == nil && (len(operands) != 1 || operands[0] == "" || *path == "") {
		err = fmt.Errorf("record-triage requires --store and one alert ID; see --help")
	}
	if err != nil {
		if *path != "" && len(operands) > 0 {
			return refuseEpisodeOptions(ctx, *path, operands, err)
		}
		return err
	}
	id := operands[0]

	opts := watch.RecordTriageOptions{
		RunID:        *runID,
		Outcome:      *outcome,
		OutcomeKind:  *outcomeKind,
		ItemID:       *itemID,
		Finding:      *finding,
		EscalateTo:   *escalateTo,
		ActionNeeded: *actionNeeded,
		Question:     *question,
		Options:      options,
	}

	err = store.EditExisting(ctx, *path, func(s *store.Store) error {
		e := watch.Evaluator{Store: s}
		needMigrate, err := e.NeedsRecordTriageMigration(ctx, id, opts)
		if err != nil {
			return err
		}
		if needMigrate {
			return errNeedSchemaMigration
		}
		return e.RecordTriage(ctx, id, opts)
	})
	if errors.Is(err, errNeedSchemaMigration) {
		ms, openErr := store.OpenMigrated(ctx, *path)
		if openErr != nil {
			err = openErr
		} else {
			err = watch.Evaluator{Store: ms}.RecordTriage(ctx, id, opts)
			if closeErr := ms.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "alert %s triage recorded (%s)\n", id, *outcome)
	return err
}
