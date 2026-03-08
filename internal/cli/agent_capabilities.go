package cli

import (
	"fmt"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var agentCapabilityCmd = &cobra.Command{
	Use:   "capability",
	Short: "Manage agent capabilities",
}

func init() {
	agentCapabilityCmd.AddCommand(agentCapAddCmd)
	agentCapabilityCmd.AddCommand(agentCapListCmd)
	agentCapabilityCmd.AddCommand(agentCapRemoveCmd)
}

var agentCapAddCmd = &cobra.Command{
	Use:   "add <agent-id> <capability>",
	Short: "Register an agent capability",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID := args[0]
		capability := strings.ToLower(strings.TrimSpace(args[1]))
		if capability == "" {
			return fmt.Errorf("capability must not be empty")
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.AddAgentCapability(database, agentID, capability); err != nil {
			return fmt.Errorf("adding capability: %w", err)
		}

		if jsonOutput {
			return printJSON(models.AgentCapability{
				AgentID:    agentID,
				Capability: capability,
			})
		}
		fmt.Printf("Added capability %q to agent %q\n", capability, agentID)
		return nil
	},
}

var agentCapListCmd = &cobra.Command{
	Use:   "list [agent-id]",
	Short: "List agent capabilities",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if len(args) == 1 {
			agentID := args[0]
			caps, err := db.ListAgentCapabilities(database, agentID)
			if err != nil {
				return fmt.Errorf("listing capabilities: %w", err)
			}
			if jsonOutput {
				var out []models.AgentCapability
				for _, c := range caps {
					out = append(out, models.AgentCapability{AgentID: agentID, Capability: c})
				}
				return printJSON(out)
			}
			if len(caps) == 0 {
				fmt.Printf("No capabilities registered for agent %q\n", agentID)
				return nil
			}
			fmt.Printf("Capabilities for agent %q:\n", agentID)
			for _, c := range caps {
				fmt.Printf("  • %s\n", c)
			}
			return nil
		}

		// List all agents' capabilities.
		all, err := db.ListAllAgentCapabilities(database)
		if err != nil {
			return fmt.Errorf("listing capabilities: %w", err)
		}
		if jsonOutput {
			return printJSON(all)
		}
		if len(all) == 0 {
			fmt.Println("No agent capabilities registered")
			return nil
		}
		for agentID, caps := range all {
			fmt.Printf("%s: %s\n", agentID, strings.Join(caps, ", "))
		}
		return nil
	},
}

var agentCapRemoveCmd = &cobra.Command{
	Use:   "remove <agent-id> <capability>",
	Short: "Remove an agent capability",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID := args[0]
		capability := strings.ToLower(strings.TrimSpace(args[1]))

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.RemoveAgentCapability(database, agentID, capability); err != nil {
			return fmt.Errorf("removing capability: %w", err)
		}

		if jsonOutput {
			return printJSON(models.AgentCapability{
				AgentID:    agentID,
				Capability: capability,
			})
		}
		fmt.Printf("Removed capability %q from agent %q\n", capability, agentID)
		return nil
	},
}
