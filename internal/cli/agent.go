package cli

import (
	"fmt"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

func init() {
	nextCmd.Flags().String("cycle", "", "Filter by cycle type")
	doneCmd.Flags().String("result", "", "Work result description")
	failCmd.Flags().String("reason", "", "Failure reason")
	heartbeatCmd.Flags().String("message", "", "Status message")
}

var nextCmd = &cobra.Command{
	Use:   "next",
	Short: "Get the next work item for an agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		w, err := engine.GetNextWorkItem(database)
		if err != nil {
			if jsonOutput {
				return printJSON(map[string]string{"status": "no_work"})
			}
			fmt.Println("No work items available.")
			return nil
		}

		if jsonOutput {
			// Return enriched context — everything an agent needs in one payload
			ctx, ctxErr := engine.GetWorkContext(database, w)
			if ctxErr == nil {
				return printJSON(ctx)
			}
			// Fall back to bare work item if context building fails
			return printJSON(w)
		}

		fmt.Printf("Work Item #%d\n", w.ID)
		fmt.Printf("  Feature: %s\n", w.FeatureID)
		fmt.Printf("  Type:    %s\n", w.WorkType)
		if w.AgentPrompt != "" {
			fmt.Printf("  Prompt:  %s\n", w.AgentPrompt)
		}
		return nil
	},
}

var doneCmd = &cobra.Command{
	Use:   "done",
	Short: "Mark current work item as complete",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		result, _ := cmd.Flags().GetString("result")
		if err := engine.CompleteWorkItem(database, result); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "done"})
		}
		fmt.Println("✓ Work item marked as done.")
		return nil
	},
}

var failCmd = &cobra.Command{
	Use:   "fail",
	Short: "Mark current work item as failed",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		reason, _ := cmd.Flags().GetString("reason")
		if err := engine.FailWorkItem(database, reason); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "failed"})
		}
		fmt.Println("✗ Work item marked as failed.")
		return nil
	},
}

var heartbeatCmd = &cobra.Command{
	Use:   "heartbeat",
	Short: "Agent heartbeat signal",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		message, _ := cmd.Flags().GetString("message")

		w, err := db.GetActiveWorkItem(database)
		if err != nil {
			if jsonOutput {
				return printJSON(map[string]string{"status": "no_active_work"})
			}
			fmt.Println("No active work item.")
			return nil
		}

		if err := db.CreateHeartbeat(database, &models.Heartbeat{
			FeatureID: w.FeatureID,
			Message:   message,
		}); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "ok", "feature": w.FeatureID})
		}
		fmt.Println("♥ Heartbeat recorded.")
		return nil
	},
}
