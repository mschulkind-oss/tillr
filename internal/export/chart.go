package export

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// statusColors maps feature/roadmap statuses to SVG-friendly colors.
var statusColors = map[string]string{
	"draft":        "#6b7280",
	"planning":     "#f59e0b",
	"implementing": "#3b82f6",
	"agent-qa":     "#8b5cf6",
	"human-qa":     "#a855f7",
	"done":         "#22c55e",
	"blocked":      "#ef4444",
	"critical":     "#ef4444",
	"high":         "#f97316",
	"medium":       "#eab308",
	"low":          "#22c55e",
	"nice-to-have": "#3b82f6",
	"proposed":     "#6b7280",
	"accepted":     "#3b82f6",
	"in-progress":  "#f59e0b",
	"completed":    "#22c55e",
	"deferred":     "#9ca3af",
	"rejected":     "#ef4444",
}

func colorForKey(key string) string {
	if c, ok := statusColors[key]; ok {
		return c
	}
	// Deterministic fallback color based on string hash
	colors := []string{"#3b82f6", "#ef4444", "#22c55e", "#f59e0b", "#8b5cf6", "#ec4899", "#14b8a6", "#f97316"}
	h := 0
	for _, c := range key {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return colors[h%len(colors)]
}

// BarChartSVG generates a horizontal bar chart as SVG.
func BarChartSVG(w io.Writer, title string, data map[string]int) error {
	if len(data) == 0 {
		return fmt.Errorf("no data to chart")
	}

	// Sort keys for consistent output
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Find max value for scaling
	maxVal := 0
	for _, v := range data {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	barHeight := 32
	barSpacing := 8
	labelWidth := 120
	chartWidth := 500
	padding := 40
	totalHeight := padding*2 + 40 + len(keys)*(barHeight+barSpacing)
	totalWidth := labelWidth + chartWidth + padding*2

	pr := newPrinter(w)

	pr.printf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">
<style>
  text { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; }
  .title { font-size: 16px; font-weight: 600; fill: #1f2937; }
  .label { font-size: 12px; fill: #4b5563; text-anchor: end; dominant-baseline: middle; }
  .value { font-size: 11px; fill: #6b7280; dominant-baseline: middle; }
  .bar { rx: 4; ry: 4; }
  .bg { fill: #f9fafb; }
  .grid { stroke: #e5e7eb; stroke-width: 1; }
</style>
<rect class="bg" width="%d" height="%d" rx="8" ry="8"/>
`, totalWidth, totalHeight, totalWidth, totalHeight, totalWidth, totalHeight)

	// Title
	pr.printf(`<text class="title" x="%d" y="%d">%s</text>
`, padding, padding, escapeXML(title))

	y := padding + 40

	for _, key := range keys {
		val := data[key]
		barWidth := int(float64(val) / float64(maxVal) * float64(chartWidth-60))
		if barWidth < 4 {
			barWidth = 4
		}
		color := colorForKey(key)

		// Label
		pr.printf(`<text class="label" x="%d" y="%d">%s</text>
`, padding+labelWidth-8, y+barHeight/2, escapeXML(key))

		// Bar
		pr.printf(`<rect class="bar" x="%d" y="%d" width="%d" height="%d" fill="%s" opacity="0.85"/>
`, padding+labelWidth, y, barWidth, barHeight, color)

		// Value
		pr.printf(`<text class="value" x="%d" y="%d">%d</text>
`, padding+labelWidth+barWidth+8, y+barHeight/2, val)

		y += barHeight + barSpacing
	}

	pr.printf("</svg>\n")
	return pr.err
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
