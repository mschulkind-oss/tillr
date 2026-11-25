package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func runRule(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("rule requires list, show, add, tune, reset or export")
	}
	command := args[0]
	if command == "--help" || command == "-h" {
		_, err := io.WriteString(out, help)
		return err
	}
	f := flag.NewFlagSet("rule "+command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	dir := f.String("rules", "", "directory of TOML rules")
	var by string
	var sets []string
	var needsArg, writes bool
	switch command {
	case "list", "export":
	case "show":
		needsArg = true
	case "add", "tune", "reset":
		needsArg, writes = true, true
		f.StringVar(&by, "by", "", "actor recording the rule change")
		if command == "tune" {
			f.Func("set", "key=TOML", func(value string) error {
				sets = append(sets, value)
				return nil
			})
		}
	default:
		return fmt.Errorf("unknown rule command %q", command)
	}
	operands, err := parseEpisodeArgs(f, args[1:])
	if err == flag.ErrHelp {
		_, err = io.WriteString(out, help)
		return err
	}
	if err != nil {
		return err
	}
	wantArgs := 0
	if needsArg {
		wantArgs = 1
	}
	if len(operands) != wantArgs || (needsArg && operands[0] == "") || *path == "" || *dir == "" {
		return fmt.Errorf("invalid arguments for rule %s; see --help", command)
	}
	var arg string
	if needsArg {
		arg = operands[0]
	}
	if writes && by == "" {
		return fmt.Errorf("--by is required and must not be empty")
	}
	if command == "tune" && len(sets) == 0 {
		return fmt.Errorf("tune requires at least one --set")
	}
	files, err := watch.LoadRules(*dir)
	if err != nil {
		return err
	}
	storePath, err := filepath.EvalSymlinks(*path)
	if err != nil {
		return fmt.Errorf("store %s: %w", *path, err)
	}
	if writes {
		var data []byte
		if command == "add" {
			if data, err = os.ReadFile(arg); err != nil {
				return err
			}
			if _, err := watch.LoadRuleDocument(arg, data); err != nil {
				return err
			}
		}
		return store.EditExisting(ctx, storePath, func(s *store.Store) error {
			changes := watch.RuleStore{Store: s, FileRules: files}
			switch command {
			case "add":
				return changes.Add(ctx, arg, data, by)
			case "tune":
				return changes.Tune(ctx, arg, sets, by)
			default: // reset
				return changes.Reset(ctx, arg, by)
			}
		})
	}
	var buf bytes.Buffer
	if err := store.ReadWithoutCreating(ctx, storePath, func(s *store.Store) error {
		return s.ReadTx(ctx, func(rd *store.Reader) error {
			if command == "export" {
				stored, err := watch.ReadStoredRules(rd)
				if err != nil {
					return err
				}
				return printRuleTOML(&buf, stored)
			}
			effective, err := watch.EffectiveRules(rd, files)
			if err != nil {
				return err
			}
			return reportRules(effective, command, arg, &buf)
		})
	}); err != nil {
		return err
	}
	_, err = out.Write(buf.Bytes())
	return err
}

func reportRules(effective []watch.EffectiveRule, command, id string, out io.Writer) error {
	for _, r := range effective {
		if command == "show" {
			if r.ID == id {
				return printRuleTOML(out, []watch.Rule{r.Rule})
			}
			continue
		}
		if _, err := fmt.Fprintf(out, "rule %s source=%s enabled=%t scope=%s pattern=%s\n", r.ID, r.Source, r.Enabled, r.Scope, r.When.Pattern); err != nil {
			return err
		}
	}
	if command == "show" {
		return fmt.Errorf("no effective rule %q", id)
	}
	return nil
}

func printRuleTOML(out io.Writer, rules []watch.Rule) error {
	text, err := watch.EncodeRules(rules)
	if err != nil || len(text) == 0 {
		return err
	}
	_, err = out.Write(text)
	return err
}
