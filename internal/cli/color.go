package cli

import (
	"hash/fnv"
	"os"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Color helpers for the CLI. Auto-disable when stdout isn't a TTY
// (or when NO_COLOR is set), so piped consumers / JSON consumers /
// CI logs see plain text.
//
// Use the semantic functions (Status, Persona, Header, Dim, etc.) —
// not raw ANSI escapes. The semantic functions hide the color
// detection and let us re-skin without rewriting call sites.
//
// `--json` output never uses these helpers; the JSON encoder writes
// straight to stdout.

var (
	colorsEnabled = computeColorsEnabled()

	// Palette. Subdued; dark-mode and light-mode friendly.
	cBold    = lipgloss.NewStyle().Bold(true)
	cDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	cAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	cHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Bold(true)
	cSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	cDanger  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	cWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	cInfo    = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	cCode    = lipgloss.NewStyle().Foreground(lipgloss.Color("180"))
)

// computeColorsEnabled inspects stdout and the NO_COLOR env var.
// True only if stdout is a TTY and NO_COLOR is unset.
func computeColorsEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

func styled(s string, st lipgloss.Style) string {
	if !colorsEnabled {
		return s
	}
	return st.Render(s)
}

// Bold renders bold text. No-op when colors are disabled.
func Bold(s string) string { return styled(s, cBold) }

// Dim renders subtle / muted text.
func Dim(s string) string { return styled(s, cDim) }

// Accent renders the project's primary accent.
func Accent(s string) string { return styled(s, cAccent) }

// Header renders a section heading.
func Header(s string) string { return styled(s, cHeader) }

// Success renders affirmative text (green).
func Success(s string) string { return styled(s, cSuccess) }

// Danger renders error / blocking text (red).
func Danger(s string) string { return styled(s, cDanger) }

// Warning renders cautionary text (orange).
func Warning(s string) string { return styled(s, cWarning) }

// Info renders informational accents (cyan/blue).
func Info(s string) string { return styled(s, cInfo) }

// Code renders inline code / paths / IDs (tan).
func Code(s string) string { return styled(s, cCode) }

// Status colors a feature/run status string. Defaults to dim for
// unknown statuses.
func Status(s string) string {
	switch s {
	case "draft", "queued", "pending":
		return Dim(s)
	case "claimed", "running":
		return Info(s)
	case "human-qa", "needs_review":
		return Warning(s)
	case "done", "completed":
		return Success(s)
	case "blocked":
		return Warning(s)
	case "error", "failed", "rejected":
		return Danger(s)
	default:
		return Dim(s)
	}
}

// Persona colors a persona name. Same name gets the same color via a
// stable hash → palette lookup, so the same persona reads the same
// across the whole UI.
func Persona(name string) string {
	if !colorsEnabled || name == "" {
		return name
	}
	palette := []string{"75", "141", "108", "180", "215", "204", "111"}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	idx := int(h.Sum32()) % len(palette)
	if idx < 0 {
		idx = -idx
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(palette[idx]))
	return style.Render(name)
}

// Money colors a USD amount: zero → dim, >$1 → warning, >$10 → danger.
func Money(usd float64) string {
	formatted := fmtUSD(usd)
	switch {
	case usd <= 0:
		return Dim(formatted)
	case usd < 1.0:
		return formatted
	case usd < 10.0:
		return Warning(formatted)
	default:
		return Danger(formatted)
	}
}

// Duration colors a millisecond duration: <1s green, <10s default,
// <60s warning, ≥60s danger.
func Duration(ms int64) string {
	formatted := durFmt(ms)
	switch {
	case ms < 1000:
		return Success(formatted)
	case ms < 10_000:
		return formatted
	case ms < 60_000:
		return Warning(formatted)
	default:
		return Danger(formatted)
	}
}

func fmtUSD(f float64) string {
	// Standard "$0.0123" 4-decimal format.
	scaled := int64(f*10000 + 0.5)
	whole := scaled / 10000
	frac := scaled - whole*10000
	fs := iToA(frac)
	for len(fs) < 4 {
		fs = "0" + fs
	}
	return "$" + iToA(whole) + "." + fs
}

func durFmt(ms int64) string {
	switch {
	case ms < 1000:
		return iToA(ms) + "ms"
	case ms < 60_000:
		s := float64(ms) / 1000
		return ftoaPrec(s, 1) + "s"
	case ms < 3_600_000:
		m := float64(ms) / 60_000
		return ftoaPrec(m, 1) + "m"
	default:
		h := float64(ms) / 3_600_000
		return ftoaPrec(h, 1) + "h"
	}
}

func iToA(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ftoaPrec(f float64, prec int) string {
	if prec < 0 {
		prec = 0
	}
	scale := int64(1)
	for i := 0; i < prec; i++ {
		scale *= 10
	}
	if f < 0 {
		return "-" + ftoaPrec(-f, prec)
	}
	scaled := int64(f*float64(scale) + 0.5)
	whole := scaled / scale
	frac := scaled - whole*scale
	if prec == 0 {
		return iToA(whole)
	}
	fs := iToA(frac)
	for len(fs) < prec {
		fs = "0" + fs
	}
	return iToA(whole) + "." + fs
}
