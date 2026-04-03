package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/engine"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
)

var workstreamCmd = &cobra.Command{
	Use:     "workstream",
	Aliases: []string{"ws"},
	Short:   "Manage workstreams (human-tracked threads of work)",
}

func init() {
	workstreamCmd.AddCommand(workstreamCreateCmd)
	workstreamCmd.AddCommand(workstreamEditCmd)
	workstreamCmd.AddCommand(workstreamListCmd)
	workstreamCmd.AddCommand(workstreamShowCmd)
	workstreamCmd.AddCommand(workstreamNoteCmd)
	workstreamCmd.AddCommand(workstreamResolveCmd)
	workstreamCmd.AddCommand(workstreamLinkCmd)
	workstreamCmd.AddCommand(workstreamCloseCmd)

	workstreamCreateCmd.Flags().String("description", "", "Workstream description")
	workstreamCreateCmd.Flags().String("tags", "", "Comma-separated tags")
	workstreamCreateCmd.Flags().String("parent", "", "Parent workstream ID")
	workstreamCreateCmd.Flags().String("id", "", "Vanity slug (auto-generated from name if omitted)")
	workstreamCreateCmd.Flags().Int("priority", 0, "Sort priority (higher = shown first)")

	workstreamEditCmd.Flags().String("name", "", "Rename workstream")
	workstreamEditCmd.Flags().String("description", "", "Update description")
	workstreamEditCmd.Flags().String("tags", "", "Update tags")
	workstreamEditCmd.Flags().Int("priority", -1, "Sort priority (higher = shown first)")

	workstreamListCmd.Flags().Bool("all", false, "Include closed/archived workstreams")

	workstreamNoteCmd.Flags().String("type", "note", "Note type (note|question|decision|idea|import)")
	workstreamNoteCmd.Flags().String("source", "", "Note source (e.g. slack)")

	workstreamLinkCmd.Flags().String("feature", "", "Link to feature ID (owned by this workstream)")
	workstreamLinkCmd.Flags().String("depends", "", "Link to feature ID (workstream depends on this)")
	workstreamLinkCmd.Flags().String("doc", "", "Link to document path")
	workstreamLinkCmd.Flags().String("url", "", "Link to URL")
	workstreamLinkCmd.Flags().String("discussion", "", "Link to discussion ID")
	workstreamLinkCmd.Flags().String("label", "", "Link label")
}

var workstreamCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new workstream",
	Args:  cobra.ExactArgs(1),
	Example: `  tillr workstream create "Auth Redesign" --description "Migrate to OAuth2" --tags "auth,security"
  tillr workstream create "API v2" --parent auth-redesign --id api-v2`,
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

		description, _ := cmd.Flags().GetString("description")
		tags, _ := cmd.Flags().GetString("tags")
		parentID, _ := cmd.Flags().GetString("parent")
		vanityID, _ := cmd.Flags().GetString("id")
		priority, _ := cmd.Flags().GetInt("priority")

		id := vanityID
		if id == "" {
			id = engine.Slug(args[0])
		}

		w := &models.Workstream{
			ID:          id,
			ProjectID:   p.ID,
			ParentID:    parentID,
			Name:        args[0],
			Description: description,
			Status:      "active",
			Tags:        tags,
			SortOrder:   priority,
		}
		if err := db.CreateWorkstream(database, w); err != nil {
			return fmt.Errorf("creating workstream: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			EventType: "workstream.created",
			Data:      fmt.Sprintf(`{"id":%q,"name":%q}`, w.ID, w.Name),
		})

		if jsonOutput {
			return printJSON(w)
		}
		fmt.Printf("Created workstream %s: %s\n", w.ID, w.Name)
		return nil
	},
}

var workstreamEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Edit a workstream",
	Args:  cobra.ExactArgs(1),
	Example: `  tillr workstream edit my-ws --priority 10
  tillr workstream edit my-ws --name "New Name" --description "Updated desc"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		id := args[0]
		updates := make(map[string]any)

		if cmd.Flags().Changed("name") {
			v, _ := cmd.Flags().GetString("name")
			updates["name"] = v
		}
		if cmd.Flags().Changed("description") {
			v, _ := cmd.Flags().GetString("description")
			updates["description"] = v
		}
		if cmd.Flags().Changed("tags") {
			v, _ := cmd.Flags().GetString("tags")
			updates["tags"] = v
		}
		if cmd.Flags().Changed("priority") {
			v, _ := cmd.Flags().GetInt("priority")
			updates["sort_order"] = v
		}

		if len(updates) == 0 {
			return fmt.Errorf("no changes specified")
		}

		if err := db.UpdateWorkstream(database, id, updates); err != nil {
			return fmt.Errorf("updating workstream: %w", err)
		}

		ws, err := db.GetWorkstream(database, id)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(ws)
		}
		fmt.Printf("Updated workstream %s\n", ws.ID)
		return nil
	},
}

var workstreamListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workstreams",
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

		showAll, _ := cmd.Flags().GetBool("all")
		status := "active"
		if showAll {
			status = ""
		}

		workstreams, err := db.ListWorkstreams(database, p.ID, status)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(workstreams)
		}

		if len(workstreams) == 0 {
			fmt.Println("No workstreams found.")
			return nil
		}

		fmt.Printf("%-20s %-10s %-12s %s\n", "ID", "STATUS", "TAGS", "NAME")
		fmt.Println(strings.Repeat("-", 70))
		for _, w := range workstreams {
			tags := w.Tags
			if len(tags) > 12 {
				tags = tags[:9] + "..."
			}
			fmt.Printf("%-20s %-10s %-12s %s\n", w.ID, w.Status, tags, w.Name)
		}
		return nil
	},
}

var workstreamShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show workstream details with notes and links",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		detail, err := db.GetWorkstreamDetail(database, args[0])
		if err != nil {
			return fmt.Errorf("workstream %q not found", args[0])
		}

		if jsonOutput {
			return printJSON(detail)
		}

		w := detail.Workstream
		fmt.Printf("Workstream: %s\n", w.Name)
		fmt.Printf("  ID: %s | Status: %s | Created: %s\n", w.ID, w.Status, w.CreatedAt)
		if w.Description != "" {
			fmt.Printf("  Description: %s\n", w.Description)
		}
		if w.Tags != "" {
			fmt.Printf("  Tags: %s\n", w.Tags)
		}
		if w.ParentID != "" {
			fmt.Printf("  Parent: %s\n", w.ParentID)
		}

		if len(detail.Children) > 0 {
			fmt.Printf("\nChildren (%d):\n", len(detail.Children))
			for _, c := range detail.Children {
				fmt.Printf("  %-20s [%s] %s\n", c.ID, c.Status, c.Name)
			}
		}

		if len(detail.Notes) > 0 {
			fmt.Printf("\nNotes (%d):\n", len(detail.Notes))
			fmt.Println(strings.Repeat("-", 60))
			for _, n := range detail.Notes {
				resolved := ""
				if n.Resolved != 0 {
					resolved = " [resolved]"
				}
				typeTag := ""
				if n.NoteType != "note" {
					typeTag = fmt.Sprintf(" [%s]", n.NoteType)
				}
				source := ""
				if n.Source != "" {
					source = fmt.Sprintf(" (via %s)", n.Source)
				}
				fmt.Printf("  #%d%s%s%s (%s):\n", n.ID, typeTag, source, resolved, n.CreatedAt)
				for _, line := range strings.Split(n.Content, "\n") {
					fmt.Printf("    %s\n", line)
				}
			}
		}

		if len(detail.Links) > 0 {
			fmt.Printf("\nLinks (%d):\n", len(detail.Links))
			for _, l := range detail.Links {
				target := l.TargetID
				if l.TargetURL != "" {
					target = l.TargetURL
				}
				label := ""
				if l.Label != "" {
					label = fmt.Sprintf(" (%s)", l.Label)
				}
				fmt.Printf("  #%d [%s] %s%s\n", l.ID, l.LinkType, target, label)
			}
		}

		return nil
	},
}

var workstreamNoteCmd = &cobra.Command{
	Use:   "note <id> <text>",
	Short: "Add a note to a workstream",
	Args:  cobra.ExactArgs(2),
	Example: `  tillr workstream note auth-redesign "Decided to use PKCE flow"
  tillr workstream note auth-redesign "Should we support SAML?" --type question
  tillr workstream note auth-redesign "Import from Slack" --type import --source slack`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Verify workstream exists
		if _, err := db.GetWorkstream(database, args[0]); err != nil {
			return fmt.Errorf("workstream %q not found", args[0])
		}

		noteType, _ := cmd.Flags().GetString("type")
		source, _ := cmd.Flags().GetString("source")

		validTypes := map[string]bool{"note": true, "question": true, "decision": true, "idea": true, "import": true}
		if !validTypes[noteType] {
			return fmt.Errorf("invalid note type %q: must be one of note, question, decision, idea, import", noteType)
		}

		n := &models.WorkstreamNote{
			WorkstreamID: args[0],
			Content:      args[1],
			NoteType:     noteType,
			Source:       source,
		}
		if err := db.CreateWorkstreamNote(database, n); err != nil {
			return fmt.Errorf("creating note: %w", err)
		}

		p, _ := db.GetProject(database)
		if p != nil {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				EventType: "workstream.noted",
				Data:      fmt.Sprintf(`{"workstream_id":%q,"note_id":%d,"type":%q}`, args[0], n.ID, noteType),
			})
		}

		if jsonOutput {
			return printJSON(n)
		}
		fmt.Printf("Added %s #%d to workstream %s\n", noteType, n.ID, args[0])
		return nil
	},
}

var workstreamResolveCmd = &cobra.Command{
	Use:   "resolve <id> <note-id>",
	Short: "Mark a workstream note as resolved",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Verify workstream exists
		if _, err := db.GetWorkstream(database, args[0]); err != nil {
			return fmt.Errorf("workstream %q not found", args[0])
		}

		noteID, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid note ID: %s", args[1])
		}

		if err := db.UpdateWorkstreamNote(database, noteID, map[string]any{"resolved": 1}); err != nil {
			return fmt.Errorf("resolving note: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"workstream_id": args[0], "note_id": noteID, "resolved": true})
		}
		fmt.Printf("Resolved note #%d on workstream %s\n", noteID, args[0])
		return nil
	},
}

var workstreamLinkCmd = &cobra.Command{
	Use:   "link <id>",
	Short: "Link a workstream to a feature, doc, URL, or discussion",
	Args:  cobra.ExactArgs(1),
	Example: `  tillr workstream link auth-redesign --feature user-auth --label "Main feature"
  tillr workstream link auth-redesign --depends api-auth --label "Needs auth first"
  tillr workstream link auth-redesign --url "https://wiki.example.com/auth" --label "Wiki page"
  tillr workstream link auth-redesign --doc docs/auth-spec.md
  tillr workstream link auth-redesign --discussion 5`,
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		// Verify workstream exists
		if _, err := db.GetWorkstream(database, args[0]); err != nil {
			return fmt.Errorf("workstream %q not found", args[0])
		}

		featureID, _ := cmd.Flags().GetString("feature")
		dependsID, _ := cmd.Flags().GetString("depends")
		docPath, _ := cmd.Flags().GetString("doc")
		url, _ := cmd.Flags().GetString("url")
		discussionID, _ := cmd.Flags().GetString("discussion")
		label, _ := cmd.Flags().GetString("label")

		l := &models.WorkstreamLink{
			WorkstreamID: args[0],
			Label:        label,
		}

		switch {
		case featureID != "":
			l.LinkType = "feature"
			l.TargetID = featureID
		case dependsID != "":
			l.LinkType = "feature-dependency"
			l.TargetID = dependsID
		case docPath != "":
			l.LinkType = "doc"
			l.TargetURL = docPath
		case url != "":
			l.LinkType = "url"
			l.TargetURL = url
		case discussionID != "":
			l.LinkType = "discussion"
			l.TargetID = discussionID
		default:
			return fmt.Errorf("specify one of --feature, --depends, --doc, --url, or --discussion")
		}

		if err := db.CreateWorkstreamLink(database, l); err != nil {
			return fmt.Errorf("creating link: %w", err)
		}

		if jsonOutput {
			return printJSON(l)
		}

		target := l.TargetID
		if l.TargetURL != "" {
			target = l.TargetURL
		}
		fmt.Printf("Linked workstream %s -> [%s] %s\n", args[0], l.LinkType, target)
		return nil
	},
}

var workstreamCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "Close/archive a workstream",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if _, err := db.GetWorkstream(database, args[0]); err != nil {
			return fmt.Errorf("workstream %q not found", args[0])
		}

		if err := db.ArchiveWorkstream(database, args[0]); err != nil {
			return fmt.Errorf("closing workstream: %w", err)
		}

		p, _ := db.GetProject(database)
		if p != nil {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				EventType: "workstream.closed",
				Data:      fmt.Sprintf(`{"workstream_id":%q}`, args[0]),
			})
		}

		if jsonOutput {
			return printJSON(map[string]any{"workstream_id": args[0], "status": "archived"})
		}
		fmt.Printf("Closed workstream %s\n", args[0])
		return nil
	},
}
