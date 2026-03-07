package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

// discussionTemplate holds metadata and content for a discussion template.
type discussionTemplate struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

var discussionTemplates = map[string]discussionTemplate{
	"rfc": {
		Name:        "rfc",
		Description: "Request for Comments",
		Body: `## Problem

_Describe the problem or need this RFC addresses._

## Proposed Solution

_Describe the proposed approach in detail._

## Alternatives

_What other approaches were considered? Why were they rejected?_

## Open Questions

- _Question 1_
- _Question 2_
`,
	},
	"adr": {
		Name:        "adr",
		Description: "Architecture Decision Record",
		Body: `## Context

_What is the issue that we're seeing that is motivating this decision or change?_

## Decision

_What is the change that we're proposing and/or doing?_

## Consequences

_What becomes easier or more difficult to do because of this change?_
`,
	},
	"bug-report": {
		Name:        "bug-report",
		Description: "Bug Report",
		Body: `## Steps to Reproduce

1. _Step 1_
2. _Step 2_
3. _Step 3_

## Expected Behavior

_What should happen?_

## Actual Behavior

_What actually happens?_

## Environment

- OS: _e.g., macOS 14.0_
- Version: _e.g., v1.2.3_
- Other relevant details: _..._
`,
	},
	"retro": {
		Name:        "retro",
		Description: "Retrospective",
		Body: `## What Went Well

- _Item 1_
- _Item 2_

## What Didn't Go Well

- _Item 1_
- _Item 2_

## Action Items

- [ ] _Action 1_
- [ ] _Action 2_
`,
	},
	"design": {
		Name:        "design",
		Description: "Design Review",
		Body: `## Overview

_Brief summary of the design being proposed._

## Goals

- _Goal 1_
- _Goal 2_

## Non-goals

- _Non-goal 1_
- _Non-goal 2_

## Design

_Detailed description of the design. Include diagrams, API shapes, data models, etc._

## Risks

- _Risk 1_
- _Risk 2_
`,
	},
}

var discussCmd = &cobra.Command{
	Use:   "discuss",
	Short: "RFC-style discussions for agent collaboration",
}

func init() {
	discussCmd.AddCommand(discussNewCmd)
	discussCmd.AddCommand(discussListCmd)
	discussCmd.AddCommand(discussShowCmd)
	discussCmd.AddCommand(discussCommentCmd)
	discussCmd.AddCommand(discussResolveCmd)
	discussCmd.AddCommand(discussTemplatesCmd)

	discussNewCmd.Flags().String("feature", "", "Link to feature ID")
	discussNewCmd.Flags().String("author", "agent", "Author name/ID")
	discussNewCmd.Flags().String("template", "", "Use a discussion template (rfc, adr, bug-report, retro, design)")

	discussListCmd.Flags().String("feature", "", "Filter by feature ID")
	discussListCmd.Flags().String("status", "", "Filter by status (open/resolved/merged/closed)")

	discussCommentCmd.Flags().String("author", "agent", "Comment author")
	discussCommentCmd.Flags().String("type", "comment", "Comment type (comment/proposal/approval/objection/revision/decision)")
	discussCommentCmd.Flags().Int("reply-to", 0, "Reply to comment ID (for threading)")
}

var discussNewCmd = &cobra.Command{
	Use:   "new <title>",
	Short: "Start a new discussion/RFC",
	Args:  cobra.ExactArgs(1),
	Example: `  # Start an RFC discussion
  lifecycle discuss new "RFC: Authentication Strategy" --feature user-auth --author architect-agent

  # Add a typed comment
  lifecycle discuss comment 1 "I propose using JWT tokens" --type proposal --author design-agent

  # Approve or object
  lifecycle discuss comment 1 "Agreed" --type approval --author review-agent`,
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

		featureID, _ := cmd.Flags().GetString("feature")
		author, _ := cmd.Flags().GetString("author")
		templateName, _ := cmd.Flags().GetString("template")

		var body string
		if templateName != "" {
			tmpl, ok := discussionTemplates[templateName]
			if !ok {
				names := sortedTemplateNames()
				return fmt.Errorf("unknown template %q (available: %s)", templateName, strings.Join(names, ", "))
			}
			body = tmpl.Body
		}

		d := &models.Discussion{
			ProjectID: p.ID,
			FeatureID: featureID,
			Title:     args[0],
			Body:      body,
			Author:    author,
			Status:    "open",
		}
		if err := db.CreateDiscussion(database, d); err != nil {
			return fmt.Errorf("creating discussion: %w", err)
		}

		_ = db.InsertEvent(database, &models.Event{
			ProjectID: p.ID,
			FeatureID: featureID,
			EventType: "discussion.created",
			Data:      fmt.Sprintf(`{"id":%d,"title":%q,"author":%q}`, d.ID, d.Title, author),
		})

		if jsonOutput {
			return printJSON(d)
		}
		fmt.Printf("✓ Created discussion #%d: %s\n", d.ID, d.Title)
		return nil
	},
}

var discussListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discussions",
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

		featureID, _ := cmd.Flags().GetString("feature")
		status, _ := cmd.Flags().GetString("status")

		discussions, err := db.ListDiscussions(database, p.ID, featureID, status)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(discussions)
		}

		if len(discussions) == 0 {
			fmt.Println("No discussions found.")
			return nil
		}

		fmt.Printf("%-4s %-10s %-12s %-4s %s\n", "ID", "STATUS", "AUTHOR", "💬", "TITLE")
		fmt.Println(strings.Repeat("─", 70))
		for _, d := range discussions {
			fmt.Printf("%-4d %-10s %-12s %-4d %s\n", d.ID, d.Status, d.Author, d.CommentCount, d.Title)
		}
		return nil
	},
}

var discussShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show discussion with all comments",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid discussion ID: %s", args[0])
		}

		d, err := db.GetDiscussion(database, id)
		if err != nil {
			return fmt.Errorf("discussion not found: %d", id)
		}

		if jsonOutput {
			return printJSON(d)
		}

		fmt.Printf("Discussion #%d: %s\n", d.ID, d.Title)
		fmt.Printf("  Status: %s | Author: %s | Created: %s\n", d.Status, d.Author, d.CreatedAt)
		if d.FeatureID != "" {
			fmt.Printf("  Feature: %s\n", d.FeatureID)
		}
		fmt.Println(strings.Repeat("─", 60))
		for _, c := range d.Comments {
			prefix := ""
			if c.ParentID > 0 {
				prefix = "  ↳ "
			}
			typeTag := ""
			if c.CommentType != "comment" {
				typeTag = fmt.Sprintf(" [%s]", c.CommentType)
			}
			fmt.Printf("%s#%d %s%s (%s):\n", prefix, c.ID, c.Author, typeTag, c.CreatedAt)
			for _, line := range strings.Split(c.Content, "\n") {
				fmt.Printf("%s  %s\n", prefix, line)
			}
			fmt.Println()
		}
		return nil
	},
}

var discussCommentCmd = &cobra.Command{
	Use:   "comment <discussion-id> <content>",
	Short: "Add a comment to a discussion",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		discussionID, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid discussion ID: %s", args[0])
		}

		author, _ := cmd.Flags().GetString("author")
		commentType, _ := cmd.Flags().GetString("type")
		replyTo, _ := cmd.Flags().GetInt("reply-to")

		c := &models.DiscussionComment{
			DiscussionID: discussionID,
			Author:       author,
			Content:      args[1],
			ParentID:     replyTo,
			CommentType:  commentType,
		}
		if err := db.AddDiscussionComment(database, c); err != nil {
			return fmt.Errorf("adding comment: %w", err)
		}

		p, _ := db.GetProject(database)
		if p != nil {
			d, _ := db.GetDiscussion(database, discussionID)
			featureID := ""
			if d != nil {
				featureID = d.FeatureID
			}
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				FeatureID: featureID,
				EventType: "discussion.commented",
				Data:      fmt.Sprintf(`{"discussion_id":%d,"comment_id":%d,"author":%q,"type":%q}`, discussionID, c.ID, author, commentType),
			})
		}

		if jsonOutput {
			return printJSON(c)
		}
		fmt.Printf("✓ Added %s #%d to discussion #%d\n", commentType, c.ID, discussionID)
		return nil
	},
}

var discussResolveCmd = &cobra.Command{
	Use:   "resolve <id> [status]",
	Short: "Resolve or close a discussion (default: resolved)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid discussion ID: %s", args[0])
		}

		status := "resolved"
		if len(args) > 1 {
			status = args[1]
		}

		if err := db.UpdateDiscussionStatus(database, id, status); err != nil {
			return err
		}

		p, _ := db.GetProject(database)
		if p != nil {
			_ = db.InsertEvent(database, &models.Event{
				ProjectID: p.ID,
				EventType: "discussion.resolved",
				Data:      fmt.Sprintf(`{"discussion_id":%d,"status":%q}`, id, status),
			})
		}

		if jsonOutput {
			d, _ := db.GetDiscussion(database, id)
			return printJSON(d)
		}
		fmt.Printf("✓ Discussion #%d → %s\n", id, status)
		return nil
	},
}

var discussTemplatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List available discussion templates",
	RunE: func(_ *cobra.Command, _ []string) error {
		names := sortedTemplateNames()

		if jsonOutput {
			out := make([]discussionTemplate, 0, len(names))
			for _, n := range names {
				out = append(out, discussionTemplates[n])
			}
			return printJSON(out)
		}

		fmt.Printf("%-12s %s\n", "NAME", "DESCRIPTION")
		fmt.Println(strings.Repeat("─", 40))
		for _, n := range names {
			t := discussionTemplates[n]
			fmt.Printf("%-12s %s\n", t.Name, t.Description)
		}
		fmt.Printf("\nUse --template <name> with 'discuss new' to apply a template.\n")
		return nil
	},
}

func sortedTemplateNames() []string {
	names := make([]string, 0, len(discussionTemplates))
	for k := range discussionTemplates {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
