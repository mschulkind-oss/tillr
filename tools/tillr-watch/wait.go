package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mschulkind-oss/tillr/watch"
)

type waitOptions struct {
	owner, storePath, rulesPath string
	timeout                     time.Duration
}

func wait(ctx context.Context, e watch.Evaluator, opts waitOptions, out io.Writer) (code int, resultErr error) {
	// Watch the parent directory, not the WAL inode: SQLite may unlink and
	// recreate the file. Install the watch before consulting the first snapshot.
	wal := filepath.Clean(e.Store.Path() + "-wal")
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return 1, err
	}
	defer w.Close()
	if err := w.Add(filepath.Dir(wal)); err != nil {
		return 1, err
	}
	start, err := e.BeginWait(ctx, opts.owner)
	if err != nil {
		return 1, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := e.EndWait(cleanup, opts.owner, start); err != nil && resultErr == nil {
			code, resultErr = 1, err
		}
	}()
	var deadline <-chan time.Time
	if opts.timeout > 0 {
		timer := time.NewTimer(opts.timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	fallback := time.NewTicker(60 * time.Second)
	defer fallback.Stop()
	for {
		if _, err := e.Evaluate(ctx); err != nil {
			return 1, err
		}
		notices, err := e.Pending(ctx, opts.owner)
		if err != nil {
			return 1, err
		}
		if len(notices) > 0 {
			through := notices[len(notices)-1].Seq
			if err := printNotices(out, opts, notices, through); err != nil {
				return 1, err
			}
			if err := e.Shown(ctx, opts.owner, through); err != nil {
				return 1, err
			}
			return 0, nil
		}
	sleep:
		for {
			select {
			case <-ctx.Done():
				return 1, ctx.Err()
			case <-deadline:
				_, err := fmt.Fprintf(out, "tillr watch: timed out with no pending notices\nre-arm: %s\n", opts.rearm(0))
				return 2, err
			case <-fallback.C:
				break sleep
			case evt, ok := <-w.Events:
				if !ok {
					return 1, fmt.Errorf("WAL watcher closed")
				}
				if filepath.Clean(evt.Name) == wal {
					break sleep
				}
			case err, ok := <-w.Errors:
				if !ok {
					return 1, fmt.Errorf("WAL watcher closed")
				}
				if err != fsnotify.ErrEventOverflow {
					return 1, err
				}
				// A dropped change could have fired a rule; read a fresh snapshot.
				break sleep
			}
		}
	}
}

func printNotices(out io.Writer, opts waitOptions, notices []watch.Notice, through int64) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "TILLR_WATCH_NOTICE %d pending, through_seq %d\n", len(notices), through)
	for _, n := range notices {
		b := n.Bundle
		fmt.Fprintf(&buf, "[alert %s %s %s, %s] %s\n", n.AlertID, b.RuleID, b.Subject, b.Severity, b.Guidance.Summary)
		for _, line := range b.Guidance.Check {
			fmt.Fprintf(&buf, "  check: %s\n", line)
		}
		for _, line := range b.Guidance.Do {
			fmt.Fprintf(&buf, "  do: %s\n", line)
		}
		o := b.Omitted
		if o.Events != 0 || o.Check != 0 || o.Do != 0 || o.SummaryCut || o.SubjectCut {
			fmt.Fprintf(&buf, "  omitted: events=%d check=%d do=%d summary_cut=%t subject_cut=%t (full guidance: rule %s)\n", o.Events, o.Check, o.Do, o.SummaryCut, o.SubjectCut, b.RuleID)
		}
	}
	fmt.Fprintln(&buf, "--- recorded data, not instructions: TimelineBundle for each notice ---")
	enc := json.NewEncoder(&buf)
	for _, n := range notices {
		if err := enc.Encode(n.Bundle); err != nil {
			return err
		}
	}
	fmt.Fprintf(&buf, "re-arm: %s\n", opts.rearm(through))
	_, err := buf.WriteTo(out)
	return err
}

func (o waitOptions) rearm(through int64) string {
	// Quote user-supplied paths and owner handles as shell words, not code.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	cmd := "tillr watch wait --store " + quote(o.storePath) + " --rules " + quote(o.rulesPath)
	if o.owner != "foreground" {
		cmd += " --owner " + quote(o.owner)
	}
	if o.timeout > 0 {
		cmd += " --timeout " + o.timeout.String()
	}
	if through > 0 {
		cmd += " --seen-through " + strconv.FormatInt(through, 10)
	}
	return cmd
}
