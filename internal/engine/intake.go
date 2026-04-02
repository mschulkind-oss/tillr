package engine

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
)

// CategorizeIdea analyzes raw idea input and categorizes it as bug/feature/improvement.
// Returns the category and a rewritten title/description.
func CategorizeIdea(idea *models.IdeaQueueItem) (ideaType string, title string, description string) {
	raw := strings.ToLower(idea.RawInput)

	// Simple heuristic categorization based on keywords
	bugKeywords := []string{"bug", "broken", "crash", "error", "fix", "fail", "wrong", "issue", "not working", "doesn't work", "500", "404"}
	improvementKeywords := []string{"improve", "enhance", "better", "optimize", "refactor", "cleanup", "performance", "slow", "faster"}

	ideaType = "feature" // default
	for _, kw := range bugKeywords {
		if strings.Contains(raw, kw) {
			ideaType = "bug"
			break
		}
	}
	if ideaType == "feature" {
		for _, kw := range improvementKeywords {
			if strings.Contains(raw, kw) {
				ideaType = "feature" // improvements are tracked as features
				break
			}
		}
	}

	// Rewrite title: capitalize and clean up
	title = idea.Title
	if title == "" {
		// Generate title from first line of raw input
		lines := strings.SplitN(idea.RawInput, "\n", 2)
		title = strings.TrimSpace(lines[0])
		if len(title) > 80 {
			title = title[:77] + "..."
		}
	}

	// Rewrite description: ensure it has structure
	description = idea.RawInput
	if !strings.Contains(description, "##") {
		// Add minimal structure
		var b strings.Builder
		switch ideaType {
		case "bug":
			b.WriteString("## Bug Report\n\n")
			b.WriteString(idea.RawInput)
			b.WriteString("\n\n## Expected Behavior\n\n_To be determined._\n")
			b.WriteString("\n## Steps to Reproduce\n\n_Extracted from description above._\n")
		default:
			b.WriteString("## Description\n\n")
			b.WriteString(idea.RawInput)
			b.WriteString("\n\n## Acceptance Criteria\n\n_To be determined based on implementation._\n")
		}
		description = b.String()
	}

	return ideaType, title, description
}

// GenerateIdeaSpec creates a basic spec from the categorized idea.
func GenerateIdeaSpec(ideaType, title, description string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)

	switch ideaType {
	case "bug":
		b.WriteString("## Problem\n\n")
		b.WriteString(description)
		b.WriteString("\n\n## Fix Approach\n\n")
		b.WriteString("Investigate root cause and implement fix.\n\n")
		b.WriteString("## Verification\n\n")
		b.WriteString("- [ ] Bug no longer reproducible\n")
		b.WriteString("- [ ] No regression in related functionality\n")
	default:
		b.WriteString("## Overview\n\n")
		b.WriteString(description)
		b.WriteString("\n\n## Implementation Notes\n\n")
		b.WriteString("_To be filled during implementation._\n\n")
		b.WriteString("## Acceptance Criteria\n\n")
		b.WriteString("- [ ] Feature implemented as described\n")
		b.WriteString("- [ ] No breaking changes to existing functionality\n")
	}

	return b.String()
}

// ProcessIdea performs automatic intake processing on a pending idea:
// 1. Categorizes as bug/feature/improvement
// 2. Rewrites title/description
// 3. Generates spec
// 4. Creates a feature linked to the original idea
// No human approval gate required.
func ProcessIdea(database *sql.DB, projectID string, idea *models.IdeaQueueItem) (*models.Feature, error) {
	// Mark as processing
	if err := db.UpdateIdeaStatus(database, idea.ID, "processing"); err != nil {
		return nil, fmt.Errorf("marking idea as processing: %w", err)
	}

	// Categorize and rewrite
	ideaType, title, description := CategorizeIdea(idea)

	// Update idea type if categorization changed it
	if ideaType != idea.IdeaType {
		if err := db.UpdateIdeaType(database, idea.ID, ideaType); err != nil {
			return nil, fmt.Errorf("updating idea type: %w", err)
		}
	}

	// Generate spec
	spec := GenerateIdeaSpec(ideaType, title, description)

	// Set spec on idea
	if err := db.SetIdeaSpec(database, idea.ID, spec); err != nil {
		return nil, fmt.Errorf("setting idea spec: %w", err)
	}

	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		EventType: "idea.auto_processed",
		Data:      fmt.Sprintf(`{"idea_id":%d,"type":%q,"title":%q}`, idea.ID, ideaType, title),
	})

	// Create feature from the idea with high priority (8)
	priority := 8
	if ideaType == "bug" {
		priority = 9 // bugs get higher priority
	}

	f, err := AddFeature(database, projectID, title, description, spec, "", priority, nil, "")
	if err != nil {
		return nil, fmt.Errorf("creating feature from idea: %w", err)
	}

	// Link feature to idea and mark as approved
	if err := db.ApproveIdea(database, idea.ID, f.ID); err != nil {
		return nil, fmt.Errorf("approving idea: %w", err)
	}

	_ = db.InsertEvent(database, &models.Event{
		ProjectID: projectID,
		FeatureID: f.ID,
		EventType: "idea.auto_approved",
		Data:      fmt.Sprintf(`{"idea_id":%d,"feature_id":%q}`, idea.ID, f.ID),
	})

	// If auto-implement is set, start a cycle
	if idea.AutoImplement {
		_, _ = StartCycle(database, projectID, f.ID, "feature-implementation")
	}

	return f, nil
}

// ProcessPendingIdeas processes all pending ideas in the queue automatically.
func ProcessPendingIdeas(database *sql.DB, projectID string) ([]ProcessedIdeaResult, error) {
	ideas, err := db.ListIdeas(database, projectID, "pending", "")
	if err != nil {
		return nil, fmt.Errorf("listing pending ideas: %w", err)
	}

	var results []ProcessedIdeaResult
	for _, idea := range ideas {
		ideaCopy := idea
		f, err := ProcessIdea(database, projectID, &ideaCopy)
		result := ProcessedIdeaResult{
			IdeaID:    idea.ID,
			IdeaTitle: idea.Title,
		}
		if err != nil {
			result.Error = err.Error()
		} else {
			result.FeatureID = f.ID
			result.FeatureName = f.Name
			result.Category = ideaCopy.IdeaType
		}
		results = append(results, result)
	}

	return results, nil
}

// ProcessedIdeaResult holds the result of processing a single idea.
type ProcessedIdeaResult struct {
	IdeaID      int    `json:"idea_id"`
	IdeaTitle   string `json:"idea_title"`
	FeatureID   string `json:"feature_id,omitempty"`
	FeatureName string `json:"feature_name,omitempty"`
	Category    string `json:"category,omitempty"`
	Error       string `json:"error,omitempty"`
}
