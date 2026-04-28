package cli

import (
	"fmt"
	"sort"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or set tillr config (DB-backed key/value)",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print all config key/value pairs",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		all, err := db.ConfigList(database)
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(all)
		}
		if len(all) == 0 {
			fmt.Println("(no config set)")
			return nil
		}
		keys := make([]string, 0, len(all))
		for k := range all {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%-22s %s\n", k, all[k])
		}
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config key",
	Args:  cobra.ExactArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.ConfigSet(database, args[0], args[1]); err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(map[string]string{args[0]: args[1]})
		}
		fmt.Printf("Set %s = %s\n", args[0], args[1])
		return nil
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Read a config key",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		v, err := db.ConfigGet(database, args[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return printJSON(map[string]string{args[0]: v})
		}
		fmt.Println(v)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
}
