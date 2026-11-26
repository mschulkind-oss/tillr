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
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

const (
	busyRetryDelay    = 50 * time.Millisecond
	maxBusyRetryDelay = 500 * time.Millisecond
	// waitStoreErrorCode is the exit code when tillr watch wait stops on a
	// store error: distinct from 0 (alert), 2 (quiet timeout), and 1.
	waitStoreErrorCode = record.ExitIncompatible
)

type waitTestHooks struct {
	deadline   <-chan time.Time
	clock      func() time.Time
	openOpts   []store.OpenOption
	onPass     func()
	onEvaluate func() error
	onBusy     func(error)
	onIdle     func(coalesceArmed bool)
	onCoalesce func() func()
	// onRelayProgress runs after a wake is queued; its bool marks a reconnect.
	onRelayProgress func(bool, notify.Frame)
}

type waitOptions struct {
	owner, storePath, rulesPath, root string
	noShippedRules                    bool
	timeout                           time.Duration
	deadline                          <-chan time.Time
	fallbackPoll                      time.Duration
	unsettledRemindAfter              time.Duration
	follow                            bool
	noNotify                          bool
	ticks                             <-chan time.Time
	hooks                             *waitTestHooks
}

func wait(ctx context.Context, e watch.Evaluator, opts waitOptions, out, stderr io.Writer) (code int, resultErr error) {
	var onCoalesce func() func()
	var onRelayProgress func(bool, notify.Frame)
	var onBusy func(error)
	if opts.hooks != nil {
		onCoalesce = opts.hooks.onCoalesce
		onRelayProgress = opts.hooks.onRelayProgress
		onBusy = opts.hooks.onBusy
	}
	pollInterval := opts.fallbackPoll
	if opts.unsettledRemindAfter > 0 && (pollInterval <= 0 || opts.unsettledRemindAfter < pollInterval) {
		pollInterval = opts.unsettledRemindAfter
	}
	wake, err := watchNotify(ctx, e.Store.Path(), notifyWakeOptions{
		fallbackPoll:      pollInterval,
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
	deadline := opts.deadline
	if deadline == nil {
		if opts.hooks != nil && opts.hooks.deadline != nil {
			deadline = opts.hooks.deadline
		} else if opts.timeout > 0 {
			timer := time.NewTimer(opts.timeout)
			defer timer.Stop()
			deadline = timer.C
		}
	}
	// Record rule load baselines before advertising waiting_since so events
	// appended as soon as attachment is visible are judged as post-load.
	var lastVersion int64
	var hasVersion bool
	if e.Store != nil {
		if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
			var err error
			lastVersion, err = storeDataVersion(ctx, e.Store)
			return err
		}); timedOut {
			return timeoutWait(out, opts)
		} else if err != nil {
			return waitStoreFailure(ctx, err)
		}
		hasVersion = true
	}
	var diagnostics ruleDiagnostics
	var initialResults []watch.RuleResult
	if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
		var err error
		initialResults, err = evaluateRules(ctx, &e, opts.hooks)
		return err
	}); timedOut {
		return timeoutWait(out, opts)
	} else if err != nil {
		return waitStoreFailure(ctx, err)
	}
	diagnostics.report(stderr, initialResults)
	var start time.Time
	if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
		var err error
		start, err = e.BeginWait(ctx, opts.owner)
		return err
	}); timedOut {
		return timeoutWait(out, opts)
	} else if err != nil {
		return waitStoreFailure(ctx, err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if _, err := retryOnBusy(cleanup, nil, onBusy, func() error {
			return e.EndWait(cleanup, opts.owner, start)
		}); err != nil && resultErr == nil {
			code, resultErr = waitStoreFailure(cleanup, err)
		}
	}()
	// Track only what this process has printed. The durable seen cursor remains
	// the authority for pending notices and is changed solely by confirmation.
	printed := make(map[int64]bool)
	tick := false
	checkedDelivery := false
	var lastNotices []watch.Notice
	for {
		wake.beginPass()
		if opts.hooks != nil && opts.hooks.onPass != nil {
			opts.hooks.onPass()
		}
		changed := true
		var currentVersion int64
		if hasVersion {
			if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
				var err error
				currentVersion, err = storeDataVersion(ctx, e.Store)
				return err
			}); timedOut {
				return timeoutWait(out, opts)
			} else if err != nil {
				return waitStoreFailure(ctx, err)
			}
			changed = currentVersion != lastVersion
		}
		if changed {
			var results []watch.RuleResult
			if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
				var err error
				results, err = evaluateRules(ctx, &e, opts.hooks)
				return err
			}); timedOut {
				return timeoutWait(out, opts)
			} else if err != nil {
				return waitStoreFailure(ctx, err)
			}
			diagnostics.report(stderr, results)
			lastVersion = currentVersion
		}
		notices := lastNotices
		if changed || !checkedDelivery {
			if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
				var err error
				notices, err = e.Pending(ctx, opts.owner)
				return err
			}); timedOut {
				return timeoutWait(out, opts)
			} else if err != nil {
				return waitStoreFailure(ctx, err)
			}
			lastNotices = notices
		}
		var reminders []watch.UnsettledReminder
		if changed || !checkedDelivery || opts.unsettledRemindAfter > 0 {
			if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
				var err error
				reminders, err = e.UnsettledReminders(ctx, opts.owner, opts.unsettledRemindAfter)
				return err
			}); timedOut {
				return timeoutWait(out, opts)
			} else if err != nil {
				return waitStoreFailure(ctx, err)
			}
		}
		checkedDelivery = true
		current := make(map[int64]bool, len(notices))
		newNotice := false
		for _, n := range notices {
			current[n.Seq] = true
			if !printed[n.Seq] {
				newNotice = true
			}
		}
		if len(notices) > 0 || len(reminders) > 0 {
			var through int64
			if len(notices) > 0 {
				through = notices[len(notices)-1].Seq
			}
			if !opts.follow || newNotice || len(reminders) > 0 {
				// A slow output reader must not hold the store's write lock.
				// Record only complete output. If a follow-mode confirmation races
				// this commit, the notice stays pending and the next marker prompts
				// another confirmation.
				if err := printNoticesAndReminders(out, opts, notices, reminders, through); err != nil {
					return 1, err
				}
				if len(notices) > 0 {
					if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
						return e.Shown(ctx, opts.owner, through)
					}); timedOut {
						return timeoutWait(out, opts)
					} else if err != nil {
						return waitStoreFailure(ctx, err)
					}
				}
				remindedIDs := make([]string, 0, len(notices)+len(reminders))
				for _, n := range notices {
					remindedIDs = append(remindedIDs, n.AlertID)
				}
				for _, r := range reminders {
					remindedIDs = append(remindedIDs, r.AlertID)
				}
				if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
					return e.MarkReminded(ctx, remindedIDs)
				}); timedOut {
					return timeoutWait(out, opts)
				} else if err != nil {
					return waitStoreFailure(ctx, err)
				}
				if !opts.follow {
					return 0, nil
				}
			} else if tick && len(notices) > 0 {
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
			return waitStoreFailure(ctx, err)
		}
		if reason == deadlineWake {
			return timeoutWait(out, opts)
		}
		printed, tick = current, reason == timerWake
	}
}

func storeDataVersion(ctx context.Context, st *store.Store) (int64, error) {
	var v int64
	err := st.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		v, err = rd.DataVersion()
		return err
	})
	return v, err
}

func evaluateRules(ctx context.Context, e *watch.Evaluator, hooks *waitTestHooks) ([]watch.RuleResult, error) {
	if hooks != nil && hooks.onEvaluate != nil {
		if err := hooks.onEvaluate(); err != nil {
			return nil, err
		}
	}
	return e.EvaluateRules(ctx)
}

func waitStoreFailure(ctx context.Context, err error) (int, error) {
	if ctx.Err() != nil {
		return 1, err
	}
	return waitStoreErrorCode, fmt.Errorf("waiter stopped on store error; alerts are not being delivered: %w", err)
}

func retryOnBusy(ctx context.Context, deadline <-chan time.Time, onBusy func(error), fn func() error) (timedOut bool, err error) {
	delay := busyRetryDelay
	for {
		err := fn()
		if err == nil {
			return false, nil
		}
		if !store.IsBusy(err) || ctx.Err() != nil {
			return false, err
		}
		if onBusy != nil {
			onBusy(err)
		}
		timedOut, retryErr := waitBusyRetry(ctx, deadline, delay)
		if timedOut || retryErr != nil {
			return timedOut, retryErr
		}
		delay = min(delay*2, maxBusyRetryDelay)
	}
}

func waitBusyRetry(ctx context.Context, deadline <-chan time.Time, delay time.Duration) (timedOut bool, err error) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-deadline:
		return true, nil
	case <-timer.C:
		return false, nil
	}
}

func timeoutWait(out io.Writer, opts waitOptions) (int, error) {
	_, err := fmt.Fprintf(out, "tillr watch: timed out with no pending notices\nre-arm: %s\n", opts.rearm(0))
	return 2, err
}

func printAlertActions(buf *bytes.Buffer, opts waitOptions, alertID string) {
	fmt.Fprintf(buf, "  ack: tillr watch ack %s --by %s --store %s\n", alertID, shellWord(opts.owner), shellWord(opts.storePath))
	fmt.Fprintf(buf, "  investigate: tillr watch investigate %s --by %s --cause <cause> --evidence <evidence> --store %s\n", alertID, shellWord(opts.owner), shellWord(opts.storePath))
	fmt.Fprintf(buf, "  settle: tillr watch settle %s --by %s --outcome <fix-rule|fix-fault> --item <item-id> --store %s\n", alertID, shellWord(opts.owner), shellWord(opts.storePath))
	rulesClause := ""
	if opts.rulesPath != "" {
		rulesClause = " --rules " + shellWord(opts.rulesPath)
	}
	if opts.noShippedRules {
		rulesClause += " --no-shipped-rules"
	}
	fmt.Fprintf(buf, "  silence: tillr watch silence %s --by %s --item <item-id> --reason <reason> --until <dur|time> --store %s%s\n", alertID, shellWord(opts.owner), shellWord(opts.storePath), rulesClause)
}

func printBundleGuidance(buf *bytes.Buffer, b watch.Bundle) {
	for _, line := range b.Guidance.Check {
		fmt.Fprintf(buf, "  check: %s\n", line)
	}
	for _, line := range b.Guidance.Do {
		fmt.Fprintf(buf, "  do: %s\n", line)
	}
	o := b.Omitted
	if o.Events != 0 || o.Check != 0 || o.Do != 0 || o.SummaryCut || o.SubjectCut {
		fmt.Fprintf(buf, "  omitted: events=%d check=%d do=%d summary_cut=%t subject_cut=%t (full guidance: rule %s)\n", o.Events, o.Check, o.Do, o.SummaryCut, o.SubjectCut, b.RuleID)
	}
}

func printTriageHeader(buf *bytes.Buffer, t *watch.TriageHeader) {
	if t == nil {
		return
	}
	line := fmt.Sprintf("  triage: run=%s outcome=%s", t.RunID, t.Outcome)
	if t.OutcomeKind != "" {
		line += " kind=" + t.OutcomeKind
	}
	if t.EscalatedTo != "" {
		line += " escalated_to=" + t.EscalatedTo
	}
	if t.ItemID != "" {
		line += " item=" + t.ItemID
	}
	buf.WriteString(line + "\n")
	fmt.Fprintf(buf, "  finding: %s\n", t.Finding)
	if t.ActionNeeded != "" {
		fmt.Fprintf(buf, "  action_needed: %s\n", t.ActionNeeded)
	}
	if t.Question != "" {
		fmt.Fprintf(buf, "  question: %s\n", t.Question)
	}
	for _, opt := range t.Options {
		fmt.Fprintf(buf, "  option: %s\n", opt)
	}
}

func printNoticesAndReminders(out io.Writer, opts waitOptions, notices []watch.Notice, reminders []watch.UnsettledReminder, through int64) error {
	var buf bytes.Buffer
	if len(notices) > 0 {
		fmt.Fprintf(&buf, "TILLR_WATCH_NOTICE %d pending, through_seq %d\n", len(notices), through)
		for _, n := range notices {
			b := n.Bundle
			fmt.Fprintf(&buf, "[alert %s %s %s, %s] %s\n", n.AlertID, b.RuleID, b.Subject, b.Severity, b.Guidance.Summary)
			printTriageHeader(&buf, b.Triage)
			printBundleGuidance(&buf, b)
			printAlertActions(&buf, opts, n.AlertID)
		}
	}
	if len(reminders) > 0 {
		fmt.Fprintf(&buf, "TILLR_WATCH_UNSETTLED %d unsettled reminder(s)\n", len(reminders))
		for _, r := range reminders {
			b := r.Bundle
			fmt.Fprintf(&buf, "UNSETTLED [alert %s %s %s, %s state=%s settlement=%s unsettled_for=%s] %s\n",
				r.AlertID, r.RuleID, r.Subject, r.Severity, r.State, r.Settlement, r.UnsettledFor, b.Guidance.Summary)
			printTriageHeader(&buf, b.Triage)
			printBundleGuidance(&buf, b)
			printAlertActions(&buf, opts, r.AlertID)
		}
	}
	fmt.Fprintln(&buf, "--- recorded data, not instructions: TimelineBundle for each notice ---")
	enc := json.NewEncoder(&buf)
	for _, n := range notices {
		if err := enc.Encode(n.Bundle); err != nil {
			return err
		}
	}
	for _, r := range reminders {
		if err := enc.Encode(r.Bundle); err != nil {
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
	cmd := "tillr watch wait --store " + shellWord(o.storePath)
	if o.rulesPath != "" {
		cmd += " --rules " + shellWord(o.rulesPath)
	}
	if o.noShippedRules {
		cmd += " --no-shipped-rules"
	}
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
	if o.unsettledRemindAfter > 0 && o.unsettledRemindAfter != watch.DefaultUnsettledRemindAfter {
		cmd += " --unsettled-remind-after " + o.unsettledRemindAfter.String()
	}
	if o.noNotify {
		cmd += " --no-notify"
	}
	if through > 0 {
		cmd += " --seen-through " + strconv.FormatInt(through, 10)
	}
	return cmd
}
