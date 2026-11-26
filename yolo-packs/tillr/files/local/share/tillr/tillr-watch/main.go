// Command tillr-watch observes events and reports alert episodes without applying remedies.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	// Match tillr-store's producer registry. Linking the drain package registers
	// its event kinds; no drain command or loop runs in this process.
	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/buildstamp"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

const help = `Usage:
  tillr watch eval --once --store PATH --rules DIR [--root DIR]
  tillr watch status --store PATH --rules DIR [--root DIR] [--unsettled] [--recent DUR] [--json]
  tillr watch daemon --store PATH --rules DIR [--root DIR]
  tillr watch wait --store PATH --rules DIR [--root DIR] [--owner ID] [--timeout DUR | --follow] [--fallback-poll DUR] [--unsettled-remind-after DUR] [--no-notify] [--seen-through SEQ]
  tillr watch seen --store PATH --through SEQ [--owner ID]
  tillr watch ack ID --store PATH --by ACTOR [--note TEXT]
  tillr watch investigate ID --store PATH --by ACTOR --cause TEXT --evidence TEXT
  tillr watch settle ID --store PATH --by ACTOR --outcome (fix-rule|fix-fault) --item ITEM_ID [--link-kind filed|linked] [--cause TEXT --evidence TEXT]
  tillr watch claim-triage ID --run RUN_ID --store PATH
  tillr watch record-triage ID --run RUN_ID --outcome (filed|linked|escalated) --finding TEXT [--outcome-kind (fix-fault|fix-rule)] [--item ITEM_ID] [--escalate-to (operator-session|owner)] [--action-needed TEXT] [--question TEXT] [--option TEXT]... --store PATH
  tillr watch silence ID --store PATH --by ACTOR --item ITEM_ID --reason TEXT (--until DUR_OR_TIME | --until-change) [--cause TEXT --evidence TEXT] [--rules DIR]
  tillr watch silence --rule ID [--var KEY=VAL ...] --store PATH --rules DIR --by ACTOR --item ITEM_ID --reason TEXT (--until DUR_OR_TIME | --until-change) --cause TEXT --evidence TEXT
  tillr watch silence list --store PATH [--all] [--json]
  tillr watch silence lift SILENCE_ID --store PATH --by ACTOR --reason TEXT
  tillr watch rule list --store PATH --rules DIR
  tillr watch rule show ID --store PATH --rules DIR
  tillr watch rule add FILE --store PATH --rules DIR --by ACTOR
  tillr watch rule tune ID --store PATH --rules DIR --by ACTOR --set KEY=TOML [--set KEY=TOML ...]
  tillr watch rule reset ID --store PATH --rules DIR --by ACTOR
  tillr watch rule export --store PATH --rules DIR
  tillr watch lint [<repo-or-dir>] [--charter <path>] [--workflows <path>] [--policy <path>]... [--rules <path>]

Watch reports conditions from store events. It never executes guidance or dispatches a run.
`

func main() {
	defer store.FlushSQLiteTraceAtExit()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	store.FlushSQLiteTraceAtExit()
	os.Exit(code)
}

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	return runWithTicks(ctx, args, out, stderr, nil)
}

// ticks overrides the fallback clock in tests; wait uses a 15-second fallback
// ticker and daemon uses a 60-second ticker.
func runWithTicks(ctx context.Context, args []string, out, stderr io.Writer, ticks <-chan time.Time) int {
	return runWithWaitHooks(ctx, args, out, stderr, ticks, nil)
}

func runWithWaitHooks(ctx context.Context, args []string, out, stderr io.Writer, ticks <-chan time.Time, hooks *waitTestHooks) int {
	fail := func(err error) int { fmt.Fprintln(stderr, "tillr watch:", err); return 1 }
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, help)
		return 0
	}
	if args[0] == "--version" {
		fmt.Fprintln(out, buildstamp.Read().String())
		return 0
	}
	face := args[0]
	switch face {
	case "ack", "investigate", "settle":
		if err := runEpisode(ctx, face, args[1:], out); err != nil {
			if errors.Is(err, watch.ErrInvestigationRequired) {
				fmt.Fprintln(stderr, "tillr watch:", err)
				return 2
			}
			return fail(err)
		}
		return 0
	case "claim-triage":
		if err := runClaimTriage(ctx, args[1:], out); err != nil {
			return fail(err)
		}
		return 0
	case "record-triage":
		if err := runRecordTriage(ctx, args[1:], out); err != nil {
			if errors.Is(err, watch.ErrInvalidTriageOutcome) {
				fmt.Fprintln(stderr, "tillr watch:", err)
				return 2
			}
			return fail(err)
		}
		return 0
	case "silence":
		if err := runSilence(ctx, args[1:], out); err != nil {
			if errors.Is(err, watch.ErrInvestigationRequired) || errors.Is(err, watch.ErrInvalidSilenceBinding) || strings.Contains(err.Error(), watch.ErrInvalidSilenceBinding.Error()) {
				fmt.Fprintln(stderr, "tillr watch:", err)
				return 2
			}
			return fail(err)
		}
		return 0
	case "rule":
		if err := runRule(ctx, args[1:], out); err != nil {
			return fail(err)
		}
		return 0
	case "lint":
		return runLint(ctx, args[1:], out, stderr)
	}
	f := flag.NewFlagSet(face, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	var dir, owner, root, recentStr string
	var timeout, fallbackPoll, unsettledRemindAfter, recent time.Duration
	var cursor int64
	var once, follow, noNotify, asJSON, onlyUnsettled bool
	switch face {
	case "eval", "wait", "status", "daemon":
		f.StringVar(&dir, "rules", "", "directory of TOML rules")
		f.StringVar(&root, "root", "", "repository for Git projections")
	case "seen":
	default:
		return fail(fmt.Errorf("unknown face %q", face))
	}
	switch face {
	case "eval":
		f.BoolVar(&once, "once", false, "evaluate once")
	case "status":
		f.BoolVar(&asJSON, "json", false, "print the status report as JSON")
		f.BoolVar(&onlyUnsettled, "unsettled", false, "list every unsettled episode, including resolved ones")
		f.StringVar(&recentStr, "recent", "", "list episodes opened, triaged, and resolved within this window")
	case "wait":
		f.StringVar(&owner, "owner", "foreground", "delivery owner")
		f.DurationVar(&timeout, "timeout", 0, "time to wait for a notice")
		f.BoolVar(&follow, "follow", false, "remain attached after notices")
		f.DurationVar(&fallbackPoll, "fallback-poll", defaultFallbackPoll, "fallback poll interval")
		f.DurationVar(&unsettledRemindAfter, "unsettled-remind-after", watch.DefaultUnsettledRemindAfter, "re-emit unsettled alerts after this interval")
		f.BoolVar(&noNotify, "no-notify", false, "disable notification socket subscription")
		f.Int64Var(&cursor, "seen-through", -1, "confirm a previously shown cursor before evaluation")
	case "seen":
		f.StringVar(&owner, "owner", "foreground", "delivery owner")
		f.Int64Var(&cursor, "through", -1, "confirm a previously shown cursor")
	}
	if err := f.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			fmt.Fprint(out, help)
			return 0
		}
		return fail(err)
	}
	set := map[string]bool{}
	f.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if f.NArg() != 0 || *path == "" || (face != "seen" && dir == "") || (face == "eval" && !once) || (face == "seen" && !set["through"]) {
		return fail(fmt.Errorf("invalid arguments; see --help"))
	}
	if (set["through"] || set["seen-through"]) && cursor < 0 {
		return fail(fmt.Errorf("seen cursor must be nonnegative"))
	}
	if follow && set["timeout"] {
		return fail(fmt.Errorf("--follow cannot be combined with --timeout"))
	}
	if set["timeout"] && timeout <= 0 {
		return fail(fmt.Errorf("timeout must be positive"))
	}
	if set["fallback-poll"] && fallbackPoll <= 0 {
		return fail(fmt.Errorf("fallback-poll must be positive"))
	}
	if set["unsettled-remind-after"] && unsettledRemindAfter <= 0 {
		return fail(fmt.Errorf("unsettled-remind-after must be positive"))
	}
	if set["owner"] && owner == "" {
		return fail(fmt.Errorf("owner must not be empty"))
	}
	if set["recent"] {
		recent = watch.ParseWindow(recentStr)
		if recent <= 0 {
			var parseErr error
			recent, parseErr = time.ParseDuration(recentStr)
			if parseErr != nil || recent <= 0 {
				return fail(fmt.Errorf("recent must be a positive duration"))
			}
		}
	}
	var rules []watch.Rule
	if face != "seen" {
		var err error
		rules, err = watch.LoadRules(dir)
		if err != nil {
			return fail(err)
		}
	}
	if face == "daemon" && ctx.Err() != nil {
		return 0
	}
	var err error
	storePath := *path
	if face == "wait" || face == "status" || face == "daemon" {
		// Use the underlying store for WAL wakeups and attachment evidence,
		// even if one command uses a symlink. Keep the argument for re-arm.
		storePath, err = filepath.EvalSymlinks(storePath)
		if err != nil {
			return fail(err)
		}
	}
	if face == "status" {
		if err := printStatus(ctx, watch.Evaluator{Rules: rules, Root: root}, storePath, onlyUnsettled, recent, asJSON, out, stderr); err != nil {
			return fail(err)
		}
		return 0
	}
	if face == "wait" {
		waitOpts := waitOptions{
			owner:                owner,
			storePath:            *path,
			rulesPath:            dir,
			root:                 root,
			timeout:              timeout,
			fallbackPoll:         fallbackPoll,
			unsettledRemindAfter: unsettledRemindAfter,
			follow:               follow,
			noNotify:             noNotify,
			ticks:                ticks,
			hooks:                hooks,
		}
		var deadline <-chan time.Time
		if hooks != nil && hooks.deadline != nil {
			deadline = hooks.deadline
		} else if timeout > 0 {
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			deadline = timer.C
		}
		waitOpts.deadline = deadline
		var openOpts []store.OpenOption
		var onBusy func(error)
		if hooks != nil {
			openOpts = hooks.openOpts
			onBusy = hooks.onBusy
		}
		failWaitStore := func(err error) int {
			code, wrapped := waitStoreFailure(ctx, err)
			fmt.Fprintln(stderr, "tillr watch:", wrapped)
			return code
		}
		var s *store.Store
		if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
			var err error
			s, err = store.OpenMigrated(ctx, storePath, openOpts...)
			return err
		}); timedOut {
			code, err := timeoutWait(out, waitOpts)
			if err != nil {
				return fail(err)
			}
			return code
		} else if err != nil {
			return failWaitStore(err)
		}
		defer s.Close()
		e := watch.Evaluator{Store: s, Rules: rules, Root: root}
		if hooks != nil && hooks.clock != nil {
			e.Clock = hooks.clock
		}
		if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
			return e.CheckRules(ctx)
		}); timedOut {
			code, err := timeoutWait(out, waitOpts)
			if err != nil {
				return fail(err)
			}
			return code
		} else if err != nil {
			if strings.HasPrefix(err.Error(), "rule ") {
				return fail(err)
			}
			return failWaitStore(err)
		}
		attachment, err := attach(storePath, owner)
		if err != nil {
			return fail(err)
		}
		defer attachment.Close()
		// Confirm before evaluating or printing any rule diagnostics.
		if set["seen-through"] {
			if timedOut, err := retryOnBusy(ctx, deadline, onBusy, func() error {
				return e.Seen(ctx, owner, cursor)
			}); timedOut {
				code, err := timeoutWait(out, waitOpts)
				if err != nil {
					return fail(err)
				}
				return code
			} else if err != nil {
				if strings.Contains(err.Error(), "seen cursor") {
					return fail(err)
				}
				return failWaitStore(err)
			}
		}
		code, err := wait(ctx, e, waitOpts, out, stderr)
		if err != nil {
			fmt.Fprintln(stderr, "tillr watch:", err)
			if code != 0 {
				return code
			}
			return 1
		}
		return code
	}
	s, err := store.OpenMigrated(ctx, storePath)
	if err != nil {
		if face == "daemon" && ctx.Err() != nil {
			return 0
		}
		return fail(err)
	}
	defer s.Close()
	e := watch.Evaluator{Store: s, Rules: rules, Root: root}
	if hooks != nil && hooks.clock != nil {
		e.Clock = hooks.clock
	}
	// Confirm before evaluating or printing any rule diagnostics.
	if set["through"] {
		if err := e.Seen(ctx, owner, cursor); err != nil {
			return fail(err)
		}
	}
	switch face {
	case "eval":
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			return fail(err)
		}
		var diagnostics ruleDiagnostics
		diagnostics.report(stderr, results)
		reportSkipped(stderr, results, nil)
	case "daemon":
		if err := daemon(ctx, e, stderr, ticks); err != nil {
			return fail(err)
		}
	}
	return 0
}
