package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// designDoc is the declaration form this lane reads: an open-questions section whose
// items are bolded, linked, em-dash-led questions. The prose in §1 and the bullet in §3
// are the negative controls the parser has to survive.
const designDoc = `---
title: "fixture design"
status: proposed
---

# Fixture design

## 1. The shape

Prose that mentions OQ-9 in passing and must not be surfaced.

## 2. Open questions

- **[` + "`OQ-1`" + `](#2-open-questions) — the first question.** Its body explains it.
- **[` + "`OQ-2`" + `](#2-open-questions) — the second question.** Its body explains it.

## 3. A later section

- **[` + "`OQ-3`" + `](#3-a-later-section) — not an open question.** This bullet is outside the
  open-questions section and must not be surfaced.
`

// designDocEdited is the same document after a design pass: the first question's text
// changed and a third question was added. A show that reads the document at show time
// answers with this; a show that stored or cached the questions answers with the old set.
const designDocEdited = `# Fixture design

## 2. Open questions

- **[` + "`OQ-1`" + `](#2-open-questions) — the first question, reworded.** Its body explains it.
- **[` + "`OQ-2`" + `](#2-open-questions) — the second question.** Its body explains it.
- **[` + "`OQ-4`" + `](#2-open-questions) — a question added later.** Its body explains it.
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
		Questions       []struct {
			QuestionID string `json:"question_id"`
			Text       string `json:"text"`
		} `json:"questions"`
		Rule string `json:"rule"`
	} `json:"payload"`
}

// show reads one item back with the document root named explicitly.
func show(t *testing.T, db, root, id string) (int, []byte) {
	t.Helper()
	return run(t, "show", "--store", db, "--item", id, "--root", root)
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
	if got.Payload.Questions[0].QuestionID != "OQ-1" || got.Payload.Questions[0].Text != "the first question." {
		t.Fatalf("the first question is %q / %q", got.Payload.Questions[0].QuestionID, got.Payload.Questions[0].Text)
	}
	if got.Payload.Questions[1].QuestionID != "OQ-2" || got.Payload.Questions[1].Text != "the second question." {
		t.Fatalf("the second question is %q / %q", got.Payload.Questions[1].QuestionID, got.Payload.Questions[1].Text)
	}
}

// designDocIdentifierFirst is the second declaration form in use: the bold run carries
// only the identifier and the question follows it. The repository's own design documents
// are written this way, so a parser that read the bold run as the text answered every
// question with its own identifier (measured 2026-11-11 against
// docs/design/board-item-state-model.md, whose items are all of that form).
const designDocIdentifierFirst = `# Fixture design

## 1. Open questions

- **OQ-1:** Is the first question read?
- **OQ-2:** And the second one, with a [link](#1-open-questions) in it?
`

// Both declaration forms answer with the question, not with the identifier.
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
	if got.Payload.Questions[0].QuestionID != "OQ-1" || got.Payload.Questions[0].Text != "Is the first question read?" {
		t.Fatalf("the first question is %q / %q", got.Payload.Questions[0].QuestionID, got.Payload.Questions[0].Text)
	}
	if got.Payload.Questions[1].QuestionID != "OQ-2" || got.Payload.Questions[1].Text != "And the second one, with a link in it?" {
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
	if got.Payload.Questions[0].Text != "the first question, reworded." {
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
	if len(got.Payload.Questions) != 0 {
		t.Fatalf("show surfaced %d questions from a finished design", len(got.Payload.Questions))
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

// Only the open-questions section is read: an OQ- token in prose and a bullet in a
// later section are not questions.
func TestShowReadsOnlyTheOpenQuestionsSection(t *testing.T) {
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
