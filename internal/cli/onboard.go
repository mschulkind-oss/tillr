package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind/lifecycle/internal/config"
	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
	"github.com/spf13/cobra"
)

var onboardCmd = &cobra.Command{
	Use:   "onboard [flags]",
	Short: "Onboard an existing project for lifecycle management",
	Long: `Onboard scans an existing project directory, initializes lifecycle tracking
if needed, and outputs a structured analysis with guidance for agents.

This is the recommended entry point for agents working with a new codebase.
It detects languages, CI configuration, git history, git tags, GitHub issues/PRs
(if gh CLI is available), and existing trackers, then provides step-by-step
instructions for setting up features, milestones, and iteration cycles.

In non-interactive mode (--yes), the command will automatically:
  - Initialize the project if not already done
  - Create suggested milestones from git tags
  - Create features from detected work items (recent commits, GitHub issues)
  - Create a "Self-Hosting Bootstrap" roadmap item if none exists`,
	Example: `  # Onboard an existing project (interactive)
  lifecycle onboard

  # Onboard with a specific name, non-interactive
  lifecycle onboard --name my-project --yes

  # Scan project and output analysis as JSON (for agents)
  lifecycle onboard --json

  # Scan only, don't create anything
  lifecycle onboard --scan --json`,
	RunE: runOnboard,
}

func init() {
	onboardCmd.Flags().String("name", "", "Project name (auto-detects from directory or git remote)")
	onboardCmd.Flags().Bool("scan", false, "Scan only — output analysis without creating features or milestones")
	onboardCmd.Flags().Bool("yes", false, "Non-interactive mode: auto-accept all suggestions")
}

// ProjectAnalysis holds the results of scanning a project directory.
type ProjectAnalysis struct {
	ProjectDir          string             `json:"project_dir"`
	ProjectName         string             `json:"project_name"`
	Initialized         bool               `json:"initialized"`
	AlreadyExisted      bool               `json:"already_existed"`
	Languages           []LanguageCount    `json:"languages"`
	GitCommits          int                `json:"git_commits"`
	GitDetected         bool               `json:"git_detected"`
	GitTags             []string           `json:"git_tags,omitempty"`
	RecentCommits       []CommitInfo       `json:"recent_commits,omitempty"`
	SuggestedMilestones []string           `json:"suggested_milestones,omitempty"`
	SuggestedFeatures   []SuggestedFeature `json:"suggested_features,omitempty"`
	GitHubAvailable     bool               `json:"github_available"`
	GitHubIssues        []GitHubItem       `json:"github_issues,omitempty"`
	GitHubPRs           []GitHubItem       `json:"github_prs,omitempty"`
	CIConfigs           []string           `json:"ci_configs"`
	Trackers            []string           `json:"trackers"`
	SkillsDetected      []string           `json:"skills_detected,omitempty"`
	ReadmeFound         bool               `json:"readme_found"`
	ReadmePreview       string             `json:"readme_preview,omitempty"`
	ReadmeSize          int64              `json:"readme_size,omitempty"`
	Guidance            string             `json:"guidance,omitempty"`
	CreatedMilestones   []string           `json:"created_milestones,omitempty"`
	CreatedFeatures     []string           `json:"created_features,omitempty"`
	CreatedRoadmapItems []string           `json:"created_roadmap_items,omitempty"`
}

// LanguageCount tracks files by extension.
type LanguageCount struct {
	Extension string `json:"extension"`
	Count     int    `json:"count"`
}

// CommitInfo holds a parsed commit from git log.
type CommitInfo struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Type    string `json:"type,omitempty"`
}

// SuggestedFeature is a feature detected from git history or GitHub.
type SuggestedFeature struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Status string `json:"status"`
}

// GitHubItem represents a GitHub issue or PR.
type GitHubItem struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Labels string `json:"labels,omitempty"`
}

func runOnboard(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	name, _ := cmd.Flags().GetString("name")
	scanOnly, _ := cmd.Flags().GetBool("scan")
	autoYes, _ := cmd.Flags().GetBool("yes")

	if name == "" {
		name = detectProjectName(cwd)
	}

	var initialized, alreadyExisted bool

	// Check if already initialized
	cfgPath := filepath.Join(cwd, config.ConfigFileName)
	if _, err := os.Stat(cfgPath); err != nil {
		if scanOnly {
			// Scan-only mode: don't initialize, just scan
		} else {
			cfg := &config.Config{
				ProjectDir: cwd,
				DBPath:     config.DefaultDBName,
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
	} else {
		alreadyExisted = true
		if !jsonOutput && !scanOnly {
			fmt.Printf("! Project already initialized in %s — continuing with scan\n", cwd)
		}
	}

	analysis := scanProject(cwd, name, initialized)
	analysis.AlreadyExisted = alreadyExisted

	// If not scan-only, create resources from suggestions
	if !scanOnly && (initialized || alreadyExisted) {
		created, err := applyOnboardSuggestions(cwd, analysis, autoYes)
		if err != nil && !jsonOutput {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
		if created != nil {
			analysis.CreatedMilestones = created.milestones
			analysis.CreatedFeatures = created.features
			analysis.CreatedRoadmapItems = created.roadmapItems
		}
	}

	if jsonOutput {
		return printJSON(analysis)
	}

	// Human-readable output
	if initialized {
		fmt.Printf("✓ Initialized project %q in %s\n", name, cwd)
	} else if !alreadyExisted {
		fmt.Printf("✓ Scanned project %q (use without --scan to initialize)\n", name)
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
		if len(analysis.GitTags) > 0 {
			fmt.Printf("  Git tags:     %s\n", strings.Join(analysis.GitTags, ", "))
		}
	} else {
		fmt.Println("  Git:          not detected")
	}

	// GitHub
	if analysis.GitHubAvailable {
		fmt.Printf("  GitHub:       %d open issues, %d open PRs\n", len(analysis.GitHubIssues), len(analysis.GitHubPRs))
	}

	// CI
	if len(analysis.CIConfigs) > 0 {
		fmt.Printf("  CI/CD:        %s\n", strings.Join(analysis.CIConfigs, ", "))
	}

	// Trackers
	if len(analysis.Trackers) > 0 {
		fmt.Printf("  Trackers:     %s\n", strings.Join(analysis.Trackers, ", "))
	}

	// Skills
	if len(analysis.SkillsDetected) > 0 {
		fmt.Printf("  Skills:       %s\n", strings.Join(analysis.SkillsDetected, ", "))
	}

	// README
	if analysis.ReadmeFound {
		fmt.Printf("  README:       Found (%.1fKB)\n", float64(analysis.ReadmeSize)/1024.0)
	}

	// Suggested milestones
	if len(analysis.SuggestedMilestones) > 0 {
		fmt.Printf("\nSuggested Milestones (from git tags):\n")
		for _, m := range analysis.SuggestedMilestones {
			fmt.Printf("  · %s\n", m)
		}
	}

	// Suggested features
	if len(analysis.SuggestedFeatures) > 0 {
		fmt.Printf("\nDetected Work Items:\n")
		for _, f := range analysis.SuggestedFeatures {
			fmt.Printf("  · [%s] %s (source: %s)\n", f.Status, f.Name, f.Source)
		}
	}

	// Created resources
	if len(analysis.CreatedMilestones) > 0 {
		fmt.Printf("\n✓ Created milestones: %s\n", strings.Join(analysis.CreatedMilestones, ", "))
	}
	if len(analysis.CreatedFeatures) > 0 {
		fmt.Printf("✓ Created features: %s\n", strings.Join(analysis.CreatedFeatures, ", "))
	}
	if len(analysis.CreatedRoadmapItems) > 0 {
		fmt.Printf("✓ Created roadmap items: %s\n", strings.Join(analysis.CreatedRoadmapItems, ", "))
	}

	fmt.Println()
	fmt.Println(buildGuidanceText())

	return nil
}

// detectProjectName tries git remote origin, then falls back to directory name.
func detectProjectName(dir string) string {
	// Try git remote
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		url := strings.TrimSpace(string(out))
		// Extract repo name from URL (handles both HTTPS and SSH)
		// e.g. "https://github.com/user/repo.git" → "repo"
		// e.g. "git@github.com:user/repo.git" → "repo"
		url = strings.TrimSuffix(url, ".git")
		if idx := strings.LastIndex(url, "/"); idx >= 0 {
			return url[idx+1:]
		}
		if idx := strings.LastIndex(url, ":"); idx >= 0 {
			return url[idx+1:]
		}
	}
	return filepath.Base(dir)
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
		scanGitHistory(dir, a)
		scanGitTags(dir, a)
	}

	// GitHub CLI detection
	scanGitHub(dir, a)

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

	// Skills/agent configuration detection
	skillPaths := []struct {
		path string
		name string
	}{
		{"AGENTS.md", "AGENTS.md"},
		{".github/copilot-instructions.md", "Copilot Instructions"},
		{".cursorrules", "Cursor Rules"},
		{".clinerules", "Cline Rules"},
		{".github/CODEOWNERS", "CODEOWNERS"},
	}
	for _, s := range skillPaths {
		if _, err := os.Stat(filepath.Join(dir, s.path)); err == nil {
			a.SkillsDetected = append(a.SkillsDetected, s.name)
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

// scanGitHistory parses recent commits to detect feature-like work.
func scanGitHistory(dir string, a *ProjectAnalysis) {
	logCmd := exec.Command("git", "log", "--oneline", "--no-decorate", "-50")
	logCmd.Dir = dir
	logOut, err := logCmd.Output()
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(logOut)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return
	}
	a.GitCommits = len(lines)

	// Parse conventional commits for feature suggestions
	seen := make(map[string]bool)
	for _, line := range lines {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		subject := parts[1]

		ci := CommitInfo{Hash: hash, Subject: subject}

		// Detect conventional commit types
		commitType, featureName := parseConventionalCommit(subject)
		ci.Type = commitType

		if len(a.RecentCommits) < 20 {
			a.RecentCommits = append(a.RecentCommits, ci)
		}

		// Suggest features from feat: commits
		if commitType == "feat" && featureName != "" && !seen[featureName] {
			seen[featureName] = true
			a.SuggestedFeatures = append(a.SuggestedFeatures, SuggestedFeature{
				Name:   featureName,
				Source: "git-commit",
				Status: "done",
			})
		}
	}
}

// parseConventionalCommit extracts type and description from conventional commits.
func parseConventionalCommit(subject string) (commitType, description string) {
	// Match: "type(scope): description" or "type: description"
	lower := strings.ToLower(subject)
	for _, prefix := range []string{"feat", "fix", "docs", "refactor", "test", "chore", "ci", "perf", "style"} {
		if strings.HasPrefix(lower, prefix+"(") {
			// type(scope): description
			idx := strings.Index(subject, ")")
			if idx >= 0 && idx+1 < len(subject) {
				rest := strings.TrimLeft(subject[idx+1:], ": ")
				return prefix, rest
			}
		}
		if strings.HasPrefix(lower, prefix+":") {
			rest := strings.TrimLeft(subject[len(prefix)+1:], " ")
			return prefix, rest
		}
	}
	return "", subject
}

// scanGitTags detects tags and suggests milestones from them.
func scanGitTags(dir string, a *ProjectAnalysis) {
	tagCmd := exec.Command("git", "tag", "--sort=-v:refname")
	tagCmd.Dir = dir
	tagOut, err := tagCmd.Output()
	if err != nil {
		return
	}
	tagLines := strings.Split(strings.TrimSpace(string(tagOut)), "\n")
	for _, tag := range tagLines {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		a.GitTags = append(a.GitTags, tag)
		if len(a.GitTags) >= 10 {
			break
		}
	}

	// Suggest milestones from tags
	if len(a.GitTags) > 0 {
		// The latest tag becomes a "done" milestone, suggest a next version
		latest := a.GitTags[0]
		a.SuggestedMilestones = append(a.SuggestedMilestones, latest+" (completed)")
		nextVersion := suggestNextVersion(latest)
		if nextVersion != "" {
			a.SuggestedMilestones = append(a.SuggestedMilestones, nextVersion+" (next)")
		}
	}
}

// suggestNextVersion bumps a semver-like tag to the next minor version.
func suggestNextVersion(tag string) string {
	tag = strings.TrimPrefix(tag, "v")
	parts := strings.Split(tag, ".")
	if len(parts) < 2 {
		return ""
	}
	// Try to increment minor version
	var minor int
	if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
		return ""
	}
	parts[1] = fmt.Sprintf("%d", minor+1)
	if len(parts) >= 3 {
		parts[2] = "0"
	}
	return "v" + strings.Join(parts, ".")
}

// scanGitHub detects GitHub issues and PRs if gh CLI is available.
func scanGitHub(dir string, a *ProjectAnalysis) {
	// Check if gh CLI is available
	ghPath, err := exec.LookPath("gh")
	if err != nil || ghPath == "" {
		return
	}

	// Check if we're in a GitHub repo
	checkCmd := exec.Command("gh", "repo", "view", "--json", "name")
	checkCmd.Dir = dir
	if _, err := checkCmd.Output(); err != nil {
		return
	}
	a.GitHubAvailable = true

	// Fetch open issues
	issueCmd := exec.Command("gh", "issue", "list", "--state", "open", "--limit", "20", "--json", "number,title,state,labels")
	issueCmd.Dir = dir
	if issueOut, err := issueCmd.Output(); err == nil {
		parseGitHubItems(issueOut, a, true)
	}

	// Fetch open PRs
	prCmd := exec.Command("gh", "pr", "list", "--state", "open", "--limit", "10", "--json", "number,title,state,labels")
	prCmd.Dir = dir
	if prOut, err := prCmd.Output(); err == nil {
		parseGitHubItems(prOut, a, false)
	}
}

// parseGitHubItems parses JSON output from gh CLI.
func parseGitHubItems(data []byte, a *ProjectAnalysis, isIssue bool) {
	type ghLabel struct {
		Name string `json:"name"`
	}
	type ghItem struct {
		Number int       `json:"number"`
		Title  string    `json:"title"`
		State  string    `json:"state"`
		Labels []ghLabel `json:"labels"`
	}
	var items []ghItem
	if err := json.Unmarshal(data, &items); err != nil {
		return
	}
	for _, item := range items {
		var labelNames []string
		for _, l := range item.Labels {
			labelNames = append(labelNames, l.Name)
		}
		gi := GitHubItem{
			Number: item.Number,
			Title:  item.Title,
			State:  item.State,
			Labels: strings.Join(labelNames, ", "),
		}
		if isIssue {
			a.GitHubIssues = append(a.GitHubIssues, gi)
			// Suggest features from issues
			status := "draft"
			if item.State == "closed" {
				status = "done"
			}
			a.SuggestedFeatures = append(a.SuggestedFeatures, SuggestedFeature{
				Name:   item.Title,
				Source: fmt.Sprintf("github-issue-#%d", item.Number),
				Status: status,
			})
		} else {
			a.GitHubPRs = append(a.GitHubPRs, gi)
		}
	}
}

type createdResources struct {
	milestones   []string
	features     []string
	roadmapItems []string
}

// applyOnboardSuggestions creates milestones, features, and roadmap items from analysis.
func applyOnboardSuggestions(cwd string, analysis *ProjectAnalysis, autoYes bool) (*createdResources, error) {
	database, _, err := openDB()
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	defer database.Close() //nolint:errcheck

	p, err := db.GetProject(database)
	if err != nil {
		return nil, fmt.Errorf("getting project: %w", err)
	}

	created := &createdResources{}

	// Create milestones from git tags (only the "next" suggestion)
	for _, ms := range analysis.SuggestedMilestones {
		if !strings.HasSuffix(ms, "(next)") {
			continue
		}
		msName := strings.TrimSuffix(ms, " (next)")
		if !autoYes && !jsonOutput {
			fmt.Printf("  Create milestone %q? (use --yes to auto-accept) [skipped]\n", msName)
			continue
		}
		msID := engine.Slug(msName)
		if _, err := db.GetMilestone(database, msID); err == nil {
			continue // already exists
		}
		if err := db.CreateMilestone(database, &models.Milestone{
			ID:        msID,
			ProjectID: p.ID,
			Name:      msName,
			Status:    "active",
		}); err == nil {
			created.milestones = append(created.milestones, msName)
		}
	}

	// Create features from suggestions (limit to 10)
	featureLimit := 10
	for i, sf := range analysis.SuggestedFeatures {
		if i >= featureLimit {
			break
		}
		if !autoYes && !jsonOutput {
			fmt.Printf("  Create feature %q? (use --yes to auto-accept) [skipped]\n", sf.Name)
			continue
		}
		featureID := engine.Slug(sf.Name)
		if _, err := db.GetFeature(database, featureID); err == nil {
			continue // already exists
		}
		if _, err := engine.AddFeature(database, p.ID, sf.Name,
			fmt.Sprintf("Auto-detected from %s", sf.Source),
			"", "", 0, nil, ""); err == nil {
			// Set status if not draft
			if sf.Status != "" && sf.Status != "draft" {
				_ = engine.TransitionFeature(database, p.ID, featureID, sf.Status)
			}
			created.features = append(created.features, sf.Name)
		}
	}

	// Create "Self-Hosting Bootstrap" roadmap item if none exists
	existing, _ := db.ListRoadmapItems(database, p.ID)
	hasBootstrap := false
	for _, ri := range existing {
		if strings.Contains(strings.ToLower(ri.Title), "self-hosting") || strings.Contains(strings.ToLower(ri.Title), "bootstrap") {
			hasBootstrap = true
			break
		}
	}
	if !hasBootstrap {
		if !autoYes && !jsonOutput {
			fmt.Println("  Create \"Self-Hosting Bootstrap\" roadmap item? (use --yes to auto-accept) [skipped]")
		} else {
			bootstrapID := engine.Slug("self-hosting-bootstrap")
			if err := db.CreateRoadmapItem(database, &models.RoadmapItem{
				ID:          bootstrapID,
				ProjectID:   p.ID,
				Title:       "Self-Hosting Bootstrap",
				Description: "Set up lifecycle to manage its own development. Import existing work, create milestones, and establish iteration cycles.",
				Category:    "meta",
				Priority:    "high",
				Effort:      "m",
			}); err == nil {
				created.roadmapItems = append(created.roadmapItems, "Self-Hosting Bootstrap")
			}
		}
	}

	return created, nil
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
