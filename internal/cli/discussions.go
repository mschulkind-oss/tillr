package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
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
	discussCmd.AddCommand(discussTemplateAddCmd)
	discussCmd.AddCommand(discussTemplateShowCmd)
	discussCmd.AddCommand(discussTemplateRemoveCmd)
	discussCmd.AddCommand(discussVoteCmd)
	discussCmd.AddCommand(discussVotesCmd)

	discussNewCmd.Flags().String("feature", "", "Link to feature ID")
	discussNewCmd.Flags().String("author", "agent", "Author name/ID")
	discussNewCmd.Flags().String("template", "", "Use a discussion template (rfc, adr, bug-report, retro, design)")

	discussListCmd.Flags().String("feature", "", "Filter by feature ID")
	discussListCmd.Flags().String("status", "", "Filter by status (open/resolved/merged/closed)")

	discussCommentCmd.Flags().String("author", "agent", "Comment author")
	discussCommentCmd.Flags().String("type", "comment", "Comment type (comment/proposal/approval/objection/revision/decision)")
	discussCommentCmd.Flags().Int("reply-to", 0, "Reply to comment ID (for threading)")

	discussVoteCmd.Flags().String("voter", "agent", "Voter name/ID")

	discussCmd.AddCommand(discussPollCmd)
	discussCmd.AddCommand(discussReactCmd)

	discussPollCmd.AddCommand(discussPollCreateCmd)
	discussPollCmd.AddCommand(discussPollVoteCmd)
	discussPollCmd.AddCommand(discussPollResultsCmd)
	discussPollCmd.AddCommand(discussPollCloseCmd)

	discussPollCreateCmd.Flags().String("type", "single", "Poll type: single or multiple choice")
	discussPollCreateCmd.Flags().String("created-by", "agent", "Poll creator name/ID")
	discussPollVoteCmd.Flags().String("voter", "agent", "Voter name/ID")
	discussReactCmd.Flags().String("voter", "agent", "Voter name/ID")
}

var discussNewCmd = &cobra.Command{
	Use:   "new <title>",
	Short: "Start a new discussion/RFC",
	Args:  cobra.ExactArgs(1),
	Example: `  # Start an RFC discussion
  tillr discuss new "RFC: Authentication Strategy" --feature user-auth --author architect-agent

  # Add a typed comment
  tillr discuss comment 1 "I propose using JWT tokens" --type proposal --author design-agent

  # Approve or object
  tillr discuss comment 1 "Agreed" --type approval --author review-agent`,
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
			if ok {
				body = tmpl.Body
			} else {
				// Check DB templates
				dbTmpl, dbErr := db.GetDiscussionTemplate(database, templateName)
				if dbErr != nil {
					names := sortedTemplateNames()
					return fmt.Errorf("unknown template %q (available: %s)", templateName, strings.Join(names, ", "))
				}
				body = dbTmpl.Body
			}
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

		fmt.Printf("%-4s %-10s %-12s %-4s %-6s %s\n", "ID", "STATUS", "AUTHOR", "💬", "VOTES", "TITLE")
		fmt.Println(strings.Repeat("─", 78))
		for _, d := range discussions {
			voteStr := ""
			if len(d.Votes) > 0 {
				parts := make([]string, 0, len(d.Votes))
				for r, c := range d.Votes {
					parts = append(parts, fmt.Sprintf("%s%d", r, c))
				}
				voteStr = strings.Join(parts, " ")
			}
			fmt.Printf("%-4d %-10s %-12s %-4d %-6s %s\n", d.ID, d.Status, d.Author, d.CommentCount, voteStr, d.Title)
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
		if len(d.Votes) > 0 {
			parts := make([]string, 0, len(d.Votes))
			for r, c := range d.Votes {
				parts = append(parts, fmt.Sprintf("%s %d", r, c))
			}
			fmt.Printf("  Votes: %s\n", strings.Join(parts, "  "))
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
		// Combine built-in and DB templates
		allTemplates := getAllTemplates()

		if jsonOutput {
			return printJSON(allTemplates)
		}

		if len(allTemplates) == 0 {
			fmt.Println("No templates found.")
			return nil
		}

		fmt.Printf("%-12s %-8s %s\n", "NAME", "SOURCE", "DESCRIPTION")
		fmt.Println(strings.Repeat("─", 50))
		for _, t := range allTemplates {
			source := "custom"
			if _, ok := discussionTemplates[t.Name]; ok {
				source = "builtin"
			}
			fmt.Printf("%-12s %-8s %s\n", t.Name, source, t.Description)
		}
		fmt.Printf("\nUse --template <name> with 'discuss new' to apply a template.\n")
		return nil
	},
}

var discussTemplateAddCmd = &cobra.Command{
	Use:   "template-add <name> <description> <body>",
	Short: "Create a custom discussion template",
	Args:  cobra.ExactArgs(3),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		t := &models.DiscussionTemplate{
			Name:        args[0],
			Description: args[1],
			Body:        args[2],
			IsBuiltin:   false,
		}
		if err := db.CreateDiscussionTemplate(database, t); err != nil {
			return fmt.Errorf("creating template: %w", err)
		}

		if jsonOutput {
			return printJSON(t)
		}
		fmt.Printf("Created template: %s\n", t.Name)
		return nil
	},
}

var discussTemplateShowCmd = &cobra.Command{
	Use:   "template-show <name>",
	Short: "Show a discussion template",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name := args[0]

		// Check built-in first
		if tmpl, ok := discussionTemplates[name]; ok {
			if jsonOutput {
				return printJSON(tmpl)
			}
			fmt.Printf("Template: %s (builtin)\n", tmpl.Name)
			fmt.Printf("Description: %s\n", tmpl.Description)
			fmt.Println(strings.Repeat("─", 40))
			fmt.Println(tmpl.Body)
			return nil
		}

		// Check DB
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		t, err := db.GetDiscussionTemplate(database, name)
		if err != nil {
			return fmt.Errorf("template %q not found", name)
		}

		if jsonOutput {
			return printJSON(t)
		}
		fmt.Printf("Template: %s (custom)\n", t.Name)
		fmt.Printf("Description: %s\n", t.Description)
		fmt.Println(strings.Repeat("─", 40))
		fmt.Println(t.Body)
		return nil
	},
}

var discussTemplateRemoveCmd = &cobra.Command{
	Use:   "template-rm <name>",
	Short: "Remove a custom discussion template",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		name := args[0]

		if _, ok := discussionTemplates[name]; ok {
			return fmt.Errorf("cannot remove built-in template %q", name)
		}

		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.DeleteDiscussionTemplate(database, name); err != nil {
			return fmt.Errorf("removing template: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"removed": name})
		}
		fmt.Printf("Removed template: %s\n", name)
		return nil
	},
}

// getAllTemplates merges built-in and DB templates, with DB overriding built-in.
func getAllTemplates() []discussionTemplate {
	names := sortedTemplateNames()
	result := make([]discussionTemplate, 0, len(names))
	for _, n := range names {
		result = append(result, discussionTemplates[n])
	}

	// Try to load DB templates
	database, _, err := openDB()
	if err != nil {
		return result
	}
	defer database.Close() //nolint:errcheck

	dbTemplates, err := db.ListDiscussionTemplates(database)
	if err != nil {
		return result
	}

	seen := make(map[string]bool)
	for _, t := range result {
		seen[t.Name] = true
	}
	for _, t := range dbTemplates {
		if !seen[t.Name] {
			result = append(result, discussionTemplate{
				Name:        t.Name,
				Description: t.Description,
				Body:        t.Body,
			})
		}
	}
	return result
}

func sortedTemplateNames() []string {
	names := make([]string, 0, len(discussionTemplates))
	for k := range discussionTemplates {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

var discussVoteCmd = &cobra.Command{
	Use:   "vote <discussion-id> <reaction>",
	Short: "Add a reaction to a discussion (👍 👎 🎉 ❤️ 🤔)",
	Args:  cobra.ExactArgs(2),
	Example: `  # Add a thumbs-up reaction
  tillr discuss vote 1 👍
  tillr discuss vote 1 👍 --voter design-agent`,
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

		reaction := args[1]
		if !db.ValidReactions[reaction] {
			valid := make([]string, 0, len(db.ValidReactions))
			for r := range db.ValidReactions {
				valid = append(valid, r)
			}
			sort.Strings(valid)
			return fmt.Errorf("invalid reaction %q (valid: %s)", reaction, strings.Join(valid, " "))
		}

		voter, _ := cmd.Flags().GetString("voter")

		v := &models.DiscussionVote{
			DiscussionID: discussionID,
			Voter:        voter,
			Reaction:     reaction,
		}
		if err := db.AddDiscussionVote(database, v); err != nil {
			return fmt.Errorf("adding vote: %w", err)
		}

		if jsonOutput {
			return printJSON(v)
		}
		fmt.Printf("✓ %s reacted %s to discussion #%d\n", voter, reaction, discussionID)
		return nil
	},
}

var discussVotesCmd = &cobra.Command{
	Use:   "votes <discussion-id>",
	Short: "Show vote counts for a discussion",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		discussionID, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid discussion ID: %s", args[0])
		}

		summary, err := db.GetDiscussionVotes(database, discussionID)
		if err != nil {
			return fmt.Errorf("getting votes: %w", err)
		}

		if jsonOutput {
			return printJSON(summary)
		}

		if summary.Total == 0 {
			fmt.Printf("Discussion #%d has no votes yet.\n", discussionID)
			return nil
		}

		fmt.Printf("Votes for discussion #%d (%d total):\n", discussionID, summary.Total)
		for reaction, count := range summary.Counts {
			fmt.Printf("  %s  %d\n", reaction, count)
		}
		return nil
	},
}

// --- Poll Commands ---

var discussPollCmd = &cobra.Command{
	Use:   "poll",
	Short: "Manage polls within discussions",
}

var discussPollCreateCmd = &cobra.Command{
	Use:     "create <discussion-id> <question> <option1> <option2> [option3...]",
	Short:   "Create a poll in a discussion",
	Args:    cobra.MinimumNArgs(4),
	Example: `  tillr discuss poll create 1 "Which API style?" "REST" "GraphQL" "gRPC"`,
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

		if _, err := db.GetDiscussion(database, discussionID); err != nil {
			return fmt.Errorf("discussion #%d not found", discussionID)
		}

		pollType, _ := cmd.Flags().GetString("type")
		createdBy, _ := cmd.Flags().GetString("created-by")

		poll := &models.DiscussionPoll{
			DiscussionID: discussionID,
			Question:     args[1],
			PollType:     pollType,
			CreatedBy:    createdBy,
		}
		options := args[2:]

		if err := db.CreateDiscussionPoll(database, poll, options); err != nil {
			return fmt.Errorf("creating poll: %w", err)
		}

		if jsonOutput {
			return printJSON(poll)
		}
		fmt.Printf("Created poll #%d: %s\n", poll.ID, poll.Question)
		for _, opt := range poll.Options {
			fmt.Printf("  [%d] %s\n", opt.ID, opt.Label)
		}
		return nil
	},
}

var discussPollVoteCmd = &cobra.Command{
	Use:   "vote <poll-id> <option-id>",
	Short: "Vote on a poll option",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		pollID, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid poll ID: %s", args[0])
		}
		optionID, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid option ID: %s", args[1])
		}

		voter, _ := cmd.Flags().GetString("voter")
		if err := db.VoteOnPoll(database, pollID, optionID, voter); err != nil {
			return fmt.Errorf("voting: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"poll_id": pollID, "option_id": optionID, "voter": voter})
		}
		fmt.Printf("%s voted on poll #%d, option #%d\n", voter, pollID, optionID)
		return nil
	},
}

var discussPollResultsCmd = &cobra.Command{
	Use:   "results <poll-id>",
	Short: "Show poll results",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		pollID, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid poll ID: %s", args[0])
		}

		poll, err := db.GetDiscussionPoll(database, pollID)
		if err != nil {
			return fmt.Errorf("poll #%d not found", pollID)
		}

		if jsonOutput {
			return printJSON(poll)
		}

		fmt.Printf("Poll #%d: %s [%s]\n", poll.ID, poll.Question, poll.Status)
		fmt.Printf("Type: %s | Created by: %s\n\n", poll.PollType, poll.CreatedBy)

		totalVotes := 0
		for _, opt := range poll.Options {
			totalVotes += opt.Votes
		}

		for _, opt := range poll.Options {
			if totalVotes > 0 {
				pct := float64(opt.Votes) / float64(totalVotes) * 100
				barLen := int(pct / 5)
				bar := strings.Repeat("#", barLen) + strings.Repeat(".", 20-barLen)
				fmt.Printf("  [%d] %-20s %s %d (%.0f%%)\n", opt.ID, opt.Label, bar, opt.Votes, pct)
			} else {
				fmt.Printf("  [%d] %-20s %d votes\n", opt.ID, opt.Label, opt.Votes)
			}
		}
		fmt.Printf("\nTotal votes: %d\n", totalVotes)
		return nil
	},
}

var discussPollCloseCmd = &cobra.Command{
	Use:   "close <poll-id>",
	Short: "Close a poll",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		pollID, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid poll ID: %s", args[0])
		}

		if err := db.CloseDiscussionPoll(database, pollID); err != nil {
			return fmt.Errorf("closing poll: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"poll_id": pollID, "status": "closed"})
		}
		fmt.Printf("Closed poll #%d\n", pollID)
		return nil
	},
}

var discussReactCmd = &cobra.Command{
	Use:   "react <discussion-id> <reaction>",
	Short: "Add a reaction to a discussion (same as vote)",
	Args:  cobra.ExactArgs(2),
	Example: `  tillr discuss react 1 thumbsup
  tillr discuss react 1 heart --voter human`,
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

		reaction := args[1]
		if !db.ValidReactions[reaction] {
			valid := make([]string, 0, len(db.ValidReactions))
			for r := range db.ValidReactions {
				valid = append(valid, r)
			}
			sort.Strings(valid)
			return fmt.Errorf("invalid reaction %q (valid: %s)", reaction, strings.Join(valid, " "))
		}

		voter, _ := cmd.Flags().GetString("voter")
		v := &models.DiscussionVote{
			DiscussionID: discussionID,
			Voter:        voter,
			Reaction:     reaction,
		}
		if err := db.AddDiscussionVote(database, v); err != nil {
			return fmt.Errorf("adding reaction: %w", err)
		}

		if jsonOutput {
			return printJSON(v)
		}
		fmt.Printf("%s reacted %s to discussion #%d\n", voter, reaction, discussionID)
		return nil
	},
}
