package cli

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind/tillr/internal/db"
	"github.com/mschulkind/tillr/internal/engine"
	"github.com/mschulkind/tillr/internal/models"
)

// templateMilestone defines a milestone to create as part of a project template.
type templateMilestone struct {
	Name        string
	Description string
	Order       int
}

// templateFeature defines a feature to create as part of a project template.
type templateFeature struct {
	Name        string
	Description string
	Spec        string
	Milestone   string // milestone name (will be slugified to get ID)
	Priority    int
	DependsOn   []string // feature names (will be slugified)
}

// templateRoadmapItem defines a roadmap item to create as part of a project template.
type templateRoadmapItem struct {
	Title       string
	Description string
	Category    string
	Priority    string
	Effort      string
	Order       int
}

// templateDiscussion defines a discussion to create as part of a project template.
type templateDiscussion struct {
	Title   string
	Body    string
	Feature string // feature name (will be slugified, optional)
	Author  string
}

// projectTemplate defines a complete project template with milestones, features,
// roadmap items, and discussions.
type projectTemplate struct {
	Name         string
	Description  string
	Milestones   []templateMilestone
	Features     []templateFeature
	RoadmapItems []templateRoadmapItem
	Discussions  []templateDiscussion
}

var projectTemplates = map[string]projectTemplate{
	"web-app": {
		Name:        "Web Application",
		Description: "Web application with auth, API, frontend, and deployment",
		Milestones: []templateMilestone{
			{Name: "MVP", Description: "Minimum viable product with core functionality", Order: 1},
			{Name: "Beta", Description: "Beta release with user feedback integration", Order: 2},
			{Name: "Launch", Description: "Production launch with monitoring and docs", Order: 3},
		},
		Features: []templateFeature{
			{
				Name:        "Authentication",
				Description: "User authentication and authorization system",
				Spec:        "1. User registration with email verification\n2. Login/logout with session management\n3. Password reset flow\n4. Role-based access control (RBAC)\n5. OAuth2 social login support",
				Milestone:   "MVP",
				Priority:    10,
			},
			{
				Name:        "REST API",
				Description: "Core REST API endpoints",
				Spec:        "1. RESTful resource endpoints with CRUD operations\n2. Input validation and error responses\n3. Pagination, filtering, and sorting\n4. Rate limiting per client\n5. OpenAPI/Swagger documentation",
				Milestone:   "MVP",
				Priority:    9,
				DependsOn:   []string{"Authentication"},
			},
			{
				Name:        "Frontend",
				Description: "Web frontend application",
				Spec:        "1. Responsive layout with mobile support\n2. Component-based UI architecture\n3. Client-side routing and navigation\n4. Form handling with validation\n5. Loading states and error handling\n6. Dark mode support",
				Milestone:   "Beta",
				Priority:    8,
				DependsOn:   []string{"REST API"},
			},
			{
				Name:        "Database Layer",
				Description: "Database schema and data access layer",
				Spec:        "1. Schema design with migrations\n2. Connection pooling configuration\n3. Query builder or ORM setup\n4. Seed data for development\n5. Backup and restore procedures",
				Milestone:   "MVP",
				Priority:    10,
			},
			{
				Name:        "Deployment",
				Description: "CI/CD pipeline and deployment infrastructure",
				Spec:        "1. Containerized deployment (Docker)\n2. CI/CD pipeline with automated tests\n3. Environment configuration (dev/staging/prod)\n4. Health check endpoints\n5. Logging and monitoring setup",
				Milestone:   "Launch",
				Priority:    7,
			},
			{
				Name:        "Testing",
				Description: "Comprehensive test suite",
				Spec:        "1. Unit tests for business logic\n2. Integration tests for API endpoints\n3. End-to-end tests for critical flows\n4. Test coverage reporting\n5. Performance/load testing baseline",
				Milestone:   "Beta",
				Priority:    8,
			},
		},
		RoadmapItems: []templateRoadmapItem{
			{
				Title:       "API Performance Optimization",
				Description: "Optimize query patterns, add caching layer, reduce p99 latency",
				Category:    "infrastructure",
				Priority:    "high",
				Effort:      "m",
				Order:       1,
			},
			{
				Title:       "User Analytics Dashboard",
				Description: "Track user engagement, feature usage, and conversion metrics",
				Category:    "feature",
				Priority:    "medium",
				Effort:      "l",
				Order:       2,
			},
			{
				Title:       "Internationalization",
				Description: "Multi-language support with i18n framework and translation workflow",
				Category:    "feature",
				Priority:    "low",
				Effort:      "l",
				Order:       3,
			},
		},
		Discussions: []templateDiscussion{
			{
				Title:  "RFC: API Versioning Strategy",
				Body:   "## Context\nAs we build the API, we need to decide on a versioning strategy.\n\n## Options\n1. URL path versioning (/v1/resources)\n2. Header-based versioning (Accept: application/vnd.api+json;version=1)\n3. Query parameter versioning (?version=1)\n\n## Decision Needed\nWhich approach best fits our use case?",
				Author: "template",
			},
		},
	},
	"cli-tool": {
		Name:        "CLI Tool",
		Description: "Command-line tool with configuration and documentation",
		Milestones: []templateMilestone{
			{Name: "Core", Description: "Core command structure and functionality", Order: 1},
			{Name: "Polish", Description: "UX improvements, error handling, and configurability", Order: 2},
			{Name: "Release", Description: "Documentation, packaging, and distribution", Order: 3},
		},
		Features: []templateFeature{
			{
				Name:        "Command Structure",
				Description: "CLI command hierarchy and argument parsing",
				Spec:        "1. Root command with global flags (--verbose, --json, --config)\n2. Subcommand hierarchy with help text\n3. Argument validation and error messages\n4. Shell completion support (bash, zsh, fish)\n5. Version command with build info",
				Milestone:   "Core",
				Priority:    10,
			},
			{
				Name:        "Configuration",
				Description: "Configuration file and environment variable support",
				Spec:        "1. Config file discovery (XDG, home dir, project dir)\n2. YAML/TOML config file format\n3. Environment variable overrides\n4. Config validation with helpful error messages\n5. Init command to generate default config",
				Milestone:   "Core",
				Priority:    9,
			},
			{
				Name:        "Core Logic",
				Description: "Primary business logic and data processing",
				Spec:        "1. Main processing pipeline\n2. Input parsing and validation\n3. Output formatting (table, JSON, plain text)\n4. Error handling with exit codes\n5. Progress indicators for long operations",
				Milestone:   "Core",
				Priority:    10,
				DependsOn:   []string{"Command Structure", "Configuration"},
			},
			{
				Name:        "Documentation",
				Description: "User documentation and examples",
				Spec:        "1. README with quick start guide\n2. Man page generation\n3. Usage examples for each command\n4. Configuration reference\n5. Troubleshooting guide",
				Milestone:   "Release",
				Priority:    7,
			},
			{
				Name:        "Testing and CI",
				Description: "Test suite and continuous integration",
				Spec:        "1. Unit tests for core logic\n2. Integration tests for CLI commands\n3. CI pipeline with lint, test, build\n4. Cross-platform build matrix\n5. Release automation",
				Milestone:   "Polish",
				Priority:    8,
			},
		},
		RoadmapItems: []templateRoadmapItem{
			{
				Title:       "Plugin System",
				Description: "Extensible plugin architecture for custom commands and integrations",
				Category:    "feature",
				Priority:    "medium",
				Effort:      "l",
				Order:       1,
			},
			{
				Title:       "Interactive Mode",
				Description: "REPL-style interactive mode with autocomplete and history",
				Category:    "feature",
				Priority:    "low",
				Effort:      "m",
				Order:       2,
			},
		},
		Discussions: []templateDiscussion{
			{
				Title:  "RFC: Output Format Standards",
				Body:   "## Context\nWe need consistent output formatting across all commands.\n\n## Proposal\n- Default: human-readable tables/text\n- --json: machine-readable JSON\n- --quiet: minimal output (exit codes only)\n\n## Questions\n- Should we support --format=csv?\n- How should we handle streaming output?",
				Author: "template",
			},
		},
	},
	"library": {
		Name:        "Library/SDK",
		Description: "Reusable library or SDK with API design and documentation",
		Milestones: []templateMilestone{
			{Name: "v1.0", Description: "Initial stable release with core API", Order: 1},
			{Name: "v2.0", Description: "Extended API with advanced features and optimizations", Order: 2},
		},
		Features: []templateFeature{
			{
				Name:        "API Design",
				Description: "Public API surface design and implementation",
				Spec:        "1. Core types and interfaces defined\n2. Builder/options pattern for configuration\n3. Error types with context and wrapping\n4. Thread-safety guarantees documented\n5. Backward compatibility policy established",
				Milestone:   "v1.0",
				Priority:    10,
			},
			{
				Name:        "Test Suite",
				Description: "Comprehensive testing infrastructure",
				Spec:        "1. Unit tests with >80% coverage\n2. Integration tests for key workflows\n3. Benchmark tests for performance-critical paths\n4. Fuzz tests for input parsing\n5. Example tests that serve as documentation",
				Milestone:   "v1.0",
				Priority:    9,
			},
			{
				Name:        "Documentation",
				Description: "API documentation and usage guides",
				Spec:        "1. Godoc/JSDoc/Sphinx API reference\n2. Getting started guide with examples\n3. Architecture overview\n4. Migration guide for version upgrades\n5. FAQ and troubleshooting",
				Milestone:   "v1.0",
				Priority:    8,
			},
			{
				Name:        "Publishing",
				Description: "Package publishing and distribution",
				Spec:        "1. Package registry setup (npm/PyPI/Go modules)\n2. Semantic versioning with changelog\n3. CI/CD release pipeline\n4. Pre-release/beta channel\n5. License and NOTICE files",
				Milestone:   "v1.0",
				Priority:    7,
				DependsOn:   []string{"API Design", "Test Suite", "Documentation"},
			},
			{
				Name:        "Advanced Features",
				Description: "Extended API with advanced capabilities",
				Spec:        "1. Advanced configuration options\n2. Middleware/plugin extension points\n3. Performance optimizations\n4. Additional output formats or adapters\n5. Observability hooks (logging, metrics, tracing)",
				Milestone:   "v2.0",
				Priority:    6,
				DependsOn:   []string{"API Design"},
			},
		},
		RoadmapItems: []templateRoadmapItem{
			{
				Title:       "Performance Benchmarks",
				Description: "Establish and maintain performance benchmarks with CI integration",
				Category:    "infrastructure",
				Priority:    "high",
				Effort:      "m",
				Order:       1,
			},
			{
				Title:       "Language Bindings",
				Description: "Generate bindings for additional languages (FFI, WASM, or native ports)",
				Category:    "feature",
				Priority:    "low",
				Effort:      "xl",
				Order:       2,
			},
		},
		Discussions: []templateDiscussion{
			{
				Title:  "RFC: Error Handling Strategy",
				Body:   "## Context\nWe need a consistent error handling approach across the API.\n\n## Options\n1. Sentinel errors with errors.Is() checks\n2. Typed error structs with errors.As()\n3. Error code enums\n\n## Considerations\n- Backward compatibility when adding new error types\n- User experience when handling errors\n- Stack trace and context preservation",
				Author: "template",
			},
		},
	},
	"microservice": {
		Name:        "Microservice",
		Description: "Microservice with API, health checks, observability, and deployment",
		Milestones: []templateMilestone{
			{Name: "Core Service", Description: "Core service with API endpoints and data layer", Order: 1},
			{Name: "Production Ready", Description: "Observability, resilience, and deployment pipeline", Order: 2},
			{Name: "Scaling", Description: "Performance optimization and horizontal scaling", Order: 3},
		},
		Features: []templateFeature{
			{
				Name:        "Service Skeleton",
				Description: "Base service structure with configuration and dependency injection",
				Spec:        "1. Service entrypoint with graceful shutdown\n2. Configuration from env vars and config files\n3. Dependency injection / service container\n4. Health check endpoints (liveness + readiness)\n5. Structured logging setup",
				Milestone:   "Core Service",
				Priority:    10,
			},
			{
				Name:        "API Endpoints",
				Description: "Core business logic API endpoints",
				Spec:        "1. RESTful or gRPC endpoint definitions\n2. Request validation and error handling\n3. Input/output serialization\n4. API versioning strategy\n5. OpenAPI or protobuf schema",
				Milestone:   "Core Service",
				Priority:    9,
				DependsOn:   []string{"Service Skeleton"},
			},
			{
				Name:        "Data Layer",
				Description: "Database integration and data access",
				Spec:        "1. Database connection with pooling\n2. Migration framework\n3. Repository pattern for data access\n4. Transaction support\n5. Seed data for development",
				Milestone:   "Core Service",
				Priority:    9,
				DependsOn:   []string{"Service Skeleton"},
			},
			{
				Name:        "Observability",
				Description: "Metrics, tracing, and structured logging",
				Spec:        "1. Prometheus metrics endpoint\n2. Distributed tracing (OpenTelemetry)\n3. Structured JSON logging with correlation IDs\n4. Request/response logging middleware\n5. Custom business metrics",
				Milestone:   "Production Ready",
				Priority:    8,
			},
			{
				Name:        "Deployment",
				Description: "Container build and deployment pipeline",
				Spec:        "1. Multi-stage Dockerfile\n2. Kubernetes manifests or Helm chart\n3. CI/CD pipeline (build, test, deploy)\n4. Environment-specific configuration\n5. Secret management integration",
				Milestone:   "Production Ready",
				Priority:    7,
			},
			{
				Name:        "Resilience",
				Description: "Circuit breakers, retries, and graceful degradation",
				Spec:        "1. Circuit breaker for external dependencies\n2. Retry with exponential backoff\n3. Timeout configuration per dependency\n4. Graceful degradation patterns\n5. Bulkhead isolation",
				Milestone:   "Scaling",
				Priority:    6,
			},
		},
		RoadmapItems: []templateRoadmapItem{
			{
				Title:       "Event-Driven Architecture",
				Description: "Add message queue integration for async processing and event sourcing",
				Category:    "architecture",
				Priority:    "high",
				Effort:      "l",
				Order:       1,
			},
			{
				Title:       "Service Mesh Integration",
				Description: "Integrate with service mesh for mTLS, traffic management, and observability",
				Category:    "infrastructure",
				Priority:    "medium",
				Effort:      "m",
				Order:       2,
			},
		},
		Discussions: []templateDiscussion{
			{
				Title:  "RFC: Inter-Service Communication",
				Body:   "## Context\nWe need to decide how this service communicates with other services.\n\n## Options\n1. Synchronous REST/gRPC calls\n2. Asynchronous message queue (Kafka, RabbitMQ)\n3. Hybrid approach\n\n## Considerations\n- Latency requirements\n- Data consistency needs\n- Failure handling and retry semantics",
				Author: "template",
			},
		},
	},
}

// templateNames returns sorted template names (excluding "empty").
func templateNames() []string {
	names := make([]string, 0, len(projectTemplates))
	for name := range projectTemplates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// applyTemplate populates a project with template data (milestones, features,
// roadmap items, and discussions).
func applyTemplate(database *sql.DB, projectID, templateName string) (int, error) {
	tmpl, ok := projectTemplates[templateName]
	if !ok {
		return 0, fmt.Errorf("unknown template %q (available: %s)",
			templateName, strings.Join(templateNames(), ", "))
	}

	created := 0

	// 1. Create milestones
	for _, m := range tmpl.Milestones {
		ms := &models.Milestone{
			ID:          engine.Slug(m.Name),
			ProjectID:   projectID,
			Name:        m.Name,
			Description: m.Description,
			SortOrder:   m.Order,
		}
		if err := db.CreateMilestone(database, ms); err != nil {
			return created, fmt.Errorf("creating milestone %q: %w", m.Name, err)
		}
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: projectID,
			EventType: "milestone.created",
			Data:      fmt.Sprintf(`{"name":%q,"source":"template"}`, m.Name),
		})
		created++
	}

	// 2. Create features
	for _, f := range tmpl.Features {
		milestoneID := ""
		if f.Milestone != "" {
			milestoneID = engine.Slug(f.Milestone)
		}
		var deps []string
		for _, d := range f.DependsOn {
			deps = append(deps, engine.Slug(d))
		}
		if _, err := engine.AddFeature(database, projectID, f.Name, f.Description,
			f.Spec, milestoneID, f.Priority, deps, ""); err != nil {
			return created, fmt.Errorf("creating feature %q: %w", f.Name, err)
		}
		created++
	}

	// 3. Create roadmap items
	for _, r := range tmpl.RoadmapItems {
		ri := &models.RoadmapItem{
			ID:          engine.Slug(r.Title),
			ProjectID:   projectID,
			Title:       r.Title,
			Description: r.Description,
			Category:    r.Category,
			Priority:    r.Priority,
			Effort:      r.Effort,
			SortOrder:   r.Order,
		}
		if err := db.CreateRoadmapItem(database, ri); err != nil {
			return created, fmt.Errorf("creating roadmap item %q: %w", r.Title, err)
		}
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: projectID,
			EventType: "roadmap.item_added",
			Data:      fmt.Sprintf(`{"title":%q,"source":"template"}`, r.Title),
		})
		created++
	}

	// 4. Create discussions
	for _, d := range tmpl.Discussions {
		featureID := ""
		if d.Feature != "" {
			featureID = engine.Slug(d.Feature)
		}
		author := d.Author
		if author == "" {
			author = "template"
		}
		disc := &models.Discussion{
			ProjectID: projectID,
			FeatureID: featureID,
			Title:     d.Title,
			Body:      d.Body,
			Author:    author,
			Status:    "open",
		}
		if err := db.CreateDiscussion(database, disc); err != nil {
			return created, fmt.Errorf("creating discussion %q: %w", d.Title, err)
		}
		_ = db.InsertEvent(database, &models.Event{
			ProjectID: projectID,
			FeatureID: featureID,
			EventType: "discussion.created",
			Data:      fmt.Sprintf(`{"title":%q,"source":"template"}`, d.Title),
		})
		created++
	}

	return created, nil
}
