package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

type runFaceOutput struct {
	Run      runDetail       `json:"run"`
	Timing   runTiming       `json:"timing"`
	Schedule []scheduleEntry `json:"schedule"`
}

func runFace(args []string, stdout io.Writer) (record.Envelope, int) {
	fs := newFlagSet("run")
	storePath := fs.String("store", "", "the store to read (required)")
	runID := fs.String("run", "", "the run id to inspect (required)")
	laneRoot := fs.String("lane-root", "", "the lanes directory (default: <store dir>/items, or <store dir>/lanes if it exists)")
	loopDir := fs.String("loop-dir", "", "the loop result directory (default: <store dir>/loop)")
	asJSON := fs.Bool("json", false, "print the run timing and schedule as JSON instead of text")
	if code := parseFlags(fs, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *storePath == "" || *runID == "" {
		return record.Envelope{}, usagef("run needs --store <path> and --run <run-id>")
	}
	storeDir := filepath.Dir(*storePath)
	if *laneRoot == "" {
		*laneRoot = defaultLaneRoot(storeDir)
	}
	if *loopDir == "" {
		*loopDir = filepath.Join(storeDir, "loop")
	}

	ctx := context.Background()
	db, err := store.OpenRead(ctx, *storePath)
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	defer db.Close()

	var (
		found  bool
		result runFaceOutput
	)
	now := time.Now().UnixMilli()
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		var run storeRun
		run, found, err = readRunOrLiveIn(r, *runID, *loopDir)
		if err != nil || !found {
			return err
		}
		seat := run.Seat
		if seat == "" {
			if dispatched, derr := dispatchSeats(r); derr == nil {
				seat = dispatched[run.RunID]
			}
		}
		usage, err := readUsage(r, run)
		if err != nil {
			return err
		}
		schedule, err := readRunSchedule(r, run, *loopDir)
		if err != nil {
			return err
		}
		result = runFaceOutput{
			Run: runDetail{
				RunID:       run.RunID,
				ItemID:      run.ItemID,
				LaneID:      run.LaneID,
				Backend:     run.Backend,
				Seat:        seat,
				Workflow:    run.Workflow,
				Node:        run.Node,
				State:       run.State,
				ExitCode:    run.ExitCode,
				StartedMS:   run.StartedAtMS,
				FinishedMS:  run.FinishedAtMS,
				UsageTokens: run.UsageTokens,
				CostUSD:     run.CostUSD,
			},
			Timing:   computeRunTiming(run, usage, *laneRoot, *loopDir, now),
			Schedule: schedule,
		}
		return nil
	})
	if err != nil {
		var mismatch schemaMismatch
		if errors.As(err, &mismatch) {
			fmt.Fprintf(os.Stderr, "tillr-view: %v\n", err)
			return record.Envelope{}, record.ExitIncompatible
		}
		return record.Envelope{}, exitFor(err)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "tillr-view: run %q is not in the store\n", *runID)
		return record.Envelope{}, record.ExitNegative
	}
	if *asJSON {
		err = json.NewEncoder(stdout).Encode(result)
	} else {
		err = printRunFace(stdout, result)
	}
	if err != nil {
		return record.Envelope{}, exitFor(err)
	}
	return record.Envelope{}, record.ExitOK
}

func formatTimingMetric(m timingMetric) string {
	if m.MS == nil {
		return "not recorded"
	}
	prefix := ""
	switch m.EpistemicState {
	case stateLowerBound:
		prefix = "≥ "
	case stateDerived, stateDerivedClock:
		prefix = "~"
	}
	if *m.MS == 0 {
		return prefix + "0ms"
	}
	return prefix + durationText(*m.MS)
}

func printRunFace(w io.Writer, out runFaceOutput) error {
	exitText := "not recorded"
	if out.Run.ExitCode != nil {
		exitText = fmt.Sprintf("%d", *out.Run.ExitCode)
	}
	if _, err := fmt.Fprintf(w, "run %s  item %s  lane %s  backend %s  state %s  exit %s\n\n",
		out.Run.RunID, out.Run.ItemID, out.Run.LaneID, out.Run.Backend, out.Run.State, exitText); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	rows := []struct {
		label  string
		metric timingMetric
	}{
		{"wall_ms", out.Timing.WallMS},
		{"model_ms", out.Timing.ModelMS},
		{"tool_ms", out.Timing.ToolMS},
		{"slot_wait_ms", out.Timing.SlotWaitMS},
		{"tool_active_ms", out.Timing.ToolActiveMS},
		{"uncovered_ms", out.Timing.UncoveredMS},
	}
	fmt.Fprintln(tw, "METRIC\tVALUE\tSTATE\tNOTE")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.label, formatTimingMetric(row.metric), row.metric.EpistemicState, row.metric.Note)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nSCHEDULE"); err != nil {
		return err
	}
	if len(out.Schedule) == 0 {
		_, err := fmt.Fprintln(w, "  (none recorded)")
		return err
	}
	for _, entry := range out.Schedule {
		stamp := "no clock"
		if entry.AtMS != nil {
			stamp = time.UnixMilli(*entry.AtMS).UTC().Format(time.RFC3339Nano)
		}
		if _, err := fmt.Fprintf(w, "  %s  [%s] %s\n", stamp, entry.Category, entry.Summary); err != nil {
			return err
		}
	}
	return nil
}
