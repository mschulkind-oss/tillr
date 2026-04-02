//go:build unix

package cli

import (
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/engine"
	"github.com/mschulkind-oss/tillr/internal/models"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// ANSI escape sequences.
const (
	ansiClearScreen = "\033[2J"
	ansiCursorHome  = "\033[H"
	ansiHideCursor  = "\033[?25l"
	ansiShowCursor  = "\033[?25h"
	ansiBold        = "\033[1m"
	ansiDim         = "\033[2m"
	ansiReset       = "\033[0m"
)

// ANSI foreground color codes.
const (
	colorRed     = 31
	colorGreen   = 32
	colorYellow  = 33
	colorBlue    = 34
	colorMagenta = 35
	colorCyan    = 36
	colorWhite   = 37
)

// tuiView represents which view is currently displayed.
type tuiView int

const (
	viewDashboard tuiView = iota
	viewFeatures
	viewMilestones
)

// --- ANSI helper functions ---

func clearScreen() {
	fmt.Print(ansiClearScreen + ansiCursorHome)
}

func moveCursor(row, col int) {
	fmt.Printf("\033[%d;%dH", row, col)
}

func colorize(text string, color int) string {
	return fmt.Sprintf("\033[%dm%s%s", color, text, ansiReset)
}

func tuiBold(text string) string {
	return ansiBold + text + ansiReset
}

func tuiDim(text string) string {
	return ansiDim + text + ansiReset
}

func tuiProgressBar(current, total, width int) string {
	if total == 0 {
		return strings.Repeat("░", width)
	}
	filled := (current * width) / total
	if filled > width {
		filled = width
	}
	return strings.Repeat("▓", filled) + strings.Repeat("░", width-filled)
}

// --- Terminal raw mode ---

func enableRawMode(fd int) (*unix.Termios, error) {
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, fmt.Errorf("getting terminal state (not a terminal?): %w", err)
	}
	raw := *old
	// Disable echo and canonical mode for character-at-a-time input.
	// Keep ISIG so Ctrl-C still generates SIGINT for safe cleanup.
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1 // 100ms read timeout
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, fmt.Errorf("setting raw mode: %w", err)
	}
	return old, nil
}

func restoreTerminal(fd int, state *unix.Termios) {
	_ = unix.IoctlSetTermios(fd, unix.TCSETS, state)
}

func readKey() byte {
	buf := make([]byte, 1)
	n, _ := os.Stdin.Read(buf)
	if n > 0 {
		return buf[0]
	}
	return 0
}

// --- Command definition ---

var interactiveCmd = &cobra.Command{
	Use:     "interactive",
	Aliases: []string{"tui"},
	Short:   "Interactive terminal dashboard",
	Long: `Launch an interactive terminal dashboard with live-updating project status.

Views:
  Dashboard    Project health, feature counts, active cycles, recent events
  Features     Feature list with status and priority
  Milestones   Milestone progress bars

Keyboard shortcuts:
  d    Dashboard view (default)
  f    Feature list view
  m    Milestone list view
  r    Refresh now
  q    Quit

The dashboard auto-refreshes every 5 seconds.`,
	RunE: runInteractive,
}

func runInteractive(_ *cobra.Command, _ []string) error {
	database, _, err := openDB()
	if err != nil {
		return err
	}
	defer database.Close() //nolint:errcheck

	if _, err = db.GetProject(database); err != nil {
		return fmt.Errorf("no project found: %w", err)
	}

	fd := int(os.Stdin.Fd())
	oldState, err := enableRawMode(fd)
	if err != nil {
		return fmt.Errorf("interactive mode requires a terminal: %w", err)
	}
	defer func() {
		fmt.Print(ansiShowCursor)
		restoreTerminal(fd, oldState)
		clearScreen()
		fmt.Println("Goodbye!")
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	fmt.Print(ansiHideCursor)

	currentView := viewDashboard
	renderTUIView(database, currentView)
	lastRefresh := time.Now()

	for {
		select {
		case <-sigCh:
			return nil
		default:
		}

		key := readKey()
		switch key {
		case 'q', 'Q':
			return nil
		case 'r', 'R':
			renderTUIView(database, currentView)
			lastRefresh = time.Now()
		case 'd', 'D':
			currentView = viewDashboard
			renderTUIView(database, currentView)
			lastRefresh = time.Now()
		case 'f', 'F':
			currentView = viewFeatures
			renderTUIView(database, currentView)
			lastRefresh = time.Now()
		case 'm', 'M':
			currentView = viewMilestones
			renderTUIView(database, currentView)
			lastRefresh = time.Now()
		}

		if time.Since(lastRefresh) >= 5*time.Second {
			renderTUIView(database, currentView)
			lastRefresh = time.Now()
		}
	}
}

// --- Rendering ---

func renderTUIView(database *sql.DB, view tuiView) {
	clearScreen()
	moveCursor(1, 1)
	switch view {
	case viewDashboard:
		renderTUIDashboard(database)
	case viewFeatures:
		renderTUIFeatures(database)
	case viewMilestones:
		renderTUIMilestones(database)
	}
	renderTUIStatusBar(view)
}

func renderTUIHeader(title string) {
	line := strings.Repeat("─", 60)
	fmt.Printf("  %s\n", colorize(line, colorCyan))
	fmt.Printf("  %s  %s\n", colorize("TILLR", colorCyan), tuiBold(title))
	fmt.Printf("  %s\n\n", colorize(line, colorCyan))
}

func renderTUIStatusBar(current tuiView) {
	fmt.Println()
	views := []struct {
		key   string
		label string
		v     tuiView
	}{
		{"d", "dashboard", viewDashboard},
		{"f", "features", viewFeatures},
		{"m", "milestones", viewMilestones},
	}
	var parts []string
	for _, v := range views {
		text := fmt.Sprintf("[%s]%s", v.key, v.label)
		if v.v == current {
			parts = append(parts, colorize(text, colorCyan))
		} else {
			parts = append(parts, tuiDim(text))
		}
	}
	parts = append(parts, tuiDim("[r]efresh"), tuiDim("[q]uit"))
	fmt.Printf("  %s\n", strings.Join(parts, "  "))
	fmt.Printf("  %s\n", tuiDim("Auto-refreshes every 5s"))
}

func tuiStatusColor(status string) int {
	switch status {
	case "done":
		return colorGreen
	case "implementing", "active":
		return colorYellow
	case "draft", "planning":
		return colorBlue
	case "blocked", "failed":
		return colorRed
	case "agent-qa", "human-qa":
		return colorMagenta
	default:
		return colorWhite
	}
}

func renderTUIDashboard(database *sql.DB) {
	overview, err := engine.GetStatusOverview(database)
	if err != nil {
		fmt.Printf("  %s %v\n", colorize("Error:", colorRed), err)
		return
	}

	renderTUIHeader(overview.Project.Name)

	// Summary counts.
	total := 0
	for _, c := range overview.FeatureCounts {
		total += c
	}
	fmt.Printf("  %s features · %s milestones · %s active cycles\n\n",
		colorize(fmt.Sprintf("%d", total), colorCyan),
		colorize(fmt.Sprintf("%d", overview.MilestoneCount), colorCyan),
		colorize(fmt.Sprintf("%d", overview.ActiveCycles), colorCyan))

	// Feature bar chart.
	if len(overview.FeatureCounts) > 0 {
		fmt.Printf("  %s\n", tuiBold("FEATURES BY STATUS"))
		statusOrder := []string{
			"done", "implementing", "agent-qa", "human-qa",
			"planning", "draft", "blocked",
		}
		rendered := make(map[string]bool)
		for _, s := range statusOrder {
			if count, ok := overview.FeatureCounts[s]; ok && count > 0 {
				bar := tuiProgressBar(count, total, 20)
				fmt.Printf("  %-14s %s %d\n",
					colorize(s, tuiStatusColor(s)),
					colorize(bar, tuiStatusColor(s)),
					count)
				rendered[s] = true
			}
		}
		for s, count := range overview.FeatureCounts {
			if !rendered[s] && count > 0 {
				bar := tuiProgressBar(count, total, 20)
				fmt.Printf("  %-14s %s %d\n",
					colorize(s, tuiStatusColor(s)),
					colorize(bar, tuiStatusColor(s)),
					count)
			}
		}
		fmt.Println()
	}

	// Active cycles.
	cycles, _ := db.ListActiveCycles(database)
	if len(cycles) > 0 {
		fmt.Printf("  %s\n", tuiBold("ACTIVE CYCLES"))
		for _, c := range cycles {
			fmt.Printf("  %s · %s · %s\n",
				colorize(c.CycleType, colorYellow),
				c.EntityID,
				tuiCycleStepInfo(c))
		}
		fmt.Println()
	}

	// Active work.
	if len(overview.ActiveWork) > 0 {
		fmt.Printf("  %s\n", tuiBold("ACTIVE WORK"))
		for _, w := range overview.ActiveWork {
			fmt.Printf("  %s %s (%s)\n",
				colorize("▸", colorGreen),
				w.WorkType,
				w.FeatureID)
		}
		fmt.Println()
	}

	// Recent events (last 5).
	if len(overview.RecentEvents) > 0 {
		fmt.Printf("  %s\n", tuiBold("RECENT EVENTS"))
		limit := min(5, len(overview.RecentEvents))
		for _, e := range overview.RecentEvents[:limit] {
			ts := tuiFormatTime(e.CreatedAt)
			line := fmt.Sprintf("  %s %s", tuiDim(ts), e.EventType)
			if e.FeatureID != "" {
				line += fmt.Sprintf(" (%s)", colorize(e.FeatureID, colorCyan))
			}
			fmt.Println(line)
		}
	}
}

func renderTUIFeatures(database *sql.DB) {
	renderTUIHeader("Features")

	p, err := db.GetProject(database)
	if err != nil {
		fmt.Printf("  %s %v\n", colorize("Error:", colorRed), err)
		return
	}

	features, err := db.ListFeatures(database, p.ID, "", "")
	if err != nil {
		fmt.Printf("  %s %v\n", colorize("Error:", colorRed), err)
		return
	}

	if len(features) == 0 {
		fmt.Printf("  %s\n", tuiDim("No features found."))
		return
	}

	// Header row.
	fmt.Printf("  %s  %s  %s  %s\n",
		tuiBold(tuiPadRight("ID", 20)),
		tuiBold(tuiPadRight("NAME", 30)),
		tuiBold(tuiPadRight("STATUS", 14)),
		tuiBold("PRI"))
	fmt.Printf("  %s\n", tuiDim(strings.Repeat("─", 72)))

	for _, f := range features {
		id := tuiTruncate(f.ID, 20)
		name := tuiTruncate(f.Name, 30)
		status := colorize(f.Status, tuiStatusColor(f.Status))
		pad := max(0, 14-len(f.Status))
		fmt.Printf("  %-20s  %-30s  %s%s  %3d\n",
			id, name, status, strings.Repeat(" ", pad), f.Priority)
	}

	fmt.Printf("\n  %s %d features\n", tuiDim("Total:"), len(features))
}

func renderTUIMilestones(database *sql.DB) {
	renderTUIHeader("Milestones")

	p, err := db.GetProject(database)
	if err != nil {
		fmt.Printf("  %s %v\n", colorize("Error:", colorRed), err)
		return
	}

	milestones, err := db.ListMilestones(database, p.ID)
	if err != nil {
		fmt.Printf("  %s %v\n", colorize("Error:", colorRed), err)
		return
	}

	if len(milestones) == 0 {
		fmt.Printf("  %s\n", tuiDim("No milestones found."))
		return
	}

	for _, m := range milestones {
		fmt.Printf("  %s  %s\n",
			tuiBold(m.Name),
			colorize(m.Status, tuiStatusColor(m.Status)))
		if m.Description != "" {
			fmt.Printf("  %s\n", tuiDim(m.Description))
		}
		bar := tuiProgressBar(m.DoneFeatures, m.TotalFeatures, 30)
		pct := 0
		if m.TotalFeatures > 0 {
			pct = (m.DoneFeatures * 100) / m.TotalFeatures
		}
		fmt.Printf("  [%s] %d/%d (%d%%)\n\n",
			colorize(bar, colorGreen),
			m.DoneFeatures, m.TotalFeatures, pct)
	}
}

// --- Helpers ---

func tuiCycleStepInfo(c models.CycleInstance) string {
	for _, ct := range models.CycleTypes {
		if ct.Name == c.CycleType {
			total := len(ct.Steps)
			if c.CurrentStep >= 0 && c.CurrentStep < total {
				return fmt.Sprintf("Step %d/%d (%s)",
					c.CurrentStep+1, total,
					colorize(ct.Steps[c.CurrentStep].Name, colorCyan))
			}
			return fmt.Sprintf("Step %d/%d", c.CurrentStep+1, total)
		}
	}
	return fmt.Sprintf("Step %d (iter %d)", c.CurrentStep+1, c.Iteration)
}

func tuiFormatTime(ts string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Format("01/02 15:04")
		}
	}
	if len(ts) > 16 {
		return ts[:16]
	}
	return ts
}

func tuiTruncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen-1] + "…"
	}
	return s
}

func tuiPadRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
