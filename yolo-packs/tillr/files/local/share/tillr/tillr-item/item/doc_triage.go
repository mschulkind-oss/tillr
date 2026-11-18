package item

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// DocTriageWorkflow is the item graph a document triage runs: one read-only reader per
// document on the scout seat, then one report writer on the design seat
// (config/tillr/workflows/doc-triage@v1.json).
const DocTriageWorkflow = "doc-triage@v1"

// DocTriageRequest is one triage to file. Docs and Roadmap together name the documents;
// Report defaults to docs/triage/<date>-<slug>.md.
type DocTriageRequest struct {
	Root        string
	Title       string
	Docs        []string
	Roadmap     string
	Report      string
	Destination Destination
	At          time.Time
	By, Via     string
}

var (
	markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	slugRun      = regexp.MustCompile(`[^a-z0-9]+`)
)

// DocTriageDocuments answers the documents a triage reads, in order and each once: the
// named documents first, then every Markdown document under docs/design/ that the roadmap
// links, in first-link order. A named document must exist; a roadmap link to a missing file
// is not a document to triage and is skipped.
func DocTriageDocuments(root string, docs []string, roadmap string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, d := range docs {
		p, err := repoMarkdown(root, d)
		if err != nil {
			return nil, err
		}
		add(p)
	}
	if roadmap != "" {
		rp, err := repoMarkdown(root, roadmap)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(filepath.Join(root, rp))
		if err != nil {
			return nil, Rejectionf("document-missing", "", "reading roadmap %s: %v", rp, err)
		}
		for _, m := range markdownLink.FindAllStringSubmatch(string(raw), -1) {
			target, _, _ := strings.Cut(m[1], "#")
			if target == "" || strings.Contains(target, "://") || path.IsAbs(target) {
				continue
			}
			p := path.Clean(path.Join(path.Dir(rp), target))
			if !strings.HasPrefix(p, "docs/design/") || !strings.HasSuffix(p, ".md") {
				continue
			}
			if info, err := os.Stat(filepath.Join(root, p)); err != nil || !info.Mode().IsRegular() {
				continue
			}
			add(p)
		}
	}
	return out, nil
}

// repoMarkdown cleans a repository-relative Markdown path and requires the file to exist.
func repoMarkdown(root, p string) (string, error) {
	clean := path.Clean(filepath.ToSlash(p))
	if p == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", Rejectionf("document-path", "", "%q is not a repository-relative path", p)
	}
	if !strings.HasSuffix(clean, ".md") {
		return "", Rejectionf("document-not-markdown", "", "%s is not a Markdown document", clean)
	}
	if info, err := os.Stat(filepath.Join(root, clean)); err != nil || !info.Mode().IsRegular() {
		return "", Rejectionf("document-missing", "", "%s is not a file in %s", clean, root)
	}
	return clean, nil
}

// docTriageBound reads the most documents doc-triage@v1 accepts from the workflow file
// itself, so the command and the workflow cannot disagree about it.
func docTriageBound(root string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(root, "config", "tillr", "workflows", DocTriageWorkflow+".json"))
	if err != nil {
		return 0, Rejectionf("workflow-missing", "", "the triage workflow config/tillr/workflows/%s.json is not readable: %v", DocTriageWorkflow, err)
	}
	var w struct {
		Args map[string]struct {
			Most int `json:"most"`
		} `json:"args"`
	}
	if err := json.Unmarshal(raw, &w); err != nil || w.Args["docs"].Most <= 0 {
		return 0, Rejectionf("workflow-missing", "", "config/tillr/workflows/%s.json declares no bounded docs argument", DocTriageWorkflow)
	}
	return w.Args["docs"].Most, nil
}

func slugOf(title string) string {
	slug := strings.Trim(slugRun.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(slug) > 48 {
		slug = strings.TrimRight(slug[:48], "-")
	}
	return slug
}

// DocTriage files one triage item straight in the store, in the caller's transaction, and
// places it. Nothing is written to any tracked file: the report reaches the tree through
// the item's own landing. Every refusal comes before the first write.
func DocTriage(ctx context.Context, tx *store.Tx, req DocTriageRequest) (record.Envelope, error) {
	title := strings.TrimSpace(req.Title)
	slug := slugOf(title)
	if slug == "" {
		return record.Envelope{}, Rejectionf("title-required", "", "a triage needs a --title with at least one letter or digit")
	}
	docs, err := DocTriageDocuments(req.Root, req.Docs, req.Roadmap)
	if err != nil {
		return record.Envelope{}, err
	}
	if len(docs) == 0 {
		return record.Envelope{}, Rejectionf("no-documents", "", "name at least one document with --docs, or a --roadmap that links documents under docs/design/")
	}
	most, err := docTriageBound(req.Root)
	if err != nil {
		return record.Envelope{}, err
	}
	if len(docs) > most {
		return record.Envelope{}, Rejectionf("too-many-documents", "", "%d documents exceed the %d %s accepts; file the triage in smaller sets", len(docs), most, DocTriageWorkflow)
	}
	date := req.At.Format("2006-01-02")
	id := "item-doc-triage-" + date + "-" + slug
	report := req.Report
	if report == "" {
		report = "docs/triage/" + date + "-" + slug + ".md"
	}
	report = path.Clean(filepath.ToSlash(report))
	if !strings.HasPrefix(report, "docs/") || !strings.HasSuffix(report, ".md") {
		return record.Envelope{}, Rejectionf("report-path", id, "the report %s must be a Markdown file under docs/", report)
	}
	for _, d := range docs {
		if d == report {
			return record.Envelope{}, Rejectionf("report-path", id, "the report %s is one of the documents being triaged; a triage changes none of them", report)
		}
	}
	if err := tx.Reader().QueryRow(`SELECT item_id FROM items WHERE item_id=?`, id).Scan(new(string)); err == nil {
		return record.Envelope{}, Rejectionf("item-exists", id, "item %s already exists; choose another --title", id)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return record.Envelope{}, Internalf("checking item %s: %v", id, err)
	}
	listed := "`" + strings.Join(docs, "`, `") + "`"
	args := make([]any, len(docs))
	for i, d := range docs {
		args[i] = d
	}
	payload := itemPayload{
		ItemID: id, Title: title, State: "in-queue", Seat: "design",
		Workflow: DocTriageWorkflow, WorkflowArgs: map[string]any{"docs": args},
		AllowedPaths: []string{report}, BlockedBy: []string{},
		Criteria: []criterion{
			{CriterionID: "c1", Text: fmt.Sprintf("The triage report `%s` has a table with one row for each of these %d documents: %s. Each row gives the verdict, the recommended action and its evidence in one sentence, and the per-document detail follows the table.", report, len(docs), listed)},
			{CriterionID: "c2", Text: "Each verdict says whether the document is still relevant and still accurate, and whether its status line and open questions are still true. Every statement about what is built, unbuilt or true cites a file, a test, a landed item, or a command and its result."},
			{CriterionID: "c3", Text: "The report recommends and changes no other file: no triaged document is edited, and no recommendation is acted on."},
		},
	}
	env, err := envelopeOf(RecordKind, id, req.At, payload)
	if err != nil {
		return record.Envelope{}, err
	}
	if _, err := Apply(ctx, tx, env); err != nil {
		return record.Envelope{}, err
	}
	placed, err := Place(ctx, tx, id, req.Destination, req.At, req.By, req.Via)
	if err != nil {
		return record.Envelope{}, err
	}
	var answer map[string]any
	if err := json.Unmarshal(placed.Payload, &answer); err != nil {
		return record.Envelope{}, Internalf("reading the placement: %v", err)
	}
	answer["report"], answer["documents"] = report, docs
	return envelopeOf("item.doc-triage", id, req.At, answer)
}

// readWorkflowArgs reads an item's workflow arguments, answering none on a store opened
// read-only before the column existed.
func readWorkflowArgs(r *store.Reader, id string) (map[string]any, error) {
	var present bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('items') WHERE name = 'workflow_args')`).Scan(&present); err != nil {
		return nil, Internalf("checking workflow arguments: %v", err)
	}
	if !present {
		return nil, nil
	}
	var stored string
	if err := r.QueryRow(`SELECT workflow_args FROM items WHERE item_id=?`, id).Scan(&stored); err != nil {
		return nil, Internalf("reading item %q's workflow arguments: %v", id, err)
	}
	return decodeWorkflowArgs(id, stored)
}
