package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

var notificationsCmd = &cobra.Command{
	Use:     "notifications",
	Aliases: []string{"notif"},
	Short:   "View and manage notifications",
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

		recipient, _ := cmd.Flags().GetString("recipient")
		unreadOnly, _ := cmd.Flags().GetBool("unread")

		notifications, err := db.ListNotifications(database, p.ID, recipient, unreadOnly, 50)
		if err != nil {
			return fmt.Errorf("listing notifications: %w", err)
		}

		if jsonOutput {
			return printJSON(notifications)
		}

		if len(notifications) == 0 {
			fmt.Println("No notifications.")
			return nil
		}

		unread, _ := db.CountUnreadNotifications(database, p.ID, recipient)
		fmt.Printf("Notifications (%d unread):\n", unread)
		fmt.Printf("%-4s %-12s %-10s %s\n", "ID", "TYPE", "READ", "MESSAGE")
		fmt.Println(strings.Repeat("-", 70))
		for _, n := range notifications {
			readMark := " "
			if !n.Read {
				readMark = "*"
			}
			fmt.Printf("%-4d %-12s %-10s %s\n", n.ID, n.Type, readMark, n.Message)
		}
		return nil
	},
}

func init() {
	notificationsCmd.Flags().String("recipient", "", "Filter by recipient")
	notificationsCmd.Flags().Bool("unread", false, "Show only unread notifications")

	notificationsCmd.AddCommand(notificationsReadCmd)
	notificationsCmd.AddCommand(notificationsClearCmd)
}

var notificationsReadCmd = &cobra.Command{
	Use:   "read <id>",
	Short: "Mark a notification as read",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid notification ID: %s", args[0])
		}

		if err := db.MarkNotificationRead(database, id); err != nil {
			return fmt.Errorf("marking notification as read: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"id": id, "read": true})
		}
		fmt.Printf("Marked notification #%d as read.\n", id)
		return nil
	},
}

var notificationsClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Mark all notifications as read",
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

		recipient, _ := cmd.Flags().GetString("recipient")
		if err := db.ClearNotifications(database, p.ID, recipient); err != nil {
			return fmt.Errorf("clearing notifications: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"status": "cleared"})
		}
		fmt.Println("All notifications marked as read.")
		return nil
	},
}
