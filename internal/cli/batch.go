package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

type batchItemResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

type batchSummary struct {
	Operation string            `json:"operation"`
	Value     string            `json:"value"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Total     int               `json:"total"`
	Results   []batchItemResult `json:"results"`
}

// readFeatureIDsFromStdinOrArgs reads feature IDs from positional args, or from
// stdin when no args are provided. Stdin accepts one ID per line or a JSON array
// (either of strings or objects with an "id" field).
func readFeatureIDsFromStdinOrArgs(args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}

	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("no feature IDs provided. Pass IDs as arguments or pipe from stdin")
	}
	if (info.Mode() & os.ModeCharDevice) != 0 {
		return nil, fmt.Errorf("no feature IDs provided. Pass IDs as arguments or pipe from stdin")
	}

	var lines []string
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading stdin: %w", err)
	}

	input := strings.TrimSpace(strings.Join(lines, "\n"))
	if input == "" {
		return nil, fmt.Errorf("no feature IDs received from stdin")
	}

	// Try JSON array of objects with "id" field (e.g. from `tillr feature list --json`)
	var objects []map[string]any
	if err := json.Unmarshal([]byte(input), &objects); err == nil {
		var ids []string
		for _, obj := range objects {
			if id, ok := obj["id"].(string); ok && id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return ids, nil
		}
	}

	// Try JSON array of strings
	var strIDs []string
	if err := json.Unmarshal([]byte(input), &strIDs); err == nil && len(strIDs) > 0 {
		return strIDs, nil
	}

	// Fall back to one ID per line
	var ids []string
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			ids = append(ids, line)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no feature IDs found in stdin")
	}
	return ids, nil
}

func printBatchSummary(summary batchSummary) error {
	if jsonOutput {
		return printJSON(summary)
	}
	for _, r := range summary.Results {
		if r.Success {
			fmt.Printf("  ✓ %s\n", r.ID)
		} else {
			fmt.Printf("  ✗ %s: %s\n", r.ID, r.Error)
		}
	}
	if summary.Failed > 0 {
		fmt.Printf("\n%s = %s: %d succeeded, %d failed (of %d)\n",
			summary.Operation, summary.Value, summary.Succeeded, summary.Failed, summary.Total)
		return fmt.Errorf("%d of %d updates failed", summary.Failed, summary.Total)
	}
	fmt.Printf("\n✓ %s = %s: %d succeeded\n", summary.Operation, summary.Value, summary.Succeeded)
	return nil
}

var validStatuses = map[string]bool{
	"draft": true, "planning": true, "implementing": true,
	"agent-qa": true, "human-qa": true, "done": true, "blocked": true,
}

var batchStatusCmd = &cobra.Command{
	Use:   "status <status> <id1> [id2...]",
	Short: "Set status for multiple features",
	Long: `Set the status of multiple features at once.

Feature IDs can be passed as arguments or piped from stdin (one per line,
or JSON array from 'tillr feature list --json').`,
	Example: `  # Set multiple features to implementing
  tillr feature batch status implementing feat-1 feat-2

  # Pipe from feature list
  tillr feature list --status draft --json | tillr feature batch status planning`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		status := args[0]
		if !validStatuses[status] {
			return fmt.Errorf("invalid status %q. Valid: draft, planning, implementing, agent-qa, human-qa, done, blocked", status)
		}

		ids, err := readFeatureIDsFromStdinOrArgs(args[1:])
		if err != nil {
			return err
		}

		database, _, dbErr := openDB()
		if dbErr != nil {
			return dbErr
		}
		defer database.Close() //nolint:errcheck

		summary := batchSummary{Operation: "status", Value: status, Total: len(ids)}
		for _, id := range ids {
			if _, getErr := db.GetFeature(database, id); getErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: fmt.Sprintf("feature not found: %s", id)})
				summary.Failed++
				continue
			}
			if updateErr := db.UpdateFeature(database, id, map[string]any{"status": status}); updateErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: updateErr.Error()})
				summary.Failed++
				continue
			}
			summary.Results = append(summary.Results, batchItemResult{ID: id, Success: true})
			summary.Succeeded++
		}

		return printBatchSummary(summary)
	},
}

var batchTagCmd = &cobra.Command{
	Use:   "tag <tag> <id1> [id2...]",
	Short: "Add a tag to multiple features",
	Long: `Add a tag to multiple features at once.

Feature IDs can be passed as arguments or piped from stdin (one per line,
or JSON array from 'tillr feature list --json').`,
	Example: `  # Tag multiple features
  tillr feature batch tag backend feat-1 feat-2

  # Pipe from feature list
  tillr feature list --status implementing --json | tillr feature batch tag sprint-3`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		tag := args[0]
		if tag == "" {
			return fmt.Errorf("tag cannot be empty")
		}

		ids, err := readFeatureIDsFromStdinOrArgs(args[1:])
		if err != nil {
			return err
		}

		database, _, dbErr := openDB()
		if dbErr != nil {
			return dbErr
		}
		defer database.Close() //nolint:errcheck

		summary := batchSummary{Operation: "tag", Value: tag, Total: len(ids)}
		for _, id := range ids {
			if _, getErr := db.GetFeature(database, id); getErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: fmt.Sprintf("feature not found: %s", id)})
				summary.Failed++
				continue
			}
			if tagErr := db.AddFeatureTag(database, id, tag); tagErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: tagErr.Error()})
				summary.Failed++
				continue
			}
			summary.Results = append(summary.Results, batchItemResult{ID: id, Success: true})
			summary.Succeeded++
		}

		return printBatchSummary(summary)
	},
}

var batchMilestoneCmd = &cobra.Command{
	Use:   "milestone <milestone-id> <id1> [id2...]",
	Short: "Move multiple features to a milestone",
	Long: `Assign multiple features to a milestone at once.

Feature IDs can be passed as arguments or piped from stdin (one per line,
or JSON array from 'tillr feature list --json').`,
	Example: `  # Move features to a milestone
  tillr feature batch milestone v2.0-polish feat-1 feat-2

  # Pipe from feature list
  tillr feature list --status draft --json | tillr feature batch milestone v1.0-mvp`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		milestoneID := args[0]
		if milestoneID == "" {
			return fmt.Errorf("milestone ID cannot be empty")
		}

		ids, err := readFeatureIDsFromStdinOrArgs(args[1:])
		if err != nil {
			return err
		}

		database, _, dbErr := openDB()
		if dbErr != nil {
			return dbErr
		}
		defer database.Close() //nolint:errcheck

		summary := batchSummary{Operation: "milestone", Value: milestoneID, Total: len(ids)}
		for _, id := range ids {
			if _, getErr := db.GetFeature(database, id); getErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: fmt.Sprintf("feature not found: %s", id)})
				summary.Failed++
				continue
			}
			if updateErr := db.UpdateFeature(database, id, map[string]any{"milestone_id": milestoneID}); updateErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: updateErr.Error()})
				summary.Failed++
				continue
			}
			summary.Results = append(summary.Results, batchItemResult{ID: id, Success: true})
			summary.Succeeded++
		}

		return printBatchSummary(summary)
	},
}

var batchPriorityCmd = &cobra.Command{
	Use:   "priority <priority> <id1> [id2...]",
	Short: "Set priority for multiple features",
	Long: `Set the priority of multiple features at once.

Feature IDs can be passed as arguments or piped from stdin (one per line,
or JSON array from 'tillr feature list --json').`,
	Example: `  # Set priority for multiple features
  tillr feature batch priority 8 feat-1 feat-2

  # Pipe from feature list
  tillr feature list --status draft --json | tillr feature batch priority 5`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		priority, parseErr := strconv.Atoi(args[0])
		if parseErr != nil {
			return fmt.Errorf("invalid priority %q: must be an integer", args[0])
		}
		if priority < 0 {
			return fmt.Errorf("priority must be non-negative, got %d", priority)
		}

		ids, err := readFeatureIDsFromStdinOrArgs(args[1:])
		if err != nil {
			return err
		}

		database, _, dbErr := openDB()
		if dbErr != nil {
			return dbErr
		}
		defer database.Close() //nolint:errcheck

		summary := batchSummary{Operation: "priority", Value: args[0], Total: len(ids)}
		for _, id := range ids {
			if _, getErr := db.GetFeature(database, id); getErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: fmt.Sprintf("feature not found: %s", id)})
				summary.Failed++
				continue
			}
			if updateErr := db.UpdateFeature(database, id, map[string]any{"priority": priority}); updateErr != nil {
				summary.Results = append(summary.Results, batchItemResult{ID: id, Error: updateErr.Error()})
				summary.Failed++
				continue
			}
			summary.Results = append(summary.Results, batchItemResult{ID: id, Success: true})
			summary.Succeeded++
		}

		return printBatchSummary(summary)
	},
}

func init() {
	featureBatchCmd.AddCommand(batchStatusCmd)
	featureBatchCmd.AddCommand(batchTagCmd)
	featureBatchCmd.AddCommand(batchMilestoneCmd)
	featureBatchCmd.AddCommand(batchPriorityCmd)
}
