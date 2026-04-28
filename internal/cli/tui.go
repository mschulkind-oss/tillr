package cli

import (
	"github.com/mschulkind-oss/tillr/internal/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Open the terminal UI (read-only inspection of features, personas, retros)",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck
		return tui.Run(database, cfg.ProjectDir)
	},
}

func init() { rootCmd.AddCommand(tuiCmd) }
