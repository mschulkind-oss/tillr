package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const roadmapPath = "docs/tillr/roadmap.md"

var (
	nonCanonicalATXRe = regexp.MustCompile(`(?m)^ {1,3}#{1,6}(?:[ \t]|$)|^#{1,6}\t`)
	h2HeadingRe       = regexp.MustCompile(`(?m)^## `)
	setextRuleRe      = regexp.MustCompile(`^\s*-{3,}\s*$`)
	htmlAnchorExactRe = regexp.MustCompile(`^<a id="[^"]+"></a>$`)
	tillrCommentRe    = regexp.MustCompile(`^<!-- tillr:.* -->$`)
	roadmapAnchorRe   = regexp.MustCompile(`(?m)^#{1,6} .*$|<a id="[^"]+"></a>`)
	selfLineLinkRe    = regexp.MustCompile(`#L\d`)
	fragmentLinkRe    = regexp.MustCompile(`(\]\(\s*<?)#`)
	refDefRe          = regexp.MustCompile(`^\s*\[[^\]]+\]:`)
	equalsRuleRe      = regexp.MustCompile(`^\s*={3,}\s*$`)
)

type roadmapSection struct {
	body      string
	startLine int
}

type roadmapLocation struct {
	path       string
	lineOffset int
	startLine  int
}

func hasCrossSectionConstruct(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimLeft(line, " \t\v\f\r")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			return true
		}
		if refDefRe.MatchString(line) || equalsRuleRe.MatchString(line) {
			return true
		}
		if strings.Contains(line, "<!-- vantage:") {
			return true
		}
		if strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "<a id=") && !strings.HasPrefix(trimmed, "<!-- tillr:") {
			return true
		}
	}
	return false
}

func splitFrontmatter(text string) (header string, rest string, hasHeader bool) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text, false
	}
	idx := strings.Index(text[3:], "\n---\n")
	if idx < 0 {
		return "", text, false
	}
	end := 3 + idx + 5
	return text[:end], text[end:], true
}

func splitRoadmapSections(text string) (string, []roadmapSection, bool) {
	if strings.Contains(text, "$$") {
		return "", nil, false
	}
	if nonCanonicalATXRe.MatchString(text) {
		return "", nil, false
	}
	header, withoutHeader, _ := splitFrontmatter(text)
	if hasCrossSectionConstruct(withoutHeader) {
		return "", nil, false
	}
	if strings.Contains(header, "planning-actions") || strings.Contains(header, "planning_actions") {
		return "", nil, false
	}
	bodyLines := splitLines(withoutHeader)
	for index, line := range bodyLines {
		if setextRuleRe.MatchString(line) && index > 0 && strings.TrimSpace(bodyLines[index-1]) != "" {
			return "", nil, false
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.TrimLeft(line, " \t\v\f\r"), "<") &&
			!htmlAnchorExactRe.MatchString(trimmed) && !tillrCommentRe.MatchString(trimmed) {
			return "", nil, false
		}
	}
	for _, line := range splitLines(text) {
		if strings.Contains(line, "<!--") && !strings.Contains(line, "-->") {
			return "", nil, false
		}
	}

	matches := h2HeadingRe.FindAllStringIndex(withoutHeader, -1)
	starts := make([]int, 0, len(matches)+2)
	starts = append(starts, len(header))
	for _, m := range matches {
		starts = append(starts, len(header)+m[0])
	}
	starts = append(starts, len(text))

	var sections []roadmapSection
	for i := 0; i < len(starts)-1; i++ {
		start, end := starts[i], starts[i+1]
		if start < end {
			startLine := strings.Count(text[:start], "\n") + 1
			sections = append(sections, roadmapSection{body: text[start:end], startLine: startLine})
		}
	}
	if len(sections) == 0 {
		return "", nil, false
	}
	return header, sections, true
}

func prepareRoadmapSections(root, head string, documents []string, keys map[string]string, unsettled map[string][]string) ([]string, map[string]roadmapLocation, []string, error) {
	paths := append([]string(nil), documents...)
	locations := make(map[string]roadmapLocation)
	var temporary []string

	hasRoadmap := false
	for _, p := range paths {
		if p == roadmapPath {
			hasRoadmap = true
			break
		}
	}
	if !hasRoadmap {
		return paths, locations, temporary, nil
	}

	rawText, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(roadmapPath)))
	if err != nil {
		return nil, nil, nil, err
	}
	text := string(rawText)
	header, sections, ok := splitRoadmapSections(text)

	entries, err := treeEntries(root, head)
	if err != nil {
		return nil, nil, nil, err
	}
	configs := make(map[string]string)
	for p, e := range entries {
		if documentConfigNames[path.Base(p)] {
			configs[p] = e.obj
		}
	}
	unrouted := false
	for p := range configs {
		if strings.HasSuffix(p, ".toml") {
			content, err := gitRun(root, true, "show", head+":"+p)
			if err != nil {
				return nil, nil, nil, err
			}
			if strings.Contains(content, "unrouted") {
				unrouted = true
				break
			}
		}
	}
	if !ok || unrouted {
		fmt.Fprintln(os.Stderr, "documents: roadmap uses cross-section markup or unrouted planning; checking the full file")
		return paths, locations, temporary, nil
	}

	code := make(map[string]string, len(documentCheckFiles))
	for _, p := range documentCheckFiles {
		if e, exists := entries[p]; exists {
			code[p] = e.obj
		} else {
			code[p] = "absent"
		}
	}
	anchors := roadmapAnchorRe.FindAllString(text, -1)
	if anchors == nil {
		anchors = []string{}
	}
	var siblings []string
	roadmapDir := path.Dir(roadmapPath)
	for p := range entries {
		if path.Dir(p) == roadmapDir {
			siblings = append(siblings, p)
		}
	}
	sort.Strings(siblings)

	var filtered []string
	for _, p := range paths {
		if p != roadmapPath {
			filtered = append(filtered, p)
		}
	}
	paths = filtered

	roadmapDirFS := filepath.Join(root, filepath.FromSlash(roadmapDir))
	for _, sec := range sections {
		named, absent := namedInputs(header+sec.body, roadmapPath, entries)
		delete(named, roadmapPath)
		absentList := make([]string, 0, len(absent))
		for a := range absent {
			absentList = append(absentList, a)
		}
		sort.Strings(absentList)

		identity := map[string]any{
			"format":   "roadmap-sections-1",
			"path":     roadmapPath,
			"mode":     entries[roadmapPath].mode,
			"header":   header,
			"anchors":  anchors,
			"configs":  configs,
			"code":     code,
			"siblings": siblings,
			"body":     sec.body,
			"named":    named,
			"absent":   absentList,
		}
		if selfLineLinkRe.MatchString(header + sec.body) {
			identity["line_count"] = len(splitLines(text))
		}
		raw, err := json.Marshal(identity)
		if err != nil {
			cleanupRoadmapSections(temporary)
			return nil, nil, nil, err
		}
		sum := sha256.Sum256(raw)
		key := hex.EncodeToString(sum[:])

		f, err := os.CreateTemp(roadmapDirFS, ".gate-roadmap-*.md")
		if err != nil {
			cleanupRoadmapSections(temporary)
			return nil, nil, nil, err
		}
		filename := f.Name()
		temporary = append(temporary, filename)
		renderedBody := fragmentLinkRe.ReplaceAllString(sec.body, "${1}roadmap.md#")
		if _, err := f.WriteString(header + renderedBody); err != nil {
			_ = f.Close()
			cleanupRoadmapSections(temporary)
			return nil, nil, nil, err
		}
		if err := f.Close(); err != nil {
			cleanupRoadmapSections(temporary)
			return nil, nil, nil, err
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			cleanupRoadmapSections(temporary)
			return nil, nil, nil, err
		}
		relSlash := filepath.ToSlash(rel)
		keys[relSlash] = key
		if u, ok := unsettled[roadmapPath]; ok {
			unsettled[relSlash] = u
		}
		locations[relSlash] = roadmapLocation{
			path:       roadmapPath,
			lineOffset: sec.startLine - 1 - strings.Count(header, "\n"),
			startLine:  sec.startLine,
		}
		paths = append(paths, relSlash)
	}
	return paths, locations, temporary, nil
}

func cleanupRoadmapSections(temporary []string) {
	for _, f := range temporary {
		_ = os.Remove(f)
	}
}
