package cli

import (
	"fmt"
	"os"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server for agent integration",
	Long: `Start a Model Context Protocol (MCP) server that exposes lifecycle tools
over stdio. This allows AI agents to interact with lifecycle directly via
JSON-RPC 2.0 instead of subprocess CLI calls.

The server reads line-delimited JSON-RPC requests from stdin and writes
responses to stdout. It implements the MCP protocol lifecycle: initialize,
tools/list, and tools/call.

Exposed tools:
  lifecycle_next       Get next work item with full context
  lifecycle_done       Mark current work item complete
  lifecycle_fail       Mark current work item as failed
  lifecycle_status     Project status overview
  lifecycle_features   List features (optional status filter)
  lifecycle_feedback   Submit feedback/ideas/bugs`,
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

		srv := mcp.NewServer(database, p.ID, os.Stdin, os.Stdout)
		return srv.Run()
	},
}
