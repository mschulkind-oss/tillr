package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/spf13/cobra"
)

var onboardCmd = &cobra.Command{
	Use:   "onboard [flags]",
	Short: "Onboard an existing project for lifecycle management",
	Long: `Onboard scans an existing project directory, initializes lifecycle tracking
if needed, and outputs a structured analysis with guidance for agents.

This is the recommended entry point for agents working with a new codebase.
It detects languages, CI configuration, git history, and existing trackers,
then provides step-by-step instructions for setting up features, milestones,
and iteration cycles.`,
	Example: `  # Onboard an existing project
  lifecycle onboard --name my-project

  # Scan project and output analysis as JSON
  lifecycle onboard --scan --json

  # Initialize and scan in one step
  lifecycle onboard --name my-project --scan`,
	RunE: runOnboard,
}

func init() {
	onboardCmd.Flags().String("name", "", "Project name (auto-detects from directory name if not provided)")
	onboardCmd.Flags().Bool("scan", false, "Scan project and output a structured analysis for the agent")
}

// ProjectAnalysis holds the results of scanning a project directory.
type ProjectAnalysis struct {
	ProjectDir    string          `json:"project_dir"`
	ProjectName   string          `json:"project_name"`
	Initialized   bool            `json:"initialized"`
	Languages     []LanguageCount `json:"languages"`
	GitCommits    int             `json:"git_commits"`
	GitDetected   bool            `json:"git_detected"`
	CIConfigs     []string        `json:"ci_configs"`
	Trackers      []string        `json:"trackers"`
	ReadmeFound   bool            `json:"readme_found"`
	ReadmePreview string          `json:"readme_preview,omitempty"`
	ReadmeSize    int64           `json:"readme_size,omitempty"`
	Guidance      string          `json:"guidance,omitempty"`
}

// LanguageCount tracks files by extension.
type LanguageCount struct {
	Extension string `json:"extension"`
	Count     int    `json:"count"`
}

func runOnboard(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	name, _ := cmd.Flags().GetString("name")
	if name == "" {
		name = filepath.Base(cwd)
	}

	var initialized bool

	// Check if already initialized
	cfgPath := filepath.Join(cwd, config.ConfigFileName)
	if _, err := os.Stat(cfgPath); err != nil {
		// Not initialized — do it now
		cfg := &config.Config{
			ProjectDir: cwd,
			DBPath:     filepath.Join(cwd, config.DefaultDBName),
			ServerPort: config.DefaultServerPort,
		}
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}
		database, err := db.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("creating database: %w", err)
		}
		if _, err := engine.InitProject(database, name); err != nil {
			_ = database.Close()
			return fmt.Errorf("initializing project: %w", err)
		}
		_ = database.Close()
		initialized = true
	}

	analysis := scanProject(cwd, name, initialized)

	if jsonOutput {
		return printJSON(analysis)
	}

	// Human-readable output
	if initialized {
		fmt.Printf("✓ Initialized project %q in %s\n", name, cwd)
	} else {
		fmt.Printf("✓ Project %q already initialized in %s\n", name, cwd)
	}

	fmt.Println()
	fmt.Println("Project Analysis:")

	// Languages
	if len(analysis.Languages) > 0 {
		var parts []string
		for _, lc := range analysis.Languages {
			parts = append(parts, fmt.Sprintf("%s (%d files)", lc.Extension, lc.Count))
		}
		fmt.Printf("  Languages:    %s\n", strings.Join(parts, ", "))
	}

	// Git
	if analysis.GitDetected {
		fmt.Printf("  Git commits:  %d (last 50 analyzed)\n", analysis.GitCommits)
	} else {
		fmt.Println("  Git:          not detected")
	}

	// CI
	if len(analysis.CIConfigs) > 0 {
		fmt.Printf("  CI/CD:        %s\n", strings.Join(analysis.CIConfigs, ", "))
	}

	// Trackers
	if len(analysis.Trackers) > 0 {
		fmt.Printf("  Trackers:     %s\n", strings.Join(analysis.Trackers, ", "))
	}

	// README
	if analysis.ReadmeFound {
		fmt.Printf("  README:       Found (%.1fKB)\n", float64(analysis.ReadmeSize)/1024.0)
	}

	fmt.Println()
	fmt.Println(buildGuidanceText())

	return nil
}

func scanProject(dir, name string, initialized bool) *ProjectAnalysis {
	a := &ProjectAnalysis{
		ProjectDir:  dir,
		ProjectName: name,
		Initialized: initialized,
	}

	// Count files by extension
	extCounts := make(map[string]int)
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Skip hidden dirs, vendor, node_modules
		if info.IsDir() {
			base := filepath.Base(path)
			if base != "." && strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			if base == "vendor" || base == "node_modules" || base == "trash" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != "" {
			extCounts[ext]++
		}
		return nil
	})

	// Sort by count, take top 5
	type extCount struct {
		ext   string
		count int
	}
	var sorted []extCount
	for ext, count := range extCounts {
		sorted = append(sorted, extCount{ext, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})
	limit := 5
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for _, ec := range sorted[:limit] {
		a.Languages = append(a.Languages, LanguageCount{
			Extension: extToLanguage(ec.ext),
			Count:     ec.count,
		})
	}

	// Git detection
	gitCmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	gitCmd.Dir = dir
	if out, err := gitCmd.Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		a.GitDetected = true
		// Count recent commits
		logCmd := exec.Command("git", "log", "--oneline", "-50")
		logCmd.Dir = dir
		if logOut, err := logCmd.Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(logOut)), "\n")
			if len(lines) > 0 && lines[0] != "" {
				a.GitCommits = len(lines)
			}
		}
	}

	// CI config detection
	ciPaths := []struct {
		path string
		name string
	}{
		{".github/workflows", ".github/workflows/"},
		{"Justfile", "Justfile"},
		{"Makefile", "Makefile"},
		{".gitlab-ci.yml", ".gitlab-ci.yml"},
		{".circleci", ".circleci/"},
		{"Jenkinsfile", "Jenkinsfile"},
		{".travis.yml", ".travis.yml"},
	}
	for _, ci := range ciPaths {
		if _, err := os.Stat(filepath.Join(dir, ci.path)); err == nil {
			a.CIConfigs = append(a.CIConfigs, ci.name)
		}
	}

	// Tracker detection
	trackerPaths := []struct {
		path string
		name string
	}{
		{".github/ISSUE_TEMPLATE", ".github/ISSUE_TEMPLATE/"},
		{"TODO.md", "TODO.md"},
		{"CHANGELOG.md", "CHANGELOG.md"},
		{"ROADMAP.md", "ROADMAP.md"},
		{".github/PULL_REQUEST_TEMPLATE.md", "PR template"},
	}
	for _, t := range trackerPaths {
		if _, err := os.Stat(filepath.Join(dir, t.path)); err == nil {
			a.Trackers = append(a.Trackers, t.name)
		}
	}

	// README detection
	readmePath := filepath.Join(dir, "README.md")
	if info, err := os.Stat(readmePath); err == nil {
		a.ReadmeFound = true
		a.ReadmeSize = info.Size()
		if data, err := os.ReadFile(readmePath); err == nil {
			preview := string(data)
			if len(preview) > 500 {
				preview = preview[:500]
			}
			a.ReadmePreview = preview
		}
	}

	a.Guidance = buildGuidanceText()

	return a
}

func extToLanguage(ext string) string {
	m := map[string]string{
		".go":   "Go",
		".js":   "JavaScript",
		".ts":   "TypeScript",
		".py":   "Python",
		".rb":   "Ruby",
		".java": "Java",
		".rs":   "Rust",
		".css":  "CSS",
		".html": "HTML",
		".md":   "Markdown",
		".json": "JSON",
		".yaml": "YAML",
		".yml":  "YAML",
		".sql":  "SQL",
		".sh":   "Shell",
		".c":    "C",
		".cpp":  "C++",
		".h":    "C/C++ Header",
		".jsx":  "JSX",
		".tsx":  "TSX",
		".toml": "TOML",
		".xml":  "XML",
		".svg":  "SVG",
	}
	if name, ok := m[ext]; ok {
		return name
	}
	return ext
}

func buildGuidanceText() string {
	return `Next Steps for Agents:
  1. Create milestones to organize work phases:
     lifecycle milestone add "v1.0 MVP" --description "Core functionality"

  2. Add existing features (use --status for work already done):
     lifecycle feature add "Feature Name" \
       --description "What it does" \
       --spec "1. Acceptance criteria..." \
       --milestone v1.0-mvp \
       --priority 8 \
       --status done          # For completed work
       --status implementing  # For work in progress
       --status draft         # For planned work

  3. Set up your roadmap:
     lifecycle roadmap add "Item Title" \
       --description "Full description" \
       --priority critical --category core --effort m

  4. Link features to roadmap items:
     lifecycle feature edit <id> --roadmap-item <roadmap-id>

  5. Add full specs to every feature:
     lifecycle feature edit <id> --spec "1. First criterion\n2. Second..."

  6. Start development cycles:
     lifecycle cycle start feature-implementation <feature-id>

  7. Check project health:
     lifecycle doctor

  8. View your project:
     lifecycle serve
     # Open http://localhost:3847

For the complete guide: lifecycle help onboard`
}
