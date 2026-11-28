package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

type waitRow struct{ item, ref, doc, qid, slug, hash, quote string }

// questionWaitsPlan files one item per name against docs/d.md, records the waits and
// overrides given, plans one tick and answers each item's decision.
func questionWaitsPlan(t *testing.T, doc string, states map[string]string, ids []string, waits []waitRow, overrides [][]string) (map[string]decision, string) {
	t.Helper()
	root := fixtureRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/d.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var records []string
	for _, id := range ids {
		docRef := "docs/d.md"
		if strings.HasPrefix(id, "request") {
			docRef = ""
		}
		records = append(records, itemRecordWithDoc(id, "in-queue", docRef))
	}
	storePath := fixture(t, records...)
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, w := range waits {
			if _, err := tx.Exec(`INSERT INTO item_question_waits(item_id, ref_key, doc_path, question_id, heading_slug, quoted_question, section_sha256, source) VALUES(?,?,?,?,?,?,?, 'plan')`,
				w.item, w.ref, w.doc, w.qid, w.slug, w.quote, w.hash); err != nil {
				return err
			}
		}
		for _, o := range overrides {
			if _, err := tx.Exec(`INSERT INTO item_question_overrides(item_id, ref_key, op, doc_path, question_id) VALUES(?,?,?,?,?)`, o[0], o[1], o[2], o[3], o[4]); err != nil {
				return err
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	var qs []map[string]any
	ids2 := make([]string, 0, len(states))
	for id := range states {
		ids2 = append(ids2, id)
	}
	sort.Strings(ids2)
	for _, id := range ids2 {
		qs = append(qs, map[string]any{"id": id, "state": states[id]})
	}
	idx := indexStub(t, map[string]any{"path": "docs/d.md", "questions": qs})
	tickPath := tick(t, "2026-11-28T00:00:00Z", []string{"lane-1", "lane-2", "lane-3", "lane-4"}, 4, policy(t, "/bin/true"))
	out := map[string]decision{}
	for _, r := range planFrom(t, storePath, tickPath, root, idx) {
		out[r.Payload.ItemID] = r.Payload
	}
	return out, storePath
}

const twoOpen = "# D\n\n## Open questions\n\nShould it refresh?\n"

func TestRecordedWaitsHoldOnlyTheNamedQuestion(t *testing.T) {
	states := map[string]string{"OQ-1": "open", "OQ-2": "open", "OQ-3": "answered"}
	got, _ := questionWaitsPlan(t, twoOpen, states, []string{"legacy", "none", "second", "both"},
		[]waitRow{
			{item: "second", ref: "docs/d.md#OQ-2", doc: "docs/d.md", qid: "OQ-2"},
			{item: "both", ref: "docs/d.md#OQ-2", doc: "docs/d.md", qid: "OQ-2"},
			{item: "both", ref: "docs/d.md#OQ-1", doc: "docs/d.md", qid: "OQ-1"},
		},
		[][]string{{"none", "*", "drop", "", ""}})
	if d := got["legacy"]; d.Action != "hold" || d.Reason != "question-open" || d.QuestionID != "OQ-1" || len(d.QuestionRefs) != 0 {
		t.Fatalf("a legacy item keeps the whole-document hold: %+v", d)
	}
	if d := got["none"]; d.Action != "dispatch" {
		t.Fatalf("an item recorded as waiting on nothing must dispatch: %+v", d)
	}
	if d := got["second"]; d.Reason != "question-open" || d.QuestionID != "OQ-2" || !reflect.DeepEqual(d.QuestionRefs, []string{"docs/d.md#OQ-2"}) {
		t.Fatalf("an item waiting on the second question names only it: %+v", d)
	}
	if d := got["both"]; !reflect.DeepEqual(d.QuestionRefs, []string{"docs/d.md#OQ-2", "docs/d.md#OQ-1"}) || d.QuestionID != "OQ-2" {
		t.Fatalf("every open ref in the item's order: %+v", d)
	}
}

func TestRecordedWaitsNarrowAndRelease(t *testing.T) {
	waits := []waitRow{
		{item: "a", ref: "docs/d.md#OQ-1", doc: "docs/d.md", qid: "OQ-1"},
		{item: "a", ref: "docs/d.md#OQ-2", doc: "docs/d.md", qid: "OQ-2"},
	}
	got, _ := questionWaitsPlan(t, twoOpen, map[string]string{"OQ-1": "answered", "OQ-2": "open"}, []string{"a"}, waits, nil)
	if d := got["a"]; !reflect.DeepEqual(d.QuestionRefs, []string{"docs/d.md#OQ-2"}) {
		t.Fatalf("answering the first narrows the hold: %+v", d)
	}
	got, _ = questionWaitsPlan(t, twoOpen, map[string]string{"OQ-1": "answered", "OQ-2": "answered"}, []string{"a"}, waits, nil)
	if d := got["a"]; d.Action != "dispatch" {
		t.Fatalf("every wait answered releases the item: %+v", d)
	}
	got, _ = questionWaitsPlan(t, twoOpen, map[string]string{"OQ-1": "answered"}, []string{"a"}, waits, nil)
	if d := got["a"]; d.Reason != "question-removed" || !reflect.DeepEqual(d.QuestionRefs, []string{"docs/d.md#OQ-2"}) {
		t.Fatalf("a vanished question holds question-removed: %+v", d)
	}
	got, _ = questionWaitsPlan(t, twoOpen, map[string]string{"OQ-1": "open", "OQ-2": "open"}, []string{"a"}, waits,
		[][]string{{"a", "docs/d.md#OQ-1", "drop", "docs/d.md", "OQ-1"}, {"a", "docs/d.md#OQ-2", "drop", "docs/d.md", "OQ-2"}})
	if d := got["a"]; d.Action != "dispatch" {
		t.Fatalf("dropping every wait releases the item: %+v", d)
	}
}

func TestRecordedProseWaitHoldsUntilReplanned(t *testing.T) {
	wait := waitRow{item: "p", ref: "docs/d.md#open-questions", doc: "docs/d.md", slug: "open-questions", hash: "0000", quote: "Should it refresh?"}
	got, _ := questionWaitsPlan(t, twoOpen, nil, []string{"p"}, []waitRow{wait}, nil)
	if d := got["p"]; d.Reason != "question-open" || d.QuestionID != "open-questions" || !reflect.DeepEqual(d.QuestionRefs, []string{"docs/d.md#open-questions"}) {
		t.Fatalf("an unedited prose question holds: %+v", d)
	}
	// The stored hash no longer matches the section (it was edited): still held, since
	// release after a re-plan belongs to a later slice.
	got, _ = questionWaitsPlan(t, "# D\n\n## Open questions\n\nEdited text.\n", nil, []string{"p"}, []waitRow{wait}, nil)
	if d := got["p"]; d.Reason != "question-open" {
		t.Fatalf("an edited prose section must keep holding: %+v", d)
	}
	got, _ = questionWaitsPlan(t, "# D\n\n## Other\n", nil, []string{"p"}, []waitRow{wait}, nil)
	if d := got["p"]; d.Reason != "question-removed" || !reflect.DeepEqual(d.QuestionRefs, []string{"docs/d.md#open-questions"}) {
		t.Fatalf("a deleted heading holds question-removed: %+v", d)
	}
}

// A standalone request names no design document, and still holds on the questions it records.
func TestRecordedWaitsHoldARequestWithNoDesignDoc(t *testing.T) {
	got, _ := questionWaitsPlan(t, twoOpen, map[string]string{"OQ-1": "open", "OQ-2": "open"}, []string{"request-held", "request-free"},
		[]waitRow{{item: "request-held", ref: "docs/d.md#OQ-2", doc: "docs/d.md", qid: "OQ-2"}}, nil)
	if d := got["request-held"]; d.Reason != "question-open" || !reflect.DeepEqual(d.QuestionRefs, []string{"docs/d.md#OQ-2"}) {
		t.Fatalf("a request with a recorded wait must hold on it: %+v", d)
	}
	if d := got["request-free"]; d.Action != "dispatch" {
		t.Fatalf("a request with no record must dispatch: %+v", d)
	}
}
