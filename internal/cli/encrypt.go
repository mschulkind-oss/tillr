package cli

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind/tillr/internal/config"
	"github.com/mschulkind/tillr/internal/crypto"
	"github.com/spf13/cobra"
)

var encryptCmd = &cobra.Command{
	Use:   "encrypt",
	Short: "Manage field-level encryption for sensitive data",
}

func init() {
	encryptCmd.AddCommand(encryptSetKeyCmd)
	encryptCmd.AddCommand(encryptFieldCmd)
	encryptCmd.AddCommand(encryptDecryptFieldCmd)
	encryptCmd.AddCommand(encryptStatusCmd)
}

// ---------------------------------------------------------------------------
// encrypt set-key
// ---------------------------------------------------------------------------

var encryptSetKeyCmd = &cobra.Command{
	Use:   "set-key",
	Short: "Set the encryption key (stores hash in config, NOT the key itself)",
	RunE: func(cmd *cobra.Command, args []string) error {
		root, err := config.FindProjectRoot()
		if err != nil {
			return fmt.Errorf("no tillr project found. Run 'tillr init <name>' first")
		}
		cfg, err := config.Load(root)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		fmt.Fprint(os.Stderr, "Enter encryption key: ")
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return fmt.Errorf("no input received")
		}
		password := strings.TrimSpace(scanner.Text())
		if password == "" {
			return fmt.Errorf("encryption key cannot be empty")
		}

		hash := sha256.Sum256([]byte(password))
		cfg.EncryptionKeyHash = hex.EncodeToString(hash[:])
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{
				"status":   "ok",
				"key_hash": cfg.EncryptionKeyHash[:16] + "...",
			})
		}
		fmt.Fprintln(os.Stderr, "✓ Encryption key hash saved to config.")
		fmt.Fprintln(os.Stderr, "  The key itself is NOT stored — you must provide it when encrypting/decrypting.")
		return nil
	},
}

// ---------------------------------------------------------------------------
// encrypt field
// ---------------------------------------------------------------------------

var encryptFieldCmd = &cobra.Command{
	Use:   "field <table> <column> <id>",
	Short: "Encrypt a specific field value in the database",
	Long: `Encrypt a specific field value using AES-256-GCM.

The <id> parameter matches the 'id' column of the target table.
You will be prompted for the encryption key (or pipe it via stdin
after the key prompt).

Example:
  tillr encrypt field features description my-feature-id`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		table, column, id := args[0], args[1], args[2]

		if err := validateTableColumn(table, column); err != nil {
			return err
		}

		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		key, err := readEncryptionKey(cfg)
		if err != nil {
			return err
		}

		// Read current value.
		value, err := readField(database, table, column, id)
		if err != nil {
			return fmt.Errorf("reading %s.%s for id %q: %w", table, column, id, err)
		}

		if crypto.IsEncrypted(value) {
			if jsonOutput {
				return printJSON(map[string]any{"status": "already_encrypted", "table": table, "column": column, "id": id})
			}
			fmt.Printf("Field %s.%s (id=%s) is already encrypted.\n", table, column, id)
			return nil
		}

		ct, err := crypto.Encrypt(value, key)
		if err != nil {
			return fmt.Errorf("encrypting: %w", err)
		}

		if err := writeField(database, table, column, id, ct); err != nil {
			return fmt.Errorf("writing encrypted value: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"status": "encrypted", "table": table, "column": column, "id": id})
		}
		fmt.Printf("✓ Encrypted %s.%s for id %q\n", table, column, id)
		return nil
	},
}

// ---------------------------------------------------------------------------
// encrypt decrypt-field
// ---------------------------------------------------------------------------

var encryptDecryptFieldCmd = &cobra.Command{
	Use:   "decrypt-field <table> <column> <id>",
	Short: "Decrypt a specific field value and store the plaintext back",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		table, column, id := args[0], args[1], args[2]

		if err := validateTableColumn(table, column); err != nil {
			return err
		}

		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		key, err := readEncryptionKey(cfg)
		if err != nil {
			return err
		}

		value, err := readField(database, table, column, id)
		if err != nil {
			return fmt.Errorf("reading %s.%s for id %q: %w", table, column, id, err)
		}

		if !crypto.IsEncrypted(value) {
			if jsonOutput {
				return printJSON(map[string]any{"status": "not_encrypted", "table": table, "column": column, "id": id})
			}
			fmt.Printf("Field %s.%s (id=%s) is not encrypted.\n", table, column, id)
			return nil
		}

		pt, err := crypto.Decrypt(value, key)
		if err != nil {
			return fmt.Errorf("decrypting: %w", err)
		}

		if err := writeField(database, table, column, id, pt); err != nil {
			return fmt.Errorf("writing decrypted value: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"status": "decrypted", "table": table, "column": column, "id": id})
		}
		fmt.Printf("✓ Decrypted %s.%s for id %q\n", table, column, id)
		return nil
	},
}

// ---------------------------------------------------------------------------
// encrypt status
// ---------------------------------------------------------------------------

var encryptStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show encryption status (how many encrypted fields exist)",
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		type fieldCount struct {
			Table  string `json:"table"`
			Column string `json:"column"`
			Count  int    `json:"count"`
		}

		var results []fieldCount
		total := 0

		for _, tc := range encryptableFields() {
			n, err := countEncrypted(database, tc[0], tc[1])
			if err != nil {
				continue // table may not exist yet
			}
			if n > 0 {
				results = append(results, fieldCount{Table: tc[0], Column: tc[1], Count: n})
				total += n
			}
		}

		keyConfigured := cfg.EncryptionKeyHash != ""

		if jsonOutput {
			return printJSON(map[string]any{
				"key_configured":   keyConfigured,
				"encrypted_fields": results,
				"total_encrypted":  total,
			})
		}

		if keyConfigured {
			fmt.Println("Encryption key: configured ✓")
		} else {
			fmt.Println("Encryption key: not set (run 'tillr encrypt set-key')")
		}

		if total == 0 {
			fmt.Println("No encrypted fields found.")
			return nil
		}

		fmt.Printf("\nEncrypted fields (%d total):\n", total)
		for _, r := range results {
			fmt.Printf("  %-20s %-20s %d values\n", r.Table, r.Column, r.Count)
		}
		return nil
	},
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// encryptableFields returns the list of [table, column] pairs that can be
// encrypted. This allowlist prevents SQL injection via arbitrary table/column
// names and limits encryption to fields that may contain sensitive data.
func encryptableFields() [][2]string {
	return [][2]string{
		{"features", "description"},
		{"features", "spec"},
		{"roadmap_items", "description"},
		{"discussions", "body"},
		{"projects", "description"},
		{"milestones", "description"},
	}
}

func validateTableColumn(table, column string) error {
	for _, tc := range encryptableFields() {
		if tc[0] == table && tc[1] == column {
			return nil
		}
	}
	var allowed []string
	for _, tc := range encryptableFields() {
		allowed = append(allowed, tc[0]+"."+tc[1])
	}
	return fmt.Errorf("table.column %q.%q is not encryptable. Allowed: %s", table, column, strings.Join(allowed, ", "))
}

func readEncryptionKey(cfg *config.Config) ([]byte, error) {
	keyEnv := os.Getenv("TILLR_ENCRYPT_KEY")
	if keyEnv != "" {
		return crypto.DeriveKey(keyEnv), nil
	}

	fmt.Fprint(os.Stderr, "Enter encryption key: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil, fmt.Errorf("no input received")
	}
	password := strings.TrimSpace(scanner.Text())
	if password == "" {
		return nil, fmt.Errorf("encryption key cannot be empty")
	}

	// Verify against stored hash if one exists.
	if cfg.EncryptionKeyHash != "" {
		h := sha256.Sum256([]byte(password))
		if hex.EncodeToString(h[:]) != cfg.EncryptionKeyHash {
			return nil, fmt.Errorf("encryption key does not match the stored key hash. Use the same key you set with 'tillr encrypt set-key'")
		}
	}

	return crypto.DeriveKey(password), nil
}

// readField reads a single text column from a table by its id.
// The query is safe because table/column are validated against an allowlist.
func readField(database *sql.DB, table, column, id string) (string, error) {
	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = ?", column, table) //nolint:gosec
	var value sql.NullString
	if err := database.QueryRow(query, id).Scan(&value); err != nil {
		return "", err
	}
	return value.String, nil
}

// writeField updates a single text column in a table by its id.
func writeField(database *sql.DB, table, column, id, value string) error {
	query := fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", table, column) //nolint:gosec
	result, err := database.Exec(query, value, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no row found with id %q in %s", id, table)
	}
	return nil
}

func countEncrypted(database *sql.DB, table, column string) (int, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s LIKE 'enc:%%'", table, column) //nolint:gosec
	var count int
	err := database.QueryRow(query).Scan(&count)
	return count, err
}
