package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const (
	deliveredPrefix     = "yolo-packs/tillr/files/local/share/tillr/"
	deliveredDocsPrefix = deliveredPrefix + "docs/"
)

var (
	scratchRe               = regexp.MustCompile(`\.yolo/durable|YOLO_DURABLE_DIR`)
	designCitationDocRe     = regexp.MustCompile(`(?i)\.(?:md|markdown)(?:#.*)?$`)
	markdownHeadingRe       = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	markdownHTMLAnchorRe    = regexp.MustCompile(`<a\s+[^>]*(?:id|name)="([^"]+)"`)
	markdownVantageQuestion = regexp.MustCompile(`<!--\s*vantage:\s*question\s+id=([^\s]+)`)
	yamlListItemRe          = regexp.MustCompile(`^-\s+(.*)$`)
)

var scratchExceptions = []struct {
	allowed string
	marker  string
}{
	{"docs/reference/tillr-distribution.md", "$YOLO_DURABLE_DIR/bin"},
	{"tools/tillr-view/README.md", "tillr-observatory/<key>/`, where `<key>`"},
	{"tools/tillr-view/README.md", "`<checkout>/.yolo/durable/tillr-observatory/logs/<hostname>-<port>/` and prints it at"},
}

var designCitationFields = map[string]bool{
	"depends-on":    true,
	"superseded-by": true,
	"supersedes":    true,
}

var designCitationRoots = []string{"", "docs", "docs/design"}

func gitOutput(root string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func trackedMarkdown(root string) ([]string, error) {
	out, err := gitOutput(root, "ls-files", "*.md")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var paths []string
	for _, p := range strings.Split(out, "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func excusedAs(path string) string {
	if !strings.HasPrefix(path, deliveredPrefix) {
		return path
	}
	rest := path[len(deliveredPrefix):]
	if strings.HasPrefix(rest, "docs/") {
		return rest
	}
	return "tools/" + rest
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func scratchCitations(root string) ([]string, error) {
	paths, err := trackedMarkdown(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, err
		}
		inFence := false
		for i, line := range splitLines(string(data)) {
			number := i + 1
			trimmedLeft := strings.TrimLeft(line, " \t\v\f\r")
			if strings.HasPrefix(trimmedLeft, "```") || strings.HasPrefix(trimmedLeft, "~~~") {
				inFence = !inFence
				continue
			}
			if inFence || !scratchRe.MatchString(line) {
				continue
			}
			excused := excusedAs(path)
			allowedHit := false
			for _, ex := range scratchExceptions {
				if excused == ex.allowed && strings.Contains(line, ex.marker) {
					allowedHit = true
					break
				}
			}
			if allowedHit {
				continue
			}
			findings = append(findings, fmt.Sprintf("%s:%d: %s", path, number, strings.TrimSpace(line)))
		}
	}
	return findings, nil
}

type frontmatterEntry struct {
	number int
	line   string
}

func frontmatterEntries(text string) []frontmatterEntry {
	lines := splitLines(text)
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			entries := make([]frontmatterEntry, 0, index-1)
			for number := 2; number <= index; number++ {
				entries = append(entries, frontmatterEntry{number: number, line: lines[number-1]})
			}
			return entries
		}
	}
	return nil
}

type frontmatterCitation struct {
	number int
	field  string
	value  string
}

func stripYAMLComment(value string) string {
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = value[:idx]
	}
	return strings.Trim(strings.TrimSpace(value), "\"'")
}

func frontmatterCitations(entries []frontmatterEntry) []frontmatterCitation {
	var citations []frontmatterCitation
	field := ""
	for _, entry := range entries {
		stripped := strings.TrimSpace(entry.line)
		name, value, hasSep := strings.Cut(stripped, ":")
		if hasSep && !strings.HasPrefix(stripped, "-") {
			trimmedName := strings.TrimSpace(name)
			if designCitationFields[trimmedName] {
				field = trimmedName
			} else {
				field = ""
				continue
			}
			value = strings.TrimSpace(value)
			if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
				for _, part := range strings.Split(value[1:len(value)-1], ",") {
					candidate := strings.Trim(strings.TrimSpace(part), "\"'")
					if candidate != "" {
						citations = append(citations, frontmatterCitation{number: entry.number, field: field, value: candidate})
					}
				}
			} else if value != "" {
				citations = append(citations, frontmatterCitation{number: entry.number, field: field, value: stripYAMLComment(value)})
			}
			continue
		}
		if field == "" {
			continue
		}
		if match := yamlListItemRe.FindStringSubmatch(stripped); match != nil {
			candidate := stripYAMLComment(strings.TrimSpace(match[1]))
			if candidate != "" {
				citations = append(citations, frontmatterCitation{number: entry.number, field: field, value: candidate})
			}
			continue
		}
		if stripped != "" && !strings.HasPrefix(entry.line, " ") && !strings.HasPrefix(entry.line, "\t") {
			field = ""
		}
	}
	return citations
}

func markdownSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), "-")
}

func markdownAnchors(text string) map[string]bool {
	anchors := make(map[string]bool)
	for _, line := range splitLines(text) {
		if match := markdownHeadingRe.FindStringSubmatch(line); match != nil {
			anchors[markdownSlug(match[1])] = true
		}
		for _, match := range markdownHTMLAnchorRe.FindAllStringSubmatch(line, -1) {
			anchors[strings.ToLower(match[1])] = true
		}
		for _, match := range markdownVantageQuestion.FindAllStringSubmatch(line, -1) {
			anchors[strings.ToLower(match[1])] = true
		}
	}
	return anchors
}

func designCitationFindings(root string) ([]string, error) {
	paths, err := trackedMarkdown(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	anchorCache := make(map[string]map[string]bool)

	anchorsOf := func(relative string) map[string]bool {
		if cached, ok := anchorCache[relative]; ok {
			return cached
		}
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			anchorCache[relative] = nil
			return nil
		}
		anchors := markdownAnchors(string(data))
		anchorCache[relative] = anchors
		return anchors
	}

	for _, path := range paths {
		if strings.HasPrefix(path, deliveredDocsPrefix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			continue
		}
		for _, c := range frontmatterCitations(frontmatterEntries(string(data))) {
			if !designCitationDocRe.MatchString(c.value) {
				continue
			}
			target, anchor, _ := strings.Cut(c.value, "#")
			resolved := ""
			if target != "" {
				bases := append([]string{filepath.Dir(path)}, designCitationRoots...)
				for _, base := range bases {
					candidate := filepath.Clean(filepath.Join(base, target))
					if strings.HasPrefix(candidate, "..") || strings.HasPrefix(candidate, "/") {
						continue
					}
					if info, err := os.Stat(filepath.Join(root, candidate)); err == nil && !info.IsDir() {
						resolved = filepath.ToSlash(candidate)
						break
					}
				}
			} else {
				resolved = path
			}
			if resolved == "" {
				findings = append(findings, fmt.Sprintf(
					"%s:%d: %s: %s: names no tracked document (looked beside the document, at the repository root, and under docs/ and docs/design/)",
					path, c.number, c.field, c.value,
				))
				continue
			}
			if anchor == "" {
				continue
			}
			present := anchorsOf(resolved)
			if present != nil && present[strings.ToLower(anchor)] {
				continue
			}
			findings = append(findings, fmt.Sprintf(
				"%s:%d: %s: %s: %s has no such section (no heading, HTML anchor or vantage question matches #%s)",
				path, c.number, c.field, c.value, resolved, anchor,
			))
		}
	}
	return findings, nil
}

func cmdDesignCitations(args []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "design-citations: could not judge: %v\n", err)
		return 4
	}
	findings, err := designCitationFindings(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "design-citations: could not judge: %v\n", err)
		return 4
	}
	if len(findings) > 0 {
		fmt.Println(strings.Join(findings, "\n"))
		return 1
	}
	return 0
}

func cmdScratch(args []string) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "scratch: could not judge: %v\n", err)
		return 4
	}
	findings, err := scratchCitations(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scratch: could not judge: %v\n", err)
		return 4
	}
	if len(findings) > 0 {
		fmt.Println(strings.Join(findings, "\n"))
		return 1
	}
	return 0
}

func cmdExcusedAs(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "excused-as: expected <path>")
		return 2
	}
	fmt.Println(excusedAs(args[0]))
	return 0
}
