package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// designDoc declares questions through Vantage directives. Plain ID mentions and
// an undeclared bullet are negative controls: only the index declares questions.
const designDoc = `---
title: "fixture design"
status: proposed
---

# Fixture design

## 1. The shape

Prose that mentions OQ-9 in passing and must not be surfaced.

## 2. Open questions

- 💬 **[` + "`OQ-1`" + `](#2-open-questions) — the first question.**

  <!-- vantage: question id=OQ-1 -->

  _Leaning:_ Not decided.

- 💬 **[` + "`OQ-2`" + `](#2-open-questions) — the second question.**

  <!-- vantage: question id=OQ-2 -->

  _Leaning:_ Not decided.


## 3. A later section

- **[` + "`OQ-3`" + `](#3-a-later-section) — not an open question.** This bullet is outside the
  open-questions section and must not be surfaced.
`

// designDocEdited is the same document after a design pass: the first question's text
// changed and a third question was added. A show that reads the document at show time
// answers with this; a show that stored or cached the questions answers with the old set.
const designDocEdited = `# Fixture design

## 2. Open questions

- 💬 **[` + "`OQ-1`" + `](#2-open-questions) — the first question, reworded.**

  <!-- vantage: question id=OQ-1 -->

  _Leaning:_ Not decided.

- 💬 **[` + "`OQ-2`" + `](#2-open-questions) — the second question.**

  <!-- vantage: question id=OQ-2 -->

  _Leaning:_ Not decided.

- 💬 **[` + "`OQ-4`" + `](#2-open-questions) — a question added later.**

  <!-- vantage: question id=OQ-4 -->

  _Leaning:_ Not decided.

`

// designDocWithoutQuestions declares no open questions: a finished design, not a broken
// reference.
const designDocWithoutQuestions = `# Fixture design

## 1. The shape

Nothing is open.
`

// writeDoc puts a document in a scratch root and returns the root.
func writeDoc(t *testing.T, name, body string) string {
	t.Helper()
	root := scratch(t, "docs")
	// Pin the index's project root here rather than inheriting the enclosing repo.
	if err := os.WriteFile(filepath.Join(root, ".vantage.toml"), []byte("[planning]\ninclude = [\"docs/**\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the document directory failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing the document failed: %v", err)
	}
	return root
}

// itemWithDoc applies one item that points at a design document.
func itemWithDoc(t *testing.T, db, doc string) {
	t.Helper()
	in := base("item_042", "an item with a design document")
	in.DesignDoc = doc
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", in)); code != 0 {
		t.Fatalf("applying the item exited %d: %s", code, out)
	}
}

// questionOut is the part of the item record this lane adds.
type questionOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		ItemID          string `json:"item_id"`
		QuestionsSource string `json:"questions_source"`
		QuestionsStatus string `json:"questions_status"`
		QuestionsReason string `json:"questions_reason"`
		Questions       []struct {
			QuestionID string `json:"question_id"`
			Text       string `json:"text"`
			State      string `json:"state"`
		} `json:"questions"`
		Rule string `json:"rule"`
	} `json:"payload"`
}

// questionsStub writes an executable Vantage index stub that parses
// <!-- vantage: question id=... --> directives under docs/ without uvx.
func questionsStub(t *testing.T) string {
	t.Helper()
	script := filepath.Join(scratch(t, "questions-stub"), "index-command")
	body := `#!/bin/sh
exec python3 - <<'PY'
import json, os, re

docs = []
if os.path.isdir("docs"):
    for dirpath, _, filenames in os.walk("docs"):
        for name in sorted(filenames):
            if not name.endswith(".md"):
                continue
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, ".").replace(os.sep, "/")
            questions = []
            bullet = ""
            with open(full, "r", encoding="utf-8") as f:
                for line in f:
                    m_b = re.match(r"^\s*(?:-|\d+\.)\s+(?:💬\s+)?(.*)$", line)
                    if m_b:
                        bullet = m_b.group(1).strip()
                        continue
                    m_q = re.search(r"<!--\s*vantage:\s*question\s+id=([^\s]+)\s*-->", line)
                    if m_q:
                        title = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", bullet)
                        title = title.replace("**", "").replace(chr(96), "").strip()
                        questions.append({"id": m_q.group(1), "state": "open", "title": title})
            docs.append({"path": rel, "questions": questions})
print(json.dumps({"index": {"documents": docs}}))
PY
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("writing questions stub failed: %v", err)
	}
	return script
}

// show reads one item back with the document root named explicitly.
func show(t *testing.T, db, root, id string) (int, []byte) {
	t.Helper()
	return run(t, "show", "--store", db, "--item", id, "--root", root, "--questions-cmd", questionsStub(t))
}

// The document's open questions are surfaced with their identifiers and text, and the
// document is named as their source (design §5.3).
func TestShowSurfacesTheDesignDocsOpenQuestions(t *testing.T) {
	root := writeDoc(t, "docs/design/fixture-design.md", designDoc)
	db := filepath.Join(scratch(t, "questions"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/fixture-design.md")
	code, out := show(t, db, root, "item_042")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	var got questionOut
	decodeAs(t, out, &got)
	if got.Payload.QuestionsSource != "docs/design/fixture-design.md" {
		t.Fatalf("questions_source is %q", got.Payload.QuestionsSource)
	}
	if len(got.Payload.Questions) != 2 {
		t.Fatalf("show surfaced %d questions, want 2", len(got.Payload.Questions))
	}
	if got.Payload.Questions[0].QuestionID != "OQ-1" || got.Payload.Questions[0].Text != "OQ-1 — the first question." {
		t.Fatalf("the first question is %q / %q", got.Payload.Questions[0].QuestionID, got.Payload.Questions[0].Text)
	}
	if got.Payload.Questions[1].QuestionID != "OQ-2" || got.Payload.Questions[1].Text != "OQ-2 — the second question." {
		t.Fatalf("the second question is %q / %q", got.Payload.Questions[1].QuestionID, got.Payload.Questions[1].Text)
	}
}

// designDocIdentifierFirst uses the identifier-first title Vantage recommends.
// Show must echo the index's title rather than interpret Markdown itself.
const designDocIdentifierFirst = `# Fixture design

## 1. Open questions

- 💬 **OQ-1: Is the first question read?**

  <!-- vantage: question id=OQ-1 -->

  _Leaning:_ Not decided.

- 💬 **OQ-2: And the second one, with a [link](#1-open-questions) in it?**

  <!-- vantage: question id=OQ-2 -->

  _Leaning:_ Not decided.

`

// The item's text is the index's full title, including its identifier.
func TestShowReadsTheIdentifierFirstDeclarationForm(t *testing.T) {
	root := writeDoc(t, "docs/design/identifier-first.md", designDocIdentifierFirst)
	db := filepath.Join(scratch(t, "questions-identifier-first"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/identifier-first.md")
	code, out := show(t, db, root, "item_042")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	var got questionOut
	decodeAs(t, out, &got)
	if len(got.Payload.Questions) != 2 {
		t.Fatalf("show surfaced %d questions, want 2", len(got.Payload.Questions))
	}
	if got.Payload.Questions[0].QuestionID != "OQ-1" || got.Payload.Questions[0].Text != "OQ-1: Is the first question read?" {
		t.Fatalf("the first question is %q / %q", got.Payload.Questions[0].QuestionID, got.Payload.Questions[0].Text)
	}
	if got.Payload.Questions[1].QuestionID != "OQ-2" || got.Payload.Questions[1].Text != "OQ-2: And the second one, with a link in it?" {
		t.Fatalf("the second question is %q / %q", got.Payload.Questions[1].QuestionID, got.Payload.Questions[1].Text)
	}
}

// The questions are read at show time: the document is edited between two shows and the
// second show answers with the edit. This is the whole point of not storing them.
func TestShowReadsTheDocumentAtShowTime(t *testing.T) {
	root := writeDoc(t, "docs/design/fixture-design.md", designDoc)
	db := filepath.Join(scratch(t, "questions-live"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/fixture-design.md")
	if code, out := show(t, db, root, "item_042"); code != 0 {
		t.Fatalf("the first show exited %d: %s", code, out)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/design/fixture-design.md"), []byte(designDocEdited), 0o644); err != nil {
		t.Fatalf("editing the document failed: %v", err)
	}
	code, out := show(t, db, root, "item_042")
	if code != 0 {
		t.Fatalf("the second show exited %d: %s", code, out)
	}
	var got questionOut
	decodeAs(t, out, &got)
	if len(got.Payload.Questions) != 3 {
		t.Fatalf("the second show surfaced %d questions, want 3", len(got.Payload.Questions))
	}
	if got.Payload.Questions[0].Text != "OQ-1 — the first question, reworded." {
		t.Fatalf("the first question reads %q after the edit", got.Payload.Questions[0].Text)
	}
}

// Nothing about the questions is stored: no table, no event, no cache. A store that
// grew a questions table would be the second copy design §5.3 forbids.
func TestShowStoresNoQuestionState(t *testing.T) {
	root := writeDoc(t, "docs/design/fixture-design.md", designDoc)
	db := filepath.Join(scratch(t, "questions-store"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/fixture-design.md")
	for i := 0; i < 2; i++ {
		if code, out := show(t, db, root, "item_042"); code != 0 {
			t.Fatalf("show %d exited %d: %s", i, code, out)
		}
	}
	s, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	if _, ok := counts["questions"]; ok {
		t.Fatal("the store grew a questions table")
	}
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	for _, e := range events {
		if strings.HasPrefix(e.Kind, "question.") {
			t.Fatalf("show appended a %q event", e.Kind)
		}
	}
}

// An item with no design document surfaces no questions and is not an error: the
// reference is optional.
func TestShowOfAnItemWithNoDesignDocSurfacesNoQuestions(t *testing.T) {
	db := filepath.Join(scratch(t, "questions-none"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "no document"))); code != 0 {
		t.Fatalf("applying the item exited %d: %s", code, out)
	}
	code, out := show(t, db, scratch(t, "root"), "item_042")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	var got questionOut
	decodeAs(t, out, &got)
	if len(got.Payload.Questions) != 0 || got.Payload.QuestionsSource != "" {
		t.Fatalf("show surfaced %d questions from %q", len(got.Payload.Questions), got.Payload.QuestionsSource)
	}
}

// A document that declares no open questions is a finished design, not a broken
// reference: exit 0 with an empty list.
func TestShowOfADocumentWithNoOpenQuestionsExitsZero(t *testing.T) {
	root := writeDoc(t, "docs/design/finished.md", designDocWithoutQuestions)
	db := filepath.Join(scratch(t, "questions-finished"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/finished.md")
	code, out := show(t, db, root, "item_042")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	var got questionOut
	decodeAs(t, out, &got)
	if got.Payload.QuestionsStatus != "known" || got.Payload.QuestionsReason != "" || len(got.Payload.Questions) != 0 {
		t.Fatalf("show did not report known empty questions: %s", out)
	}
	if got.Payload.QuestionsSource != "docs/design/finished.md" {
		t.Fatalf("questions_source is %q", got.Payload.QuestionsSource)
	}
}

// A reference to a document that is not there is a negative decision with a rule: the
// read-through cannot be trusted, so the face does not answer as if it could.
func TestShowRefusesAMissingDesignDocument(t *testing.T) {
	root := scratch(t, "root")
	db := filepath.Join(scratch(t, "questions-missing"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/absent.md")
	code, out := show(t, db, root, "item_042")
	if code != 1 {
		t.Fatalf("show exited %d, want 1: %s", code, out)
	}
	var got questionOut
	decodeAs(t, out, &got)
	if got.Kind != "item.rejection" || got.Payload.Rule != "design-doc-missing" {
		t.Fatalf("show emitted %q / rule %q", got.Kind, got.Payload.Rule)
	}
}

// A token in prose or a bullet with no directive is not an indexed question.
func TestShowReadsOnlyIndexedQuestions(t *testing.T) {
	root := writeDoc(t, "docs/design/fixture-design.md", designDoc)
	db := filepath.Join(scratch(t, "questions-section"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/fixture-design.md")
	code, out := show(t, db, root, "item_042")
	if code != 0 {
		t.Fatalf("show exited %d: %s", code, out)
	}
	var got questionOut
	decodeAs(t, out, &got)
	for _, q := range got.Payload.Questions {
		if q.QuestionID == "OQ-3" || q.QuestionID == "OQ-9" {
			t.Fatalf("show surfaced %q, which is outside the open-questions section", q.QuestionID)
		}
	}
}

// Index failures must preserve the item and distinguish unknown from no questions.
func TestShowReportsUnknownQuestionsWithoutLosingItem(t *testing.T) {
	for _, tc := range []struct{ name, body, reason string }{
		{"missing", "", "no such file"},
		{"refused", `printf '%s' '{"index":{"refused":true,"documents":[]}}'`, "refused the tree"},
		{"failed", "echo index-broken >&2; exit 7", "index-broken"},
		{"invalid", "printf '%s' 'not-json'", "invalid character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeDoc(t, "docs/design/open.md", designDoc)
			db := filepath.Join(scratch(t, "unknown"), "board.sqlite3")
			itemWithDoc(t, db, "docs/design/open.md")
			command := filepath.Join(root, "index-command")
			if tc.body != "" {
				if err := os.WriteFile(command, []byte("#!/bin/sh\n"+tc.body+"\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			code, out := run(t, "show", "--store", db, "--item", "item_042", "--root", root, "--questions-cmd", command)
			var got questionOut
			decodeAs(t, out, &got)
			if code != 0 || got.Kind != "item.record" || got.Payload.ItemID != "item_042" || got.Payload.QuestionsStatus != "unknown" || !strings.Contains(got.Payload.QuestionsReason, tc.reason) {
				t.Fatalf("show lost the item or its unknown reason: exit %d: %s", code, out)
			}
			if len(got.Payload.Questions) != 0 {
				t.Fatalf("unknown index supplied questions: %s", out)
			}
		})
	}
}

// Use the real index for the declaration the old dash-bullet parser missed.
func TestShowReadsNumberedVantageQuestion(t *testing.T) {
	root := writeDoc(t, "docs/design/numbered.md", `---
title: Numbered design
status: proposed
---
# Numbered design

## Open questions

1. 💬 **OQ-1: Should the numbered question be read?**

   <!-- vantage: question id=OQ-1 -->

   _Leaning:_ Read it.

`)
	db := filepath.Join(scratch(t, "numbered"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/numbered.md")
	code, out := show(t, db, root, "item_042")
	var got questionOut
	decodeAs(t, out, &got)
	if code != 0 || got.Payload.QuestionsStatus != "known" || len(got.Payload.Questions) != 1 || got.Payload.Questions[0].QuestionID != "OQ-1" || got.Payload.Questions[0].State != "open" {
		t.Fatalf("numbered question was not reported open: exit %d: %s", code, out)
	}
}

// An absolute document must match the relative index key even when --root is
// omitted or relative. A successful index must not erase its open question.
func TestShowReadsAbsoluteDocumentWithRelativeRoot(t *testing.T) {
	root := writeDoc(t, "docs/design/open.md", designDoc)
	db := filepath.Join(scratch(t, "absolute-document"), "board.sqlite3")
	itemWithDoc(t, db, filepath.Join(root, "docs/design/open.md"))
	command := filepath.Join(root, "index-command")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf '%s' '{\"index\":{\"documents\":[{\"path\":\"docs/design/open.md\",\"questions\":[{\"id\":\"OQ-1\",\"state\":\"open\",\"title\":\"Open question\"}]}]}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, rootArgs := range [][]string{nil, {"--root", "."}, {"--root", "docs/.."}} {
		args := []string{"show", "--store", db, "--item", "item_042", "--questions-cmd", command}
		code, out := run(t, append(args, rootArgs...)...)
		var got questionOut
		decodeAs(t, out, &got)
		if code != 0 || got.Payload.QuestionsStatus != "known" || len(got.Payload.Questions) != 1 || got.Payload.Questions[0].QuestionID != "OQ-1" {
			t.Fatalf("absolute document with %v lost its question: exit %d: %s", rootArgs, code, out)
		}
	}
}

// An override is an argv split just as in the loop, runs at root, and uses only
// answered as settled. Blocked and future states remain unanswered.
func TestShowUsesIndexCommandOverrideAndFiltersAnswered(t *testing.T) {
	root := writeDoc(t, "docs/design/open.md", designDoc)
	db := filepath.Join(scratch(t, "override"), "board.sqlite3")
	itemWithDoc(t, db, "docs/design/open.md")
	script := filepath.Join(root, "index-command")
	body := `#!/bin/sh
[ "$1" = "index" ] && [ "$2" = "--format" ] && [ "$3" = "json" ] && [ -f docs/design/open.md ] || exit 9
printf '%s' '{"index":{"documents":[{"path":"docs/design/open.md","questions":[{"id":"OQ-0","state":"answered","title":"Settled"},{"id":"OQ-1","state":"open","title":"Open"},{"id":"OQ-2","state":"blocked","title":"Blocked"},{"id":"OQ-3","state":"future","title":"Future state"}]}]}}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out := run(t, "show", "--store", db, "--item", "item_042", "--root", root, "--questions-cmd", script+" index --format json")
	var got questionOut
	decodeAs(t, out, &got)
	if code != 0 || got.Payload.QuestionsStatus != "known" || len(got.Payload.Questions) != 3 {
		t.Fatalf("override failed: exit %d: %s", code, out)
	}
	for i, want := range []struct{ id, state, title string }{{"OQ-1", "open", "Open"}, {"OQ-2", "blocked", "Blocked"}, {"OQ-3", "future", "Future state"}} {
		q := got.Payload.Questions[i]
		if q.QuestionID != want.id || q.State != want.state || q.Text != want.title {
			t.Fatalf("index's answer was changed: %+v", q)
		}
	}
}
