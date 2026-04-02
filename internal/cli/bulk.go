package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/spf13/cobra"
)

var featureBulkCmd = &cobra.Command{
	Use:   "bulk <action>",
	Short: "Bulk operations on features",
	Long: `Perform bulk operations on multiple features at once.

Actions: status, priority, milestone

Features can be selected with --ids (comma-separated) or --filter (key=value).
Use --dry-run to preview changes without applying them.`,
	Example: `  # Bulk status change
  tillr feature bulk status implementing --ids feat-1,feat-2,feat-3

  # Bulk priority with filter
  tillr feature bulk priority 8 --filter status=draft

  # Bulk milestone move with dry-run
  tillr feature bulk milestone v2.0 --ids feat-1,feat-2 --dry-run`,
}

func init() {
	featureBulkCmd.AddCommand(bulkStatusCmd)
	featureBulkCmd.AddCommand(bulkPriorityChangeCmd)
	featureBulkCmd.AddCommand(bulkMilestoneChangeCmd)

	for _, cmd := range []*cobra.Command{bulkStatusCmd, bulkPriorityChangeCmd, bulkMilestoneChangeCmd} {
		cmd.Flags().String("ids", "", "Comma-separated feature IDs")
		cmd.Flags().String("filter", "", "Filter expression (e.g. status=draft, milestone=v1.0, priority=5)")
		cmd.Flags().Bool("dry-run", false, "Preview changes without applying them")
	}
}

// resolveFeatureIDs returns feature IDs from --ids flag or --filter flag.
func resolveFeatureIDs(cmd *cobra.Command) ([]string, error) {
	idsStr, _ := cmd.Flags().GetString("ids")
	filterStr, _ := cmd.Flags().GetString("filter")

	if idsStr == "" && filterStr == "" {
		return nil, fmt.Errorf("provide --ids or --filter to select features")
	}
	if idsStr != "" && filterStr != "" {
		return nil, fmt.Errorf("use either --ids or --filter, not both")
	}

	if idsStr != "" {
		ids := strings.Split(idsStr, ",")
		var trimmed []string
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id != "" {
				trimmed = append(trimmed, id)
			}
		}
		if len(trimmed) == 0 {
			return nil, fmt.Errorf("--ids must contain at least one feature ID")
		}
		return trimmed, nil
	}

	// Parse filter
	database, _, err := openDB()
	if err != nil {
		return nil, err
	}
	defer database.Close() //nolint:errcheck

	p, err := db.GetProject(database)
	if err != nil {
		return nil, err
	}

	parts := strings.SplitN(filterStr, "=", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("filter must be in key=value format (e.g. status=draft)")
	}
	key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])

	var features []models.Feature
	switch key {
	case "status":
		features, err = db.ListFeatures(database, p.ID, value, "")
	case "milestone":
		features, err = db.ListFeatures(database, p.ID, "", value)
	case "priority":
		allFeatures, ferr := db.ListFeatures(database, p.ID, "", "")
		if ferr != nil {
			return nil, ferr
		}
		prio, perr := strconv.Atoi(value)
		if perr != nil {
			return nil, fmt.Errorf("priority filter value must be an integer")
		}
		for _, f := range allFeatures {
			if f.Priority == prio {
				features = append(features, f)
			}
		}
	default:
		return nil, fmt.Errorf("unknown filter key %q. Valid: status, milestone, priority", key)
	}
	if err != nil {
		return nil, err
	}

	var ids []string
	for _, f := range features {
		ids = append(ids, f.ID)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no features match filter %s=%s", key, value)
	}
	return ids, nil
}

type bulkResult struct {
	Action  string          `json:"action"`
	Value   string          `json:"value"`
	DryRun  bool            `json:"dry_run"`
	Total   int             `json:"total"`
	Success int             `json:"success"`
	Failed  int             `json:"failed"`
	Items   []bulkItemEntry `json:"items"`
}

type bulkItemEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

func printBulkResult(r bulkResult) error {
	if jsonOutput {
		return printJSON(r)
	}

	if r.DryRun {
		fmt.Printf("[DRY RUN] Would %s = %s for %d feature(s):\n", r.Action, r.Value, r.Total)
	} else {
		fmt.Printf("%s = %s: %d/%d succeeded\n", r.Action, r.Value, r.Success, r.Total)
	}
	for _, item := range r.Items {
		if r.DryRun {
			fmt.Printf("  - %s (%s)\n", item.ID, item.Name)
		} else if item.Success {
			fmt.Printf("  + %s\n", item.ID)
		} else {
			fmt.Printf("  x %s: %s\n", item.ID, item.Error)
		}
	}
	if r.Failed > 0 {
		return fmt.Errorf("%d of %d operations failed", r.Failed, r.Total)
	}
	return nil
}

var bulkStatusCmd = &cobra.Command{
	Use:   "status <new-status>",
	Short: "Bulk change status of features",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		newStatus := args[0]
		if !validStatuses[newStatus] {
			return fmt.Errorf("invalid status %q. Valid: draft, planning, implementing, agent-qa, human-qa, done, blocked", newStatus)
		}

		ids, err := resolveFeatureIDs(cmd)
		if err != nil {
			return err
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		database, _, dbErr := openDB()
		if dbErr != nil {
			return dbErr
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		result := bulkResult{Action: "status", Value: newStatus, DryRun: dryRun, Total: len(ids)}
		for _, id := range ids {
			f, getErr := db.GetFeature(database, id)
			if getErr != nil {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Error: "not found"})
				result.Failed++
				continue
			}

			if dryRun {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Name: f.Name, Success: true})
				result.Success++
				continue
			}

			if updateErr := db.UpdateFeature(database, id, map[string]any{"status": newStatus}); updateErr != nil {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Error: updateErr.Error()})
				result.Failed++
				continue
			}

			// Emit audit event
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: id,
				EventType: "feature.bulk_status_change",
				Data:      fmt.Sprintf(`{"from":%q,"to":%q}`, f.Status, newStatus),
			})
			result.Items = append(result.Items, bulkItemEntry{ID: id, Success: true})
			result.Success++
		}

		return printBulkResult(result)
	},
}

var bulkPriorityChangeCmd = &cobra.Command{
	Use:   "priority <new-priority>",
	Short: "Bulk change priority of features",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		newPriority, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("priority must be an integer, got %q", args[0])
		}
		if newPriority < 0 {
			return fmt.Errorf("priority must be non-negative")
		}

		ids, idErr := resolveFeatureIDs(cmd)
		if idErr != nil {
			return idErr
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		database, _, dbErr := openDB()
		if dbErr != nil {
			return dbErr
		}
		defer database.Close() //nolint:errcheck

		p, pErr := db.GetProject(database)
		if pErr != nil {
			return pErr
		}

		result := bulkResult{Action: "priority", Value: args[0], DryRun: dryRun, Total: len(ids)}
		for _, id := range ids {
			f, getErr := db.GetFeature(database, id)
			if getErr != nil {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Error: "not found"})
				result.Failed++
				continue
			}

			if dryRun {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Name: f.Name, Success: true})
				result.Success++
				continue
			}

			if updateErr := db.UpdateFeature(database, id, map[string]any{"priority": newPriority}); updateErr != nil {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Error: updateErr.Error()})
				result.Failed++
				continue
			}

			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: id,
				EventType: "feature.bulk_priority_change",
				Data:      fmt.Sprintf(`{"from":%d,"to":%d}`, f.Priority, newPriority),
			})
			result.Items = append(result.Items, bulkItemEntry{ID: id, Success: true})
			result.Success++
		}

		return printBulkResult(result)
	},
}

var bulkMilestoneChangeCmd = &cobra.Command{
	Use:   "milestone <milestone-id>",
	Short: "Bulk move features to a milestone",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		milestoneID := args[0]

		ids, err := resolveFeatureIDs(cmd)
		if err != nil {
			return err
		}
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		database, _, dbErr := openDB()
		if dbErr != nil {
			return dbErr
		}
		defer database.Close() //nolint:errcheck

		p, pErr := db.GetProject(database)
		if pErr != nil {
			return pErr
		}

		result := bulkResult{Action: "milestone", Value: milestoneID, DryRun: dryRun, Total: len(ids)}
		for _, id := range ids {
			f, getErr := db.GetFeature(database, id)
			if getErr != nil {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Error: "not found"})
				result.Failed++
				continue
			}

			if dryRun {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Name: f.Name, Success: true})
				result.Success++
				continue
			}

			if updateErr := db.UpdateFeature(database, id, map[string]any{"milestone_id": milestoneID}); updateErr != nil {
				result.Items = append(result.Items, bulkItemEntry{ID: id, Error: updateErr.Error()})
				result.Failed++
				continue
			}

			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: id,
				EventType: "feature.bulk_milestone_change",
				Data:      fmt.Sprintf(`{"from":%q,"to":%q}`, f.MilestoneID, milestoneID),
			})
			result.Items = append(result.Items, bulkItemEntry{ID: id, Success: true})
			result.Success++
		}

		return printBulkResult(result)
	},
}
