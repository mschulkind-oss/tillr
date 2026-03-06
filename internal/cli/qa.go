package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/spf13/cobra"
)

var qaCmd = &cobra.Command{
	Use:   "qa",
	Short: "Quality assurance commands",
}

func init() {
	qaCmd.AddCommand(qaPendingCmd)
	qaCmd.AddCommand(qaApproveCmd)
	qaCmd.AddCommand(qaRejectCmd)
	qaCmd.AddCommand(qaChecklistCmd)

	qaApproveCmd.Flags().String("notes", "", "Approval notes")
	qaRejectCmd.Flags().String("notes", "", "Rejection notes")
}

var qaPendingCmd = &cobra.Command{
	Use:   "pending",
	Short: "List features awaiting QA",
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

		features, err := db.PendingQAFeatures(database, p.ID)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(features)
		}

		if len(features) == 0 {
			fmt.Println("No features pending QA.")
			return nil
		}

		fmt.Printf("%-20s %-4s %s\n", "ID", "PRI", "NAME")
		fmt.Println(strings.Repeat("─", 50))
		for _, f := range features {
			fmt.Printf("%-20s %-4d %s\n", f.ID, f.Priority, f.Name)
		}
		return nil
	},
}

var qaApproveCmd = &cobra.Command{
	Use:   "approve <feature-id>",
	Short: "Approve a feature",
	Args:  cobra.ExactArgs(1),
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

		notes, _ := cmd.Flags().GetString("notes")
		if err := engine.ApproveFeatureQA(database, p.ID, args[0], notes); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "approved", "feature": args[0]})
		}
		fmt.Printf("✓ Approved feature %s → done\n", args[0])
		return nil
	},
}

var qaRejectCmd = &cobra.Command{
	Use:   "reject <feature-id>",
	Short: "Reject a feature",
	Args:  cobra.ExactArgs(1),
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

		notes, _ := cmd.Flags().GetString("notes")
		if err := engine.RejectFeatureQA(database, p.ID, args[0], notes); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "rejected", "feature": args[0]})
		}
		fmt.Printf("✗ Rejected feature %s → back to implementing\n", args[0])
		return nil
	},
}

var qaChecklistCmd = &cobra.Command{
	Use:   "checklist <feature-id>",
	Short: "Show QA checklist for a feature",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		f, err := db.GetFeature(database, args[0])
		if err != nil {
			return fmt.Errorf("feature not found: %s", args[0])
		}

		results, err := db.ListQAResults(database, args[0])
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"feature": f,
				"results": results,
			})
		}

		fmt.Printf("QA Checklist: %s (%s)\n\n", f.Name, f.Status)
		if len(results) == 0 {
			fmt.Println("  No QA results yet.")
		} else {
			for _, r := range results {
				icon := "✓"
				if !r.Passed {
					icon = "✗"
				}
				fmt.Printf("  %s [%s] %s", icon, r.QAType, r.CreatedAt)
				if r.Notes != "" {
					fmt.Printf(" — %s", r.Notes)
				}
				fmt.Println()
			}
		}
		return nil
	},
}
