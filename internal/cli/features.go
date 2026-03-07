package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

// Feature templates provide pre-populated spec content for common feature types.
var featureTemplates = map[string]struct {
	Name        string
	Description string
	Spec        string
}{
	"api-endpoint": {
		Name:        "API Endpoint",
		Description: "REST API endpoint spec",
		Spec: `## API Endpoint Specification

1. **Method & Path**
   - HTTP Method: GET | POST | PUT | PATCH | DELETE
   - Path: /api/v1/...
   - Description: 

2. **Request**
   - Headers: 
   - Query Parameters: 
   - Request Body (JSON):
     ` + "```json" + `
     {}
     ` + "```" + `

3. **Response**
   - Success Status: 200 | 201 | 204
   - Response Body (JSON):
     ` + "```json" + `
     {}
     ` + "```" + `

4. **Authentication & Authorization**
   - Auth Required: Yes | No
   - Required Roles/Permissions: 

5. **Error Responses**
   - 400 Bad Request: 
   - 401 Unauthorized: 
   - 403 Forbidden: 
   - 404 Not Found: 
   - 422 Validation Error: 

6. **Acceptance Criteria**
   - [ ] Endpoint returns correct status codes
   - [ ] Request validation rejects invalid input
   - [ ] Auth checks enforced
   - [ ] Response matches documented schema
   - [ ] Error responses include actionable messages`,
	},
	"ui-component": {
		Name:        "UI Component",
		Description: "UI component spec",
		Spec: `## UI Component Specification

1. **Component Name & Purpose**
   - Name: 
   - Purpose: 
   - Location: 

2. **Props / Inputs**
   - | Prop | Type | Required | Default | Description |
     |------|------|----------|---------|-------------|
     |      |      |          |         |             |

3. **States**
   - Loading: 
   - Empty: 
   - Error: 
   - Populated: 
   - Disabled: 

4. **Events / Interactions**
   - onClick: 
   - onChange: 
   - onSubmit: 

5. **Accessibility**
   - ARIA labels: 
   - Keyboard navigation: 
   - Screen reader support: 
   - Color contrast: 

6. **Acceptance Criteria**
   - [ ] Renders correctly in all states
   - [ ] Props are validated
   - [ ] Events fire correctly
   - [ ] Accessible via keyboard
   - [ ] Passes WCAG 2.1 AA contrast requirements`,
	},
	"cli-command": {
		Name:        "CLI Command",
		Description: "CLI command spec",
		Spec: `## CLI Command Specification

1. **Command Signature**
   - Command: lifecycle ...
   - Arguments: 
   - Description: 

2. **Flags**
   - | Flag | Type | Default | Description |
     |------|------|---------|-------------|
     |      |      |         |             |

3. **Output Formats**
   - Human-readable: 
   - JSON (--json): 

4. **Examples**
   ` + "```bash" + `
   # Basic usage
   lifecycle ...

   # With flags
   lifecycle ... --flag value
   ` + "```" + `

5. **Error Cases**
   - Missing required arguments: 
   - Invalid flag values: 
   - Not in a lifecycle project: 

6. **Acceptance Criteria**
   - [ ] Command executes successfully with valid input
   - [ ] --json flag produces valid JSON output
   - [ ] Error messages are clear and suggest next steps
   - [ ] Help text is accurate and complete
   - [ ] Works with both short and long flag forms`,
	},
	"migration": {
		Name:        "Database Migration",
		Description: "Database migration spec",
		Spec: `## Database Migration Specification

1. **Migration Purpose**
   - Description: 
   - Reason for change: 

2. **Tables**
   - New tables: 
   - Modified tables: 

3. **Columns**
   - | Table | Column | Type | Nullable | Default | Description |
     |-------|--------|------|----------|---------|-------------|
     |       |        |      |          |         |             |

4. **Indexes**
   - | Table | Columns | Type | Purpose |
     |-------|---------|------|---------|
     |       |         |      |         |

5. **Rollback Plan**
   - Reversible: Yes | No
   - Rollback steps: 
   - Data preservation: 

6. **Acceptance Criteria**
   - [ ] Migration applies cleanly on empty database
   - [ ] Migration applies cleanly on existing data
   - [ ] Rollback works without data loss
   - [ ] Indexes improve target query performance
   - [ ] No breaking changes to existing queries`,
	},
	"integration": {
		Name:        "Third-Party Integration",
		Description: "Third-party integration spec",
		Spec: `## Third-Party Integration Specification

1. **Service**
   - Name: 
   - Documentation URL: 
   - Purpose: 

2. **Authentication**
   - Auth method: API Key | OAuth2 | Bearer Token
   - Credential storage: 
   - Token refresh strategy: 

3. **Endpoints / Operations**
   - | Operation | Method | Endpoint | Description |
     |-----------|--------|----------|-------------|
     |           |        |          |             |

4. **Error Handling**
   - Retry strategy: 
   - Timeout configuration: 
   - Fallback behavior: 
   - Circuit breaker: 

5. **Rate Limits**
   - Limit: 
   - Throttling strategy: 
   - Quota monitoring: 

6. **Acceptance Criteria**
   - [ ] Authentication works with valid credentials
   - [ ] Graceful handling of auth failures
   - [ ] Retries on transient errors
   - [ ] Rate limits respected
   - [ ] Timeout handling prevents hangs`,
	},
	"bug-fix": {
		Name:        "Bug Fix",
		Description: "Bug fix spec",
		Spec: `## Bug Fix Specification

1. **Reproduction Steps**
   - Environment: 
   - Steps:
     1. 
     2. 
     3. 
   - Expected behavior: 
   - Actual behavior: 

2. **Root Cause**
   - Location (file:line): 
   - Cause: 
   - Impact: 

3. **Fix Approach**
   - Description: 
   - Files to modify: 
   - Risk assessment: Low | Medium | High

4. **Test Plan**
   - Unit tests: 
   - Integration tests: 
   - Manual verification: 

5. **Regression Check**
   - Related features to verify: 
   - Edge cases to test: 

6. **Acceptance Criteria**
   - [ ] Bug no longer reproducible
   - [ ] Reproduction test case added
   - [ ] No regressions in related features
   - [ ] Fix works across supported environments
   - [ ] Root cause documented`,
	},
}

func getTemplateNames() []string {
	names := make([]string, 0, len(featureTemplates))
	for name := range featureTemplates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var featureCmd = &cobra.Command{
	Use:   "feature",
	Short: "Manage features",
}

func init() {
	featureCmd.AddCommand(featureAddCmd)
	featureCmd.AddCommand(featureListCmd)
	featureCmd.AddCommand(featureShowCmd)
	featureCmd.AddCommand(featureEditCmd)
	featureCmd.AddCommand(featureRemoveCmd)
	featureCmd.AddCommand(featureDepsCmd)
	featureCmd.AddCommand(featureBatchCmd)
	featureCmd.AddCommand(featureTagCmd)
	featureCmd.AddCommand(featureUntagCmd)
	featureCmd.AddCommand(featureTagsCmd)
	featureCmd.AddCommand(featureEstimatesCmd)
	featureCmd.AddCommand(featureTemplatesCmd)

	featureAddCmd.Flags().String("milestone", "", "Assign to milestone")
	featureAddCmd.Flags().String("template", "", "Use a feature template (api-endpoint, ui-component, cli-command, migration, integration, bug-fix)")
	featureAddCmd.Flags().Int("priority", 0, "Priority (higher = more important)")
	featureAddCmd.Flags().StringSlice("depends-on", nil, "Feature dependencies")
	featureAddCmd.Flags().String("description", "", "Feature description")
	featureAddCmd.Flags().String("spec", "", "Feature spec / acceptance criteria (detailed requirements)")
	featureAddCmd.Flags().String("roadmap-item", "", "Link to originating roadmap item ID")
	featureAddCmd.Flags().String("status", "draft", "Initial status (draft, planning, implementing, agent-qa, human-qa, done, blocked)")
	featureAddCmd.Flags().Int("points", 0, "Story points (fibonacci: 1,2,3,5,8,13,21)")
	featureAddCmd.Flags().String("size", "", "T-shirt size (XS, S, M, L, XL)")

	featureListCmd.Flags().String("status", "", "Filter by status")
	featureListCmd.Flags().String("milestone", "", "Filter by milestone")
	featureListCmd.Flags().String("tag", "", "Filter by tag")

	featureEditCmd.Flags().String("name", "", "New name")
	featureEditCmd.Flags().String("description", "", "New description")
	featureEditCmd.Flags().String("spec", "", "New spec / acceptance criteria")
	featureEditCmd.Flags().String("status", "", "New status")
	featureEditCmd.Flags().String("milestone", "", "New milestone")
	featureEditCmd.Flags().String("roadmap-item", "", "Link to roadmap item ID")
	featureEditCmd.Flags().Int("priority", -1, "New priority")
	featureEditCmd.Flags().Int("points", -1, "Story points (fibonacci: 1,2,3,5,8,13,21)")
	featureEditCmd.Flags().String("size", "", "T-shirt size (XS, S, M, L, XL)")

	featureBatchCmd.Flags().StringSlice("ids", nil, "Feature IDs to update (comma-separated)")
	featureBatchCmd.Flags().String("status", "", "Set status for all features")
	featureBatchCmd.Flags().String("milestone", "", "Set milestone for all features")
	featureBatchCmd.Flags().Int("priority", -1, "Set priority for all features")

	featureEstimatesCmd.Flags().String("milestone", "", "Filter by milestone")
}

var featureAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new feature",
	Args:  cobra.ExactArgs(1),
	Example: `  # Add a new feature
  lifecycle feature add "User Auth" --description "JWT-based authentication" --priority 8

  # Add with full spec for agents
  lifecycle feature add "Search" --spec "1. Full-text search via FTS5\n2. Results ranked by relevance" --milestone v1.0

  # Onboarding: add already-completed feature
  lifecycle feature add "Database Layer" --status done --spec "..." --priority 10`,
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

		milestone, _ := cmd.Flags().GetString("milestone")
		priority, _ := cmd.Flags().GetInt("priority")
		deps, _ := cmd.Flags().GetStringSlice("depends-on")
		desc, _ := cmd.Flags().GetString("description")
		spec, _ := cmd.Flags().GetString("spec")
		roadmapItem, _ := cmd.Flags().GetString("roadmap-item")
		status, _ := cmd.Flags().GetString("status")
		points, _ := cmd.Flags().GetInt("points")
		size, _ := cmd.Flags().GetString("size")
		templateName, _ := cmd.Flags().GetString("template")

		if templateName != "" {
			tmpl, ok := featureTemplates[templateName]
			if !ok {
				return fmt.Errorf("unknown template %q: available templates: %s", templateName, strings.Join(getTemplateNames(), ", "))
			}
			if spec == "" {
				spec = tmpl.Spec
			}
		}

		if points != 0 {
			if err := validatePoints(points); err != nil {
				return err
			}
		}
		if size != "" {
			if err := validateSize(size); err != nil {
				return err
			}
			size = strings.ToUpper(size)
		}

		f, err := engine.AddFeature(database, p.ID, args[0], desc, spec, milestone, priority, deps, roadmapItem)
		if err != nil {
			return err
		}

		// Apply estimate fields
		if points != 0 || size != "" {
			estUpdates := map[string]any{}
			if points != 0 {
				estUpdates["estimate_points"] = points
			}
			if size != "" {
				estUpdates["estimate_size"] = size
			}
			if err := db.UpdateFeature(database, f.ID, estUpdates); err != nil {
				return fmt.Errorf("setting estimates: %w", err)
			}
			f.EstimatePoints = points
			f.EstimateSize = size
		}

		// If status is not the default "draft", set it directly
		if status != "" && status != "draft" {
			validStatuses := map[string]bool{
				"planning": true, "implementing": true, "agent-qa": true,
				"human-qa": true, "done": true, "blocked": true,
			}
			if !validStatuses[status] {
				return fmt.Errorf("invalid status %q: must be one of draft, planning, implementing, agent-qa, human-qa, done, blocked", status)
			}
			if err := db.SetFeatureStatus(database, f.ID, status); err != nil {
				return fmt.Errorf("setting feature status: %w", err)
			}
			f.Status = status
		}

		if jsonOutput {
			return printJSON(f)
		}
		fmt.Printf("✓ Added feature %q (id: %s)\n", f.Name, f.ID)
		return nil
	},
}

var featureListCmd = &cobra.Command{
	Use:   "list",
	Short: "List features",
	Long: `List all features in the project, optionally filtered by status, milestone, or tag.

Output includes feature ID, status, priority, and name. Use --json for
structured output suitable for scripting or agent consumption.`,
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

		status, _ := cmd.Flags().GetString("status")
		milestone, _ := cmd.Flags().GetString("milestone")
		tag, _ := cmd.Flags().GetString("tag")

		var features []models.Feature
		if tag != "" {
			features, err = db.GetFeaturesByTag(database, p.ID, tag)
			if err != nil {
				return err
			}
			if status != "" || milestone != "" {
				var filtered []models.Feature
				for _, f := range features {
					if status != "" && f.Status != status {
						continue
					}
					if milestone != "" && f.MilestoneID != milestone {
						continue
					}
					filtered = append(filtered, f)
				}
				features = filtered
			}
		} else {
			features, err = db.ListFeatures(database, p.ID, status, milestone)
			if err != nil {
				return err
			}
		}

		if jsonOutput {
			return printJSON(features)
		}

		if len(features) == 0 {
			fmt.Println("No features found.")
			return nil
		}

		fmt.Printf("%-20s %-14s %-4s %s\n", "ID", "STATUS", "PRI", "NAME")
		fmt.Println(strings.Repeat("─", 60))
		for _, f := range features {
			fmt.Printf("%-20s %-14s %-4d %s\n", f.ID, f.Status, f.Priority, f.Name)
		}
		return nil
	},
}

var featureShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show feature details",
	Long: `Display detailed information about a specific feature including its status,
priority, milestone, dependencies, tags, spec, and timestamps.

Use 'lifecycle feature list' to find feature IDs.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		f, err := db.GetFeature(database, args[0])
		if err != nil {
			return fmt.Errorf("feature %q not found. Run 'lifecycle feature list' to see available features", args[0])
		}

		if jsonOutput {
			return printJSON(f)
		}

		fmt.Printf("Feature: %s\n", f.Name)
		fmt.Printf("  ID:        %s\n", f.ID)
		fmt.Printf("  Status:    %s\n", f.Status)
		fmt.Printf("  Priority:  %d\n", f.Priority)
		if f.MilestoneID != "" {
			fmt.Printf("  Milestone: %s (%s)\n", f.MilestoneName, f.MilestoneID)
		}
		if f.AssignedCycle != "" {
			fmt.Printf("  Cycle:     %s\n", f.AssignedCycle)
		}
		if len(f.DependsOn) > 0 {
			fmt.Printf("  Depends:   %s\n", strings.Join(f.DependsOn, ", "))
		}
		if len(f.Tags) > 0 {
			fmt.Printf("  Tags:      %s\n", strings.Join(f.Tags, ", "))
		}
		if f.Description != "" {
			fmt.Printf("  Desc:      %s\n", f.Description)
		}
		if f.Spec != "" {
			fmt.Printf("  Spec:      %s\n", f.Spec)
		}
		if f.RoadmapItemID != "" {
			fmt.Printf("  Roadmap:   %s\n", f.RoadmapItemID)
		}
		if f.EstimatePoints > 0 {
			fmt.Printf("  Points:    %d\n", f.EstimatePoints)
		}
		if f.EstimateSize != "" {
			fmt.Printf("  Size:      %s\n", f.EstimateSize)
		}
		fmt.Printf("  Created:   %s\n", f.CreatedAt)
		return nil
	},
}

var featureEditCmd = &cobra.Command{
	Use:   "edit <id>",
	Short: "Edit feature properties",
	Long: `Update one or more properties of a feature. Status transitions are validated
through the lifecycle engine to enforce the QA gate (features must pass
through human-qa before being marked done).

Editable fields: --name, --description, --spec, --status, --milestone,
--roadmap-item, --priority.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		updates := make(map[string]any)
		if v, _ := cmd.Flags().GetString("name"); v != "" {
			updates["name"] = v
		}
		if v, _ := cmd.Flags().GetString("description"); v != "" {
			updates["description"] = v
		}
		if v, _ := cmd.Flags().GetString("spec"); v != "" {
			updates["spec"] = v
		}
		newStatus, _ := cmd.Flags().GetString("status")
		if v, _ := cmd.Flags().GetString("milestone"); v != "" {
			updates["milestone_id"] = v
		}
		if v, _ := cmd.Flags().GetString("roadmap-item"); v != "" {
			updates["roadmap_item_id"] = v
		}
		if v, _ := cmd.Flags().GetInt("priority"); v >= 0 && cmd.Flags().Changed("priority") {
			updates["priority"] = v
		}
		if cmd.Flags().Changed("points") {
			v, _ := cmd.Flags().GetInt("points")
			if v > 0 {
				if err := validatePoints(v); err != nil {
					return err
				}
			}
			updates["estimate_points"] = v
		}
		if cmd.Flags().Changed("size") {
			v, _ := cmd.Flags().GetString("size")
			if v != "" {
				if err := validateSize(v); err != nil {
					return err
				}
				v = strings.ToUpper(v)
			}
			updates["estimate_size"] = v
		}

		if len(updates) == 0 && newStatus == "" {
			return fmt.Errorf("no changes specified")
		}

		if len(updates) > 0 {
			if err := db.UpdateFeature(database, args[0], updates); err != nil {
				return fmt.Errorf("updating feature %q: %w", args[0], err)
			}
		}

		// Handle status transition through the engine to enforce QA gate
		if newStatus != "" {
			p, err := db.GetProject(database)
			if err != nil {
				return fmt.Errorf("getting project: %w", err)
			}
			if err := engine.TransitionFeature(database, p.ID, args[0], newStatus); err != nil {
				return err
			}
		}

		if jsonOutput {
			f, _ := db.GetFeature(database, args[0])
			return printJSON(f)
		}
		fmt.Printf("✓ Updated feature %s\n", args[0])
		return nil
	},
}

var featureRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a feature",
	Long: `Permanently remove a feature from the project. This also removes any
associated dependencies, tags, and work items.

Use 'lifecycle feature list' to find feature IDs.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		if err := db.DeleteFeature(database, args[0]); err != nil {
			return fmt.Errorf("removing feature %q: %w", args[0], err)
		}

		if jsonOutput {
			return printJSON(map[string]string{"deleted": args[0]})
		}
		fmt.Printf("✓ Removed feature %s\n", args[0])
		return nil
	},
}

var featureDepsCmd = &cobra.Command{
	Use:   "deps <id>",
	Short: "Show dependency tree for a feature",
	Long: `Display the dependency tree for a feature, showing what it depends on
and what depends on it. Blocking dependencies are highlighted.

Use 'lifecycle feature add --depends-on <id>' to create dependencies.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID := args[0]
		f, err := db.GetFeature(database, featureID)
		if err != nil {
			return fmt.Errorf("feature %q not found. Run 'lifecycle feature list' to see available features", featureID)
		}

		dependents, _ := db.GetFeatureDependents(database, featureID)

		if jsonOutput {
			tree, _ := db.GetFeatureDependencyTree(database, featureID)
			blocked, _ := db.GetBlockedFeatures(database)
			blockedSet := map[string]bool{}
			for _, b := range blocked {
				blockedSet[b.ID] = true
			}
			return printJSON(map[string]any{
				"feature":    f,
				"tree":       tree,
				"dependents": dependents,
				"is_blocked": blockedSet[f.ID],
			})
		}

		// Print tree header
		statusMark := statusSymbol(f.Status)
		fmt.Printf("%s (%s) %s\n", f.Name, f.Status, statusMark)

		// Print dependencies
		for i, depID := range f.DependsOn {
			dep, depErr := db.GetFeature(database, depID)
			isLast := i == len(f.DependsOn)-1
			prefix := "├── "
			if isLast && len(dependents) == 0 {
				prefix = "└── "
			}
			if depErr != nil {
				fmt.Printf("%s%s (unknown)\n", prefix, depID)
				continue
			}
			mark := statusSymbol(dep.Status)
			blocking := ""
			if dep.Status != "done" {
				blocking = " BLOCKING"
			}
			fmt.Printf("%s%s (%s) %s%s\n", prefix, dep.Name, dep.Status, mark, blocking)
			// Print transitive deps (one level)
			for j, subDepID := range dep.DependsOn {
				subDep, subErr := db.GetFeature(database, subDepID)
				subPrefix := "│   "
				if isLast && len(dependents) == 0 {
					subPrefix = "    "
				}
				connector := "├── "
				if j == len(dep.DependsOn)-1 {
					connector = "└── "
				}
				if subErr != nil {
					fmt.Printf("%s%s%s (unknown)\n", subPrefix, connector, subDepID)
					continue
				}
				subMark := statusSymbol(subDep.Status)
				fmt.Printf("%s%s%s (%s) %s\n", subPrefix, connector, subDep.Name, subDep.Status, subMark)
			}
		}

		// Print dependents (required by)
		if len(dependents) > 0 {
			fmt.Println("Required by:")
			for i, dep := range dependents {
				prefix := "├── "
				if i == len(dependents)-1 {
					prefix = "└── "
				}
				fmt.Printf("%s%s (%s)\n", prefix, dep.Name, dep.Status)
			}
		}

		return nil
	},
}

var featureBatchCmd = &cobra.Command{
	Use:   "batch",
	Short: "Batch update multiple features",
	Example: `  # Set status for multiple features
  lifecycle feature batch --ids f1,f2,f3 --status implementing

  # Set milestone for multiple features
  lifecycle feature batch --ids f1,f2 --milestone v1.0-mvp

  # Set priority for multiple features
  lifecycle feature batch --ids f1,f2,f3 --priority 8`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		ids, _ := cmd.Flags().GetStringSlice("ids")
		if len(ids) == 0 {
			return fmt.Errorf("--ids is required")
		}

		status, _ := cmd.Flags().GetString("status")
		milestone, _ := cmd.Flags().GetString("milestone")
		priority, _ := cmd.Flags().GetInt("priority")
		priorityChanged := cmd.Flags().Changed("priority")

		var field, value string
		switch {
		case status != "":
			validStatuses := map[string]bool{
				"draft": true, "planning": true, "implementing": true,
				"agent-qa": true, "human-qa": true, "done": true, "blocked": true,
			}
			if !validStatuses[status] {
				return fmt.Errorf("invalid status %q. Valid statuses: draft, planning, implementing, agent-qa, human-qa, done, blocked", status)
			}
			field, value = "status", status
		case milestone != "":
			field, value = "milestone_id", milestone
		case priorityChanged && priority >= 0:
			field, value = "priority", fmt.Sprintf("%d", priority)
		default:
			return fmt.Errorf("specify one of --status, --milestone, or --priority")
		}

		updated, err := db.BatchUpdateFeatures(database, ids, field, value)
		if err != nil {
			return fmt.Errorf("batch update: %w", err)
		}

		if jsonOutput {
			return printJSON(map[string]any{"updated": updated, "field": field, "value": value, "ids": ids})
		}
		fmt.Printf("✓ Updated %d feature(s): %s = %s\n", updated, field, value)
		return nil
	},
}

func statusSymbol(status string) string {
	switch status {
	case "done":
		return "✓"
	case "blocked":
		return "✗"
	default:
		return "○"
	}
}

var validFibonacci = map[int]bool{1: true, 2: true, 3: true, 5: true, 8: true, 13: true, 21: true}
var validSizes = map[string]bool{"XS": true, "S": true, "M": true, "L": true, "XL": true}

func validatePoints(points int) error {
	if !validFibonacci[points] {
		return fmt.Errorf("invalid story points %d: must be a fibonacci number (1, 2, 3, 5, 8, 13, 21)", points)
	}
	return nil
}

func validateSize(size string) error {
	if !validSizes[strings.ToUpper(size)] {
		return fmt.Errorf("invalid t-shirt size %q: must be one of XS, S, M, L, XL", size)
	}
	return nil
}

var featureEstimatesCmd = &cobra.Command{
	Use:   "estimates",
	Short: "Show estimation summary",
	RunE: func(cmd *cobra.Command, _ []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		p, err := db.GetProject(database)
		if err != nil {
			return err
		}

		milestone, _ := cmd.Flags().GetString("milestone")

		summary, err := db.GetEstimationSummary(database, p.ID, milestone)
		if err != nil {
			return fmt.Errorf("getting estimation summary: %w", err)
		}

		if jsonOutput {
			return printJSON(summary)
		}

		fmt.Println("📊 Feature Estimates")
		fmt.Println("━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		fmt.Printf("Total points: %d (%d completed, %d remaining)\n",
			summary.TotalPoints, summary.CompletedPoints, summary.RemainingPoints)
		fmt.Println()

		if len(summary.BySizeEntries) > 0 {
			fmt.Println("By Size:")
			for _, e := range summary.BySizeEntries {
				noun := "features"
				if e.Total == 1 {
					noun = "feature"
				}
				fmt.Printf("  %-2s: %d %s (done: %d)\n", e.Size, e.Total, noun, e.Done)
			}
			fmt.Println()
		}

		fmt.Printf("Unestimated: %d features\n", summary.Unestimated)
		return nil
	},
}

var featureTagCmd = &cobra.Command{
	Use:   "tag <feature-id> <tag1> [tag2...]",
	Short: "Add tags to a feature",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID := args[0]
		if _, err := db.GetFeature(database, featureID); err != nil {
			return fmt.Errorf("feature not found: %s", featureID)
		}

		for _, tag := range args[1:] {
			if err := db.AddFeatureTag(database, featureID, tag); err != nil {
				return fmt.Errorf("adding tag %q: %w", tag, err)
			}
		}

		tags, _ := db.GetFeatureTags(database, featureID)
		if jsonOutput {
			return printJSON(map[string]any{"feature_id": featureID, "tags": tags})
		}
		fmt.Printf("✓ Tagged %s: %s\n", featureID, strings.Join(args[1:], ", "))
		return nil
	},
}

var featureUntagCmd = &cobra.Command{
	Use:   "untag <feature-id> <tag1> [tag2...]",
	Short: "Remove tags from a feature",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(_ *cobra.Command, args []string) error {
		database, _, err := openDB()
		if err != nil {
			return err
		}
		defer database.Close() //nolint:errcheck

		featureID := args[0]
		if _, err := db.GetFeature(database, featureID); err != nil {
			return fmt.Errorf("feature not found: %s", featureID)
		}

		for _, tag := range args[1:] {
			if err := db.RemoveFeatureTag(database, featureID, tag); err != nil {
				return fmt.Errorf("removing tag %q: %w", tag, err)
			}
		}

		tags, _ := db.GetFeatureTags(database, featureID)
		if jsonOutput {
			return printJSON(map[string]any{"feature_id": featureID, "tags": tags})
		}
		fmt.Printf("✓ Untagged %s: %s\n", featureID, strings.Join(args[1:], ", "))
		return nil
	},
}

var featureTagsCmd = &cobra.Command{
	Use:   "tags",
	Short: "List all tags with feature counts",
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

		tags, err := db.ListAllTags(database, p.ID)
		if err != nil {
			return err
		}

		if jsonOutput {
			return printJSON(tags)
		}

		if len(tags) == 0 {
			fmt.Println("No tags found.")
			return nil
		}

		fmt.Printf("%-30s %s\n", "TAG", "FEATURES")
		fmt.Println(strings.Repeat("─", 40))
		for _, tc := range tags {
			fmt.Printf("%-30s %d\n", tc.Tag, tc.Count)
		}
		return nil
	},
}

var featureTemplatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List available feature templates",
	Long: `List all available feature templates that can be used with 'lifecycle feature add --template <name>'.

Templates provide pre-populated spec content for common feature types,
giving a structured starting point for acceptance criteria.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		names := getTemplateNames()

		if jsonOutput {
			type templateInfo struct {
				Name        string `json:"name"`
				Title       string `json:"title"`
				Description string `json:"description"`
			}
			out := make([]templateInfo, 0, len(names))
			for _, name := range names {
				tmpl := featureTemplates[name]
				out = append(out, templateInfo{
					Name:        name,
					Title:       tmpl.Name,
					Description: tmpl.Description,
				})
			}
			return printJSON(out)
		}

		fmt.Println("Available feature templates:")
		fmt.Println()
		fmt.Printf("  %-20s %s\n", "TEMPLATE", "DESCRIPTION")
		fmt.Printf("  %-20s %s\n", strings.Repeat("─", 18), strings.Repeat("─", 40))
		for _, name := range names {
			tmpl := featureTemplates[name]
			fmt.Printf("  %-20s %s\n", name, tmpl.Description)
		}
		fmt.Println()
		fmt.Println("Usage: lifecycle feature add <name> --template <template>")
		return nil
	},
}
