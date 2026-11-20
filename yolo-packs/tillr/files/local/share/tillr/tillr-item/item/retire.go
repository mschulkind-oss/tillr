// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package item

import (
	"regexp"
	"strings"
)

var retirementAttribute = regexp.MustCompile(strings.TrimPrefix(attribute.String(), "^"))

// RetireItemBlock removes an item's directive and criterion lines, leaving headings,
// whitespace, and unrelated prose byte-identical. It also removes the exact item id from
// other directives' blocked_by lists. found says whether a declaring block was present;
// dependencies are updated even when a worker already removed that block.
func RetireItemBlock(text, id string) (retired string, found bool) {
	blocks, _ := parseItemBlocks("", []byte(text))
	lines := strings.SplitAfter(text, "\n")
	removed := make(map[int]bool)
	for _, b := range blocks {
		start := b.line - 1
		if b.payload.ItemID == id {
			found = true
			for i := start; i < b.directiveEnd; i++ {
				removed[i] = true
			}
			for _, i := range b.criterionLines {
				removed[i] = true
			}
			continue
		}
		// Only rewrite the attribute value, not the directive's spacing or quote style.
		directive := strings.Join(lines[start:b.directiveEnd], "")
		bodyStart := strings.Index(directive, "<!--") + len("<!--")
		bodyEnd := strings.Index(directive, "-->")
		body := directive[bodyStart:bodyEnd]
		for _, m := range retirementAttribute.FindAllStringSubmatchIndex(body, -1) {
			if body[m[2]:m[3]] != "blocked_by" {
				continue
			}
			for g := 4; g <= 8; g += 2 {
				if m[g] < 0 {
					continue
				}
				value := body[m[g]:m[g+1]]
				kept := []string{}
				changed := false
				for _, part := range strings.Split(value, ",") {
					if strings.TrimSpace(part) == id {
						changed = true
					} else {
						kept = append(kept, part)
					}
				}
				if changed {
					a, z := bodyStart+m[g], bodyStart+m[g+1]
					replacement := strings.Join(kept, ",")
					if replacement == "" && g == 8 {
						// An unquoted empty value is not a valid attribute.
						replacement = `""`
					}
					directive = directive[:a] + replacement + directive[z:]
					lines[start] = directive
					for i := start + 1; i < b.directiveEnd; i++ {
						lines[i] = ""
					}
				}
				break
			}
			break
		}
	}
	var out strings.Builder
	for i, line := range lines {
		if !removed[i] {
			out.WriteString(line)
		}
	}
	return out.String(), found
}
