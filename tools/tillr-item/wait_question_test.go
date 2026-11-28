package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func waitRun(t *testing.T, args ...string) (int, string) {
	t.Helper()
	out, err := exec.Command(bin, append([]string{"wait-question"}, args...)...).CombinedOutput()
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

func readWaits(t *testing.T, s *store.Store) map[string]item.QuestionWaits {
	t.Helper()
	var got map[string]item.QuestionWaits
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		got, err = item.ReadQuestionWaits(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func canon(w item.QuestionWaits) []string {
	var out []string
	for _, r := range w.Refs {
		out = append(out, r.Canonical())
	}
	return out
}

func insertWait(t *testing.T, s *store.Store, id, doc, qid string) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_question_waits(item_id, ref_key, doc_path, question_id, source) VALUES(?,?,?,?,'plan')`, id, doc+"#"+qid, doc, qid)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionWaitsReaderAbsentForLegacyAndMath(t *testing.T) {
	path, s := placeFixture(t, "a", "b", "c")
	if got := readWaits(t, s); len(got) != 0 {
		t.Fatalf("an item with no rows must be absent (legacy), got %v", got)
	}
	insertWait(t, s, "0", "d.md", "OQ-1")
	insertWait(t, s, "0", "d.md", "OQ-2")
	insertWait(t, s, "1", "d.md", "OQ-1")
	if got := canon(readWaits(t, s)["0"]); strings.Join(got, ",") != "d.md#OQ-1,d.md#OQ-2" {
		t.Fatalf("waits in insertion order: %v", got)
	}
	if code, out := waitRun(t, "--store", path, "--item", "0", "--drop", "d.md#OQ-1", "--reason", "t"); code != 0 {
		t.Fatalf("drop: %d %s", code, out)
	}
	if code, out := waitRun(t, "--store", path, "--item", "0", "--add", "d.md#OQ-9", "--reason", "t"); code != 0 {
		t.Fatalf("add: %d %s", code, out)
	}
	if got := canon(readWaits(t, s)["0"]); strings.Join(got, ",") != "d.md#OQ-2,d.md#OQ-9" {
		t.Fatalf("waits + adds - drops: %v", got)
	}
	// A later add of a dropped ref replaces the drop.
	if code, out := waitRun(t, "--store", path, "--item", "0", "--add", "d.md#OQ-1", "--reason", "t"); code != 0 {
		t.Fatalf("re-add: %d %s", code, out)
	}
	if got := canon(readWaits(t, s)["0"]); len(got) != 3 || got[0] != "d.md#OQ-1" {
		t.Fatalf("re-added ref must wait again: %v", got)
	}
	// --none: present, empty, and add overrides cleared.
	if code, out := waitRun(t, "--store", path, "--item", "0", "--none", "--reason", "t"); code != 0 {
		t.Fatalf("none: %d %s", code, out)
	}
	w, ok := readWaits(t, s)["0"]
	if !ok || len(w.Refs) != 0 {
		t.Fatalf("--none must record metadata with no refs: %v %v", ok, w)
	}
	// An item never mentioned stays legacy; --none on a legacy item makes it known.
	if code, out := waitRun(t, "--store", path, "--item", "2", "--none", "--reason", "t"); code != 0 {
		t.Fatalf("none on legacy: %d %s", code, out)
	}
	got := readWaits(t, s)
	if _, ok := got["2"]; !ok || len(got["1"].Refs) != 1 {
		t.Fatalf("unexpected map: %v", got)
	}
}

func TestQuestionWaitsReaderToleratesMissingTables(t *testing.T) {
	_, s := placeFixture(t, "a")
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TABLE item_question_overrides`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := readWaits(t, s); len(got) != 0 {
		t.Fatalf("missing table: %v", got)
	}
}

func TestWaitQuestionRefusals(t *testing.T) {
	path, _ := placeFixture(t, "a")
	for name, args := range map[string][]string{
		"no reason":               {"--item", "0", "--none"},
		"unknown item":            {"--item", "nope", "--none", "--reason", "t"},
		"no hash":                 {"--item", "0", "--add", "d.md", "--reason", "t"},
		"two ops":                 {"--item", "0", "--none", "--add", "d.md#OQ-1", "--reason", "t"},
		"no op":                   {"--item", "0", "--reason", "t"},
		"prose add without quote": {"--item", "0", "--add", "d.md#open-questions", "--reason", "t"},
	} {
		if code, out := waitRun(t, append([]string{"--store", path}, args...)...); code == 0 {
			t.Fatalf("%s: want refusal, got success: %s", name, out)
		}
	}
	root := scratch(t, "wq-root")
	if code, _ := waitRun(t, "--store", path, "--root", root, "--item", "0", "--add", "d.md#nope", "--quote", "q", "--reason", "t"); code == 0 {
		t.Fatal("a prose add for a missing document must be refused")
	}
}

func TestWaitQuestionProseAddStoresSectionHash(t *testing.T) {
	path, s := placeFixture(t, "a")
	root := scratch(t, "wq-prose")
	doc := filepath.Join(root, "d.md")
	write := func(a, b string) {
		if err := os.WriteFile(doc, []byte("# T\n\n## Open questions\n\n"+a+"\n\n## Other\n\n"+b+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hashOf := func() string {
		code, out := waitRun(t, "--store", path, "--root", root, "--item", "0", "--add", "d.md#open-questions", "--quote", "Refresh?", "--reason", "t")
		if code != 0 {
			t.Fatalf("add: %d %s", code, out)
		}
		w := readWaits(t, s)["0"]
		if len(w.Refs) != 1 || w.Refs[0].QuestionID != "" || w.Refs[0].HeadingSlug != "open-questions" || w.Refs[0].QuotedQuestion != "Refresh?" {
			t.Fatalf("prose ref: %+v", w.Refs)
		}
		return w.Refs[0].SectionSHA256
	}
	write("Should it refresh?", "x")
	h1 := hashOf()
	write("Should it refresh?", "changed elsewhere")
	if h2 := hashOf(); h2 != h1 {
		t.Fatal("a change in another section must not change the hash")
	}
	write("Should it refresh in the background?", "x")
	if h3 := hashOf(); h3 == h1 || h3 == "" {
		t.Fatal("a change in the section must change the hash")
	}
}

func TestSectionSHA256IgnoresFencedHeadings(t *testing.T) {
	a, ok := item.SectionSHA256("## Q\n\n```\n## not\n```\n\nbody\n## Next\n", "q")
	b, _ := item.SectionSHA256("## Q\n\n```\n## not\n```\n\nbody\n## Next\nmore\n", "q")
	if !ok || a != b {
		t.Fatal("a fenced heading must not end the section and the next section must not count")
	}
	if _, ok := item.SectionSHA256("## Q\n", "gone"); ok {
		t.Fatal("a missing heading must not be found")
	}
}
