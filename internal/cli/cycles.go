package cli

import (
	"fmt"
	"strconv"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var cycleCmd = &cobra.Command{
	Use:   "cycle",
	Short: "Manage iteration cycles",
}

func init() {
	cycleCmd.AddCommand(cycleListCmd)
	cycleCmd.AddCommand(cycleStartCmd)
	cycleCmd.AddCommand(cycleStatusCmd)
	cycleCmd.AddCommand(cycleHistoryCmd)
	cycleCmd.AddCommand(cycleScoreCmd)

	cycleScoreCmd.Flags().String("notes", "", "Score notes")
	cycleScoreCmd.Flags().String("feature", "", "Feature ID (if not auto-detected)")
}

var cycleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available cycle types",
	RunE: func(cmd *cobra.Command, args []string) error {
		if jsonOutput {
			return printJSON(models.CycleTypes)
		}

		for _, ct := range models.CycleTypes {
			fmt.Printf("%-25s %s\n", ct.Name, ct.Description)
			fmt.Printf("  Steps: %s\n\n", joinSteps(ct.Steps))
		}
		return nil
	},
}

var cycleStartCmd = &cobra.Command{
	Use:   "start <type> <feature-id>",
	Short: "Start a cycle for a feature",
	Args:  cobra.ExactArgs(2),
	Example: `  # Start a feature implementation cycle
  lifecycle cycle start feature-implementation my-feature

  # Available cycle types: feature-implementation, ui-refinement, bug-triage,
  # documentation, architecture-review, release, roadmap-planning, onboarding-dx`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		c, err := engine.StartCycle(database, p.ID, args[1], args[0])
		if err != nil {
			return fmt.Errorf("starting %s cycle for feature %q: %w", args[0], args[1], err)
		}

		if jsonOutput {
			return printJSON(c)
		}
		fmt.Printf("✓ Started %s cycle for feature %s\n", c.CycleType, c.FeatureID)
		fmt.Printf("  Current step: %s\n", c.StepName)
		return nil
	},
}

var cycleStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show active cycles",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		cycles, err := db.ListActiveCycles(database)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(cycles)
		}

		if len(cycles) == 0 {
			fmt.Println("No active cycles.")
			return nil
		}

		for _, c := range cycles {
			stepName := getStepName(c.CycleType, c.CurrentStep)
			fmt.Printf("%-20s %-25s step %d/%d (%s)  iter %d\n",
				c.FeatureID, c.CycleType, c.CurrentStep+1,
				getTotalSteps(c.CycleType), stepName, c.Iteration)
		}
		return nil
	},
}

var cycleHistoryCmd = &cobra.Command{
	Use:   "history <feature-id>",
	Short: "Show cycle history for a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		cycles, err := db.ListCycleHistory(database, args[0])
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(cycles)
		}

		if len(cycles) == 0 {
			fmt.Printf("No cycle history for feature %s.\n", args[0])
			return nil
		}

		for _, c := range cycles {
			icon := "·"
			switch c.Status {
			case "completed":
				icon = "✓"
			case "active":
				icon = "▶"
			}
			fmt.Printf("%s %-25s iter %-3d step %d/%d  [%s]  %s\n",
				icon, c.CycleType, c.Iteration, c.CurrentStep+1,
				getTotalSteps(c.CycleType), c.Status,
				c.CreatedAt)
		}
		return nil
	},
}

var cycleScoreCmd = &cobra.Command{
	Use:   "score <score>",
	Short: "Submit a judge score for the current cycle step",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		score, err := strconv.ParseFloat(args[0], 64)
		if err != nil {
			return fmt.Errorf("invalid score %q: must be a number (e.g., 8.5)", args[0])
		}

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		featureID, _ := cmd.Flags().GetString("feature")
		notes, _ := cmd.Flags().GetString("notes")

		if featureID == "" {
			// Try to find from active work item
			w, err := db.GetActiveWorkItem(database)
			if err != nil {
				return fmt.Errorf("no active work item and no --feature specified. Use --feature <id> or start work with 'lifecycle next'")
			}
			featureID = w.FeatureID
		}

		if err := engine.ScoreCycleStep(database, p.ID, featureID, score, notes); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]any{"feature": featureID, "score": score})
		}
		fmt.Printf("✓ Scored %.1f for feature %s\n", score, featureID)
		return nil
	},
}

func joinSteps(steps []string) string {
	result := ""
	for i, s := range steps {
		if i > 0 {
			result += " → "
		}
		result += s
	}
	return result
}

func getStepName(cycleType string, step int) string {
	for _, ct := range models.CycleTypes {
		if ct.Name == cycleType && step < len(ct.Steps) {
			return ct.Steps[step]
		}
	}
	return "unknown"
}

func getTotalSteps(cycleType string) int {
	for _, ct := range models.CycleTypes {
		if ct.Name == cycleType {
			return len(ct.Steps)
		}
	}
	return 0
}
