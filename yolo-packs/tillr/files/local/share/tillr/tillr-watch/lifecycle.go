package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

var errNeedSchemaMigration = errors.New("schema migration needed")

func runEpisode(ctx context.Context, face string, args []string, out io.Writer) error {
	f := flag.NewFlagSet(face, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	var by, note, item, until, reason, cause, evidence, outcome, linkKind string
	var untilChange bool
	switch face {
	case "ack":
		f.StringVar(&by, "by", "", "actor acknowledging the alert")
		f.StringVar(&note, "note", "", "acknowledgement note")
		f.StringVar(&item, "item", "", "open queue item tracking the alert's cause")
	case "investigate":
		f.StringVar(&by, "by", "", "actor investigating the alert")
		f.StringVar(&cause, "cause", "", "established cause of the alert")
		f.StringVar(&evidence, "evidence", "", "concrete evidence read")
	case "settle":
		f.StringVar(&by, "by", "", "actor settling the alert")
		f.StringVar(&outcome, "outcome", "", "settlement outcome (fix-rule or fix-fault)")
		f.StringVar(&item, "item", "", "queue item ID")
		f.StringVar(&linkKind, "link-kind", "", "link kind (filed or linked)")
		f.StringVar(&cause, "cause", "", "optional inline investigation cause")
		f.StringVar(&evidence, "evidence", "", "optional inline investigation evidence")
	case "silence":
		f.StringVar(&until, "until", "", "duration or RFC 3339 time")
		f.BoolVar(&untilChange, "until-change", false, "silence until the fingerprint changes")
		f.StringVar(&reason, "reason", "", "silence reason")
	}
	operands, err := parseEpisodeArgs(f, args)
	if err == flag.ErrHelp {
		_, err = io.WriteString(out, help)
		return err
	}
	if err == nil && (len(operands) != 1 || operands[0] == "" || *path == "") {
		err = fmt.Errorf("%s requires --store and one alert ID; see --help", face)
	}
	if err != nil {
		// Include the episode state in argument refusals when it can be looked up.
		if *path != "" && len(operands) > 0 {
			return refuseEpisodeOptions(ctx, *path, operands, err)
		}
		return err
	}
	id := operands[0]
	set := map[string]bool{}
	f.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if face == "ack" && set["item"] {
		return refuseEpisodeOptions(ctx, *path, operands, errors.New("--item is not accepted on ack: ack covers one observed instance only; use 'tillr watch settle' or 'tillr watch silence' for item-linked settlement or suppression"))
	}
	if face == "settle" && set["cause"] != set["evidence"] {
		return refuseEpisodeOptions(ctx, *path, operands, errors.New("--cause and --evidence must be provided together"))
	}
	if face == "silence" && (set["until"] == set["until-change"] || set["until-change"] && !untilChange) {
		return refuseEpisodeOptions(ctx, *path, operands, fmt.Errorf("silence requires exactly one of --until and --until-change"))
	}
	optional := func(name string, value *string) *string {
		if set[name] {
			return value
		}
		return nil
	}
	settleOpts := watch.SettleOptions{
		By:       by,
		Outcome:  outcome,
		ItemID:   item,
		LinkKind: linkKind,
		Cause:    cause,
		Evidence: evidence,
	}
	apply := func(e watch.Evaluator) (string, error) {
		switch face {
		case "ack":
			return "acknowledged", e.Ack(ctx, id, by, optional("note", &note))
		case "investigate":
			return "investigated", e.Investigate(ctx, id, by, cause, evidence)
		case "settle":
			return fmt.Sprintf("settled (%s -> %s)", outcome, item), e.Settle(ctx, id, settleOpts)
		default: // silence
			return "silenced", e.Silence(ctx, id, optional("until", &until), optional("reason", &reason))
		}
	}
	var state string
	err = store.EditExisting(ctx, *path, func(s *store.Store) error {
		e := watch.Evaluator{Store: s}
		switch face {
		case "ack":
			needMigrate, err := e.NeedsAckMigration(ctx, id, by)
			if err != nil {
				return err
			}
			if needMigrate {
				return errNeedSchemaMigration
			}
		case "investigate":
			needMigrate, err := e.NeedsInvestigateMigration(ctx, id, by, cause, evidence)
			if err != nil {
				return err
			}
			if needMigrate {
				return errNeedSchemaMigration
			}
		case "settle":
			needMigrate, err := e.NeedsSettleMigration(ctx, id, settleOpts)
			if err != nil {
				return err
			}
			if needMigrate {
				return errNeedSchemaMigration
			}
		}
		var applyErr error
		state, applyErr = apply(e)
		return applyErr
	})
	if errors.Is(err, errNeedSchemaMigration) {
		ms, openErr := store.OpenMigrated(ctx, *path)
		if openErr != nil {
			err = openErr
		} else {
			state, err = apply(watch.Evaluator{Store: ms})
			if closeErr := ms.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "alert %s %s\n", id, state)
	return err
}

func parseEpisodeArgs(f *flag.FlagSet, args []string) ([]string, error) {
	var operands []string
	var firstErr error
	for rem := args; len(rem) > 0; {
		arg := rem[0]
		if arg == "--" {
			operands = append(operands, rem[1:]...)
			break
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			operands = append(operands, arg)
			rem = rem[1:]
			continue
		}
		numMinuses := 1
		if len(arg) > 1 && arg[1] == '-' {
			numMinuses = 2
		}
		name := arg[numMinuses:]
		step := 1
		if len(name) > 0 && name[0] != '-' && name[0] != '=' && !strings.Contains(name, "=") {
			if name == "help" || name == "h" {
				return nil, flag.ErrHelp
			}
			if fl := f.Lookup(name); fl != nil {
				if b, ok := fl.Value.(interface{ IsBoolFlag() bool }); (!ok || !b.IsBoolFlag()) && len(rem) > 1 {
					step = 2
				}
			}
		}
		if err := f.Parse(rem[:step]); err != nil {
			if err == flag.ErrHelp {
				return nil, flag.ErrHelp
			}
			if firstErr == nil {
				firstErr = err
			}
		}
		rem = rem[step:]
	}
	return operands, firstErr
}

func refuseEpisodeOptions(ctx context.Context, path string, operands []string, reason error) error {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return err
	}
	defer s.Close()
	e := watch.Evaluator{Store: s}
	for _, candidate := range operands {
		if candidate == "" {
			continue
		}
		if refused := e.RefuseEpisode(ctx, candidate, reason); !errors.Is(refused, watch.ErrAlertNotFound) {
			return refused
		}
	}
	if len(operands) == 1 && operands[0] != "" {
		return fmt.Errorf("alert %s not found: %w", operands[0], reason)
	}
	return reason
}
