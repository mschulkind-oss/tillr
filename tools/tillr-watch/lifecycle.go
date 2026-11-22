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

func runEpisode(ctx context.Context, face string, args []string, out io.Writer) error {
	f := flag.NewFlagSet(face, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	var by, note, until, reason string
	var untilChange bool
	switch face {
	case "ack":
		f.StringVar(&by, "by", "", "actor acknowledging the alert")
		f.StringVar(&note, "note", "", "acknowledgement note")
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
	if face == "silence" && (set["until"] == set["until-change"] || set["until-change"] && !untilChange) {
		return refuseEpisodeOptions(ctx, *path, operands, fmt.Errorf("silence requires exactly one of --until and --until-change"))
	}
	optional := func(name string, value *string) *string {
		if set[name] {
			return value
		}
		return nil
	}
	var state string
	err = store.EditExisting(ctx, *path, func(s *store.Store) error {
		e := watch.Evaluator{Store: s}
		switch face {
		case "ack":
			state = "acknowledged"
			return e.Ack(ctx, id, by, optional("note", &note))
		case "silence":
			state = "silenced"
			return e.Silence(ctx, id, optional("until", &until), optional("reason", &reason))
		default: // resolve
			state = "resolved"
			return e.Resolve(ctx, id, reason)
		}
	})
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
