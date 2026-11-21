// Command tillr-watch observes events and reports alert episodes; it never acts on them.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	// Match tillr-store's producer registry. Linking the drain package registers
	// its event kinds; no drain command or loop runs in this process.
	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

const help = `Usage:
  tillr watch eval --once --store PATH --rules DIR
  tillr watch status --store PATH --rules DIR
  tillr watch wait --store PATH --rules DIR [--owner ID] [--timeout DUR | --follow] [--seen-through SEQ]
  tillr watch seen --store PATH --through SEQ [--owner ID]

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

// ticks overrides the fallback clock in tests; production uses a 60-second ticker.
func runWithTicks(ctx context.Context, args []string, out, stderr io.Writer, ticks <-chan time.Time) int {
	fail := func(err error) int { fmt.Fprintln(stderr, "tillr watch:", err); return 1 }
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, help)
		return 0
	}
	face := args[0]
	f := flag.NewFlagSet(face, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	path := f.String("store", "", "path to the tillr state database")
	var dir, owner string
	var timeout time.Duration
	var cursor int64
	var once, follow bool
	switch face {
	case "eval", "wait", "status":
		f.StringVar(&dir, "rules", "", "directory of TOML rules")
	case "seen":
	default:
		return fail(fmt.Errorf("unknown face %q", face))
	}
	switch face {
	case "eval":
		f.BoolVar(&once, "once", false, "evaluate once")
	case "wait":
		f.StringVar(&owner, "owner", "foreground", "delivery owner")
		f.DurationVar(&timeout, "timeout", 0, "time to wait for a notice")
		f.BoolVar(&follow, "follow", false, "remain attached after notices")
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
	var s *store.Store
	var err error
	if face == "status" {
		s, err = store.OpenRead(ctx, *path)
	} else {
		storePath := *path
		if face == "wait" {
			// SQLite writes beside the underlying database, not beside a link.
			// Open and watch the same target; preserve the argument for re-arm.
			storePath, err = filepath.EvalSymlinks(storePath)
			if err != nil {
				return fail(err)
			}
		}
		s, err = store.OpenMigrated(ctx, storePath)
	}
	if err != nil {
		return fail(err)
	}
	defer s.Close()
	e := watch.Evaluator{Store: s, Rules: rules}
	// Confirm before evaluating or printing any rule diagnostics.
	if set["through"] || set["seen-through"] {
		if err := e.Seen(ctx, owner, cursor); err != nil {
			return fail(err)
		}
	}
	for _, r := range rules {
		if reason := r.UnevaluatedReason(); reason != "" {
			fmt.Fprintf(stderr, "tillr watch: rule %s not evaluated: %s\n", r.ID, reason)
		}
	}
	switch face {
	case "eval":
		counts, err := e.Evaluate(ctx)
		if err != nil {
			return fail(err)
		}
		for _, r := range rules {
			if n := counts[r.ID]; n > 0 {
				fmt.Fprintf(stderr, "tillr watch: rule %s skipped %d events: group_by %s missing or not a string\n", r.ID, n, r.When.GroupBy)
			}
		}
	case "status":
		counts, err := e.Skipped(ctx)
		if err != nil {
			return fail(err)
		}
		for _, id := range slices.Sorted(maps.Keys(counts)) {
			fmt.Fprintf(out, "rule %s skipped=%d\n", id, counts[id])
		}
	case "wait":
		code, err := wait(ctx, e, waitOptions{owner: owner, storePath: *path, rulesPath: dir, timeout: timeout, follow: follow, ticks: ticks}, out)
		if err != nil {
			return fail(err)
		}
		return code
	}
	return 0
}
