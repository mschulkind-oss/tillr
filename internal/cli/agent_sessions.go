package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage agent sessions",
}

func init() {
	agentCmd.AddCommand(agentStartCmd)
	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentShowCmd)
	agentCmd.AddCommand(agentEndCmd)

	agentStartCmd.Flags().String("task", "", "Task description")
	agentStartCmd.Flags().String("feature", "", "Feature ID to associate with")

	agentListCmd.Flags().String("status", "active", "Filter by status (active, completed, failed)")

	agentEndCmd.Flags().String("status", "completed", "End status (completed or failed)")

	updateCmd.Flags().String("message", "", "Markdown status update (required)")
	updateCmd.Flags().Int("progress", -1, "Progress percentage (0-100)")
	updateCmd.Flags().String("phase", "", "Current phase")
	updateCmd.Flags().String("eta", "", "Estimated time remaining")
	updateCmd.Flags().String("agent", "", "Agent session ID (default: most recent active)")
}

var agentStartCmd = &cobra.Command{
	Use:   "start <name>",
	Short: "Start a new agent session",
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

		task, _ := cmd.Flags().GetString("task")
		feature, _ := cmd.Flags().GetString("feature")

		sessionID := fmt.Sprintf("agent-%x", time.Now().UnixNano()&0xFFFFFF)
		s := &models.AgentSession{
			ID:              sessionID,
			ProjectID:       p.ID,
			FeatureID:       feature,
			Name:            args[0],
			TaskDescription: task,
			Status:          "active",
		}

		if err := db.CreateAgentSession(database, s); err != nil {
			return fmt.Errorf("creating agent session: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: feature,
			EventType: "agent.started",
			Data:      fmt.Sprintf(`{"session_id":%q,"name":%q,"task":%q}`, sessionID, args[0], task),
		})

		if jsonOutput {
			return printJSON(s)
		}
		fmt.Printf("✓ Started agent session %s (%s)\n", sessionID, args[0])
		return nil
	},
}

var agentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List agent sessions",
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

		status, _ := cmd.Flags().GetString("status")
		sessions, err := db.ListAgentSessions(database, p.ID, status)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(sessions)
		}

		if len(sessions) == 0 {
			fmt.Println("No agent sessions found.")
			return nil
		}

		fmt.Printf("%-20s %-15s %-10s %-6s %-12s %s\n", "ID", "NAME", "STATUS", "PROG", "PHASE", "UPDATED")
		fmt.Println(strings.Repeat("─", 80))
		for _, s := range sessions {
			phase := s.CurrentPhase
			if phase == "" {
				phase = "-"
			}
			fmt.Printf("%-20s %-15s %-10s %3d%%   %-12s %s\n", s.ID, s.Name, s.Status, s.ProgressPct, phase, s.UpdatedAt)
		}
		return nil
	},
}

var agentShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show agent session details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		s, err := db.GetAgentSession(database, args[0])
		if err != nil {
			return fmt.Errorf("agent session not found: %s", args[0])
		}

		updates, _ := db.ListStatusUpdates(database, args[0])

		if jsonOutput {
			return printJSON(map[string]any{
				"session": s,
				"updates": updates,
			})
		}

		fmt.Printf("Agent Session: %s\n", s.Name)
		fmt.Printf("  ID:       %s\n", s.ID)
		fmt.Printf("  Status:   %s\n", s.Status)
		fmt.Printf("  Progress: %d%%\n", s.ProgressPct)
		if s.FeatureID != "" {
			fmt.Printf("  Feature:  %s\n", s.FeatureID)
		}
		if s.TaskDescription != "" {
			fmt.Printf("  Task:     %s\n", s.TaskDescription)
		}
		if s.CurrentPhase != "" {
			fmt.Printf("  Phase:    %s\n", s.CurrentPhase)
		}
		if s.ETA != "" {
			fmt.Printf("  ETA:      %s\n", s.ETA)
		}
		fmt.Printf("  Created:  %s\n", s.CreatedAt)
		fmt.Printf("  Updated:  %s\n", s.UpdatedAt)

		if len(updates) > 0 {
			fmt.Printf("\nRecent Updates:\n")
			for _, u := range updates {
				progress := ""
				if u.ProgressPct != nil {
					progress = fmt.Sprintf(" [%d%%]", *u.ProgressPct)
				}
				phase := ""
				if u.Phase != "" {
					phase = fmt.Sprintf(" (%s)", u.Phase)
				}
				fmt.Printf("  %s%s%s: %s\n", u.CreatedAt, progress, phase, u.MessageMD)
			}
		}
		return nil
	},
}

var agentEndCmd = &cobra.Command{
	Use:   "end <id>",
	Short: "End an agent session",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		s, err := db.GetAgentSession(database, args[0])
		if err != nil {
			return fmt.Errorf("agent session not found: %s", args[0])
		}

		endStatus, _ := cmd.Flags().GetString("status")
		if endStatus != "completed" && endStatus != "failed" {
			return fmt.Errorf("invalid status %q: must be completed or failed", endStatus)
		}

		if err := db.EndAgentSession(database, args[0], endStatus); err != nil {
			return fmt.Errorf("ending agent session: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: s.ProjectID,
			FeatureID: s.FeatureID,
			EventType: "agent.ended",
			Data:      fmt.Sprintf(`{"session_id":%q,"name":%q,"status":%q}`, args[0], s.Name, endStatus),
		})

		if jsonOutput {
			return printJSON(map[string]string{"status": endStatus, "session": args[0]})
		}
		fmt.Printf("✓ Ended agent session %s (%s)\n", args[0], endStatus)
		return nil
	},
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Post an agent status update",
	RunE: func(cmd *cobra.Command, args []string) error {
		message, _ := cmd.Flags().GetString("message")
		if message == "" {
			return fmt.Errorf("--message is required")
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		agentID, _ := cmd.Flags().GetString("agent")

		// If no agent ID specified, find the most recent active session
		if agentID == "" {
			sessions, listErr := db.ListAgentSessions(database, p.ID, "active")
			if listErr != nil || len(sessions) == 0 {
				return fmt.Errorf("no active agent session found; use --agent <id> or start a session with 'lifecycle agent start'")
			}
			agentID = sessions[0].ID
		}

		// Verify session exists
		if _, err := db.GetAgentSession(database, agentID); err != nil {
			return fmt.Errorf("agent session not found: %s", agentID)
		}

		progress, _ := cmd.Flags().GetInt("progress")
		phase, _ := cmd.Flags().GetString("phase")
		eta, _ := cmd.Flags().GetString("eta")

		// Insert status update
		u := &models.StatusUpdate{
			AgentSessionID: agentID,
			MessageMD:      message,
			Phase:          phase,
		}
		if progress >= 0 {
			u.ProgressPct = &progress
		}

		if err := db.InsertStatusUpdate(database, u); err != nil {
			return fmt.Errorf("inserting status update: %w", err)
		}

		// Update the agent session fields
		sessionUpdates := make(map[string]any)
		if progress >= 0 {
			sessionUpdates["progress_pct"] = progress
		}
		if phase != "" {
			sessionUpdates["current_phase"] = phase
		}
		if eta != "" {
			sessionUpdates["eta"] = eta
		}
		if len(sessionUpdates) > 0 {
			if err := db.UpdateAgentSession(database, agentID, sessionUpdates); err != nil {
				return fmt.Errorf("updating agent session: %w", err)
			}
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "agent.status_update",
			Data:      fmt.Sprintf(`{"session_id":%q,"message":%q,"progress":%d}`, agentID, message, progress),
		})

		if jsonOutput {
			return printJSON(u)
		}
		fmt.Printf("✓ Status update posted to session %s\n", agentID)
		return nil
	},
}
