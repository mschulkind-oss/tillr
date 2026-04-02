package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/plugin"
	"github.com/spf13/cobra"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage tillr plugins",
	Long: `Discover and run external tillr plugins.

Plugins are external executables named tillr-plugin-<name> found on your PATH.
They communicate via JSON over stdin/stdout.

EXAMPLES
  tillr plugin list              List discovered plugins
  tillr plugin info <name>       Show plugin details
  tillr plugin run <name> <cmd>  Run a plugin command`,
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered plugins",
	Args:  cobra.NoArgs,
	RunE:  runPluginList,
}

var pluginInfoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Show plugin details",
	Args:  cobra.ExactArgs(1),
	RunE:  runPluginInfo,
}

var pluginRunCmd = &cobra.Command{
	Use:   "run <name> <command> [args...]",
	Short: "Run a plugin command",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runPluginRun,
}

func init() {
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginInfoCmd)
	pluginCmd.AddCommand(pluginRunCmd)
}

func runPluginList(_ *cobra.Command, _ []string) error {
	plugins, err := plugin.ListPlugins()
	if err != nil {
		return fmt.Errorf("listing plugins: %w", err)
	}

	if jsonOutput {
		return printJSON(plugins)
	}

	if len(plugins) == 0 {
		fmt.Println("No plugins found.")
		fmt.Println("Plugins are executables named tillr-plugin-<name> on your PATH.")
		return nil
	}

	fmt.Printf("%-20s %-10s %-40s %s\n", "NAME", "VERSION", "DESCRIPTION", "PATH")
	fmt.Printf("%-20s %-10s %-40s %s\n", "----", "-------", "-----------", "----")
	for _, p := range plugins {
		version := p.Version
		if version == "" {
			version = "-"
		}
		desc := p.Description
		if desc == "" {
			desc = "-"
		}
		fmt.Printf("%-20s %-10s %-40s %s\n", p.Name, version, desc, p.Path)
	}

	return nil
}

func runPluginInfo(_ *cobra.Command, args []string) error {
	name := args[0]

	plugins, err := plugin.Discover()
	if err != nil {
		return fmt.Errorf("discovering plugins: %w", err)
	}

	var found *plugin.Plugin
	for i := range plugins {
		if plugins[i].Name == name {
			found = &plugins[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("plugin %q not found on PATH", name)
	}

	enriched, err := plugin.QueryInfo(*found)
	if err != nil {
		return fmt.Errorf("querying plugin info: %w", err)
	}

	if jsonOutput {
		return printJSON(enriched)
	}

	fmt.Printf("Name:        %s\n", enriched.Name)
	fmt.Printf("Path:        %s\n", enriched.Path)
	fmt.Printf("Version:     %s\n", valueOrDash(enriched.Version))
	fmt.Printf("Description: %s\n", valueOrDash(enriched.Description))
	if len(enriched.Commands) > 0 {
		fmt.Printf("Commands:    %s\n", strings.Join(enriched.Commands, ", "))
	} else {
		fmt.Printf("Commands:    -\n")
	}

	return nil
}

func runPluginRun(_ *cobra.Command, args []string) error {
	name := args[0]
	command := args[1]

	plugins, err := plugin.Discover()
	if err != nil {
		return fmt.Errorf("discovering plugins: %w", err)
	}

	var found *plugin.Plugin
	for i := range plugins {
		if plugins[i].Name == name {
			found = &plugins[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("plugin %q not found on PATH", name)
	}

	// Read stdin if available (for piping JSON input).
	var input interface{}
	stat, _ := os.Stdin.Stat()
	if stat != nil && (stat.Mode()&os.ModeCharDevice) == 0 {
		// Data is being piped in.
		var raw interface{}
		dec := json.NewDecoder(os.Stdin)
		if err := dec.Decode(&raw); err == nil {
			input = raw
		}
	}

	output, err := plugin.Run(*found, command, input)
	if err != nil {
		return err
	}

	if jsonOutput {
		return printJSON(plugin.PluginResult{
			Plugin:  name,
			Command: command,
			Output:  output,
		})
	}

	_, _ = fmt.Fprintln(os.Stdout, string(output))
	return nil
}

func valueOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
