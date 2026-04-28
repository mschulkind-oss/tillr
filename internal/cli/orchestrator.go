package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/orchestrator"
	"github.com/spf13/cobra"
)

var orchestratorCmd = &cobra.Command{
	Use:     "orchestrator",
	Aliases: []string{"orch"},
	Short:   "Run the persona orchestrator daemon (Stage 0 / MVP)",
	Long: `The orchestrator is the structural enforcement of the persona
lifecycle (Principle Zero). It polls the queue, claims pending
features by persona, spawns 'claude -p' per (persona, feature) up to
max-parallelism, and on completion automatically:

  1. Appends context_entry to swarf/agents/<persona>/context.md
  2. Comments the run summary on the feature
  3. Files any follow_up_features the agent emitted
  4. Transitions feature status (done / blocked / human-qa / error)
  5. Records cost / tokens / duration in orchestrator_runs

Persona prompts don't have to "remember" any of this — the
orchestrator owns the lifecycle. See docs/principle-zero.md.`,
}

var orchestratorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the orchestrator daemon (foreground)",
	Long: `Run the orchestrator daemon. Polls the queue and dispatches up to
max-parallelism concurrent claude -p invocations per persona, captures
each invocation's structured output, and persists side effects.

Stop with Ctrl+C (graceful — waits for in-flight workers).

Use --dry-run to smoke-test without invoking claude (uses NoopSpawn
which fakes a successful run).`,
	Example: `  # Real run, max 2 in flight
  tillr orchestrator start --max-parallelism 2

  # Smoke test — does not invoke claude
  tillr orchestrator start --dry-run --max-parallelism 2 --poll-interval-sec 2

  # Slow poll cadence for low-frequency queues
  tillr orchestrator start --max-parallelism 1 --poll-interval-sec 30`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Pidfile guard
		pidFile := orchestrator.PidFile(cfg.ProjectDir)
		if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
			return err
		}
		if pidExists(pidFile) {
			return fmt.Errorf("orchestrator pidfile %s exists; run 'tillr orchestrator stop' or delete it", pidFile)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			return err
		}
		defer os.Remove(pidFile) //nolint:errcheck

		base := orchestrator.Config{
			ProjectRoot: cfg.ProjectDir,
		}
		// CLI flag overrides
		if v, _ := cmd.Flags().GetInt("max-parallelism"); v > 0 {
			base.MaxParallelism = v
		}
		if v, _ := cmd.Flags().GetInt("poll-interval-sec"); v > 0 {
			base.PollInterval = time.Duration(v) * time.Second
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		base.DryRun = dryRun

		oc, err := orchestrator.LoadConfigFromDB(database, base)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		// Override with CLI flags after DB load (CLI > DB > defaults).
		if v, _ := cmd.Flags().GetInt("max-parallelism"); v > 0 {
			oc.MaxParallelism = v
		}
		if dryRun {
			oc.DryRun = true
		}

		var spawn orchestrator.SpawnFunc
		if oc.DryRun {
			spawn = orchestrator.NoopSpawn
		}

		daemon := orchestrator.NewDaemon(database, oc, spawn)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigs
			fmt.Fprintln(os.Stderr, "received signal, shutting down...")
			cancel()
		}()

		return daemon.Run(ctx)
	},
}

var orchestratorStopCmd = &cobra.Command{
	Use:     "stop",
	Short:   "Stop the orchestrator daemon (sends SIGTERM via pid file)",
	Example: `  tillr orchestrator stop`,
	RunE: func(_ *cobra.Command, _ []string) error {
		_, cfg, err := openDB()
		if err != nil {
			return err
		}
		pidFile := orchestrator.PidFile(cfg.ProjectDir)
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return fmt.Errorf("orchestrator not running (no pidfile at %s)", pidFile)
		}
		pid, err := strconv.Atoi(string(data))
		if err != nil {
			return fmt.Errorf("invalid pidfile contents: %v", err)
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return err
		}
		fmt.Printf("Sent SIGTERM to PID %d\n", pid)
		return nil
	},
}

var orchestratorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show orchestrator status (running? active runs? recent runs?)",
	Example: `  tillr orchestrator status
  tillr --json orchestrator status   # for monitoring / dashboards`,
	RunE: func(_ *cobra.Command, _ []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		pidFile := orchestrator.PidFile(cfg.ProjectDir)
		status := orchestrator.Status{}
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil {
				if proc, err := os.FindProcess(pid); err == nil && proc.Signal(syscall.Signal(0)) == nil {
					status.Running = true
					status.PID = pid
				}
			}
		}

		active, err := db.ActiveOrchestratorRuns(database)
		if err != nil {
			return err
		}
		status.Active = active

		recent, err := db.ListOrchestratorRuns(database, 0, 20)
		if err != nil {
			return err
		}
		status.RecentRuns = recent

		oc, _ := orchestrator.LoadConfigFromDB(database, orchestrator.Config{ProjectRoot: cfg.ProjectDir})
		status.Config = oc

		if jsonOutput {
			return printJSON(status)
		}
		if status.Running {
			fmt.Printf("%s %s\n", Success("●"), Bold(fmt.Sprintf("Orchestrator running (PID %d)", status.PID)))
		} else {
			fmt.Printf("%s %s\n", Dim("○"), Dim("Orchestrator NOT running."))
		}
		fmt.Printf("%s max-parallelism=%d  poll=%s  dry-run=%v\n\n",
			Header("Config:"),
			oc.MaxParallelism, oc.PollInterval, oc.DryRun)

		fmt.Printf("%s %s\n",
			Header(fmt.Sprintf("Active runs (%d):", len(active))),
			Dim("(in flight right now)"))
		for _, r := range active {
			fmt.Printf("  %s  feat %s  %s  started %s\n",
				Code(fmt.Sprintf("#%d", r.ID)),
				Code(fmt.Sprintf("#%d", r.FeatureID)),
				Persona(r.Persona),
				Dim(r.StartedAt.Format(time.RFC3339)))
		}
		if len(active) == 0 {
			fmt.Println("  " + Dim("(none)"))
		}
		fmt.Println()

		fmt.Printf("%s\n", Header(fmt.Sprintf("Recent runs (%d):", len(recent))))
		for _, r := range recent {
			cost := 0.0
			if r.CostUSD != nil {
				cost = *r.CostUSD
			}
			dur := int64(0)
			if r.DurationMS != nil {
				dur = *r.DurationMS
			}
			fmt.Printf("  %s feat %s %-12s %s %s %s %s\n",
				Code(fmt.Sprintf("#%-4d", r.ID)),
				Code(fmt.Sprintf("#%-4d", r.FeatureID)),
				Persona(r.Persona),
				Status(fmt.Sprintf("%-13s", r.Result)),
				Money(cost),
				Duration(dur),
				Dim(r.Summary))
		}
		return nil
	},
}

var orchestratorRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List orchestrator runs (newest first)",
	Long: `Each run row records one (persona, feature) dispatch with its cost,
tokens, duration, exit code, session ID, and result.`,
	Example: `  tillr orchestrator runs
  tillr orchestrator runs --feature 4
  tillr orchestrator runs --limit 5
  tillr --json orchestrator runs`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID, _ := cmd.Flags().GetInt64("feature")
		limit, _ := cmd.Flags().GetInt("limit")
		runs, err := db.ListOrchestratorRuns(database, featureID, limit)
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(runs)
		}
		for _, r := range runs {
			cost := 0.0
			if r.CostUSD != nil {
				cost = *r.CostUSD
			}
			dur := int64(0)
			if r.DurationMS != nil {
				dur = *r.DurationMS
			}
			fmt.Printf("%s feat %s %-12s %s %s %s\n",
				Code(fmt.Sprintf("#%-4d", r.ID)),
				Code(fmt.Sprintf("#%-4d", r.FeatureID)),
				Persona(r.Persona),
				Status(fmt.Sprintf("%-13s", r.Result)),
				Money(cost),
				Duration(dur))
		}
		return nil
	},
}

func init() {
	orchestratorStartCmd.Flags().IntP("max-parallelism", "n", 0, "Max concurrent workers (default from config or 1)")
	orchestratorStartCmd.Flags().Int("poll-interval-sec", 0, "Poll interval in seconds")
	orchestratorStartCmd.Flags().Bool("dry-run", false,
		"Use the no-op spawner (does not invoke claude) — for smoke testing")

	orchestratorRunsCmd.Flags().Int64P("feature", "f", 0, "Filter by feature ID (0 = all)")
	orchestratorRunsCmd.Flags().Int("limit", 50, "Max runs to show")

	orchestratorCmd.AddCommand(orchestratorStartCmd)
	orchestratorCmd.AddCommand(orchestratorStopCmd)
	orchestratorCmd.AddCommand(orchestratorStatusCmd)
	orchestratorCmd.AddCommand(orchestratorRunsCmd)
	rootCmd.AddCommand(orchestratorCmd)
}

func pidExists(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Sending signal 0 is the standard "is the process alive?" check.
	return proc.Signal(syscall.Signal(0)) == nil
}
