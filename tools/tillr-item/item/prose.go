package item

import "strings"

// proseVerbs is the imperative list in the repository's tillr-prose-style skill.
var proseVerbs = map[string]bool{
	"add": true, "move": true, "route": true, "prevent": true,
	"isolate": true, "hold": true, "test": true,
}

// proseWarnings checks only cheap, observable shape, not prose quality. These notes go
// into Warnings, never Findings: Findings prevent a file from being synced.
func proseWarnings(b syncBlock) []SyncNote {
	var warnings []SyncNote
	if b.payload.Prose != "2" {
		warnings = append(warnings, note(b, "prose-unmarked", "the current guide mark is absent or outdated"))
	}
	shape := func(message string) { warnings = append(warnings, note(b, "prose-shape", message)) }
	words := strings.Fields(b.payload.Title)
	if len(words) == 0 || !proseVerbs[strings.ToLower(words[0])] {
		shape("title-verb: title does not start with an imperative verb from the prose guide's list")
	}
	if len(words) > 12 {
		shape("title-length: title exceeds 12 words")
	}
	if !b.proseParagraph {
		shape("description: section has no prose paragraph before the directive")
	}
	if len(b.payload.Criteria) > 8 {
		shape("criteria-count: block exceeds eight acceptance criteria")
	}
	return warnings
}

// hasProseParagraph checks the item's own heading section before its directive. Ignore
// code, comments, lists (including continuation lines), quotes, and tables rather than
// letting their text stand in for the motivation paragraph the guide asks for.
func hasProseParagraph(lines []string, fenced []bool, start, end int) bool {
	comment, nonProse := false, false
	listIndent := -1
	for i := start; i < end; i++ {
		text := strings.TrimSpace(lines[i])
		if fenced[i] {
			nonProse = false
			continue
		}
		if comment {
			if strings.Contains(text, "-->") {
				comment = false
			}
			continue
		}
		if strings.HasPrefix(text, "<!--") {
			comment = !strings.Contains(text, "-->")
			continue
		}
		if text == "" {
			nonProse = false
			continue
		}
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " \t"))
		if listMarker.MatchString(text) {
			listIndent = indent
			nonProse = true
			continue
		}
		// Blank lines do not end a list's indented continuation paragraph.
		if listIndent >= 0 {
			if indent > listIndent {
				continue
			}
			listIndent = -1
		}
		if strings.HasPrefix(text, ">") || strings.HasPrefix(text, "|") || text == "---" || text == "***" || text == "___" {
			nonProse = true
			continue
		}
		if nonProse || strings.HasPrefix(lines[i], "    ") || strings.HasPrefix(lines[i], "\t") {
			continue
		}
		return true
	}
	return false
}
