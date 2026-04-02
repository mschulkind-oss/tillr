package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/models"
	"github.com/spf13/cobra"
)

var undoCmd = &cobra.Command{
	Use:   "undo",
	Short: "Undo the last operation",
	Long: `Undo the last undoable operation in the current project.

Supported operations:
  - Feature edits (restores previous field values)
  - Feature status changes (restores previous status)
  - Feature deletions (re-inserts the deleted feature)

Use 'tillr undo list' to see recent undoable operations.
Use 'tillr redo' to redo the last undone operation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return fmt.Errorf("getting project: %w", err)
		}

		entry, err := db.GetLastUndoEntry(database, p.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				if jsonOutput {
					return printJSON(map[string]string{"message": "nothing to undo"})
				}
				fmt.Println("Nothing to undo.")
				return nil
			}
			return fmt.Errorf("finding undo entry: %w", err)
		}

		if err := applyUndo(database, entry); err != nil {
			return fmt.Errorf("applying undo: %w", err)
		}

		if err := db.MarkUndone(database, entry.ID); err != nil {
			return fmt.Errorf("marking entry as undone: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"undone":      true,
				"operation":   entry.Operation,
				"entity_type": entry.EntityType,
				"entity_id":   entry.EntityID,
			})
		}
		fmt.Printf("✓ Undone: %s %s %s\n", entry.Operation, entry.EntityType, entry.EntityID)
		return nil
	},
}

var undoListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show recent undoable operations",
	Long: `List recent operations that can be undone or have been undone.
Use --limit to control how many entries are shown (default: 10).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return fmt.Errorf("getting project: %w", err)
		}

		limit, _ := cmd.Flags().GetInt("limit")
		if limit <= 0 {
			limit = 10
		}

		entries, err := db.ListUndoEntries(database, p.ID, limit)
		if err != nil {
			return fmt.Errorf("listing undo entries: %w", err)
		}

		if jsonOutput {
			return printJSON(entries)
		}

		if len(entries) == 0 {
			fmt.Println("No undo history.")
			return nil
		}

		fmt.Printf("%-4s %-16s %-12s %-20s %-8s %s\n", "ID", "OPERATION", "TYPE", "ENTITY", "STATUS", "TIME")
		for _, e := range entries {
			status := "active"
			if e.Undone {
				status = "undone"
			}
			fmt.Printf("%-4d %-16s %-12s %-20s %-8s %s\n",
				e.ID, e.Operation, e.EntityType, e.EntityID, status, e.CreatedAt)
		}
		return nil
	},
}

var redoCmd = &cobra.Command{
	Use:   "redo",
	Short: "Redo the last undone operation",
	Long: `Redo the most recently undone operation, re-applying the change.

Only operations that were undone via 'tillr undo' can be redone.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return fmt.Errorf("getting project: %w", err)
		}

		entry, err := db.GetLastUndoneEntry(database, p.ID)
		if err != nil {
			if err == sql.ErrNoRows {
				if jsonOutput {
					return printJSON(map[string]string{"message": "nothing to redo"})
				}
				fmt.Println("Nothing to redo.")
				return nil
			}
			return fmt.Errorf("finding redo entry: %w", err)
		}

		if err := applyRedo(database, entry); err != nil {
			return fmt.Errorf("applying redo: %w", err)
		}

		if err := db.MarkRedone(database, entry.ID); err != nil {
			return fmt.Errorf("marking entry as redone: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"redone":      true,
				"operation":   entry.Operation,
				"entity_type": entry.EntityType,
				"entity_id":   entry.EntityID,
			})
		}
		fmt.Printf("✓ Redone: %s %s %s\n", entry.Operation, entry.EntityType, entry.EntityID)
		return nil
	},
}

// applyUndo restores the state from before_data based on the operation type.
func applyUndo(database *sql.DB, entry *models.UndoEntry) error {
	switch entry.Operation {
	case "feature_edit":
		return restoreFeatureFromJSON(database, entry.EntityID, entry.BeforeData)
	case "feature_delete":
		return reinsertFeatureFromJSON(database, entry.BeforeData)
	default:
		return fmt.Errorf("unsupported undo operation: %s", entry.Operation)
	}
}

// applyRedo re-applies the change from after_data based on the operation type.
func applyRedo(database *sql.DB, entry *models.UndoEntry) error {
	switch entry.Operation {
	case "feature_edit":
		return restoreFeatureFromJSON(database, entry.EntityID, entry.AfterData)
	case "feature_delete":
		return db.DeleteFeature(database, entry.EntityID)
	default:
		return fmt.Errorf("unsupported redo operation: %s", entry.Operation)
	}
}

// restoreFeatureFromJSON updates a feature's fields from a JSON snapshot.
func restoreFeatureFromJSON(database *sql.DB, id, jsonData string) error {
	var f models.Feature
	if err := json.Unmarshal([]byte(jsonData), &f); err != nil {
		return fmt.Errorf("parsing feature data: %w", err)
	}

	updates := map[string]any{
		"name":            f.Name,
		"description":     f.Description,
		"spec":            f.Spec,
		"status":          f.Status,
		"priority":        f.Priority,
		"milestone_id":    f.MilestoneID,
		"roadmap_item_id": f.RoadmapItemID,
		"previous_status": f.PreviousStatus,
		"estimate_points": f.EstimatePoints,
		"estimate_size":   f.EstimateSize,
	}

	return db.UpdateFeature(database, id, updates)
}

// reinsertFeatureFromJSON recreates a deleted feature from its JSON snapshot.
func reinsertFeatureFromJSON(database *sql.DB, jsonData string) error {
	var f models.Feature
	if err := json.Unmarshal([]byte(jsonData), &f); err != nil {
		return fmt.Errorf("parsing feature data: %w", err)
	}

	return db.CreateFeature(database, &f)
}

// LogFeatureUndo captures a feature's state before and after a mutation for undo support.
func LogFeatureUndo(database *sql.DB, projectID, operation, featureID string, before, after *models.Feature) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return fmt.Errorf("marshaling before data: %w", err)
	}

	afterJSON := []byte("{}")
	if after != nil {
		afterJSON, err = json.Marshal(after)
		if err != nil {
			return fmt.Errorf("marshaling after data: %w", err)
		}
	}

	return db.InsertUndoEntry(database, &models.UndoEntry{
		ProjectID:  projectID,
		Operation:  operation,
		EntityType: "feature",
		EntityID:   featureID,
		BeforeData: string(beforeJSON),
		AfterData:  string(afterJSON),
	})
}

func init() {
	undoCmd.AddCommand(undoListCmd)
	undoListCmd.Flags().Int("limit", 10, "Number of entries to show")
}
