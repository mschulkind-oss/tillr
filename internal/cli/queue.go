package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/spf13/cobra"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Manage the agent work queue",
}

func init() {
	queueCmd.AddCommand(queueListCmd)
	queueCmd.AddCommand(queueReassignCmd)
	queueCmd.AddCommand(queueStatsCmd)
	queueCmd.AddCommand(queueReclaimCmd)
}

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending work items in priority order",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		items, err := db.GetQueuedWorkItems(database)
		if err != nil {
			return fmt.Errorf("listing queue: %w", err)
		}

		if jsonOutput {
			return printJSON(items)
		}

		if len(items) == 0 {
			fmt.Println("No pending or active work items in queue.")
			return nil
		}

		fmt.Printf("%-5s %-20s %-15s %-8s %-12s %-15s %s\n",
			"ID", "Feature", "Work Type", "Prio", "Status", "Agent", "Created")
		fmt.Println(strings.Repeat("─", 100))
		for _, item := range items {
			agent := item.AssignedAgent
			if agent == "" {
				agent = "(unassigned)"
			}
			name := item.FeatureName
			if len(name) > 18 {
				name = name[:18] + "…"
			}
			fmt.Printf("%-5d %-20s %-15s %-8d %-12s %-15s %s\n",
				item.WorkItemID, name, item.WorkType, item.Priority,
				item.Status, agent, item.CreatedAt)
		}
		return nil
	},
}

var queueReassignCmd = &cobra.Command{
	Use:   "reassign <work-item-id>",
	Short: "Release a claimed work item back to the pending queue",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid work item ID: %s", args[0])
		}

		if err := db.ReleaseWorkItem(database, id); err != nil {
			return fmt.Errorf("releasing work item: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"released": id, "status": "pending"})
		}

		fmt.Printf("✓ Work item %d released back to pending queue.\n", id)
		return nil
	},
}

var queueStatsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show queue statistics",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		stats, err := db.GetQueueStats(database)
		if err != nil {
			return fmt.Errorf("getting queue stats: %w", err)
		}

		if jsonOutput {
			return printJSON(stats)
		}

		fmt.Printf("Queue Statistics\n")
		fmt.Println(strings.Repeat("─", 30))
		fmt.Printf("  Pending:          %d\n", stats.TotalPending)
		fmt.Printf("  Claimed (active): %d\n", stats.TotalClaimed)
		fmt.Printf("  Completed today:  %d\n", stats.TotalCompletedDay)
		return nil
	},
}

var queueReclaimCmd = &cobra.Command{
	Use:   "reclaim",
	Short: "Reclaim stale work items (no heartbeat for 30+ minutes)",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		reclaimed, err := engine.ReclaimStaleWorkItems(database, 30)
		if err != nil {
			return fmt.Errorf("reclaiming stale work items: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"reclaimed": reclaimed})
		}

		if reclaimed == 0 {
			fmt.Println("No stale work items to reclaim.")
		} else {
			fmt.Printf("✓ Reclaimed %d stale work item(s) back to pending queue.\n", reclaimed)
		}
		return nil
	},
}
