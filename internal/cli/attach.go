package cli

import (
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

var attachLabel string

var attachCmd = &cobra.Command{
	Use:   "attach <entity-type:entity-id> <path-to-image>",
	Short: "Attach an image or file to a feature, workstream, or note",
	Long: `Attach a file (typically an image) to a project entity.

The first argument specifies the entity in the format type:id, where type is
one of: feature, workstream, note. If no colon is present, "feature" is assumed.

Examples:
  tillr attach my-feature screenshot.png --label "UI mockup"
  tillr attach feature:my-feature screenshot.png
  tillr attach workstream:ws-123 diagram.png --label "architecture"`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, cfg, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Parse entity reference
		entityType := "feature"
		entityID := args[0]
		if idx := indexOf(entityID, ':'); idx >= 0 {
			entityType = entityID[:idx]
			entityID = entityID[idx+1:]
		}

		// Validate entity type
		switch entityType {
		case "feature", "workstream", "note":
		default:
			return fmt.Errorf("invalid entity type %q (must be feature, workstream, or note)", entityType)
		}

		srcPath := args[1]

		// Check source file exists
		info, err := os.Stat(srcPath)
		if err != nil {
			return fmt.Errorf("cannot read file %q: %w", srcPath, err)
		}
		if info.IsDir() {
			return fmt.Errorf("%q is a directory, not a file", srcPath)
		}

		// Determine content type
		ext := filepath.Ext(srcPath)
		contentType := mime.TypeByExtension(ext)
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// Build filename
		slug := slugify(entityID)
		ts := time.Now().Format("20060102-150405")
		filename := fmt.Sprintf("%s-%s%s", ts, slug, ext)

		// Create attachments directory under .tillr/
		attachDir := filepath.Join(cfg.ProjectDir, ".tillr", "attachments")
		if err := os.MkdirAll(attachDir, 0o755); err != nil {
			return fmt.Errorf("creating attachment directory: %w", err)
		}

		// Copy file
		src, err := os.Open(srcPath)
		if err != nil {
			return fmt.Errorf("opening source file: %w", err)
		}
		defer src.Close() //nolint:errcheck

		dst, err := os.Create(filepath.Join(attachDir, filename))
		if err != nil {
			return fmt.Errorf("creating destination file: %w", err)
		}
		defer dst.Close() //nolint:errcheck

		if _, err := io.Copy(dst, src); err != nil {
			return fmt.Errorf("copying file: %w", err)
		}

		// Create DB record
		att := &models.Attachment{
			EntityType:   entityType,
			EntityID:     entityID,
			Filename:     filename,
			OriginalName: filepath.Base(srcPath),
			Label:        attachLabel,
			ContentType:  contentType,
		}
		if err := db.CreateAttachment(database, att); err != nil {
			return fmt.Errorf("recording attachment: %w", err)
		}

		if jsonOutput {
			return printJSON(att)
		}

		fmt.Printf("Attached %s to %s:%s\n", att.OriginalName, entityType, entityID)
		fmt.Printf("  Filename: %s\n", filename)
		fmt.Printf("  Path:     %s\n", filepath.Join(attachDir, filename))
		if att.Label != "" {
			fmt.Printf("  Label:    %s\n", att.Label)
		}
		fmt.Printf("  URL:      /api/attachments/%s\n", filename)
		return nil
	},
}

func init() {
	attachCmd.Flags().StringVar(&attachLabel, "label", "", "Description/label for the attachment")
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// slugify converts a name to a URL-safe slug (reuses server logic concept).
func slugify(name string) string {
	var b []byte
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b = append(b, byte(r))
		} else if r >= 'A' && r <= 'Z' {
			b = append(b, byte(r-'A'+'a'))
		} else if r == ' ' || r == '-' || r == '_' {
			b = append(b, '-')
		}
	}
	// Collapse multiple dashes
	result := string(b)
	for len(result) > 0 && result[0] == '-' {
		result = result[1:]
	}
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	return result
}
