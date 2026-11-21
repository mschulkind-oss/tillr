package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func alarmsFace(args []string) int {
	fs := newFlagSet("alarms")
	path := fs.String("store", "", "the store to read (required)")
	root := fs.String("root", ".", "repository whose accepted policy is in use")
	policy := fs.String("policy", "", "the backend policy, when not using the loop's accepted policy")
	clock := fs.String("now", "", "observation time in RFC 3339 (default: now)")
	asJSON := fs.Bool("json", false, "print the active alarms as JSON")
	if code := parseFlags(fs, args); code != record.ExitOK {
		if code == helpExit {
			return record.ExitOK
		}
		return code
	}
	if *path == "" {
		return usagef("alarms needs --store <path>")
	}
	now := time.Now().UTC()
	if *clock != "" {
		parsed, err := time.Parse(time.RFC3339, *clock)
		if err != nil {
			return usagef("--now %q is not an RFC 3339 timestamp: %v", *clock, err)
		}
		now = parsed
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return exitFor(err)
	}
	defer s.Close()
	var active []alarms.Alarm
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		active, err = readAlarms(r, *root, *policy, now)
		return err
	})
	if err != nil {
		return exitFor(err)
	}
	if *asJSON {
		if active == nil {
			active = []alarms.Alarm{}
		}
		err = json.NewEncoder(os.Stdout).Encode(active)
	} else {
		err = alarms.Print(os.Stdout, active)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: writing alarms: %v\n", err)
		return record.ExitExternal
	}
	if len(active) > 0 {
		return record.ExitNegative
	}
	return record.ExitOK
}

func readAlarms(r *store.Reader, root, ref string, now time.Time) ([]alarms.Alarm, error) {
	policy, err := alarms.ReadPolicy(root, ref)
	if err != nil {
		return nil, err
	}
	return alarms.Read(r, now, policy)
}

func alarmWaitingRows(active []alarms.Alarm, rows []waitingRow) []waitingRow {
	if len(active) == 0 {
		return rows
	}
	first := make([]waitingRow, 0, len(active)+len(rows))
	for _, a := range active {
		first = append(first, waitingRow{ItemID: "alarm:" + a.Key(), Backend: a.Backend, Class: "alarm", Where: "backend", Why: a.Cause, Mover: "operator; " + a.Command, Since: a.Since, ClassSource: "store", Alarm: &a})
	}
	return append(first, rows...)
}
