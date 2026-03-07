package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/mschulkind/lifecycle/internal/server"
	"github.com/spf13/cobra"
)

var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage webhook notifications",
	Long: `Register, list, remove, and test webhook endpoints.
Webhooks receive HTTP POST notifications when lifecycle events occur.

Each delivery includes:
  - JSON payload with event details
  - X-Lifecycle-Event header with the event type
  - X-Lifecycle-Signature header (HMAC-SHA256, if secret is configured)
  - X-Lifecycle-Delivery header with a unique delivery ID`,
}

func init() {
	webhookCmd.AddCommand(webhookAddCmd)
	webhookCmd.AddCommand(webhookListCmd)
	webhookCmd.AddCommand(webhookRemoveCmd)
	webhookCmd.AddCommand(webhookTestCmd)

	webhookAddCmd.Flags().String("events", "", "Comma-separated event types to subscribe to (default: all)")
	webhookAddCmd.Flags().String("secret", "", "Shared secret for HMAC-SHA256 signature verification")
}

var webhookAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Register a webhook URL",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		url := args[0]
		eventsFlag, _ := cmd.Flags().GetString("events")
		secret, _ := cmd.Flags().GetString("secret")

		// Build events JSON array
		eventsJSON := "[]"
		if eventsFlag != "" {
			parts := strings.Split(eventsFlag, ",")
			var trimmed []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					trimmed = append(trimmed, p)
				}
			}
			if len(trimmed) > 0 {
				b, _ := json.Marshal(trimmed)
				eventsJSON = string(b)
			}
		}

		wh := &models.Webhook{
			ID:     server.GenerateWebhookID(),
			URL:    url,
			Secret: secret,
			Events: eventsJSON,
			Active: true,
		}

		if err := db.CreateWebhook(database, wh); err != nil {
			return fmt.Errorf("creating webhook: %w", err)
		}

		if jsonOutput {
			return printJSON(wh)
		}

		fmt.Printf("Registered webhook %s → %s\n", wh.ID, wh.URL)
		if eventsFlag != "" {
			fmt.Printf("  Events: %s\n", eventsFlag)
		} else {
			fmt.Printf("  Events: all\n")
		}
		if secret != "" {
			fmt.Printf("  Secret: configured\n")
		}
		return nil
	},
}

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered webhooks",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		webhooks, err := db.ListWebhooks(database)
		if err != nil {
			return fmt.Errorf("listing webhooks: %w", err)
		}

		if jsonOutput {
			if webhooks == nil {
				webhooks = []models.Webhook{}
			}
			return printJSON(webhooks)
		}

		if len(webhooks) == 0 {
			fmt.Println("No webhooks registered. Use 'lifecycle webhook add <url>' to register one.")
			return nil
		}

		for _, wh := range webhooks {
			status := "active"
			if !wh.Active {
				status = "inactive"
			}
			events := "all"
			if wh.Events != "" && wh.Events != "[]" {
				events = wh.Events
			}
			hasSecret := "no"
			if wh.Secret != "" {
				hasSecret = "yes"
			}
			fmt.Printf("%-16s %-8s secret=%-3s events=%-20s %s\n", wh.ID, status, hasSecret, events, wh.URL)
		}
		return nil
	},
}

var webhookRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a webhook",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.DeleteWebhook(database, args[0]); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]string{"removed": args[0]})
		}

		fmt.Printf("Removed webhook %s\n", args[0])
		return nil
	},
}

var webhookTestCmd = &cobra.Command{
	Use:   "test <id>",
	Short: "Send a test event to verify a webhook works",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		wh, err := db.GetWebhook(database, args[0])
		if err != nil {
			return fmt.Errorf("webhook %q not found: %w", args[0], err)
		}

		statusCode, err := server.SendTestWebhook(wh)
		if err != nil {
			if jsonOutput {
				return printJSON(map[string]any{
					"webhook_id": wh.ID,
					"url":        wh.URL,
					"success":    false,
					"error":      err.Error(),
				})
			}
			return fmt.Errorf("test delivery to %s failed: %w", wh.URL, err)
		}

		success := statusCode >= 200 && statusCode < 300

		if jsonOutput {
			return printJSON(map[string]any{
				"webhook_id":  wh.ID,
				"url":         wh.URL,
				"status_code": statusCode,
				"success":     success,
			})
		}

		if success {
			fmt.Printf("✓ Test delivery to %s succeeded (HTTP %d)\n", wh.URL, statusCode)
		} else {
			fmt.Printf("✗ Test delivery to %s returned HTTP %d\n", wh.URL, statusCode)
		}
		return nil
	},
}
