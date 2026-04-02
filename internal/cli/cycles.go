package cli

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/engine"
	"github.com/mschulkind-oss/tillr/internal/models"
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
	cycleCmd.AddCommand(cycleAdvanceCmd)

	cycleScoreCmd.Flags().String("notes", "", "Score notes")
	cycleScoreCmd.Flags().String("feature", "", "Feature ID (if not auto-detected)")

	cycleAdvanceCmd.Flags().String("feature", "", "Feature ID (required)")
	cycleAdvanceCmd.Flags().Bool("approve", false, "Approve the current human step and advance")
	cycleAdvanceCmd.Flags().Bool("reject", false, "Reject the current human step (stays on step)")
	cycleAdvanceCmd.Flags().String("notes", "", "Notes for the approval/rejection")
	_ = cycleAdvanceCmd.MarkFlagRequired("feature")
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
  tillr cycle start feature-implementation my-feature

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
		fmt.Printf("✓ Started %s cycle for feature %s\n", c.CycleType, c.EntityID)
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
				c.EntityID, c.CycleType, c.CurrentStep+1,
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
				return fmt.Errorf("no active work item and no --feature specified. Use --feature <id> or start work with 'tillr next'")
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

func joinSteps(steps []models.CycleStep) string {
	result := ""
	for i, s := range steps {
		if i > 0 {
			result += " → "
		}
		label := s.Name
		if s.Human {
			label += " (human)"
		}
		result += label
	}
	return result
}

func getStepName(cycleType string, step int) string {
	for _, ct := range models.CycleTypes {
		if ct.Name == cycleType && step < len(ct.Steps) {
			return ct.Steps[step].Name
		}
	}
	// Check custom templates in DB.
	if database, _, err := openDB(); err == nil {
		defer database.Close() //nolint:errcheck
		if t, err := db.GetCycleTemplate(database, cycleType); err == nil && step < len(t.Steps) {
			return t.Steps[step].Name
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
	// Check custom templates in DB.
	if database, _, err := openDB(); err == nil {
		defer database.Close() //nolint:errcheck
		if t, err := db.GetCycleTemplate(database, cycleType); err == nil {
			return len(t.Steps)
		}
	}
	return 0
}

var cycleAdvanceCmd = &cobra.Command{
	Use:   "advance",
	Short: "Manually advance a human-owned cycle step",
	Long: `Advance (approve) or reject the current human-owned cycle step.
Only human-owned steps can be advanced manually; agent steps must be
completed through the normal agent workflow.`,
	Example: `  # Approve the current human step
  tillr cycle advance --feature my-feature --approve --notes "Looks good"

  # Reject the current human step
  tillr cycle advance --feature my-feature --reject --notes "Needs more work on error handling"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID, _ := cmd.Flags().GetString("feature")
		approve, _ := cmd.Flags().GetBool("approve")
		reject, _ := cmd.Flags().GetBool("reject")
		notes, _ := cmd.Flags().GetString("notes")

		if !approve && !reject {
			return fmt.Errorf("specify --approve or --reject")
		}
		if approve && reject {
			return fmt.Errorf("cannot specify both --approve and --reject")
		}

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		c, err := db.GetActiveCycle(database, featureID)
		if err != nil {
			return fmt.Errorf("no active cycle for feature %s", featureID)
		}

		ct := findCycleType(database, c.CycleType)
		if ct == nil {
			return fmt.Errorf("unknown cycle type: %s", c.CycleType)
		}

		if c.CurrentStep >= len(ct.Steps) {
			return fmt.Errorf("cycle is beyond its defined steps")
		}

		if !ct.IsHumanStep(c.CurrentStep) {
			return fmt.Errorf("current step %q is not human-owned; use the agent workflow to advance it", ct.Steps[c.CurrentStep].Name)
		}

		stepName := ct.Steps[c.CurrentStep].Name

		if reject {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: featureID,
				EventType: "cycle.step.rejected",
				Data:      fmt.Sprintf(`{"step":%q,"notes":%q}`, stepName, notes),
			})

			if jsonOutput {
				return printJSON(map[string]any{
					"feature": featureID,
					"step":    stepName,
					"action":  "rejected",
					"notes":   notes,
				})
			}
			fmt.Printf("Rejected step %q for feature %s (staying on current step)\n", stepName, featureID)
			if notes != "" {
				fmt.Printf("  Notes: %s\n", notes)
			}
			return nil
		}

		// Approve: advance to next step or complete
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: featureID,
			EventType: "cycle.step.approved",
			Data:      fmt.Sprintf(`{"step":%q,"notes":%q}`, stepName, notes),
		})

		nextStep := c.CurrentStep + 1
		if nextStep >= len(ct.Steps) {
			// Complete the cycle
			if err := db.UpdateCycleInstance(database, c.ID, c.CurrentStep, c.Iteration, "completed"); err != nil {
				return fmt.Errorf("completing cycle: %w", err)
			}

			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: featureID,
				EventType: "cycle.completed",
				Data:      fmt.Sprintf(`{"cycle_type":%q}`, c.CycleType),
			})

			if jsonOutput {
				return printJSON(map[string]any{
					"feature": featureID,
					"step":    stepName,
					"action":  "approved",
					"result":  "completed",
				})
			}
			fmt.Printf("Approved step %q for feature %s - cycle completed!\n", stepName, featureID)
			return nil
		}

		// Advance to next step
		if err := db.UpdateCycleInstance(database, c.ID, nextStep, c.Iteration, "active"); err != nil {
			return fmt.Errorf("advancing cycle: %w", err)
		}

		nextStepName := ct.Steps[nextStep].Name

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: featureID,
			EventType: "cycle.advanced",
			Data:      fmt.Sprintf(`{"from":%q,"to":%q}`, stepName, nextStepName),
		})

		// Create work item for agent steps
		if !ct.Steps[nextStep].Human {
			_ = db.CreateWorkItem(database, &models.WorkItem{
				FeatureID: featureID,
				WorkType:  nextStepName,
			})
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"feature":   featureID,
				"step":      stepName,
				"action":    "approved",
				"next_step": nextStepName,
			})
		}
		fmt.Printf("Approved step %q for feature %s -> now at %q\n", stepName, featureID, nextStepName)
		return nil
	},
}

// findCycleType resolves a cycle type by name, checking built-in types first,
// then custom templates in the DB.
func findCycleType(database *sql.DB, name string) *models.CycleType {
	for i := range models.CycleTypes {
		if models.CycleTypes[i].Name == name {
			return &models.CycleTypes[i]
		}
	}
	if t, err := db.GetCycleTemplate(database, name); err == nil && t != nil {
		return &models.CycleType{
			Name:        t.Name,
			Description: t.Description,
			Steps:       t.Steps,
		}
	}
	return nil
}
