package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

type varFlagMap map[string]string

func (m *varFlagMap) String() string { return "" }

func (m *varFlagMap) Set(raw string) error {
	k, v, ok := strings.Cut(raw, "=")
	if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
		return fmt.Errorf("%w: --var must be KEY=VALUE with non-empty key and value, got %q", watch.ErrInvalidSilenceBinding, raw)
	}
	if *m == nil {
		*m = make(map[string]string)
	}
	(*m)[k] = v
	return nil
}

func runSilence(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return runSilenceList(ctx, args[1:], out)
		case "lift":
			return runSilenceLift(ctx, args[1:], out)
		}
	}
	return runSilenceCreate(ctx, args, out)
}

func runSilenceCreate(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("silence", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	rulesDir := f.String("rules", "", "directory of TOML rules")
	root := f.String("root", "", "repository root for config/tillr/watch")
	noShipped := f.Bool("no-shipped-rules", false, "omit tillr's embedded shipped rules")
	var ruleID, by, item, until, reason, cause, evidence string
	var untilChange bool
	vars := varFlagMap{}
	f.StringVar(&ruleID, "rule", "", "rule ID to silence")
	f.Var(&vars, "var", "variable binding KEY=VALUE (repeatable)")
	f.StringVar(&by, "by", "", "actor creating the silence")
	f.StringVar(&item, "item", "", "open queue item ID")
	f.StringVar(&until, "until", "", "duration or RFC 3339 time")
	f.BoolVar(&untilChange, "until-change", false, "silence until the fingerprint changes")
	f.StringVar(&reason, "reason", "", "silence reason")
	f.StringVar(&cause, "cause", "", "inline investigation cause")
	f.StringVar(&evidence, "evidence", "", "inline investigation evidence")

	operands, err := parseEpisodeArgs(f, args)
	if err == flag.ErrHelp {
		_, err = io.WriteString(out, help)
		return err
	}
	if err != nil {
		if *path != "" && len(operands) == 1 && operands[0] != "" {
			return refuseEpisodeOptions(ctx, *path, operands, err)
		}
		return err
	}
	if *path == "" {
		return fmt.Errorf("silence requires --store; see --help")
	}
	set := map[string]bool{}
	f.Visit(func(fl *flag.Flag) { set[fl.Name] = true })

	if set["rule"] && len(operands) > 0 {
		return refuseEpisodeOptions(ctx, *path, operands, fmt.Errorf("silence accepts either an alert ID or --rule, not both"))
	}
	if !set["rule"] && (len(operands) != 1 || operands[0] == "") {
		err := fmt.Errorf("silence requires one alert ID or --rule; see --help")
		if len(operands) > 0 {
			return refuseEpisodeOptions(ctx, *path, operands, err)
		}
		return err
	}
	if set["rule"] && ruleID == "" {
		return fmt.Errorf("--rule must not be empty")
	}
	if set["rules"] && *rulesDir == "" {
		return fmt.Errorf("--rules must not be empty")
	}
	if set["cause"] != set["evidence"] {
		if len(operands) == 1 {
			return refuseEpisodeOptions(ctx, *path, operands, errors.New("--cause and --evidence must be provided together"))
		}
		return errors.New("--cause and --evidence must be provided together")
	}
	if set["until"] == set["until-change"] || (set["until-change"] && !untilChange) {
		if len(operands) == 1 {
			return refuseEpisodeOptions(ctx, *path, operands, fmt.Errorf("silence requires exactly one of --until and --until-change"))
		}
		return fmt.Errorf("silence requires exactly one of --until and --until-change")
	}

	var rules []watch.Rule
	if set["rule"] || *rulesDir != "" {
		rules, err = loadCLIRules(*rulesDir, *root, *path, *noShipped)
		if err != nil {
			return err
		}
	}

	opts := watch.SilenceOptions{
		RuleID:      ruleID,
		Vars:        vars,
		By:          by,
		ItemID:      item,
		Reason:      reason,
		UntilChange: untilChange,
		Cause:       cause,
		Evidence:    evidence,
	}
	if len(operands) == 1 {
		opts.AlertID = operands[0]
	}
	if set["until"] {
		u := until
		opts.Until = &u
	}

	var rec watch.SilenceRecord
	err = store.EditExisting(ctx, *path, func(s *store.Store) error {
		e := watch.Evaluator{Store: s, Rules: rules}
		needMigrate, err := e.NeedsSilenceMigration(ctx, opts)
		if err != nil {
			return err
		}
		if needMigrate {
			return errNeedSchemaMigration
		}
		rec, err = e.Silence(ctx, opts)
		return err
	})
	if errors.Is(err, errNeedSchemaMigration) {
		ms, openErr := store.OpenMigrated(ctx, *path)
		if openErr != nil {
			err = openErr
		} else {
			rec, err = watch.Evaluator{Store: ms, Rules: rules}.Silence(ctx, opts)
			if closeErr := ms.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return err
	}
	if opts.AlertID != "" {
		_, err = fmt.Fprintf(out, "alert %s silenced (%s)\n", opts.AlertID, rec.SilenceID)
	} else {
		_, err = fmt.Fprintf(out, "silence %s created (rule=%s vars=%s item=%s)\n", rec.SilenceID, rec.RuleID, rec.VarsJSON, rec.TrackingItemID)
	}
	return err
}

func runSilenceList(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("silence list", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	includeAll := f.Bool("all", false, "include lifted silences")
	asJSON := f.Bool("json", false, "print silences as JSON")
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp {
			_, err = io.WriteString(out, help)
			return err
		}
		return err
	}
	if f.NArg() != 0 || *path == "" {
		return fmt.Errorf("silence list requires --store; see --help")
	}
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return err
	}
	defer s.Close()
	silences, err := watch.Evaluator{Store: s}.ListSilences(ctx, *includeAll)
	if err != nil {
		return err
	}
	if *asJSON {
		if silences == nil {
			silences = []watch.SilenceRecord{}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(silences)
	}
	if len(silences) == 0 {
		if *includeAll {
			_, err = io.WriteString(out, "no silences\n")
		} else {
			_, err = io.WriteString(out, "no active silences\n")
		}
		return err
	}
	for _, sil := range silences {
		bound := "fingerprint=" + sil.SilenceOnFingerprint
		if sil.SilenceUntilMS != nil {
			bound = "until=" + time.UnixMilli(*sil.SilenceUntilMS).UTC().Format(time.RFC3339)
		}
		line := fmt.Sprintf("silence %s rule=%s vars=%s item=%q by=%q %s matched_alerts=%d",
			sil.SilenceID, sil.RuleID, sil.VarsJSON, sil.TrackingItemID, sil.CreatedBy, bound, sil.MatchedAlerts)
		if sil.LiftedAt != nil {
			line += fmt.Sprintf(" lifted_by=%q lifted_reason=%q", sil.LiftedBy, sil.LiftedReason)
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}

func runSilenceLift(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("silence lift", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	by := f.String("by", "", "actor lifting the silence")
	reason := f.String("reason", "", "reason for lifting the silence")
	operands, err := parseEpisodeArgs(f, args)
	if err == flag.ErrHelp {
		_, err = io.WriteString(out, help)
		return err
	}
	if err != nil {
		return err
	}
	if len(operands) != 1 || operands[0] == "" || *path == "" {
		return fmt.Errorf("silence lift requires --store and one silence ID; see --help")
	}
	silenceID := operands[0]
	err = store.EditExisting(ctx, *path, func(s *store.Store) error {
		e := watch.Evaluator{Store: s}
		needMigrate, err := e.NeedsLiftSilenceMigration(ctx, silenceID, *by, *reason)
		if err != nil {
			return err
		}
		if needMigrate {
			return errNeedSchemaMigration
		}
		return e.LiftSilence(ctx, silenceID, *by, *reason)
	})
	if errors.Is(err, errNeedSchemaMigration) {
		ms, openErr := store.OpenMigrated(ctx, *path)
		if openErr != nil {
			err = openErr
		} else {
			err = watch.Evaluator{Store: ms}.LiftSilence(ctx, silenceID, *by, *reason)
			if closeErr := ms.Close(); err == nil {
				err = closeErr
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "silence %s lifted\n", silenceID)
	return err
}
