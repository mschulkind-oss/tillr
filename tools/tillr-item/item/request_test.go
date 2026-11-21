package item_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// requestRoot is a repository holding the real workflow files a request may name.
func requestRoot(t *testing.T) string {
	t.Helper()
	root := scratch(t)
	dir := filepath.Join(root, "config", "tillr", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"scouted-design@v1", "planned-build@v1", "doc-triage@v1"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "tillr", "workflows", ref+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ref+".json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// fileRequest checks the workflow as the face does (before the transaction) and files.
func fileRequest(t *testing.T, s *store.Store, root string, f item.RequestFiling) (record.Envelope, error) {
	t.Helper()
	if f.At.IsZero() {
		f.At = triageAt
	}
	if f.Destination == (item.Destination{}) {
		f.Destination = item.Destination{To: "last"}
	}
	ref := f.Workflow
	if ref == "" {
		ref = item.RequestDefaultWorkflow[f.Seat]
		if f.Seat == "" {
			ref = item.RequestDefaultWorkflow["build"]
		}
	}
	if err := item.RequestWorkflowCheck(root, ref); err != nil {
		return record.Envelope{}, err
	}
	var env record.Envelope
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if store.HoldsWrite() == false {
			return fmt.Errorf("the filing ran outside a write transaction")
		}
		var err error
		env, err = item.FileRequest(context.Background(), tx, f)
		return err
	})
	return env, err
}

func requestID(words string) string {
	return "item-request-" + fmt.Sprintf("%x", sha256.Sum256([]byte(words)))[:10]
}

// The four requests of 2026-11-18 the owner wanted filed instead of run by hand: a design
// seat editing one document, a design seat authoring a new one, a code change with a plan,
// a build and a verify, and a small config change in one run.
var fourRequests = []struct {
	name     string
	filing   item.RequestFiling
	seat     string
	workflow string
}{
	{"rewrite-questions", item.RequestFiling{
		Words: []byte("Rewrite the open questions in docs/tillr/decision-provenance-review.md to the design-doc skill's walkthrough form.\n"),
		Title: "Rewrite the provenance review's open questions", Seat: "design", Workflow: "single-worker@v1",
		Paths: []string{"docs/tillr/decision-provenance-review.md"},
	}, "design", "single-worker@v1"},
	{"new-design", item.RequestFiling{
		Words: []byte("Author a new design for X under docs/design/, scouting the tree first, and add it to the roadmap.\n"),
		Title: "Design X", Seat: "design",
		Paths:    []string{"docs/design/x.md", "docs/tillr/roadmap.md"},
		Criteria: []string{"docs/tillr/roadmap.md links docs/design/x.md."},
	}, "design", "scouted-design@v1"},
	{"code-change", item.RequestFiling{
		Words: []byte("Remove the tillr-trace index cache; readers answer from source records.\n"),
		Title: "Remove the tillr-trace index cache",
		Paths: []string{"tools/tillr-trace/", "tools/tillr-view/"},
	}, "build", "planned-build@v1"},
	{"config-rows", item.RequestFiling{
		Words: []byte("Add two rows to yolo-packs/copilot-models/pack.toml: {{ model-a }} and model-b.\n"),
		Title: "List two more Copilot models", Workflow: "single-worker@v1",
		Paths: []string{"yolo-packs/copilot-models/pack.toml"},
	}, "build", "single-worker@v1"},
}

// Each of the four is one in-queue item that names its workflow and fence, carries the
// fixed criteria (none of which asks for an item block) plus any of its own, and keeps its
// words byte for byte in the store. Nothing is written to the repository.
func TestFileRequestFilesEachShape(t *testing.T) {
	root := requestRoot(t)
	for _, tc := range fourRequests {
		t.Run(tc.name, func(t *testing.T) {
			s := syncStore(t)
			env, err := fileRequest(t, s, root, tc.filing)
			if err != nil {
				t.Fatal(err)
			}
			id := requestID(string(tc.filing.Words))
			if env.Kind != "item.request" || !strings.Contains(string(env.Payload), `"item_id":"`+id+`"`) || !strings.Contains(string(env.Payload), `"workflow":"`+tc.workflow+`"`) {
				t.Fatalf("answer %s %s", env.Kind, env.Payload)
			}
			shown, raw := showTriage(t, s, id, root)
			if shown.Title != tc.filing.Title || shown.State != "in-queue" || shown.Seat != tc.seat || shown.Workflow != tc.workflow || shown.DesignDoc != "" || shown.WorkflowArgs != nil || !shown.Express {
				t.Fatalf("item %s", raw)
			}
			var express int
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				return r.QueryRow(`SELECT express FROM items WHERE item_id=?`, id).Scan(&express)
			}); err != nil || express != 1 {
				t.Fatalf("express=%d err=%v", express, err)
			}
			if !reflect.DeepEqual(shown.AllowedPaths, tc.filing.Paths) {
				t.Fatalf("fence %v, want %v", shown.AllowedPaths, tc.filing.Paths)
			}
			if want := 4 + len(tc.filing.Criteria); len(shown.Criteria) != want {
				t.Fatalf("%d criteria, want %d: %s", len(shown.Criteria), want, raw)
			}
			var c3 struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(shown.Criteria[2], &c3)
			if !strings.HasPrefix(c3.Text, "No `<!-- tillr: item … -->` comment is added, edited or relied on") || strings.Contains(raw, "item block that carries") {
				t.Fatalf("criteria: %s", raw)
			}
			var words []byte
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				var err error
				words, err = item.RequestWords(r, id)
				return err
			}); err != nil || string(words) != string(tc.filing.Words) {
				t.Fatalf("words %q %v", words, err)
			}
			if n := count(t, s, `SELECT COUNT(*) FROM request_words`); n != 1 {
				t.Fatalf("%d request rows", n)
			}
		})
	}
}

// Filing the same words with the same choices again answers the first item's place and
// writes nothing; the same words with a different choice are refused, naming that item.
func TestFileRequestIsIdempotent(t *testing.T) {
	root := requestRoot(t)
	s := syncStore(t)
	f := fourRequests[2].filing
	first, err := fileRequest(t, s, root, f)
	if err != nil {
		t.Fatal(err)
	}
	head := count(t, s, `SELECT MAX(seq) FROM events`)
	again, err := fileRequest(t, s, root, f)
	if err != nil {
		t.Fatal(err)
	}
	var a, b map[string]any
	_ = json.Unmarshal(first.Payload, &a)
	_ = json.Unmarshal(again.Payload, &b)
	if again.Kind != "item.request" || b["item_id"] != a["item_id"] || b["place"] != float64(1) {
		t.Fatalf("refile answered %s %s", again.Kind, again.Payload)
	}
	if n := count(t, s, `SELECT MAX(seq) FROM events`); n != head {
		t.Fatalf("a refile wrote events: %d -> %d", head, n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM items`); n != 1 {
		t.Fatalf("%d items after a refile", n)
	}
	f.Title = "Another title"
	_, err = fileRequest(t, s, root, f)
	if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "request-differs" || rej.ItemID != a["item_id"] {
		t.Fatalf("same words, other title: %v", err)
	}
}

func TestFileRequestOrdinaryAndIdempotence(t *testing.T) {
	root := requestRoot(t)
	s := syncStore(t)
	f := fourRequests[2].filing
	f.Ordinary = true
	first, err := fileRequest(t, s, root, f)
	if err != nil {
		t.Fatal(err)
	}
	var a map[string]any
	_ = json.Unmarshal(first.Payload, &a)
	id := a["item_id"].(string)
	shown, raw := showTriage(t, s, id, root)
	if shown.Express {
		t.Fatalf("ordinary request marked express: %s", raw)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM item_criteria WHERE item_id=? AND lower(text) LIKE '%report%'`, id); n != 0 {
		t.Fatalf("filed request attached %d criteria about a report", n)
	}
	var express int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT express FROM items WHERE item_id=?`, id).Scan(&express)
	}); err != nil || express != 0 {
		t.Fatalf("express=%d err=%v", express, err)
	}
	head := count(t, s, `SELECT MAX(seq) FROM events`)
	again, err := fileRequest(t, s, root, f)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	_ = json.Unmarshal(again.Payload, &b)
	if again.Kind != "item.request" || b["item_id"] != id || b["place"] != float64(1) {
		t.Fatalf("ordinary refile answered %s %s", again.Kind, again.Payload)
	}
	if n := count(t, s, `SELECT MAX(seq) FROM events`); n != head {
		t.Fatalf("ordinary refile wrote events: %d -> %d", head, n)
	}
	f.Ordinary = false
	_, err = fileRequest(t, s, root, f)
	if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "request-differs" || rej.ItemID != id {
		t.Fatalf("same words, toggled ordinary: %v", err)
	}
}

// Every refusal comes before the first write, and names its rule.
func TestFileRequestRefusesBeforeWriting(t *testing.T) {
	root := requestRoot(t)
	good := fourRequests[3].filing
	with := func(change func(*item.RequestFiling)) item.RequestFiling {
		f := good
		f.Paths = append([]string(nil), good.Paths...)
		change(&f)
		return f
	}
	for name, tc := range map[string]struct {
		filing item.RequestFiling
		rule   string
	}{
		"no-words":       {with(func(f *item.RequestFiling) { f.Words = []byte(" \n\t") }), "words-empty"},
		"not-text":       {with(func(f *item.RequestFiling) { f.Words = []byte{0xff, 0xfe} }), "words-not-text"},
		"no-title":       {with(func(f *item.RequestFiling) { f.Title = "  " }), "title-required"},
		"two-lines":      {with(func(f *item.RequestFiling) { f.Title = "one\ntwo" }), "title-required"},
		"title-braces":   {with(func(f *item.RequestFiling) { f.Title = "a {{TASK}}" }), "title-braces"},
		"no-fence":       {with(func(f *item.RequestFiling) { f.Paths = nil }), "fence-required"},
		"absolute":       {with(func(f *item.RequestFiling) { f.Paths = []string{"/etc/passwd"} }), "fence-wildcard"},
		"traversal":      {with(func(f *item.RequestFiling) { f.Paths = []string{"docs/../../x"} }), "fence-wildcard"},
		"wildcard":       {with(func(f *item.RequestFiling) { f.Paths = []string{"docs/*.md"} }), "fence-wildcard"},
		"whole-tree":     {with(func(f *item.RequestFiling) { f.Paths = []string{"."} }), "fence-wildcard"},
		"needs-args":     {with(func(f *item.RequestFiling) { f.Workflow = "doc-triage@v1" }), "workflow-needs-args"},
		"no-such":        {with(func(f *item.RequestFiling) { f.Workflow = "no-such@v1" }), "workflow-missing"},
		"bad-ref":        {with(func(f *item.RequestFiling) { f.Workflow = "Planned" }), "workflow-reference"},
		"no-default":     {with(func(f *item.RequestFiling) { f.Seat, f.Workflow = "verify", "" }), "workflow-required"},
		"many-criteria":  {with(func(f *item.RequestFiling) { f.Criteria = []string{"a", "b", "c", "d", "e"} }), "too-many-criteria"},
		"empty-criteria": {with(func(f *item.RequestFiling) { f.Criteria = []string{" "} }), "criterion-empty"},
		"unknown-after":  {with(func(f *item.RequestFiling) { f.Destination = item.Destination{After: "item-nope"} }), "unknown-item"},
	} {
		t.Run(name, func(t *testing.T) {
			s := syncStore(t)
			var err error
			if tc.rule == "workflow-required" {
				// No default exists for the seat, so there is no file to check first.
				err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
					_, err := item.FileRequest(context.Background(), tx, tc.filing)
					return err
				})
			} else {
				_, err = fileRequest(t, s, root, tc.filing)
			}
			if rej, ok := item.RejectionOf(err); !ok || rej.Rule != tc.rule {
				t.Fatalf("want %s, got %v", tc.rule, err)
			}
			if n := count(t, s, `SELECT COUNT(*) FROM items`); n != 0 {
				t.Fatalf("a refused request wrote %d item(s)", n)
			}
		})
	}
}

// A filed request is declared by its record: no document block can carry its words, so a
// document-fed sync does not count it as unshared, while a plain stored item still is.
func TestAFiledRequestIsNotUnshared(t *testing.T) {
	root := requestRoot(t)
	s := syncStore(t)
	runSync(t, s, root, doc("docs/plan.md", goodBlock))
	if _, err := fileRequest(t, s, root, fourRequests[0].filing); err != nil {
		t.Fatal(err)
	}
	if _, err := applyRecord(t, s, seatRecord(t, "item-private", "build")); err != nil {
		t.Fatal(err)
	}
	report := runSync(t, s, root, doc("docs/plan.md", goodBlock))
	if !report.DocumentFed || !reflect.DeepEqual(report.UnsharedOpenIDs, []string{"item-private"}) {
		t.Fatalf("unshared %v (document-fed %v), want only item-private", report.UnsharedOpenIDs, report.DocumentFed)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM items WHERE item_id=?`, requestID(string(fourRequests[0].filing.Words))); n != 1 {
		t.Fatalf("sync removed the request: %d", n)
	}
}
