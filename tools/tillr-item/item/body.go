package item

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefinitionBlockExists reports whether the document still declares the same item.
// It uses sync's directive parser, including its code-fence exclusions.
func DefinitionBlockExists(file, itemID string, raw []byte) bool {
	blocks, _ := parseItemBlocks(file, raw)
	for _, block := range blocks {
		if block.payload.ItemID == itemID {
			return true
		}
	}
	return false
}

// DefinitionBlockIDs reads declared identities, even when another field needs
// repair. Fixing or moving an existing declaration does not create a new item.
func DefinitionBlockIDs(file string, raw []byte) []string {
	blocks, _ := parseItemBlocks(file, raw)
	ids := []string{}
	for _, b := range blocks {
		if itemIDShape.MatchString(b.payload.ItemID) {
			ids = append(ids, b.payload.ItemID)
		}
	}
	return ids
}

// BlocksFrom reads actual item declarations carrying the named source. Code-fenced
// examples and malformed declarations are not outcomes; sync owns the parsing rules.
func BlocksFrom(file, from string, raw []byte) []string {
	blocks, findings := parseItemBlocks(file, raw)
	ids := []string{}
	for _, b := range blocks {
		if b.attributes["from"] != from || from == "" {
			continue
		}
		valid := true
		for _, finding := range findings {
			if finding.Line == b.line {
				valid = false
				break
			}
		}
		if valid {
			ids = append(ids, b.payload.ItemID)
		}
	}
	return ids
}

// DefinitionBody reads the section named by body_ref, excluding unrelated sections.
// Missing files and anchors are explicit input values: creating the missing source is
// a change too. Other read errors are returned, never mistaken for unchanged input.
func DefinitionBody(root, ref, itemID string) (string, error) {
	if ref == "" || root == "" {
		return "body source not named", nil
	}
	file, _, _ := strings.Cut(ref, "#")
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return "body source missing: " + ref, nil
	}
	if err != nil {
		return "", err
	}
	return DefinitionBodyContent(ref, itemID, raw), nil
}

// DefinitionBodyContent reads the same definition from pinned git blob bytes rather
// than a working tree. Missing anchors stay explicit, so deleting a block is a change.
func DefinitionBodyContent(ref, itemID string, raw []byte) string {
	_, anchor, _ := strings.Cut(ref, "#")
	lines := splitLines(raw)
	blocks, _ := parseItemBlocks(ref, raw)
	for _, block := range blocks {
		if block.payload.ItemID != itemID {
			continue
		}
		i := block.line - 1
		indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " \t"))
		if indent == 0 {
			break
		}
		// Roadmap bullets may share a heading and body_ref. Their own enclosing
		// bullet, not that heading's entire section, is the definition.
		for k := i - 1; k >= 0; k-- {
			if headingLine.MatchString(lines[k]) {
				break
			}
			m := bulletTitle.FindStringSubmatch(lines[k])
			if m == nil || len(m[1]) >= indent {
				continue
			}
			end := len(lines)
			fence := fenceState{}
			for n := k + 1; n < len(lines); n++ {
				if fence.step(lines[n]) || strings.TrimSpace(lines[n]) == "" {
					continue
				}
				if len(lines[n])-len(strings.TrimLeft(lines[n], " \t")) <= len(m[1]) {
					end = n
					break
				}
			}
			return strings.Join(lines[k:end], "\n")
		}
	}
	if anchor == "" {
		return string(raw)
	}
	start, level := -1, 0
	fence := fenceState{}
	for i, line := range lines {
		if fence.step(line) {
			continue
		}
		m := headingLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if start >= 0 && len(m[1]) <= level {
			return strings.Join(lines[start:i], "\n")
		}
		if start < 0 && headingSlug(strings.TrimSpace(strings.TrimRight(m[2], "#"))) == anchor {
			start, level = i, len(m[1])
		}
	}
	if start >= 0 {
		return strings.Join(lines[start:], "\n")
	}
	return fmt.Sprintf("body anchor missing: %s", ref)
}
