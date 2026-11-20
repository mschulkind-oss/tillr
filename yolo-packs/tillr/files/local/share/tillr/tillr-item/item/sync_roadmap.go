// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package item

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// A roadmap is a tracked docs/**/roadmap.md in the sync input, not a fixed inventory.
func isRoadmap(file string, selected ...[]string) bool {
	if len(selected) > 0 && len(selected[0]) > 0 {
		return containsID(selected[0], file)
	}
	return strings.HasPrefix(file, "docs/") && path.Base(file) == "roadmap.md"
}

// roadmapLines visits prose outside code fences, tracking a parked heading and
// all its descendants until a heading at the same or a higher level ends it.
func roadmapLines(doc SyncDocument, visit func(int, string, bool)) {
	fence := fenceState{}
	parkedDepth := 0
	for i, line := range splitLines(doc.Content) {
		if fence.step(line) {
			continue
		}
		if h := headingLine.FindStringSubmatch(line); h != nil {
			depth := len(h[1])
			if parkedDepth != 0 && depth <= parkedDepth {
				parkedDepth = 0
			}
			for _, section := range parkedSections {
				if strings.HasPrefix(h[2], section) {
					parkedDepth = depth
				}
			}
		}
		visit(i+1, line, parkedDepth != 0)
	}
}

func roadmapLink(file, target string) string {
	if strings.Contains(target, ":") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") {
		return ""
	}
	target, _, _ = strings.Cut(target, "#")
	return path.Clean(path.Join(path.Dir(file), target))
}

// roadmapBlocks interleaves entries, not individual blocks. A linked document
// belongs to the first roadmap reaching it; its blocks remain together.
func roadmapBlocks(docs []SyncDocument, blocks []syncBlock, selected []string) []syncBlock {
	docs = append([]SyncDocument(nil), docs...)
	sort.SliceStable(docs, func(i, j int) bool {
		if len(selected) > 0 {
			position := func(file string) int {
				for n, p := range selected {
					if p == file {
						return n
					}
				}
				return len(selected)
			}
			if a, b := position(docs[i].Path), position(docs[j].Path); a != b {
				return a < b
			}
		}
		return docs[i].Path < docs[j].Path
	})
	byFile := map[string][]syncBlock{}
	for _, b := range blocks {
		byFile[b.file] = append(byFile[b.file], b)
	}
	claimed := map[string]bool{}
	var roadmaps [][][]syncBlock
	for _, d := range docs {
		if !isRoadmap(d.Path, selected) {
			continue
		}
		var entries [][]syncBlock
		roadmapLines(d, func(line int, text string, parked bool) {
			if parked {
				return
			}
			for _, b := range byFile[d.Path] {
				if b.line == line {
					entries = append(entries, []syncBlock{b})
				}
			}
			for _, m := range linkTarget.FindAllStringSubmatch(text, -1) {
				target := roadmapLink(d.Path, m[1])
				if isRoadmap(target) || claimed[target] || len(byFile[target]) == 0 {
					continue
				}
				claimed[target] = true
				entries = append(entries, byFile[target])
			}
		})
		roadmaps = append(roadmaps, entries)
	}
	result := []syncBlock{}
	seen := map[string]bool{}
	appendBlocks := func(bs []syncBlock) {
		for _, b := range bs {
			key := fmt.Sprintf("%s:%d", b.file, b.line)
			if !seen[key] {
				result = append(result, b)
				seen[key] = true
			}
		}
	}
	for i := 0; ; i++ {
		any := false
		for _, entries := range roadmaps {
			if i < len(entries) {
				any = true
				appendBlocks(entries[i])
			}
		}
		if !any {
			break
		}
	}
	for _, d := range docs {
		appendBlocks(byFile[d.Path])
	}
	return result
}

// appendKeys allocates a batch beyond the old maximum without respacing it.
// One item keeps the familiar suffix m; larger batches use fixed-width base-36
// counters after that suffix instead of growing one digit per imported item.
func appendKeys(last string, n int) []string {
	keys := make([]string, n)
	if n == 1 {
		keys[0] = last + "m"
		return keys
	}
	width, capacity := 1, 36
	for capacity <= n {
		width++
		capacity *= 36
	}
	for i := range keys {
		digits := make([]byte, width)
		value := i + 1
		for j := width - 1; j >= 0; j-- {
			digits[j] = RankAlphabet[value%36]
			value /= 36
		}
		keys[i] = last + "m" + string(digits)
	}
	return keys
}
