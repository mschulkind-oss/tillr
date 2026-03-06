package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "lifecycle",
	Short: "Human-in-the-loop project management for agentic development",
	Long: `Lifecycle is a project management tool that bridges human product owners
and AI agents. It tracks, visualizes, and steers work as it flows through
defined iteration cycles — acting as the project manager for agentic development.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(nextCmd)
	rootCmd.AddCommand(doneCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(doctorCmd)
}

var initCmd = &cobra.Command{
	Use:   "init [project-name]",
	Short: "Initialize a new lifecycle project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Initializing project: %s\n", args[0])
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show project status overview",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Project status: not yet implemented")
		return nil
	},
}

var nextCmd = &cobra.Command{
	Use:   "next",
	Short: "Get the next work item for an agent to work on",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Next work item: not yet implemented")
		return nil
	},
}

var doneCmd = &cobra.Command{
	Use:   "done",
	Short: "Mark current work item as complete",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Done: not yet implemented")
		return nil
	},
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the web viewer with live reload",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Web viewer: not yet implemented")
		return nil
	},
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate environment and project setup",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Doctor: not yet implemented")
		return nil
	},
}
