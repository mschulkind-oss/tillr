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
	Use:   "orchestrator",
	Short: "Run the persona orchestrator daemon (Stage 0 / MVP)",
}

var orchestratorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the orchestrator daemon (foreground)",
	Long: `Run the orchestrator daemon. Polls the queue and dispatches up to
max-parallelism concurrent claude -p invocations per persona, capturing
each invocation's structured output and persisting side effects:

  - Append context_entry to swarf/agents/<persona>/context.md
  - Comment summary on the feature
  - File any follow_up_features
  - Transition feature status (done | blocked | needs_review)

Stop with Ctrl+C (graceful — waits for in-flight workers).`,
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
		if v, _ := cmd.Flags().GetFloat64("max-budget-usd"); v > 0 {
			base.MaxBudgetUSD = v
		}
		if v, _ := cmd.Flags().GetInt("max-turns"); v > 0 {
			base.MaxTurns = v
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
	Use:   "stop",
	Short: "Stop the orchestrator daemon (sends SIGTERM via pid file)",
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
			fmt.Printf("Orchestrator running (PID %d)\n", status.PID)
		} else {
			fmt.Println("Orchestrator NOT running.")
		}
		fmt.Printf("Config: max-parallelism=%d  poll=%s  max-budget=$%.2f  max-turns=%d  dry-run=%v\n\n",
			oc.MaxParallelism, oc.PollInterval, oc.MaxBudgetUSD, oc.MaxTurns, oc.DryRun)

		fmt.Printf("Active runs (%d):\n", len(active))
		for _, r := range active {
			fmt.Printf("  #%d  feat #%d  %s  started %s\n",
				r.ID, r.FeatureID, r.Persona, r.StartedAt.Format(time.RFC3339))
		}
		fmt.Println()

		fmt.Printf("Recent runs (%d):\n", len(recent))
		for _, r := range recent {
			cost := 0.0
			if r.CostUSD != nil {
				cost = *r.CostUSD
			}
			dur := int64(0)
			if r.DurationMS != nil {
				dur = *r.DurationMS
			}
			fmt.Printf("  #%-4d feat #%-4d %-12s %-13s $%.4f %dms %s\n",
				r.ID, r.FeatureID, r.Persona, r.Result, cost, dur, r.Summary)
		}
		return nil
	},
}

var orchestratorRunsCmd = &cobra.Command{
	Use:   "runs",
	Short: "List orchestrator runs",
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
			fmt.Printf("#%-4d feat #%-4d %-12s %-13s\n", r.ID, r.FeatureID, r.Persona, r.Result)
		}
		return nil
	},
}

func init() {
	orchestratorStartCmd.Flags().IntP("max-parallelism", "n", 0, "Max concurrent workers (default from config or 1)")
	orchestratorStartCmd.Flags().Int("poll-interval-sec", 0, "Poll interval in seconds")
	orchestratorStartCmd.Flags().Float64("max-budget-usd", 0, "Max budget per worker in USD")
	orchestratorStartCmd.Flags().Int("max-turns", 0, "Max agentic turns per worker")
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
