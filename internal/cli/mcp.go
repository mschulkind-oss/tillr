package cli

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server for agent integration",
	Long: `Start a Model Context Protocol (MCP) server that exposes tillr tools
over stdio. This allows AI agents to interact with tillr directly via
JSON-RPC 2.0 instead of subprocess CLI calls.

The server reads line-delimited JSON-RPC requests from stdin and writes
responses to stdout. It implements the MCP protocol tillr: initialize,
tools/list, and tools/call.

Exposed tools:
  tillr_next       Get next work item with full context
  tillr_done       Mark current work item complete
  tillr_fail       Mark current work item as failed
  tillr_status     Project status overview
  tillr_features   List features (optional status filter)
  tillr_feedback   Submit feedback/ideas/bugs`,
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
