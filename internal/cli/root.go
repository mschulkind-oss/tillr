package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/mschulkind/lifecycle/internal/server"
	"github.com/spf13/cobra"
)

var jsonOutput bool

var rootCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Human-in-the-loop project management for agentic development",
	Long: `Lifecycle is a project management tool that bridges human product owners
and AI agents. It tracks, visualizes, and steers work as it flows through
defined iteration cycles — acting as the project manager for agentic development.

QUICK START
  lifecycle onboard                  Onboard an existing project (recommended)
  lifecycle init my-project          Create a new project from scratch
  lifecycle doctor                   Check environment health
  lifecycle status                   Project overview dashboard

AGENT WORKFLOW
  lifecycle next --json              Get next work item (returns full context)
  lifecycle done --result "..."      Complete current work
  lifecycle fail --reason "..."      Report failure
  lifecycle heartbeat                Signal agent is alive

FEATURES
  lifecycle feature add "Name"       Create a feature (--spec, --priority, --milestone)
  lifecycle feature list             List all features (--status, --milestone)
  lifecycle feature show <id>        Feature details with full history
  lifecycle feature edit <id>        Update feature properties
  lifecycle feature remove <id>      Remove a feature

ITERATION CYCLES
  lifecycle cycle list               Show available cycle types
  lifecycle cycle start <type> <id>  Start a cycle for a feature
  lifecycle cycle status             View active cycle progress
  lifecycle cycle history <id>       Cycle history for a feature
  lifecycle cycle score 8.5          Submit judge score

QA
  lifecycle qa pending               Features awaiting QA
  lifecycle qa approve <feature>     Approve feature (--notes)
  lifecycle qa reject <feature>      Reject → back to development

ROADMAP
  lifecycle roadmap show             View roadmap (--format table|json|markdown)
  lifecycle roadmap add "Title"      Add item (--priority, --category, --effort)
  lifecycle roadmap edit <id>        Update roadmap item
  lifecycle roadmap prioritize       Interactive prioritization
  lifecycle roadmap export           Export roadmap (--format md|json)

MILESTONES
  lifecycle milestone add "Name"     Create a milestone
  lifecycle milestone list           List milestones with progress
  lifecycle milestone show <id>      Milestone details

COLLABORATION
  lifecycle discuss new "RFC: ..."   Start a discussion
  lifecycle discuss list             List discussions
  lifecycle discuss comment <id>     Add to discussion
  lifecycle discuss resolve <id>     Resolve a discussion

HISTORY & SEARCH
  lifecycle history                  Event history (--feature, --since, --type)
  lifecycle search <query>           Full-text search across project data
  lifecycle log                      Compact activity log

WEB VIEWER
  lifecycle serve                    Start web dashboard at :3847

Use "lifecycle [command] --help" for detailed information about any command.
Use "lifecycle --json" on any command for structured output (critical for agents).`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

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

	// Short aliases for common commands (CLI Aliases roadmap item)
	rootCmd.AddCommand(aliasCmd("f", featureCmd, "Alias for 'feature'"))
	rootCmd.AddCommand(aliasCmd("m", milestoneCmd, "Alias for 'milestone'"))
	rootCmd.AddCommand(aliasCmd("r", roadmapCmd, "Alias for 'roadmap'"))
	rootCmd.AddCommand(aliasCmd("c", cycleCmd, "Alias for 'cycle'"))
	rootCmd.AddCommand(aliasCmd("d", discussCmd, "Alias for 'discuss'"))
	rootCmd.AddCommand(aliasCmd("q", qaCmd, "Alias for 'qa'"))
	rootCmd.AddCommand(aliasCmd("s", searchCmd, "Alias for 'search'"))
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
		return nil, nil, fmt.Errorf("no lifecycle project found. Run 'lifecycle init <name>' first")
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, nil, fmt.Errorf("loading config: %w", err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("opening database: %w", err)
	}
	return database, cfg, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
