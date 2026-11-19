package item_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

var triageAt = time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)

// triageRoot is a repository holding the real doc-triage@v1 workflow file and the named
// documents, so the bound the command enforces is the one the workflow declares.
func triageRoot(t *testing.T, docs map[string]string) string {
	t.Helper()
	root := scratch(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "tillr", "workflows", "doc-triage@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"config/tillr/workflows/doc-triage@v1.json": string(raw)}
	for path, body := range docs {
		files[path] = body
	}
	for path, body := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fileTriage(t *testing.T, s *store.Store, req item.DocTriageRequest) (record.Envelope, error) {
	t.Helper()
	var env record.Envelope
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		env, err = item.DocTriage(context.Background(), tx, req)
		return err
	})
	return env, err
}

type shownTriage struct {
	ItemID       string            `json:"item_id"`
	Title        string            `json:"title"`
	State        string            `json:"state"`
	Seat         string            `json:"seat"`
	Workflow     string            `json:"workflow"`
	WorkflowArgs map[string]any    `json:"workflow_args"`
	AllowedPaths []string          `json:"allowed_paths"`
	DesignDoc    string            `json:"design_doc"`
	Express      bool              `json:"express"`
	Criteria     []json.RawMessage `json:"criteria"`
}

func showTriage(t *testing.T, s *store.Store, id, root string) (shownTriage, string) {
	t.Helper()
	var env record.Envelope
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		env, err = item.Show(r, id, root)
		return err
	}); err != nil {
		t.Fatalf("showing %s: %v", id, err)
	}
	var shown shownTriage
	if err := json.Unmarshal(env.Payload, &shown); err != nil {
		t.Fatal(err)
	}
	return shown, string(env.Payload)
}

// An item's record may carry the arguments its own workflow is bound with. They are stored,
// answered by show, and refused on an item that names no workflow to bind them to.
func TestAnItemStoresItsWorkflowArguments(t *testing.T) {
	s := syncStore(t)
	env := seatRecord(t, "item-with-args", "design")
	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["workflow"] = "doc-triage@v1"
	payload["workflow_args"] = map[string]any{"docs": []string{"docs/a.md", "docs/b.md"}}
	env.Payload, _ = json.Marshal(payload)
	if _, err := applyRecord(t, s, env); err != nil {
		t.Fatal(err)
	}
	if got := column(t, s, `SELECT workflow_args FROM items WHERE item_id='item-with-args'`); got != `{"docs":["docs/a.md","docs/b.md"]}` {
		t.Fatalf("stored workflow_args %q", got)
	}
	shown, _ := showTriage(t, s, "item-with-args", ".")
	if !reflect.DeepEqual(shown.WorkflowArgs, map[string]any{"docs": []any{"docs/a.md", "docs/b.md"}}) {
		t.Fatalf("show answered %+v", shown.WorkflowArgs)
	}

	delete(payload, "workflow")
	payload["item_id"] = "item-args-alone"
	env.Payload, _ = json.Marshal(payload)
	_, err := applyRecord(t, s, env)
	if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "workflow-args-without-workflow" {
		t.Fatalf("arguments with no workflow: %v", err)
	}
}

// A triage of named documents is one item: the doc-triage@v1 workflow bound to the
// documents in the order given, the design seat, and a fence of exactly the one report.
func TestDocTriageFilesOneItemForTheNamedDocuments(t *testing.T) {
	root := triageRoot(t, map[string]string{"docs/design/b.md": "# B\n", "docs/design/a.md": "# A\n"})
	s := syncStore(t)
	env, err := fileTriage(t, s, item.DocTriageRequest{
		Root: root, Title: "Check two designs", Docs: []string{"docs/design/b.md", "./docs/design/a.md", "docs/design/b.md"},
		Destination: item.Destination{To: "last"}, At: triageAt, By: "owner", Via: "cli",
	})
	if err != nil {
		t.Fatal(err)
	}
	const id = "item-doc-triage-2026-11-18-check-two-designs"
	if env.Kind != "item.doc-triage" || !strings.Contains(string(env.Payload), `"item_id":"`+id+`"`) {
		t.Fatalf("answer %s %s", env.Kind, env.Payload)
	}
	shown, raw := showTriage(t, s, id, root)
	const report = "docs/triage/2026-11-18-check-two-designs.md"
	if shown.Title != "Check two designs" || shown.State != "in-queue" || shown.Seat != "design" || shown.Workflow != "doc-triage@v1" || shown.DesignDoc != "" {
		t.Fatalf("item %s", raw)
	}
	if !reflect.DeepEqual(shown.AllowedPaths, []string{report}) {
		t.Fatalf("fence %v, want exactly the report", shown.AllowedPaths)
	}
	if !reflect.DeepEqual(shown.WorkflowArgs, map[string]any{"docs": []any{"docs/design/b.md", "docs/design/a.md"}}) {
		t.Fatalf("workflow args %v", shown.WorkflowArgs)
	}
	for _, want := range []string{report, "docs/design/b.md", "docs/design/a.md", "changes no other file"} {
		if !strings.Contains(raw, want) {
			t.Errorf("criteria do not name %q: %s", want, raw)
		}
	}
	// Filing writes no tracked file: the report reaches the tree through the item's landing.
	if _, err := os.Stat(filepath.Join(root, report)); !os.IsNotExist(err) {
		t.Fatalf("filing wrote the report: %v", err)
	}
	_, err = fileTriage(t, s, item.DocTriageRequest{Root: root, Title: "Check two designs", Docs: []string{"docs/design/a.md"}, Destination: item.Destination{To: "last"}, At: triageAt})
	if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "item-exists" {
		t.Fatalf("second triage with the same id: %v", err)
	}
}

// A roadmap expands to the design documents it links, in first-link order, each once;
// links outside docs/design/, anchors and missing files are not triaged.
func TestDocTriageExpandsARoadmapToTheDesignsItRoutes(t *testing.T) {
	roadmap := "# Roadmap\n\n1. [Graphs](../design/graphs.md#the-words) first.\n" +
		"2. [Tiering](../design/tiering.md) and [graphs again](../design/graphs.md).\n" +
		"3. [Drain](../../tools/tillr-drain/README.md), [pi](../pi/roadmap.md), [anchor](#ordering-basis).\n" +
		"4. [Gone](../design/missing.md).\n"
	root := triageRoot(t, map[string]string{
		"docs/tillr/roadmap.md": roadmap, "docs/design/graphs.md": "# G\n", "docs/design/tiering.md": "# T\n", "docs/pi/roadmap.md": "# P\n",
	})
	docs, err := item.DocTriageDocuments(root, []string{"docs/design/extra.md"}, "")
	if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "document-missing" || docs != nil {
		t.Fatalf("a missing named document: %v %v", docs, err)
	}
	docs, err = item.DocTriageDocuments(root, nil, "docs/tillr/roadmap.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"docs/design/graphs.md", "docs/design/tiering.md"}; !reflect.DeepEqual(docs, want) {
		t.Fatalf("roadmap expanded to %v, want %v", docs, want)
	}
	s := syncStore(t)
	if _, err := fileTriage(t, s, item.DocTriageRequest{Root: root, Title: "Tillr roadmap designs", Roadmap: "docs/tillr/roadmap.md", Report: "docs/tillr/triage/designs.md", Destination: item.Destination{To: "first"}, At: triageAt}); err != nil {
		t.Fatal(err)
	}
	shown, _ := showTriage(t, s, "item-doc-triage-2026-11-18-tillr-roadmap-designs", root)
	if !reflect.DeepEqual(shown.AllowedPaths, []string{"docs/tillr/triage/designs.md"}) {
		t.Fatalf("fence %v", shown.AllowedPaths)
	}
	if !reflect.DeepEqual(shown.WorkflowArgs["docs"], []any{"docs/design/graphs.md", "docs/design/tiering.md"}) {
		t.Fatalf("docs %v", shown.WorkflowArgs)
	}
}

// The command refuses what the workflow would refuse later, before any item exists: no
// documents, more documents than the workflow's declared bound, a report outside docs/, and
// a report that is itself one of the documents being triaged.
func TestDocTriageRefusesBeforeWriting(t *testing.T) {
	docs := map[string]string{}
	var many []string
	for i := 0; i < 49; i++ {
		path := "docs/design/d" + strings.Repeat("x", i) + ".md"
		docs[path] = "# D\n"
		many = append(many, path)
	}
	root := triageRoot(t, docs)
	for name, tc := range map[string]struct {
		req  item.DocTriageRequest
		rule string
	}{
		"none":         {item.DocTriageRequest{Title: "Empty"}, "no-documents"},
		"over-bound":   {item.DocTriageRequest{Title: "Too many", Docs: many}, "too-many-documents"},
		"no-title":     {item.DocTriageRequest{Docs: many[:1]}, "title-required"},
		"report-out":   {item.DocTriageRequest{Title: "Out", Docs: many[:1], Report: "tools/report.md"}, "report-path"},
		"report-self":  {item.DocTriageRequest{Title: "Self", Docs: many[:1], Report: many[0]}, "report-path"},
		"not-markdown": {item.DocTriageRequest{Title: "Text", Docs: []string{"config/tillr/workflows/doc-triage@v1.json"}}, "document-not-markdown"},
	} {
		t.Run(name, func(t *testing.T) {
			s := syncStore(t)
			tc.req.Root, tc.req.At, tc.req.Destination = root, triageAt, item.Destination{To: "last"}
			_, err := fileTriage(t, s, tc.req)
			if rej, ok := item.RejectionOf(err); !ok || rej.Rule != tc.rule {
				t.Fatalf("want %s, got %v", tc.rule, err)
			}
			if n := count(t, s, `SELECT COUNT(*) FROM items`); n != 0 {
				t.Fatalf("a refused triage wrote %d item(s)", n)
			}
		})
	}
}

// An item whose record carries its workflow's arguments is declared by the store: no
// document block can carry those arguments, so it is not counted as unshared residue in a
// document-fed store, while an ordinary stored item with no block still is.
func TestAStoreDeclaredItemIsNotUnshared(t *testing.T) {
	root := triageRoot(t, map[string]string{"docs/design/a.md": "# A\n"})
	s := syncStore(t)
	runSync(t, s, root, doc("docs/plan.md", goodBlock))
	if _, err := fileTriage(t, s, item.DocTriageRequest{Root: root, Title: "One design", Docs: []string{"docs/design/a.md"}, Destination: item.Destination{To: "last"}, At: triageAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := applyRecord(t, s, seatRecord(t, "item-private", "build")); err != nil {
		t.Fatal(err)
	}
	report := runSync(t, s, root, doc("docs/plan.md", goodBlock))
	if !report.DocumentFed || !reflect.DeepEqual(report.UnsharedOpenIDs, []string{"item-private"}) {
		t.Fatalf("unshared %v (document-fed %v), want only item-private", report.UnsharedOpenIDs, report.DocumentFed)
	}
}
