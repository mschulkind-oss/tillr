package cli

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

// getAgentSessionID returns a stable agent session ID.
// Priority: LIFECYCLE_AGENT_ID env var > "agent-{hostname}-{pid}".
func getAgentSessionID() string {
	if id := os.Getenv("LIFECYCLE_AGENT_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return "agent-" + host + "-" + strconv.Itoa(os.Getpid())
}

// ensureAgentSession creates or updates an agent session so the web dashboard
// can see active agents. If the session already exists, its status is updated.
func ensureAgentSession(database *sql.DB, projectID, status string) string {
	agentID := getAgentSessionID()

	existing, err := db.GetAgentSession(database, agentID)
	if err != nil || existing == nil {
		// Session doesn't exist — create it.
		host, _ := os.Hostname()
		if host == "" {
			host = "unknown"
		}
		s := &models.AgentSession{
			ID:        agentID,
			ProjectID: projectID,
			Name:      host + "-agent",
			Status:    status,
		}
		if createErr := db.CreateAgentSession(database, s); createErr != nil {
			// Best-effort: don't fail the command if session creation fails.
			return agentID
		}
	} else {
		// Session exists — update status.
		_ = db.UpdateAgentSessionStatus(database, agentID, status)
	}
	return agentID
}

func init() {
	nextCmd.Flags().String("cycle", "", "Filter by cycle type")
	doneCmd.Flags().String("result", "", "Work result description")
	failCmd.Flags().String("reason", "", "Failure reason")
	heartbeatCmd.Flags().String("message", "", "Status message")
	advanceCmd.Flags().String("result", "", "Work result description")
}

var nextCmd = &cobra.Command{
	Use:   "next",
	Short: "Get the next work item for an agent",
	Long: `Claim and return the next available work item for an agent to work on.

Returns an enriched WorkContext (with --json) containing the work item,
feature details, cycle state, prior results, and agent guidance — everything
an agent needs to execute the task without additional context.

If no work items are available, returns a "no_work" status.`,
	Example: `  # Get next work item as JSON (primary agent interface)
  lifecycle next --json

  # Filter by cycle type
  lifecycle next --cycle feature-implementation --json

  # Typical agent loop:
  #   WORK=$(lifecycle next --json)
  #   # Read agent_guidance field, do the work
  #   lifecycle done --result "Completed: ..."`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Register/update agent session so the web dashboard can see this agent.
		if p, pErr := db.GetProject(database); pErr == nil {
			ensureAgentSession(database, p.ID, "active")
		}

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
				addNotificationsToContext(database, ctx)
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
		showPriorityNotifications(database)
		return nil
	},
}

var doneCmd = &cobra.Command{
	Use:   "done",
	Short: "Mark current work item as complete",
	Long: `Mark the currently active work item as complete and record the result.

The --result flag should summarize what was accomplished. This result is
stored in the work item history and passed to subsequent cycle steps.

If no work item is active, this command will return an error. Use
'lifecycle next' to claim a work item first.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Update agent session status to idle.
		if p, pErr := db.GetProject(database); pErr == nil {
			ensureAgentSession(database, p.ID, "idle")
		}

		result, _ := cmd.Flags().GetString("result")
		if err := engine.CompleteWorkItem(database, result); err != nil {
			return fmt.Errorf("completing work item: %w", err)
		}

		if jsonOutput {
			err := printJSON(map[string]string{"status": "done"})
			showPriorityNotifications(database)
			return err
		}
		fmt.Println("✓ Work item marked as done.")
		showPriorityNotifications(database)
		return nil
	},
}

var failCmd = &cobra.Command{
	Use:   "fail",
	Short: "Mark current work item as failed",
	Long: `Mark the currently active work item as failed and record the reason.

The --reason flag should explain why the work item failed. This allows
the project manager to decide whether to retry, reassign, or skip the work.

If no work item is active, this command will return an error.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Update agent session status to failed.
		if p, pErr := db.GetProject(database); pErr == nil {
			ensureAgentSession(database, p.ID, "failed")
		}

		reason, _ := cmd.Flags().GetString("reason")
		if err := engine.FailWorkItem(database, reason); err != nil {
			return fmt.Errorf("failing work item: %w", err)
		}

		if jsonOutput {
			err := printJSON(map[string]string{"status": "failed"})
			showPriorityNotifications(database)
			return err
		}
		fmt.Println("✗ Work item marked as failed.")
		showPriorityNotifications(database)
		return nil
	},
}

var heartbeatCmd = &cobra.Command{
	Use:   "heartbeat",
	Short: "Agent heartbeat signal",
	Long: `Send a heartbeat signal to indicate the agent is still alive and working.

Heartbeats are recorded against the currently active work item. Use
--message to include a status update (e.g., "Running tests...").
Stale agents without recent heartbeats may have their work reclaimed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		message, _ := cmd.Flags().GetString("message")

		// Ensure agent session exists and update its timestamp.
		if p, pErr := db.GetProject(database); pErr == nil {
			agentID := ensureAgentSession(database, p.ID, "active")
			_ = db.UpdateAgentHeartbeat(database, agentID, "")
		}

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

var advanceCmd = &cobra.Command{
	Use:   "advance",
	Short: "Complete current work and get next assignment in one call",
	Long: `Atomically marks the current work item as done and returns the next
work item. This is the preferred agent command — it eliminates the
gap between "done" and "next" where another agent could steal work.

Returns the same enriched WorkContext as "lifecycle next --json" but
also includes a "completed" field showing what was just finished.`,
	Example: `  # Agent loop using advance:
  WORK=$(lifecycle next --json)     # bootstrap first item
  # ... do the work ...
  WORK=$(lifecycle advance --result "Implemented X" --json)
  # WORK now contains the next assignment`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Update agent session status to active.
		if p, pErr := db.GetProject(database); pErr == nil {
			ensureAgentSession(database, p.ID, "active")
		}

		result, _ := cmd.Flags().GetString("result")

		// Step 1: Complete current work item
		completedItem, completeErr := engine.CompleteWorkItemAndReturn(database, result)
		if completeErr != nil {
			return fmt.Errorf("completing work: %w", completeErr)
		}

		// Step 2: Get next work item
		next, nextErr := engine.GetNextWorkItem(database)

		if jsonOutput {
			response := map[string]any{
				"completed": map[string]any{
					"id":        completedItem.ID,
					"feature":   completedItem.FeatureID,
					"work_type": completedItem.WorkType,
					"result":    result,
				},
			}
			if nextErr == nil {
				ctx, ctxErr := engine.GetWorkContext(database, next)
				if ctxErr == nil {
					response["next"] = ctx
				} else {
					response["next"] = next
				}
			} else {
				response["next"] = nil
				response["status"] = "no_more_work"
			}
			ideaCount, featureCount, _ := db.CountPendingHighPriorityItems(database)
			if ideaCount > 0 || featureCount > 0 {
				response["notifications"] = map[string]any{
					"pending_ideas":          ideaCount,
					"high_priority_features": featureCount,
				}
			}
			return printJSON(response)
		}

		fmt.Printf("✓ Completed: %s (#%d)\n", completedItem.WorkType, completedItem.ID)
		if nextErr == nil {
			fmt.Printf("→ Next: %s for %s (#%d)\n", next.WorkType, next.FeatureID, next.ID)
		} else {
			fmt.Println("  No more work items available.")
		}
		showPriorityNotifications(database)
		return nil
	},
}

// showPriorityNotifications prints human-readable notifications about pending
// high-priority work items to stderr so they don't interfere with JSON piping.
func showPriorityNotifications(database *sql.DB) {
	if jsonOutput {
		return
	}
	ideaCount, featureCount, err := db.CountPendingHighPriorityItems(database)
	if err != nil || (ideaCount == 0 && featureCount == 0) {
		return
	}
	if ideaCount > 0 {
		fmt.Fprintf(os.Stderr, "⚡ %d pending idea(s) to process\n", ideaCount)
	}
	if featureCount > 0 {
		fmt.Fprintf(os.Stderr, "🔴 %d high-priority feature(s) awaiting work\n", featureCount)
	}
}

// addNotificationsToContext populates the Notifications field on a WorkContext
// with pending idea and high-priority feature counts for JSON responses.
func addNotificationsToContext(database *sql.DB, ctx *models.WorkContext) {
	ideaCount, featureCount, err := db.CountPendingHighPriorityItems(database)
	if err != nil || (ideaCount == 0 && featureCount == 0) {
		return
	}
	ctx.Notifications = map[string]any{
		"pending_ideas":          ideaCount,
		"high_priority_features": featureCount,
	}
}
