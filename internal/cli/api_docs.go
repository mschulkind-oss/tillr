package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "API management commands",
}

func init() {
	apiCmd.AddCommand(apiDocsCmd)
	apiDocsCmd.Flags().String("format", "json", "Output format: json (OpenAPI spec), text (summary)")
	apiDocsCmd.Flags().StringP("output", "o", "", "Write to file instead of stdout")
}

var apiDocsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Output the API documentation (OpenAPI 3.0 spec)",
	Long: `Generates and outputs the OpenAPI 3.0 specification for the tillr HTTP API.

Use --format json (default) for the full OpenAPI spec.
Use --format text for a human-readable summary of all endpoints.

The spec can be used with Swagger UI, Redoc, or any OpenAPI-compatible tool.`,
	Example: `  tillr api docs                       # Print OpenAPI JSON to stdout
  tillr api docs -o openapi.json       # Write to file
  tillr api docs --format text         # Human-readable summary`,
	RunE: func(cmd *cobra.Command, args []string) error {
		format, _ := cmd.Flags().GetString("format")
		output, _ := cmd.Flags().GetString("output")

		spec := generateOpenAPISpec()

		var result string
		switch format {
		case "text":
			result = formatSpecAsText(spec)
		default:
			b, err := json.MarshalIndent(spec, "", "  ")
			if err != nil {
				return fmt.Errorf("marshaling spec: %w", err)
			}
			result = string(b)
		}

		if output != "" {
			if err := os.WriteFile(output, []byte(result+"\n"), 0o644); err != nil {
				return fmt.Errorf("writing file: %w", err)
			}
			fmt.Printf("Wrote API docs to %s\n", output)
			return nil
		}

		fmt.Println(result)
		return nil
	},
}

// generateOpenAPISpec builds the full OpenAPI 3.0 specification for the tillr API.
func generateOpenAPISpec() map[string]any {
	type apiRoute struct{ method, path, summary, desc, tag string }
	rr := []apiRoute{
		{"GET", "/api/status", "Project overview dashboard", "", "Project"},
		{"GET", "/api/stats", "Project statistics", "", "Project"},
		{"GET", "/api/stats/burndown", "Burndown chart data", "", "Project"},
		{"GET", "/api/stats/heatmap", "Activity heatmap data", "", "Project"},
		{"GET", "/api/stats/activity-heatmap", "Daily activity heatmap", "", "Project"},
		{"GET", "/api/search", "Full-text search", "Query: ?q=term", "Project"},
		{"GET", "/api/history", "Event history", "Params: ?feature, ?type, ?since, ?limit", "Project"},
		{"GET", "/api/features", "List features", "Params: ?status, ?milestone", "Features"},
		{"GET", "/api/features/{id}", "Feature details", "", "Features"},
		{"GET", "/api/tags", "List feature tags", "", "Features"},
		{"GET", "/api/milestones", "List milestones", "", "Milestones"},
		{"GET", "/api/milestones/{id}", "Milestone details", "", "Milestones"},
		{"GET", "/api/roadmap", "List roadmap items", "", "Roadmap"},
		{"PATCH", "/api/roadmap/{id}", "Update roadmap status", "", "Roadmap"},
		{"GET", "/api/cycles", "List active cycles", "", "Cycles"},
		{"GET", "/api/cycles/{id}", "Cycle details", "", "Cycles"},
		{"GET", "/api/qa/{feature-id}", "QA results", "", "QA"},
		{"POST", "/api/qa/{feature-id}", "Submit QA result", "", "QA"},
		{"GET", "/api/ideas", "List ideas", "", "Ideas"},
		{"POST", "/api/ideas", "Submit idea", "", "Ideas"},
		{"GET", "/api/ideas/{id}", "Idea details", "", "Ideas"},
		{"GET", "/api/decisions", "List decisions", "", "Decisions"},
		{"GET", "/api/decisions/{id}", "Decision details", "", "Decisions"},
		{"GET", "/api/discussions", "List discussions", "", "Discussions"},
		{"GET", "/api/discussions/{id}", "Discussion details", "", "Discussions"},
		{"GET", "/api/agents", "List agent sessions", "", "Agents"},
		{"GET", "/api/agents/{id}", "Agent details", "", "Agents"},
		{"GET", "/api/agents/coordination", "Coordination status", "", "Agents"},
		{"GET", "/api/agents/status", "Heartbeat dashboard", "", "Agents"},
		{"GET", "/api/worktrees", "List worktrees", "", "Worktrees"},
		{"GET", "/api/worktrees/{id}", "Worktree details", "", "Worktrees"},
		{"GET", "/api/git/log", "Git commit log", "", "Git"},
		{"GET", "/api/git/branches", "Git branches", "", "Git"},
		{"GET", "/api/context", "List context entries", "", "Context"},
		{"GET", "/api/context/{id}", "Context entry details", "", "Context"},
		{"GET", "/api/queue", "Work queue with stats", "", "Queue"},
		{"GET", "/api/export/features", "Export features", "?format=json|md|csv", "Export"},
		{"GET", "/api/export/roadmap", "Export roadmap", "?format=json|md|csv", "Export"},
		{"GET", "/api/export/decisions", "Export decisions", "?format=json|md|csv", "Export"},
		{"GET", "/api/export/all", "Export all data", "?format=json|md", "Export"},
		{"GET", "/api/dashboards", "List dashboards", "", "Dashboards"},
		{"GET", "/api/dashboards/{id}", "Dashboard details", "", "Dashboards"},
		{"GET", "/api/spec-document", "Aggregate spec document", "", "Spec"},
		{"GET", "/api/docs", "API docs page (HTML)", "", "Docs"},
		{"GET", "/api/openapi.json", "OpenAPI 3.0 spec (JSON)", "", "Docs"},
		{"GET", "/api/dependencies", "Dependency graph", "", "Dependencies"},
		{"GET", "/ws", "WebSocket real-time updates", "", "WebSocket"},
	}
	paths := map[string]any{}
	tagSet := map[string]bool{}
	for _, r := range rr {
		tagSet[r.tag] = true
		m := strings.ToLower(r.method)
		op := map[string]any{"summary": r.summary, "tags": []string{r.tag}, "responses": map[string]any{"200": map[string]any{"description": "OK"}}}
		if r.desc != "" {
			op["description"] = r.desc
		}
		if _, ok := paths[r.path]; !ok {
			paths[r.path] = map[string]any{}
		}
		paths[r.path].(map[string]any)[m] = op
	}
	var tagList []map[string]string
	for t := range tagSet {
		tagList = append(tagList, map[string]string{"name": t})
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Tillr API", "description": "REST API for tillr project management.", "version": "1.0.0"},
		"servers": []map[string]any{{"url": "http://localhost:3847", "description": "Local development server"}},
		"paths":   paths,
		"tags":    tagList,
	}
}

func formatSpecAsText(spec map[string]any) string {
	var b strings.Builder

	b.WriteString("Tillr API Documentation\n")
	b.WriteString(strings.Repeat("=", 50) + "\n\n")

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		b.WriteString("No endpoints found.\n")
		return b.String()
	}

	type routeEntry struct {
		method, path, summary string
	}
	var routes []routeEntry
	for path, methods := range paths {
		mm, ok := methods.(map[string]any)
		if !ok {
			continue
		}
		for method, opVal := range mm {
			op, ok := opVal.(map[string]any)
			if !ok {
				continue
			}
			summary, _ := op["summary"].(string)
			routes = append(routes, routeEntry{
				method:  strings.ToUpper(method),
				path:    path,
				summary: summary,
			})
		}
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].path != routes[j].path {
			return routes[i].path < routes[j].path
		}
		return routes[i].method < routes[j].method
	})

	for _, r := range routes {
		b.WriteString(fmt.Sprintf("  %-7s %-40s %s\n", r.method, r.path, r.summary))
	}

	b.WriteString(fmt.Sprintf("\nTotal endpoints: %d\n", len(routes)))
	return b.String()
}
