package item

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// ReconcileInput names the tree and optional explicit Vantage index command.
// Reconcile is a report only: sync remains the intake for authored item blocks.
type ReconcileInput struct {
	Root    string
	Command []string
}

// ReconcileDocument is one indexed document's classification. The six cases
// follow the ordered table in docs/design/tillr-queue-persistence.md Section 10.2.
type ReconcileDocument struct {
	Path      string
	Case      string
	Reason    string
	Questions []string
	Evidence  string
	Block     string
}

// ReconcileReport partitions the entire index and records the tracked docs boundary.
type ReconcileReport struct {
	Documents    []ReconcileDocument
	Counts       map[string]int
	OutsideIndex []string
}

var reconcileCases = []string{"unstaged", "terminal", "built", "decomposable", "design", "held"}
var reconcileSlugRun = regexp.MustCompile(`[^a-z0-9]+`)

func reconcileSlug(path string) string {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "docs/")
	path = strings.TrimSuffix(path, ".md")
	return reconcileSlugRun.ReplaceAllString(strings.ToLower(path), "-")
}

type reconcileIndexDocument struct {
	Path   string `json:"path"`
	Stage  string `json:"stage"`
	Status string `json:"status"`
}

type reconcileCarryingItem struct {
	id, state, designDoc, bodyRef string
	finished                      bool
}

// Reconcile reads every input before returning any report. A nil reader means
// only document blocks carry work; it never discovers or creates a default store.
func Reconcile(ctx context.Context, r *store.Reader, in ReconcileInput) (ReconcileReport, error) {
	empty := ReconcileReport{}
	raw, err := vantage.RunIndexCommand(ctx, in.Root, in.Command)
	if err != nil {
		return empty, Externalf("index-unreadable: %v", err)
	}
	index := vantage.ParseIndex(raw, vantage.IndexCommandName(in.Command))
	if _, err := index.Questions(""); err != nil {
		return empty, Externalf("index-unreadable: %v", err)
	}
	// Question parsing and refusal are shared with the planner. Decode only the
	// stage/status fields its question-only API does not expose.
	var envelope struct {
		Index struct {
			Documents  []reconcileIndexDocument `json:"documents"`
			Skipped    json.RawMessage          `json:"skipped"`
			Unreadable json.RawMessage          `json:"unreadable"`
		} `json:"index"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return empty, Externalf("index-unreadable: %v", err)
	}
	entries := envelope.Index.Documents
	if len(entries) == 0 {
		return empty, Externalf("index-empty: no documents")
	}
	for _, field := range []struct {
		name string
		raw  json.RawMessage
	}{{"skipped", envelope.Index.Skipped}, {"unreadable", envelope.Index.Unreadable}} {
		if len(field.raw) > 0 && string(field.raw) != "null" && string(field.raw) != "[]" && string(field.raw) != "{}" && string(field.raw) != "0" {
			return empty, Externalf("index-incomplete: %s=%s", field.name, field.raw)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	slugs := map[string]string{}
	indexed := map[string]bool{}
	for i := range entries {
		p := filepath.ToSlash(filepath.Clean(entries[i].Path))
		if p == "." || filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
			return empty, Externalf("index-path-invalid: %q", entries[i].Path)
		}
		entries[i].Path = p
		slug := reconcileSlug(p)
		if other, ok := slugs[slug]; ok {
			return empty, Rejectionf("slug-collision", "", "slug-collision: %q and %q have slug %q", other, p, slug)
		}
		slugs[slug] = p
		indexed[p] = true
	}
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--", "*.md")
	cmd.Dir = in.Root
	tracked, err := cmd.Output()
	if err != nil {
		return empty, Externalf("listing tracked Markdown: %v", err)
	}
	paths := map[string]bool{}
	report := ReconcileReport{Counts: map[string]int{}, OutsideIndex: []string{}}
	for _, c := range reconcileCases {
		report.Counts[c] = 0
	}
	for _, p := range strings.Split(string(tracked), "\x00") {
		if strings.HasPrefix(p, "docs/") {
			paths[p] = true
			if !indexed[p] {
				report.OutsideIndex = append(report.OutsideIndex, p)
			}
		}
	}
	for p := range indexed {
		paths[p] = true
	}
	sort.Strings(report.OutsideIndex)
	blocks := map[string][]syncBlock{}
	blockIDs := map[string]bool{}
	for p := range paths {
		content, err := os.ReadFile(filepath.Join(in.Root, p))
		if err != nil {
			return empty, Externalf("reading document %q: %v", p, err)
		}
		parsed, findings := parseItemBlocks(p, content)
		if len(findings) > 0 {
			return empty, Externalf("item-block-invalid: %s:%d: %s: %s", p, findings[0].Line, findings[0].Rule, findings[0].Message)
		}
		blocks[p] = parsed
		for _, b := range parsed {
			blockIDs[b.payload.ItemID] = true
		}
	}
	stored := map[string]reconcileCarryingItem{}
	if r != nil {
		d, err := itemDefinition(r)
		if err != nil {
			return empty, err
		}
		rows, err := r.Query(`SELECT item_id, state, COALESCE(design_doc,''), COALESCE(body_ref,'') FROM items`)
		if err != nil {
			return empty, Internalf("reading carrying items: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var it reconcileCarryingItem
			if err := rows.Scan(&it.id, &it.state, &it.designDoc, &it.bodyRef); err != nil {
				return empty, Internalf("reading carrying item: %v", err)
			}
			// Legacy retired rows are terminal, but legacy in-progress is not:
			// frozen means no new writes, not that the work finished.
			it.finished = d.IsFinished(it.state) || it.state == "abandoned" || it.state == "deleted"
			stored[it.id] = it
		}
		if err := rows.Err(); err != nil {
			return empty, Internalf("reading carrying items: %v", err)
		}
	}
	for _, entry := range entries {
		p := entry.Path
		slug := reconcileSlug(p)
		questions, err := index.Questions(p)
		if err != nil {
			return empty, Externalf("reading questions: %v", err)
		}
		result := ReconcileDocument{Path: p}
		for _, q := range questions {
			if q.State != vantage.QuestionAnswered {
				result.Questions = append(result.Questions, q.ID)
			}
		}
		carried := len(blocks[p]) > 0
		allFinished, landed := true, false
		for _, it := range stored {
			if reconcileRef(in.Root, it.designDoc) != p && reconcileRef(in.Root, it.bodyRef) != p {
				continue
			}
			carried = true
			allFinished = allFinished && it.finished
			landed = landed || it.state == "landed"
		}
		unlanded := 0
		for _, b := range blocks[p] {
			implemented := false
			if it, ok := stored[b.payload.ItemID]; ok && it.state == "landed" {
				implemented = true
			}
			for _, it := range stored {
				if it.state == "landed" && reconcileRef(in.Root, it.bodyRef) == p && strings.Contains(it.bodyRef, "#") && strings.SplitN(it.bodyRef, "#", 2)[1] == headingSlug(b.heading) {
					implemented = true
				}
			}
			if !implemented {
				unlanded++
			}
		}
		accepted := (entry.Stage == "DESIGN" || entry.Stage == "DECIDED") && entry.Status == "accepted" && len(result.Questions) == 0
		graduateID := "item-graduate-" + slug
		_, graduateStored := stored[graduateID]
		switch {
		case entry.Stage == "":
			result.Case = "unstaged"
		case entry.Stage == "CURRENT" || entry.Stage == "GRADUATED" || entry.Stage == "SUPERSEDED":
			result.Case = "terminal"
		case entry.Stage == "BUILT" || (accepted && carried && allFinished && landed && unlanded == 0):
			result.Case = "built"
			result.Evidence = "inferred-from-landed-items"
			if entry.Stage == "BUILT" {
				result.Evidence = "stage: BUILT"
			}
			if blockIDs[graduateID] || graduateStored {
				result.Reason = "graduating"
			} else {
				result.Block = reconcileProposal(graduateID, "docs/", true)
			}
		case accepted && len(blocks[p]) > 0:
			result.Case = "decomposable"
		case accepted:
			result.Case = "design"
			if carried {
				result.Reason = "carried"
			} else {
				result.Block = reconcileProposal("item-design-"+slug, p+",docs/roadmap.md,docs/tillr/roadmap.md,docs/pi/roadmap.md", false)
			}
		default:
			result.Case = "held"
			switch {
			case len(result.Questions) > 0:
				result.Reason = "question-open"
			case entry.Stage == "SKETCH":
				result.Reason = "needs-design"
			default:
				result.Reason = "unruled"
			}
		}
		report.Counts[result.Case]++
		report.Documents = append(report.Documents, result)
	}
	return report, nil
}

func reconcileRef(root, ref string) string {
	ref = strings.SplitN(ref, "#", 2)[0]
	if ref == "" {
		return ""
	}
	if filepath.IsAbs(ref) {
		abs, err := filepath.Abs(root)
		if err != nil {
			return ""
		}
		ref, err = filepath.Rel(abs, ref)
		if err != nil {
			return ""
		}
	}
	return filepath.ToSlash(filepath.Clean(ref))
}

func reconcileProposal(id, paths string, graduate bool) string {
	title, criterion := "Decompose the accepted design", "Decompose the accepted design into fenced tillr item blocks in this document; do not implement code."
	if graduate {
		title = "Graduate the built design"
		criterion = "Compact the built design into evergreen reference documentation, mark the source GRADUATED or retire it, and repoint inbound links."
	}
	return fmt.Sprintf("### %s\n\n<!-- tillr: item id=%q seat=\"design\" paths=%q -->\n\n- **c1:** %s\n", title, id, paths, criterion)
}

// FormatReconcileReport has no clock or map-order dependence, so unchanged inputs
// produce identical text. Suggested blocks are specimens to paste, not writes.
func FormatReconcileReport(report ReconcileReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reconcile: %d documents (read-only)\n", len(report.Documents))
	for _, c := range reconcileCases {
		fmt.Fprintf(&b, "%s: %d\n", c, report.Counts[c])
	}
	for _, d := range report.Documents {
		fmt.Fprintf(&b, "%s: %s", d.Path, d.Case)
		for _, detail := range []string{d.Evidence, d.Reason, strings.Join(d.Questions, ",")} {
			if detail != "" {
				fmt.Fprintf(&b, " %s", detail)
			}
		}
		b.WriteByte('\n')
		if d.Block != "" {
			fmt.Fprintf(&b, "```markdown\n%s```\n", d.Block)
		}
	}
	for _, p := range report.OutsideIndex {
		fmt.Fprintf(&b, "outside-index: %s\n", p)
	}
	return b.String()
}
