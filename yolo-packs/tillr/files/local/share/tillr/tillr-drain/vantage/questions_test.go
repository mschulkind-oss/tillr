package vantage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An index that is not an index is refused rather than read as a tree with no documents.
// This is the rule the whole file exists for: `{"index":{}}` must never answer "no
// questions" for every item at once.
func TestParseVantageIndexRefusesADocumentThatIsNotAnIndex(t *testing.T) {
	cases := map[string]string{
		"no index section":     `{"tool":"vantage-check"}`,
		"no documents":         `{"index":{}}`,
		"null documents":       `{"index":{"documents":null}}`,
		"refused":              `{"index":{"refused":true,"documents":[]}}`,
		"not json":             `vantage-check: command failed`,
		"documents not a list": `{"index":{"documents":{}}}`,
	}
	for name, document := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseVantageIndex([]byte(document)); err == nil {
				t.Fatalf("%s was read as an index: %s", name, document)
			}
		})
	}
}

// An index nobody read answers an error, not an empty list. The zero value is the value a
// caller that forgot to read one holds, so this is the case that stops such a caller from
// dispatching on "no questions".
func TestAZeroVantageIndexAnswersUnknown(t *testing.T) {
	var index VantageIndex
	if _, err := index.Questions("docs/design/x.md"); err == nil {
		t.Fatalf("a zero index answered a document instead of refusing")
	}
}

// A document the index reports with no questions carries none, and one it does not report at
// all is the same answer: the index answered for the tree, so a document it does not list is
// one it found nothing to ask about.
func TestVantageIndexAnswersPerDocument(t *testing.T) {
	index, err := parseVantageIndex([]byte(`{"index":{"documents":[
		{"path":"docs/design/open.md","questions":[{"id":"OQ-1","state":"open"}]},
		{"path":"docs/design/done.md","questions":[]}
	]}}`))
	if err != nil {
		t.Fatalf("an index the tool would send was refused: %v", err)
	}
	if questions, err := index.Questions("docs/design/open.md"); err != nil || len(questions) != 1 {
		t.Fatalf("the open document answered %v / %v, want one question", questions, err)
	}
	// The paths are compared cleaned and slash-separated, so an item's `./docs/...` is the
	// same document as the index's `docs/...`.
	if questions, err := index.Questions("./docs/design/open.md"); err != nil || len(questions) != 1 {
		t.Fatalf("a './'-spelled document answered %v / %v, want the same one question", questions, err)
	}
	for _, path := range []string{"docs/design/done.md", "docs/design/unlisted.md"} {
		if questions, err := index.Questions(path); err != nil || len(questions) != 0 {
			t.Fatalf("%s answered %v / %v, want no questions", path, questions, err)
		}
	}
}

// FirstUnanswered reads only `answered` as settled: an open question, and a state this
// version does not know, both hold the item.
func TestFirstUnansweredTreatsEveryStateButAnsweredAsOpen(t *testing.T) {
	questions := []Question{
		{ID: "OQ-1", State: QuestionAnswered},
		{ID: "OQ-2", State: "blocked"},
		{ID: "OQ-3", State: "open"},
	}
	question, ok := FirstUnanswered(questions)
	if !ok || question.ID != "OQ-2" {
		t.Fatalf("FirstUnanswered answered %q / %v, want OQ-2", question.ID, ok)
	}
	if _, ok := FirstUnanswered([]Question{{ID: "OQ-1", State: QuestionAnswered}}); ok {
		t.Fatalf("a settled document answered an open question")
	}
}

// The tree is read before the tool, so a document that is not there is `design-doc-missing`
// even when the index command would fail: the missing file is a fact no tool can change.
func TestVantageQuestionsAnswersAMissingDocumentWithoutRunningTheTool(t *testing.T) {
	source := &VantageQuestions{
		Root:    t.TempDir(),
		Command: []string{"definitely-not-a-tool-7f3a"},
		Context: context.Background(),
	}
	answer, err := source.Document("docs/design/gone.md")
	if err != nil {
		t.Fatalf("a missing document answered an error: %v", err)
	}
	if answer.Present {
		t.Fatalf("a document that is not there was reported present")
	}
	// The tool was never run, which is what makes this answer survive its absence.
	if source.read {
		t.Fatalf("the index was read for a document that is not in the tree")
	}
}

// The tool's failure is the source's error, and the planner turns it into
// `questions-unknown` rather than into an empty question list.
func TestVantageQuestionsReportsAnUnreadableIndex(t *testing.T) {
	root := t.TempDir()
	writeDocument(t, root, "docs/design/open.md")
	source := &VantageQuestions{Root: root, Command: []string{"false"}, Context: context.Background()}
	if _, err := source.Document("docs/design/open.md"); err == nil {
		t.Fatalf("an index command that failed answered a document")
	}
}

// writeDocument writes one file under root, creating the directories it needs.
func writeDocument(t *testing.T, root, path string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating the directory for %s failed: %v", path, err)
	}
	if err := os.WriteFile(full, []byte("# the document\n"), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}
