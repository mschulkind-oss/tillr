// Command tillr-watch observes events and reports alert episodes without applying remedies.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	// Match tillr-store's producer registry. Linking the drain package registers
	// its event kinds; no drain command or loop runs in this process.
	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

const help = `Usage:
  tillr watch eval --once --store PATH --rules DIR [--root DIR]
  tillr watch status --store PATH --rules DIR [--root DIR] [--json]
  tillr watch daemon --store PATH --rules DIR [--root DIR]
  tillr watch wait --store PATH --rules DIR [--root DIR] [--owner ID] [--timeout DUR | --follow] [--fallback-poll DUR] [--no-notify] [--seen-through SEQ]
  tillr watch seen --store PATH --through SEQ [--owner ID]
  tillr watch ack ID --store PATH --by ACTOR [--note TEXT]
  tillr watch track ID --store PATH --item ITEM_ID [--by ACTOR] [--note TEXT]
  tillr watch silence ID --store PATH (--until DUR_OR_TIME | --until-change) [--reason TEXT]
  tillr watch resolve ID --store PATH --reason TEXT
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
	fail := func(err error) int { fmt.Fprintln(stderr, "tillr watch:", err); return 1 }
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, help)
		return 0
	}
	face := args[0]
	switch face {
	case "ack", "track", "silence", "resolve":
		if err := runEpisode(ctx, face, args[1:], out); err != nil {
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
	var dir, owner, root string
	var timeout, fallbackPoll time.Duration
	var cursor int64
	var once, follow, noNotify, asJSON bool
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
	case "wait":
		f.StringVar(&owner, "owner", "foreground", "delivery owner")
		f.DurationVar(&timeout, "timeout", 0, "time to wait for a notice")
		f.BoolVar(&follow, "follow", false, "remain attached after notices")
		f.DurationVar(&fallbackPoll, "fallback-poll", defaultFallbackPoll, "fallback poll interval")
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
	if set["owner"] && owner == "" {
		return fail(fmt.Errorf("owner must not be empty"))
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
		if err := printStatus(ctx, watch.Evaluator{Rules: rules, Root: root}, storePath, asJSON, out, stderr); err != nil {
			return fail(err)
		}
		return 0
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
	if face == "wait" {
		if err := e.CheckRules(ctx); err != nil {
			return fail(err)
		}
		attachment, err := attach(storePath, owner)
		if err != nil {
			return fail(err)
		}
		defer attachment.Close()
	}
	// Confirm before evaluating or printing any rule diagnostics.
	if set["through"] || set["seen-through"] {
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
	case "wait":
		code, err := wait(ctx, e, waitOptions{owner: owner, storePath: *path, rulesPath: dir, root: root, timeout: timeout, fallbackPoll: fallbackPoll, follow: follow, noNotify: noNotify, ticks: ticks}, out, stderr)
		if err != nil {
			return fail(err)
		}
		return code
	}
	return 0
}
