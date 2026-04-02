package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind/tillr/internal/config"
	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/mschulkind/tillr/internal/server"
	"github.com/spf13/cobra"
)

var jsonOutput bool
var cmdStartTime time.Time

var rootCmd = &cobra.Command{
	Use:   "tillr",
	Short: "Human-in-the-loop project management for agentic development",
	Long: `Tillr is a project management tool that bridges human product owners
and AI agents. It tracks, visualizes, and steers work as it flows through
defined iteration cycles — acting as the project manager for agentic development.

QUICK START
  tillr onboard                  Onboard an existing project (recommended)
  tillr init my-project          Create a new project from scratch
  tillr doctor                   Check environment health
  tillr status                   Project overview dashboard

AGENT WORKFLOW
  tillr next --json              Get next work item (returns full context)
  tillr done --result "..."      Complete current work
  tillr fail --reason "..."      Report failure
  tillr heartbeat                Signal agent is alive

FEATURES
  tillr feature add "Name"       Create a feature (--spec, --priority, --milestone)
  tillr feature list             List all features (--status, --milestone)
  tillr feature show <id>        Feature details with full history
  tillr feature edit <id>        Update feature properties
  tillr feature remove <id>      Remove a feature

ITERATION CYCLES
  tillr cycle list               Show available cycle types
  tillr cycle start <type> <id>  Start a cycle for a feature
  tillr cycle status             View active cycle progress
  tillr cycle history <id>       Cycle history for a feature
  tillr cycle score 8.5          Submit judge score

QA
  tillr qa pending               Features awaiting QA
  tillr qa approve <feature>     Approve feature (--notes)
  tillr qa reject <feature>      Reject → back to development

ROADMAP
  tillr roadmap show             View roadmap (--format table|json|markdown)
  tillr roadmap add "Title"      Add item (--priority, --category, --effort)
  tillr roadmap edit <id>        Update roadmap item
  tillr roadmap prioritize       Interactive prioritization
  tillr roadmap export           Export roadmap (--format md|json)

MILESTONES
  tillr milestone add "Name"     Create a milestone
  tillr milestone list           List milestones with progress
  tillr milestone show <id>      Milestone details

COLLABORATION
  tillr discuss new "RFC: ..."   Start a discussion
  tillr discuss list             List discussions
  tillr discuss comment <id>     Add to discussion
  tillr discuss resolve <id>     Resolve a discussion

HISTORY & SEARCH
  tillr history                  Event history (--feature, --since, --type)
  tillr search <query>           Full-text search across project data
  tillr log                      Compact activity log

WEB VIEWER
  tillr serve                    Start web dashboard at :3847

Use "tillr [command] --help" for detailed information about any command.
Use "tillr --json" on any command for structured output (critical for agents).`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		cliErr, ok := err.(*CLIError)
		if ok {
			formatError(cliErr)
			os.Exit(cliErr.ExitCode)
		}
		// Wrap standard errors with hints
		hint := hintForError(err)
		if jsonOutput {
			out := map[string]string{"error": err.Error()}
			if hint != "" {
				out["hint"] = hint
			}
			data, _ := json.MarshalIndent(out, "", "  ")
			fmt.Fprintln(os.Stderr, string(data))
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			if hint != "" {
				fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
			}
		}
		os.Exit(ExitUserError)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	// Record command execution timing for perf metrics.
	rootCmd.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		cmdStartTime = time.Now()
	}
	rootCmd.PersistentPostRun = func(cmd *cobra.Command, _ []string) {
		recordCommandMetric(cmd, true)
	}

	// Wire up webhook dispatch so every InsertEvent triggers matching webhooks.
	db.WebhookDispatchFunc = func(database *sql.DB, event *models.Event) {
		server.DispatchWebhooks(database, event)
	}

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(nextCmd)
	rootCmd.AddCommand(doneCmd)
	rootCmd.AddCommand(failCmd)
	rootCmd.AddCommand(heartbeatCmd)
	rootCmd.AddCommand(advanceCmd)
	rootCmd.AddCommand(featureCmd)
	rootCmd.AddCommand(milestoneCmd)
	rootCmd.AddCommand(roadmapCmd)
	rootCmd.AddCommand(cycleCmd)
	rootCmd.AddCommand(qaCmd)
	rootCmd.AddCommand(historyCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(discussCmd)
	rootCmd.AddCommand(onboardCmd)
	rootCmd.AddCommand(completionCmd)
	rootCmd.AddCommand(agentCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(ideaCmd)
	rootCmd.AddCommand(bugCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(worktreeCmd)
	rootCmd.AddCommand(gitCmd)
	rootCmd.AddCommand(queueCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(decisionCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(releaseNotesCmd)
	rootCmd.AddCommand(timeCmd)
	rootCmd.AddCommand(webhookCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(sprintCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(tagCmd)
	rootCmd.AddCommand(auditCmd)
	rootCmd.AddCommand(analyticsCmd)
	rootCmd.AddCommand(perfCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(undoCmd)
	rootCmd.AddCommand(redoCmd)
	rootCmd.AddCommand(encryptCmd)
	rootCmd.AddCommand(projectCmd)
	rootCmd.AddCommand(dashboardCmd)
	rootCmd.AddCommand(pluginCmd)
	rootCmd.AddCommand(interactiveCmd)
	rootCmd.AddCommand(hooksCmd)
	rootCmd.AddCommand(apiCmd)
	rootCmd.AddCommand(syncAgentsCmd)
	rootCmd.AddCommand(notificationsCmd)
	rootCmd.AddCommand(templateCmd)
	rootCmd.AddCommand(apiKeyCmd)
	rootCmd.AddCommand(workstreamCmd)
	rootCmd.AddCommand(daemonCmd)

	// Short aliases for common commands (CLI Aliases roadmap item)
	rootCmd.AddCommand(aliasCmd("f", featureCmd, "Alias for 'feature'"))
	rootCmd.AddCommand(aliasCmd("m", milestoneCmd, "Alias for 'milestone'"))
	rootCmd.AddCommand(aliasCmd("r", roadmapCmd, "Alias for 'roadmap'"))
	rootCmd.AddCommand(aliasCmd("c", cycleCmd, "Alias for 'cycle'"))
	rootCmd.AddCommand(aliasCmd("d", discussCmd, "Alias for 'discuss'"))
	rootCmd.AddCommand(aliasCmd("q", qaCmd, "Alias for 'qa'"))
	rootCmd.AddCommand(aliasCmd("s", searchCmd, "Alias for 'search'"))
	rootCmd.AddCommand(aliasCmd("sp", sprintCmd, "Alias for 'sprint'"))
}

func aliasCmd(name string, target *cobra.Command, short string) *cobra.Command {
	alias := *target
	alias.Use = name
	alias.Short = short
	alias.Hidden = true
	alias.Aliases = nil
	return &alias
}

func openDB() (*sql.DB, *config.Config, error) {
	root, err := config.FindProjectRoot()
	if err != nil {
		return nil, nil, userError("no tillr project found", nil, "Run 'tillr init <name>' to create a new project, or 'tillr onboard' to onboard an existing one.")
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}

	// If an active project is configured, validate it exists.
	if cfg.ActiveProject != "" {
		if _, err := db.GetProjectByID(database, cfg.ActiveProject); err != nil {
			database.Close() //nolint:errcheck
			return nil, nil, fmt.Errorf("active project %q not found. Run 'tillr project list' to see available projects", cfg.ActiveProject)
		}
	}

	return database, cfg, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// commandPath returns the full subcommand path (e.g. "feature add").
func commandPath(cmd *cobra.Command) string {
	var parts []string
	root := cmd.Root()
	for c := cmd; c != nil && c != root; c = c.Parent() {
		parts = append([]string{c.Name()}, parts...)
	}
	return strings.Join(parts, " ")
}

// recordCommandMetric silently records command timing to the metrics table.
// Failures are swallowed to avoid disrupting the user's workflow.
func recordCommandMetric(cmd *cobra.Command, success bool) {
	if cmdStartTime.IsZero() {
		return
	}
	durationMs := float64(time.Since(cmdStartTime).Microseconds()) / 1000.0
	name := commandPath(cmd)
	if name == "" || name == "perf show" {
		return
	}

	database, _, err := openDB()
	if err != nil {
		return
	}
	defer database.Close() //nolint:errcheck
	_ = db.InsertCommandMetric(database, name, durationMs, success, 0)
}
