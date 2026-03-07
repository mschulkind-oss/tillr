package cli

import (
	"fmt"
	"strconv"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage project configuration defaults",
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configSetCmd)
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration (merged defaults + file)",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := config.FindProjectRoot()
		if err != nil {
			return fmt.Errorf("no lifecycle project found. Run 'lifecycle init <name>' first")
		}
		cfg, err := config.Load(root)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if jsonOutput {
			return printJSON(cfg)
		}

		fmt.Println("Current Configuration:")
		fmt.Printf("  project_dir:          %s\n", cfg.ProjectDir)
		fmt.Printf("  db_path:              %s\n", cfg.DBPath)
		fmt.Printf("  server_port:          %d\n", cfg.ServerPort)
		fmt.Printf("  default_milestone:    %s\n", valOrNone(cfg.DefaultMilestone))
		fmt.Printf("  default_priority:     %d\n", cfg.DefaultPriority)
		fmt.Printf("  theme:                %s\n", cfg.Theme)
		fmt.Printf("  agent_timeout_minutes: %d\n", cfg.AgentTimeout)
		return nil
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create .lifecycle.yaml with defaults",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := config.FindProjectRoot()
		if err != nil {
			return fmt.Errorf("no lifecycle project found. Run 'lifecycle init <name>' first")
		}

		cfg := config.Defaults()
		if err := config.SaveYAML(cfg, root); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"path": root + "/" + config.YAMLConfigName})
		}
		fmt.Printf("✓ Created %s/%s with defaults\n", root, config.YAMLConfigName)
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value in .lifecycle.yaml",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := config.FindProjectRoot()
		if err != nil {
			return fmt.Errorf("no lifecycle project found. Run 'lifecycle init <name>' first")
		}

		// Load existing YAML or start from defaults
		cfg, err := config.LoadYAML(root)
		if err != nil {
			cfg = config.Defaults()
		}

		key, value := args[0], args[1]
		switch key {
		case "default_milestone":
			cfg.DefaultMilestone = value
		case "default_priority":
			v, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("default_priority must be an integer")
			}
			cfg.DefaultPriority = v
		case "server_port":
			v, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("server_port must be an integer")
			}
			cfg.ServerPort = v
		case "theme":
			valid := map[string]bool{"system": true, "dark": true, "light": true}
			if !valid[value] {
				return fmt.Errorf("theme must be one of: system, dark, light")
			}
			cfg.Theme = value
		case "agent_timeout_minutes":
			v, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("agent_timeout_minutes must be an integer")
			}
			cfg.AgentTimeout = v
		case "db_path":
			cfg.DBPath = value
		default:
			return fmt.Errorf("unknown config key %q. Valid keys: default_milestone, default_priority, server_port, theme, agent_timeout_minutes, db_path", key)
		}

		if err := config.SaveYAML(cfg, root); err != nil {
			return fmt.Errorf("writing config: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"key": key, "value": value})
		}
		fmt.Printf("✓ Set %s = %s\n", key, value)
		return nil
	},
}

func valOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
