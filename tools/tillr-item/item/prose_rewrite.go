package item

import (
	"fmt"
	"strings"
)

// ProseRewriteOriginal identifies the sync-produced rewrite of one document item.
func ProseRewriteOriginal(id string) string {
	original, ok := strings.CutPrefix(id, "rewrite-")
	if !ok || !itemIDShape.MatchString(original) {
		return ""
	}
	return original
}

// ProseRewriteCriteria is the fixed contract, not a copy of the original request.
var ProseRewriteCriteria = []string{
	"Rewrite the title and the section's prose to the prose guide.",
	"Keep the directive's id, paths, blocked_by and seat unchanged.",
	"Keep every criterion's meaning and number; add or remove no criterion.",
	"Apply the current prose guide mark and pass its shape check.",
}

// ProseRewriteFindings retains all cheap prose diagnostics for each document item.
func ProseRewriteFindings(report SyncReport) map[string][]SyncNote {
	result := map[string][]SyncNote{}
	for _, notes := range [][]SyncNote{report.Warnings, report.Findings} {
		for _, n := range notes {
			if n.ItemID != "" && (n.Rule == "prose-unmarked" || n.Rule == "prose-shape") {
				result[n.ItemID] = append(result[n.ItemID], n)
			}
		}
	}
	return result
}

func proseRewriteWrites(report *SyncReport, blocks []syncBlock, held map[string]storedState, ready string) []fileWrites {
	findings := ProseRewriteFindings(*report)
	writes := []fileWrites{}
	seen := map[string]bool{}
	for _, b := range blocks {
		original := b.payload.ItemID
		id := "rewrite-" + original
		if seen[original] || len(findings[original]) == 0 || !itemIDShape.MatchString(original) || held[original].state == "landed" {
			continue
		}
		seen[original] = true
		if _, exists := held[id]; exists {
			continue
		}
		p := itemPayload{ItemID: id, Title: "Add clear prose for " + original, BodyRef: b.payload.BodyRef, State: ready, Seat: SeatBuild, AllowedPaths: []string{b.file}, BlockedBy: []string{}, Criteria: []criterion{}}
		for i, text := range ProseRewriteCriteria {
			p.Criteria = append(p.Criteria, criterion{CriterionID: fmt.Sprintf("c%d", i+1), Text: text})
		}
		writes = append(writes, fileWrites{file: b.file, heading: map[string]SyncNote{id: note(b, "", "")}, payloads: []itemPayload{p}, created: map[string]bool{id: true}})
		report.Created = append(report.Created, id)
	}
	return writes
}

// ValidateProseRewrite compares parsed pinned blobs, never mutable worker files.
// Criterion meaning remains a review obligation; identity and count are mechanical.
func ValidateProseRewrite(file, original string, before, after []byte) string {
	oldBlocks, _ := parseItemBlocks(file, before)
	newBlocks, notes := parseItemBlocks(file, after)
	var old, next *syncBlock
	for i := range oldBlocks {
		if oldBlocks[i].payload.ItemID == original {
			old = &oldBlocks[i]
			break
		}
	}
	for i := range newBlocks {
		if newBlocks[i].payload.ItemID == original {
			if next != nil {
				return "id: duplicate original item"
			}
			next = &newBlocks[i]
		}
	}
	if old == nil {
		return "id: original item absent from base"
	}
	if next == nil {
		return "id: original item removed or renamed"
	}
	for _, n := range notes {
		if n.ItemID == original || n.ItemID == "" {
			return n.Rule + ": " + n.Message
		}
	}
	// Compare attribute strings, including omitted versus explicitly empty values.
	a, z := old.attributes, next.attributes
	for _, key := range []string{"id", "paths", "blocked_by", "seat"} {
		av, ap := a[key]
		zv, zp := z[key]
		if av != zv || ap != zp {
			return fmt.Sprintf("%s changed: %q -> %q", key, av, zv)
		}
	}
	ids := map[string]bool{}
	for _, c := range old.payload.Criteria {
		ids[c.CriterionID] = true
	}
	if len(old.payload.Criteria) != len(next.payload.Criteria) {
		return "criteria: a criterion was added or removed"
	}
	for _, c := range next.payload.Criteria {
		if !ids[c.CriterionID] {
			return "criteria: criterion number changed to " + c.CriterionID
		}
	}
	if warnings := proseWarnings(*next); len(warnings) > 0 {
		return warnings[0].Rule + ": " + warnings[0].Message
	}
	return ""
}
