package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var hooksCmd = &cobra.Command{
	Use:   "hooks",
	Short: "Show available hook points for agent/CLI integration",
	Long: `Lists all hook points where agents (Copilot CLI, Claude Code, etc.)
can integrate with tillr.

Each hook shows the command, description, and example usage.
Use --json for structured output suitable for agent consumption.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		hooks := []hookInfo{
			{
				Name:        "work-intake",
				Command:     "tillr next --json",
				Description: "Get the next work item with full context (feature, spec, cycle state, guidance)",
				Phase:       "start",
				Example:     `WORK=$(tillr next --json); echo "$WORK" | jq '.agent_guidance'`,
			},
			{
				Name:        "work-complete",
				Command:     "tillr done --result '...'",
				Description: "Mark current work item as complete with result description",
				Phase:       "end",
				Example:     `tillr done --result "Implemented login page with OAuth2 support"`,
			},
			{
				Name:        "work-fail",
				Command:     "tillr fail --reason '...'",
				Description: "Report work item failure with reason",
				Phase:       "end",
				Example:     `tillr fail --reason "Build failed: missing libssl dependency"`,
			},
			{
				Name:        "heartbeat",
				Command:     "tillr heartbeat --message '...'",
				Description: "Send keep-alive signal during long-running tasks",
				Phase:       "during",
				Example:     `tillr heartbeat --message "Running test suite (75% complete)"`,
			},
			{
				Name:        "idea-submit",
				Command:     "tillr idea submit '<title>' --description '...'",
				Description: "Submit a new idea/feature request/bug report to the queue",
				Phase:       "any",
				Example:     `tillr idea submit "Add dark mode" --description "Users requested dark mode" --type feature`,
			},
			{
				Name:        "idea-process",
				Command:     "tillr idea process [id]",
				Description: "Auto-categorize and create features from pending ideas (no approval gate)",
				Phase:       "any",
				Example:     `tillr idea process  # Process all pending ideas`,
			},
			{
				Name:        "status-check",
				Command:     "tillr status --json",
				Description: "Get project overview with feature counts, active work, and recent events",
				Phase:       "any",
				Example:     `tillr status --json | jq '.feature_counts'`,
			},
			{
				Name:        "feature-query",
				Command:     "tillr feature show <id> --json",
				Description: "Get full feature details including spec, dependencies, and history",
				Phase:       "any",
				Example:     `tillr feature show my-feature --json | jq '.spec'`,
			},
			{
				Name:        "search",
				Command:     "tillr search '<query>' --json",
				Description: "Full-text search across features, roadmap, ideas, and discussions",
				Phase:       "any",
				Example:     `tillr search "authentication" --json`,
			},
			{
				Name:        "cycle-start",
				Command:     "tillr cycle start <type> <feature-id>",
				Description: "Start an iteration cycle (feature-implementation, bug-triage, etc.)",
				Phase:       "start",
				Example:     `tillr cycle start feature-implementation my-feature`,
			},
			{
				Name:        "qa-submit",
				Command:     "tillr qa approve|reject <feature-id>",
				Description: "Submit QA results for features in human-qa status",
				Phase:       "end",
				Example:     `tillr qa approve my-feature --notes "Looks good, all tests pass"`,
			},
			{
				Name:        "discuss",
				Command:     "tillr discuss new '<title>' --body '...'",
				Description: "Start an RFC/discussion thread for collaborative decision-making",
				Phase:       "any",
				Example:     `tillr discuss new "RFC: API versioning" --body "Proposal..."`,
			},
		}

		if jsonOutput {
			return printJSON(hooks)
		}

		fmt.Println("Available Hook Points")
		fmt.Println(strings.Repeat("=", 60))
		fmt.Println()

		for _, h := range hooks {
			fmt.Printf("  %s [%s]\n", h.Name, h.Phase)
			fmt.Printf("    %s\n", h.Description)
			fmt.Printf("    Command: %s\n", h.Command)
			fmt.Printf("    Example: %s\n", h.Example)
			fmt.Println()
		}

		fmt.Println("For detailed integration guide, see: docs/guides/copilot-integration.md")
		return nil
	},
}

type hookInfo struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Phase       string `json:"phase"` // start, during, end, any
	Example     string `json:"example"`
}
