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
	var by, note, item, until, reason string
	var untilChange bool
	switch face {
	case "ack":
		f.StringVar(&by, "by", "", "actor acknowledging the alert")
		f.StringVar(&note, "note", "", "acknowledgement note")
		f.StringVar(&item, "item", "", "open queue item tracking the alert's cause")
	case "track":
		f.StringVar(&item, "item", "", "open queue item tracking the alert's cause")
		f.StringVar(&by, "by", "", "actor recording the tracking item")
		f.StringVar(&note, "note", "", "tracking note")
	case "silence":
		f.StringVar(&until, "until", "", "duration or RFC 3339 time")
		f.BoolVar(&untilChange, "until-change", false, "silence until the fingerprint changes")
		f.StringVar(&reason, "reason", "", "silence reason")
	case "resolve":
		f.StringVar(&reason, "reason", "", "resolution note")
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
		return refuseEpisodeOptions(ctx, *path, operands, errors.New("--item is not accepted on ack: ack covers one observed instance only; use 'tillr watch track' or 'tillr watch silence' for item-linked suppression"))
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
	apply := func(e watch.Evaluator) (string, error) {
		switch face {
		case "ack":
			return "acknowledged", e.Ack(ctx, id, by, optional("note", &note))
		case "track":
			return "tracked by " + item, e.Track(ctx, id, item, optional("by", &by), optional("note", &note))
		case "silence":
			return "silenced", e.Silence(ctx, id, optional("until", &until), optional("reason", &reason))
		default: // resolve
			return "resolved", e.Resolve(ctx, id, reason)
		}
	}
	var state string
	err = store.EditExisting(ctx, *path, func(s *store.Store) error {
		e := watch.Evaluator{Store: s}
		switch face {
		case "track":
			if !set["item"] || item == "" {
				return e.RefuseEpisode(ctx, id, errors.New("--item is required and must not be empty"))
			}
			if set["by"] && by == "" {
				return e.RefuseEpisode(ctx, id, errors.New("--by must not be empty"))
			}
			needMigrate, err := e.NeedsTrackingMigration(ctx, id, item, optional("by", &by))
			if err != nil {
				return err
			}
			if needMigrate {
				return errNeedSchemaMigration
			}
		case "ack":
			needMigrate, err := e.NeedsAckMigration(ctx, id, by)
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
