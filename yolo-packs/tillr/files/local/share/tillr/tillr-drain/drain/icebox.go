package drain

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-item/docshare"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

type iceboxEntry struct {
	Title   string
	Roadmap string
}

var (
	atxHeadingPattern     = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?[ \t]*$`)
	listItemPattern       = regexp.MustCompile(`^[ \t]*(?:[-*+]|\d+[.)])[ \t]+(.*)$`)
	markdownLinkPattern   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\r\n]+)\)`)
	inlineRepoDocsPattern = regexp.MustCompile(`docs/[A-Za-z0-9._/-]+\.md`)
	shortTableIDPattern   = regexp.MustCompile(`^[A-Za-z]?\d+[A-Za-z]?$`)
)

var genericDocLinkLabels = map[string]bool{
	"design":        true,
	"design doc":    true,
	"doc":           true,
	"document":      true,
	"documentation": true,
	"link":          true,
	"plan":          true,
	"spec":          true,
}

func formatIceboxHold(entry iceboxEntry) string {
	return fmt.Sprintf("%s: %q in %s", ReasonIcebox, entry.Title, entry.Roadmap)
}

func readIceboxDesignDocs(root string, configured *[]string) (map[string]iceboxEntry, error) {
	if root == "" {
		return nil, nil
	}
	var configuredRoadmaps []string
	if configured != nil {
		configuredRoadmaps = *configured
	} else {
		cfg, err := settings.Read(root)
		if err != nil {
			return nil, Usagef("%v", err)
		}
		configuredRoadmaps = cfg.Sync.Roadmaps
	}
	roadmaps, err := configuredOrDiscoveredRoadmaps(root, configuredRoadmaps)
	if err != nil {
		return nil, err
	}
	if len(roadmaps) == 0 {
		return nil, nil
	}
	icebox := map[string]iceboxEntry{}
	for _, rel := range roadmaps {
		full := filepath.Join(root, filepath.FromSlash(rel))
		raw, err := os.ReadFile(full)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, Externalf("reading roadmap %s: %v", rel, err)
		}
		for doc, entry := range parseRoadmapIcebox(rel, raw) {
			if _, exists := icebox[doc]; !exists {
				icebox[doc] = entry
			}
		}
	}
	return icebox, nil
}

func configuredOrDiscoveredRoadmaps(root string, configured []string) ([]string, error) {
	if len(configured) > 0 {
		out := make([]string, 0, len(configured))
		for _, rel := range configured {
			clean := path.Clean(strings.TrimSpace(rel))
			if clean != "" && clean != "." {
				out = append(out, clean)
			}
		}
		return out, nil
	}
	docsRoot := filepath.Join(root, "docs")
	if _, err := os.Stat(docsRoot); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, Externalf("stat docs directory: %v", err)
	}
	var roadmaps []string
	err := filepath.WalkDir(docsRoot, func(full string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(rel)
		if strings.HasPrefix(slash, "docs/") && path.Base(slash) == "roadmap.md" {
			roadmaps = append(roadmaps, slash)
		}
		return nil
	})
	if err != nil {
		return nil, Externalf("walking docs for roadmaps: %v", err)
	}
	sort.Strings(roadmaps)
	return roadmaps, nil
}

func parseRoadmapIcebox(roadmapRel string, raw []byte) map[string]iceboxEntry {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	fenced := make([]bool, len(lines))
	var fence docshare.Fence
	for i, line := range lines {
		fenced[i] = fence.Step(line)
	}

	tableHeader := map[int]bool{}
	for i := 0; i+1 < len(lines); i++ {
		if !fenced[i] && !fenced[i+1] && isTableRow(lines[i]) && isTableSeparator(lines[i+1]) {
			tableHeader[i] = true
		}
	}

	activeDocs := map[string]bool{}
	var activeIceboxDepth int
	var inactiveOtherDepth int
	atParagraphStart := true
	for i, line := range lines {
		if fenced[i] {
			atParagraphStart = true
			continue
		}
		if depth, headingText, ok := parseATXHeading(line); ok {
			plain := plainMarkdownText(headingText)
			if activeIceboxDepth > 0 && depth <= activeIceboxDepth && !strings.HasPrefix(plain, "Icebox") {
				activeIceboxDepth = 0
			}
			if activeIceboxDepth == 0 && strings.HasPrefix(plain, "Icebox") {
				activeIceboxDepth = depth
			}
			if inactiveOtherDepth > 0 && depth <= inactiveOtherDepth && !isInactiveRoadmapHeading(plain) {
				inactiveOtherDepth = 0
			}
			if activeIceboxDepth == 0 && inactiveOtherDepth == 0 && isInactiveRoadmapHeading(plain) {
				inactiveOtherDepth = depth
			}
			if activeIceboxDepth == 0 && inactiveOtherDepth == 0 {
				if first := firstWholeDocLinkTarget(roadmapRel, headingText); first != "" {
					activeDocs[first] = true
				}
			}
			atParagraphStart = true
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			atParagraphStart = true
			continue
		}
		if activeIceboxDepth == 0 && inactiveOtherDepth == 0 {
			if isTableRow(line) {
				if !tableHeader[i] && !isTableSeparator(line) {
					if first := firstWholeDocLinkTarget(roadmapRel, line); first != "" {
						activeDocs[first] = true
					}
				}
				atParagraphStart = true
				continue
			}
			if body, ok := parseListItem(line); ok {
				if first := firstWholeDocLinkTarget(roadmapRel, body); first != "" {
					activeDocs[first] = true
				}
				atParagraphStart = false
				continue
			}
			if atParagraphStart {
				if first := firstWholeDocLinkTarget(roadmapRel, line); first != "" {
					activeDocs[first] = true
				}
			}
		}
		atParagraphStart = false
	}

	entries := map[string]iceboxEntry{}
	recordTarget := func(target, title string) {
		if activeDocs[target] {
			return
		}
		if _, exists := entries[target]; exists {
			return
		}
		if title == "" {
			title = target
		}
		entries[target] = iceboxEntry{Title: title, Roadmap: roadmapRel}
	}

	iceboxDepth := 0
	subheading := ""
	for i := 0; i < len(lines); i++ {
		if fenced[i] {
			continue
		}
		if depth, headingText, ok := parseATXHeading(lines[i]); ok {
			plain := plainMarkdownText(headingText)
			if iceboxDepth == 0 {
				if strings.HasPrefix(plain, "Icebox") {
					iceboxDepth = depth
					subheading = ""
				}
				continue
			}
			if depth <= iceboxDepth {
				if strings.HasPrefix(plain, "Icebox") {
					iceboxDepth = depth
					subheading = ""
				} else {
					iceboxDepth = 0
					subheading = ""
				}
				continue
			}
			subheading = extractIceboxLineTitle(headingText, "")
			for _, target := range extractWholeDocTargets(roadmapRel, headingText) {
				recordTarget(target, subheading)
			}
			continue
		}
		if iceboxDepth == 0 || strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if isTableRow(lines[i]) {
			if tableHeader[i] || isTableSeparator(lines[i]) {
				continue
			}
			title := extractIceboxTableTitle(splitTableCells(lines[i]), subheading)
			for _, target := range extractWholeDocTargets(roadmapRel, lines[i]) {
				recordTarget(target, title)
			}
			continue
		}
		if body, ok := parseListItem(lines[i]); ok {
			parts := []string{body}
			j := i + 1
			for j < len(lines) && !fenced[j] {
				if strings.TrimSpace(lines[j]) == "" || isTableRow(lines[j]) {
					break
				}
				if _, _, h := parseATXHeading(lines[j]); h {
					break
				}
				if _, li := parseListItem(lines[j]); li {
					break
				}
				parts = append(parts, strings.TrimSpace(lines[j]))
				j++
			}
			i = j - 1
			title := extractIceboxLineTitle(body, subheading)
			for _, target := range extractWholeDocTargets(roadmapRel, strings.Join(parts, "\n")) {
				recordTarget(target, title)
			}
			continue
		}
		startLine := strings.TrimSpace(lines[i])
		parts := []string{startLine}
		j := i + 1
		for j < len(lines) && !fenced[j] {
			if strings.TrimSpace(lines[j]) == "" || isTableRow(lines[j]) {
				break
			}
			if _, _, h := parseATXHeading(lines[j]); h {
				break
			}
			if _, li := parseListItem(lines[j]); li {
				break
			}
			parts = append(parts, strings.TrimSpace(lines[j]))
			j++
		}
		i = j - 1
		title := extractIceboxLineTitle(startLine, subheading)
		for _, target := range extractWholeDocTargets(roadmapRel, strings.Join(parts, "\n")) {
			recordTarget(target, title)
		}
	}
	return entries
}

func isInactiveRoadmapHeading(plain string) bool {
	return strings.HasPrefix(plain, "Exploration") ||
		strings.HasPrefix(plain, "What this page does not cover") ||
		strings.HasPrefix(plain, "What this file does not cover")
}

func parseATXHeading(line string) (int, string, bool) {
	m := atxHeadingPattern.FindStringSubmatch(line)
	if m == nil {
		return 0, "", false
	}
	text := strings.TrimSpace(m[2])
	// Trim optional closing ATX hashes (`## Heading ##`).
	if trimmed := strings.TrimRight(text, "#"); len(trimmed) < len(text) && (trimmed == "" || trimmed[len(trimmed)-1] == ' ' || trimmed[len(trimmed)-1] == '\t') {
		text = strings.TrimSpace(trimmed)
	}
	return len(m[1]), text, true
}

func parseListItem(line string) (string, bool) {
	m := listItemPattern.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func isTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") && len(trimmed) >= 2
}

func isTableSeparator(line string) bool {
	if !isTableRow(line) {
		return false
	}
	cells := splitTableCells(line)
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		c := strings.TrimSpace(cell)
		c = strings.TrimPrefix(c, ":")
		c = strings.TrimSuffix(c, ":")
		if len(c) < 2 || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

func splitTableCells(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	raw := strings.Split(trimmed, "|")
	cells := make([]string, len(raw))
	for i, c := range raw {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

func firstWholeDocLinkTarget(roadmapRel, text string) string {
	for _, m := range markdownLinkPattern.FindAllStringSubmatch(text, -1) {
		if target := resolveRoadmapDocLink(roadmapRel, m[2]); target != "" {
			return target
		}
	}
	return ""
}

func extractWholeDocTargets(roadmapRel, text string) []string {
	var targets []string
	seen := map[string]bool{}
	add := func(target string) {
		if target != "" && !seen[target] {
			seen[target] = true
			targets = append(targets, target)
		}
	}
	for _, m := range markdownLinkPattern.FindAllStringSubmatch(text, -1) {
		add(resolveRoadmapDocLink(roadmapRel, m[2]))
	}
	// Strip Markdown links before scanning bare/inline-code repo paths so a fragment
	// link like `[OQ](docs/design/x.md#OQ-1)` is never misread as bare `docs/design/x.md`.
	stripped := markdownLinkPattern.ReplaceAllString(text, " ")
	for _, idx := range inlineRepoDocsPattern.FindAllStringIndex(stripped, -1) {
		start, end := idx[0], idx[1]
		if !validDocPathStart(stripped, start) || !validWholeDocPathEnd(stripped, end) {
			continue
		}
		candidate := path.Clean(stripped[start:end])
		if isIceboxDesignDocTarget(candidate) {
			add(candidate)
		}
	}
	return targets
}

func resolveRoadmapDocLink(roadmapRel, rawTarget string) string {
	target := strings.TrimSpace(rawTarget)
	if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
		target = strings.TrimSpace(target[1 : len(target)-1])
	} else if fields := strings.Fields(target); len(fields) > 0 {
		target = fields[0]
	}
	if target == "" || strings.Contains(target, "#") || strings.Contains(target, ":") || strings.HasPrefix(target, "/") || strings.Contains(target, "\\") {
		return ""
	}
	resolved := path.Clean(path.Join(path.Dir(roadmapRel), target))
	if !isIceboxDesignDocTarget(resolved) {
		return ""
	}
	return resolved
}

func isIceboxDesignDocTarget(rel string) bool {
	if rel == "" || strings.Contains(rel, "\\") || path.IsAbs(rel) {
		return false
	}
	if !strings.HasPrefix(rel, "docs/") || !strings.HasSuffix(rel, ".md") {
		return false
	}
	if path.Base(rel) == "roadmap.md" || strings.HasPrefix(rel, "docs/reference/") {
		return false
	}
	return true
}

func extractIceboxTableTitle(cells []string, fallback string) string {
	if len(cells) == 0 {
		return fallback
	}
	firstPlain := plainMarkdownText(cells[0])
	if len(cells) >= 2 && shortTableIDPattern.MatchString(firstPlain) {
		second := extractIceboxLineTitle(cells[1], "")
		if second != "" {
			return firstPlain + ": " + second
		}
	}
	for _, cell := range cells {
		if t := extractIceboxLineTitle(cell, ""); t != "" && !genericDocLinkLabels[strings.ToLower(t)] {
			return t
		}
	}
	if firstPlain != "" {
		return firstPlain
	}
	return fallback
}

func extractIceboxLineTitle(text, fallback string) string {
	s := strings.TrimSpace(text)
	if s == "" {
		return fallback
	}
	// 1. Leading bold span (`**Entry title** ...`).
	if strings.HasPrefix(s, "**") {
		if end := strings.Index(s[2:], "**"); end > 0 {
			if title := plainMarkdownText(s[2 : 2+end]); title != "" {
				return title
			}
		}
	}
	// 2. Leading Markdown link (`[Entry title](target) ...`), unless generic (`[design](...)`).
	if strings.HasPrefix(s, "[") {
		if m := markdownLinkPattern.FindStringSubmatchIndex(s); m != nil && m[0] == 0 {
			label := plainMarkdownText(s[m[2]:m[3]])
			if label != "" && !genericDocLinkLabels[strings.ToLower(label)] {
				return label
			}
			rest := strings.TrimSpace(s[m[1]:])
			for _, sep := range []string{"—", "–", "-", ":"} {
				if strings.HasPrefix(rest, sep) {
					if after := extractIceboxLineTitle(strings.TrimSpace(rest[len(sep):]), ""); after != "" {
						return after
					}
				}
			}
		}
	}
	// 3. Lead text before parenthetical link or clause separator.
	plain := plainMarkdownText(s)
	for _, sep := range []string{" (", " — ", " – ", ": ", ", "} {
		if idx := strings.Index(plain, sep); idx > 0 {
			lead := strings.TrimSpace(plain[:idx])
			if lead != "" && !genericDocLinkLabels[strings.ToLower(lead)] {
				return lead
			}
		}
	}
	if plain != "" && !genericDocLinkLabels[strings.ToLower(plain)] {
		return plain
	}
	return fallback
}

func plainMarkdownText(s string) string {
	out := markdownLinkPattern.ReplaceAllString(s, "$1")
	out = strings.ReplaceAll(out, "**", "")
	out = strings.ReplaceAll(out, "__", "")
	out = strings.ReplaceAll(out, "`", "")
	return strings.Join(strings.Fields(out), " ")
}

func normalizeDesignDocRef(ref string) string {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return ""
	}
	file, _, _ := strings.Cut(trimmed, "#")
	file = strings.TrimPrefix(strings.TrimSpace(file), "./")
	if file == "" || strings.Contains(file, "\\") || path.IsAbs(file) {
		return ""
	}
	clean := path.Clean(file)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

func checkIceboxHold(r *store.Reader, row itemRow, root string, defReady string, enabled bool, iceboxDocs map[string]iceboxEntry, schema versionSchema) (string, error) {
	if !enabled || len(iceboxDocs) == 0 || row.State != defReady {
		return "", nil
	}
	return iceboxHoldReason(r, row, root, iceboxDocs, schema.itemColumns["prose"], schema.hasCriteria)
}

func iceboxHoldReason(r *store.Reader, row itemRow, root string, iceboxDocs map[string]iceboxEntry, hasProse, hasCriteria bool) (string, error) {
	if len(iceboxDocs) == 0 {
		return "", nil
	}
	if doc := normalizeDesignDocRef(row.DesignDoc); doc != "" {
		if entry, ok := iceboxDocs[doc]; ok {
			return formatIceboxHold(entry), nil
		}
	}
	sources, err := readItemWordsSources(r, row.ItemID, hasProse, hasCriteria)
	if err != nil {
		return "", err
	}
	for _, text := range sources {
		if entry, ok := matchEarliestIceboxDoc(text, iceboxDocs); ok {
			return formatIceboxHold(entry), nil
		}
	}
	return "", nil
}

func readItemWordsSources(r *store.Reader, itemID string, hasProse, hasCriteria bool) ([]string, error) {
	var sources []string
	words, err := item.RequestWords(r, itemID)
	if err != nil {
		return nil, err
	}
	if len(words) > 0 {
		sources = append(sources, string(words))
	}
	if hasProse {
		var prose string
		err := r.QueryRow(`SELECT COALESCE(prose, '') FROM items WHERE item_id=?`, itemID).Scan(&prose)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if prose != "" {
			sources = append(sources, prose)
		}
	}
	if hasCriteria {
		rows, err := r.Query(`SELECT text FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, itemID)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		for rows.Next() {
			var text string
			if err := rows.Scan(&text); err != nil {
				rows.Close()
				return nil, err
			}
			if text != "" {
				b.WriteString(text)
				b.WriteByte('\n')
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if b.Len() > 0 {
			sources = append(sources, b.String())
		}
	}
	intake, err := readIdeaIntake(r, itemID)
	if err != nil {
		return nil, err
	}
	if intake != nil && intake.intake != "" {
		if raw, err := os.ReadFile(intake.intake); err == nil && len(raw) > 0 {
			sources = append(sources, string(raw))
		}
	}
	return sources, nil
}

func matchEarliestIceboxDoc(text string, iceboxDocs map[string]iceboxEntry) (iceboxEntry, bool) {
	if text == "" {
		return iceboxEntry{}, false
	}
	bestPos := -1
	bestDoc := ""
	for doc := range iceboxDocs {
		pos := findDesignDocMention(text, doc)
		if pos < 0 {
			continue
		}
		if bestPos < 0 || pos < bestPos || (pos == bestPos && doc < bestDoc) {
			bestPos = pos
			bestDoc = doc
		}
	}
	if bestPos < 0 {
		return iceboxEntry{}, false
	}
	return iceboxDocs[bestDoc], true
}

func findDesignDocMention(text, doc string) int {
	if doc == "" {
		return -1
	}
	offset := 0
	for offset <= len(text)-len(doc) {
		rel := strings.Index(text[offset:], doc)
		if rel < 0 {
			return -1
		}
		start := offset + rel
		end := start + len(doc)
		if validDocPathStart(text, start) && validItemDocPathEnd(text, end) {
			return start
		}
		offset = start + 1
	}
	return -1
}

func validDocPathStart(s string, start int) bool {
	if start == 0 {
		return true
	}
	if start >= 2 && s[start-2:start] == "./" {
		if start == 2 {
			return true
		}
		return !isPathWordChar(s[start-3]) && s[start-3] != '/' && s[start-3] != '.'
	}
	prev := s[start-1]
	return !isPathWordChar(prev) && prev != '/' && prev != '.'
}

func validWholeDocPathEnd(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	next := s[end]
	if next == '#' || next == '/' || isPathWordChar(next) {
		return false
	}
	if next == '.' && end+1 < len(s) && isPathWordChar(s[end+1]) {
		return false
	}
	return true
}

func validItemDocPathEnd(s string, end int) bool {
	if end >= len(s) {
		return true
	}
	next := s[end]
	if next == '#' {
		return true
	}
	if next == '/' || isPathWordChar(next) {
		return false
	}
	if next == '.' && end+1 < len(s) && isPathWordChar(s[end+1]) {
		return false
	}
	return true
}

func isPathWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}
