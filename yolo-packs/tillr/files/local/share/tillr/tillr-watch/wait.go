package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/watch"
)

type waitTestHooks struct {
	deadline   <-chan time.Time
	onPass     func()
	onIdle     func(coalesceArmed bool)
	onCoalesce func() func()
	// onRelayProgress runs after a wake is queued; its bool marks a reconnect.
	onRelayProgress func(bool, notify.Frame)
}

type waitOptions struct {
	owner, storePath, rulesPath, root string
	timeout                           time.Duration
	fallbackPoll                      time.Duration
	follow                            bool
	noNotify                          bool
	ticks                             <-chan time.Time
	hooks                             *waitTestHooks
}

func wait(ctx context.Context, e watch.Evaluator, opts waitOptions, out, stderr io.Writer) (code int, resultErr error) {
	var onCoalesce func() func()
	var onRelayProgress func(bool, notify.Frame)
	if opts.hooks != nil {
		onCoalesce = opts.hooks.onCoalesce
		onRelayProgress = opts.hooks.onRelayProgress
	}
	wake, err := watchNotify(ctx, e.Store.Path(), notifyWakeOptions{
		fallbackPoll:      opts.fallbackPoll,
		noNotify:          opts.noNotify,
		ticks:             opts.ticks,
		minNotifyInterval: defaultMinNotifyInterval,
		onCoalesce:        onCoalesce,
		onRelayProgress:   onRelayProgress,
	})
	if err != nil {
		return 1, err
	}
	defer wake.close()
	// Record rule load baselines before advertising waiting_since so events
	// appended as soon as attachment is visible are judged as post-load.
	if _, err := e.EvaluateRules(ctx); err != nil {
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
	if opts.hooks != nil && opts.hooks.deadline != nil {
		deadline = opts.hooks.deadline
	} else if opts.timeout > 0 {
		timer := time.NewTimer(opts.timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	// Track only what this process has printed. The durable seen cursor remains
	// the authority for pending notices and is changed solely by confirmation.
	printed := make(map[int64]bool)
	tick := false
	var diagnostics ruleDiagnostics
	for {
		wake.beginPass()
		if opts.hooks != nil && opts.hooks.onPass != nil {
			opts.hooks.onPass()
		}
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			return 1, err
		}
		diagnostics.report(stderr, results)
		notices, err := e.Pending(ctx, opts.owner)
		if err != nil {
			return 1, err
		}
		current := make(map[int64]bool, len(notices))
		newNotice := false
		for _, n := range notices {
			current[n.Seq] = true
			if !printed[n.Seq] {
				newNotice = true
			}
		}
		if len(notices) > 0 {
			through := notices[len(notices)-1].Seq
			if !opts.follow || newNotice {
				// A slow output reader must not hold the store's write lock.
				// Record only complete output. If a follow-mode confirmation races
				// this commit, the notice stays pending and the next marker prompts
				// another confirmation.
				if err := printNotices(out, opts, notices, through); err != nil {
					return 1, err
				}
				if err := e.Shown(ctx, opts.owner, through); err != nil {
					return 1, err
				}
				if !opts.follow {
					return 0, nil
				}
			} else if tick {
				if _, err := fmt.Fprintf(out, "TILLR_WATCH_NOTICE %d pending, through_seq %d\n", len(notices), through); err != nil {
					return 1, err
				}
			}
		}
		if opts.hooks != nil && opts.hooks.onIdle != nil {
			opts.hooks.onIdle(wake.coalesceArmed())
		}
		reason, err := wake.next(ctx, deadline)
		if err != nil {
			return 1, err
		}
		if reason == deadlineWake {
			_, err := fmt.Fprintf(out, "tillr watch: timed out with no pending notices\nre-arm: %s\n", opts.rearm(0))
			return 2, err
		}
		printed, tick = current, reason == timerWake
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
		fmt.Fprintf(&buf, "  ack: tillr watch ack %s --by %s --store %s\n", n.AlertID, shellWord(opts.owner), shellWord(opts.storePath))
		fmt.Fprintf(&buf, "  track: tillr watch track %s --item <item-id> --by %s --store %s\n", n.AlertID, shellWord(opts.owner), shellWord(opts.storePath))
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

// Quote user-supplied paths and owner handles as shell words, not code.
func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (o waitOptions) rearm(through int64) string {
	cmd := "tillr watch wait --store " + shellWord(o.storePath) + " --rules " + shellWord(o.rulesPath)
	if o.root != "" {
		cmd += " --root " + shellWord(o.root)
	}
	if o.owner != "foreground" {
		cmd += " --owner " + shellWord(o.owner)
	}
	if o.timeout > 0 {
		cmd += " --timeout " + o.timeout.String()
	}
	if o.follow {
		cmd += " --follow"
	}
	if o.fallbackPoll > 0 && o.fallbackPoll != defaultFallbackPoll {
		cmd += " --fallback-poll " + o.fallbackPoll.String()
	}
	if o.noNotify {
		cmd += " --no-notify"
	}
	if through > 0 {
		cmd += " --seen-through " + strconv.FormatInt(through, 10)
	}
	return cmd
}
