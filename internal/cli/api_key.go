package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/config"
	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/spf13/cobra"
)

var apiKeyCmd = &cobra.Command{
	Use:   "api-key",
	Short: "Manage API authentication tokens",
}

func init() {
	apiKeyCmd.AddCommand(apiKeyCreateCmd)
	apiKeyCmd.AddCommand(apiKeyListCmd)
	apiKeyCmd.AddCommand(apiKeyRevokeCmd)

	apiKeyCreateCmd.Flags().StringSlice("scopes", []string{"read", "write"}, "Token scopes (read, write, admin, agent)")
	apiKeyCreateCmd.Flags().String("expires", "", "Expiration duration (e.g. 30d, 90d, 1y)")
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func parseDuration(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}

	var amount int
	var unit string
	for i, c := range s {
		if c < '0' || c > '9' {
			var err error
			amount, err = strconv.Atoi(s[:i])
			if err != nil {
				return time.Time{}, fmt.Errorf("invalid duration %q", s)
			}
			unit = s[i:]
			break
		}
	}
	if amount == 0 {
		return time.Time{}, fmt.Errorf("invalid duration %q", s)
	}

	now := time.Now().UTC()
	switch strings.ToLower(unit) {
	case "d", "day", "days":
		return now.AddDate(0, 0, amount), nil
	case "w", "week", "weeks":
		return now.AddDate(0, 0, amount*7), nil
	case "m", "month", "months":
		return now.AddDate(0, amount, 0), nil
	case "y", "year", "years":
		return now.AddDate(amount, 0, 0), nil
	default:
		return time.Time{}, fmt.Errorf("unknown duration unit %q (use d, w, m, or y)", unit)
	}
}

var validScopes = map[string]bool{
	"read": true, "write": true, "admin": true, "agent": true,
}

var apiKeyCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new API token",
	Args:  cobra.ExactArgs(1),
	Example: `  tillr api-key create "CI Pipeline" --scopes read,write
  tillr api-key create "Agent Token" --scopes read,write,agent --expires 90d`,
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

		name := args[0]
		scopes, _ := cmd.Flags().GetStringSlice("scopes")
		expiresStr, _ := cmd.Flags().GetString("expires")

		// Validate scopes
		for _, s := range scopes {
			if !validScopes[s] {
				return fmt.Errorf("invalid scope %q. Valid scopes: read, write, admin, agent", s)
			}
		}

		// Parse expiration
		var expiresAt string
		if expiresStr != "" {
			t, err := parseDuration(expiresStr)
			if err != nil {
				return err
			}
			expiresAt = t.Format("2006-01-02 15:04:05")
		}

		// Generate token
		token, err := config.GenerateAPIKey()
		if err != nil {
			return fmt.Errorf("generating token: %w", err)
		}

		tokenHash := hashToken(token)
		id, err := db.CreateAPIToken(database, p.ID, name, tokenHash, scopes, expiresAt)
		if err != nil {
			return fmt.Errorf("creating token: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"id":         id,
				"name":       name,
				"token":      token,
				"scopes":     scopes,
				"expires_at": expiresAt,
			})
		}

		fmt.Printf("Created API token %q (id: %d)\n", name, id)
		fmt.Printf("Token: %s\n", token)
		fmt.Printf("Scopes: %s\n", strings.Join(scopes, ", "))
		if expiresAt != "" {
			fmt.Printf("Expires: %s\n", expiresAt)
		}
		fmt.Println("\nSave this token now - it cannot be retrieved later.")
		fmt.Println("Use in requests: Authorization: Bearer <token>")
		return nil
	},
}

var apiKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API tokens",
	RunE: func(_ *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		tokens, err := db.ListAPITokens(database, p.ID)
		if err != nil {
			return fmt.Errorf("listing tokens: %w", err)
		}

		if jsonOutput {
			return printJSON(tokens)
		}

		if len(tokens) == 0 {
			fmt.Println("No API tokens. Create one with: tillr api-key create <name>")
			return nil
		}

		fmt.Printf("%-6s %-20s %-20s %-12s %-10s\n", "ID", "Name", "Scopes", "Expires", "Status")
		fmt.Println(strings.Repeat("-", 70))
		for _, t := range tokens {
			status := "active"
			if t.RevokedAt != "" {
				status = "revoked"
			} else if t.ExpiresAt != "" {
				exp, _ := time.Parse("2006-01-02 15:04:05", t.ExpiresAt)
				if !exp.IsZero() && time.Now().After(exp) {
					status = "expired"
				}
			}
			expires := "(never)"
			if t.ExpiresAt != "" {
				expires = t.ExpiresAt[:10]
			}
			fmt.Printf("%-6d %-20s %-20s %-12s %-10s\n",
				t.ID, t.Name, strings.Join(t.Scopes, ","), expires, status)
		}
		return nil
	},
}

var apiKeyRevokeCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke an API token",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid token ID %q: must be an integer", args[0])
		}

		if err := db.RevokeAPIToken(database, id); err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(map[string]any{"id": id, "revoked": true})
		}
		fmt.Printf("Revoked API token %d\n", id)
		return nil
	},
}
