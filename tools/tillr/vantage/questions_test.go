package vantage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
func TestWithoutQuestionsKeepsUnexcludedQuestionsInOrder(t *testing.T) {
	questions := []Question{{ID: "OQ-3"}, {ID: "OQ-2"}, {ID: "OQ-1"}}
	got := WithoutQuestions(questions, []string{"OQ-2", "absent"})
	if len(got) != 2 || got[0].ID != "OQ-3" || got[1].ID != "OQ-1" {
		t.Fatalf("unexpected filtered questions: %+v", got)
	}
}

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

// The index's repository-relative keys also answer absolute references when the
// caller uses a relative root, including the empty root's current-directory default.
func TestVantageQuestionsResolvesAbsoluteDocumentAgainstRelativeRoot(t *testing.T) {
	root := t.TempDir()
	writeDocument(t, root, "docs/design/open.md")
	t.Chdir(root)
	index := ParseIndex([]byte(`{"index":{"documents":[{"path":"docs/design/open.md","questions":[{"id":"OQ-1","state":"open"}]}]}}`), "fixture")
	for _, relativeRoot := range []string{"", ".", "docs/.."} {
		answer, err := Preloaded(relativeRoot, index).Document(filepath.Join(root, "docs/design/open.md"))
		if err != nil || !answer.Present || len(answer.Questions) != 1 || answer.Questions[0].ID != "OQ-1" {
			t.Fatalf("root %q lost the absolute document's question: %+v / %v", relativeRoot, answer, err)
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

func allowDefaultIndexForTest(t *testing.T) {
	t.Helper()
	allowDefaultIndexInTest = true
	t.Cleanup(func() { allowDefaultIndexInTest = false })
}

func TestRealQuestionIndexReaderRefusesToRunUnderGoTest(t *testing.T) {
	root := t.TempDir()
	_, err := ReadVantageIndex(context.Background(), root, nil).Questions("docs/open.md")
	if err == nil || !strings.Contains(err.Error(), "refused under go test") {
		t.Fatalf("expected refusal under go test, got: %v", err)
	}
	if _, err := RunIndexCommand(context.Background(), root, []string{"uvx", "vantage-check", "index", "--format", "json"}); err == nil || !strings.Contains(err.Error(), "refused under go test") {
		t.Fatalf("expected refusal for explicit uvx under go test, got: %v", err)
	}
}

func TestIndexDefaultUsesUvxWithoutAStandaloneChecker(t *testing.T) {
	allowDefaultIndexForTest(t)
	root := t.TempDir()
	record := filepath.Join(root, "arguments")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$INDEX_ARGUMENTS\"\nprintf '%s\\n' '{\"index\":{\"documents\":[{\"path\":\"docs/open.md\",\"questions\":[{\"id\":\"OQ-1\",\"state\":\"open\"}]}]}}'\n"
	if err := os.WriteFile(filepath.Join(root, "uvx"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("INDEX_ARGUMENTS", record)
	index := ReadVantageIndex(context.Background(), root, nil)
	questions, err := index.Questions("docs/open.md")
	if err != nil || len(questions) != 1 || questions[0].ID != "OQ-1" {
		t.Fatalf("default index lost the open question: %v / %v", questions, err)
	}
	args, err := os.ReadFile(record)
	if err != nil || string(args) != "vantage-check\nindex\n--format\njson\n" {
		t.Fatalf("package runner arguments = %q / %v", args, err)
	}
}

func TestIndexOverrideDoesNotRequireUvx(t *testing.T) {
	root := t.TempDir()
	stub := filepath.Join(root, "custom-index")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n[ \"$1\" = custom ] || exit 9\nprintf '%s\\n' '{\"index\":{\"documents\":[]}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	if _, err := ReadVantageIndex(context.Background(), root, []string{stub, "custom"}).Questions("docs/open.md"); err != nil {
		t.Fatalf("explicit command depended on the default: %v", err)
	}
}

func TestMissingUvxDoesNotFallBackToAStandaloneChecker(t *testing.T) {
	allowDefaultIndexForTest(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "vantage-check"), []byte("#!/bin/sh\nprintf '%s\\n' '{\"index\":{\"documents\":[]}}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	_, err := ReadVantageIndex(context.Background(), root, nil).Questions("docs/open.md")
	if err == nil || !strings.Contains(err.Error(), "uvx vantage-check index --format json") {
		t.Fatalf("missing package runner did not report the intended command: %v", err)
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

func TestDocumentLedgerIDs(t *testing.T) {
	v := VantageQuestions{Root: t.TempDir(), Command: []string{"printf", "%s", `{"index":{"documents":[{"path":"ruled.md","questions":[],"ledgerIds":["ruled-1"]},{"path":"plain.md","questions":[]}]}}`}}
	for _, name := range []string{"ruled.md", "plain.md"} {
		if err := os.WriteFile(filepath.Join(v.Root, name), []byte("# Design\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ruled, err := v.Document("ruled.md")
	if err != nil || !ruled.Present || len(ruled.LedgerIDs) != 1 || ruled.LedgerIDs[0] != "ruled-1" {
		t.Fatalf("ruled: %+v %v", ruled, err)
	}
	plain, err := v.Document("plain.md")
	if err != nil || !plain.Present || len(plain.LedgerIDs) != 0 {
		t.Fatalf("plain: %+v %v", plain, err)
	}
}

// A blocked question's blockedOn is a list of objects in vantage-check 0.12, not strings.
// The reader keeps each blocker as one readable string, whichever shape the index used.
func TestParseVantageIndexReadsBlockedOnInEveryShape(t *testing.T) {
	document := `{"index":{"documents":[{"path":"docs/a.md","questions":[
		{"id":"Q1","state":"blocked","blockedOn":[
			"legacy-string",
			{"kind":"external","name":"owner-ready-to-run-a-round"},
			{"kind":"target","raw":"./tillr-storage.md#OQ-TS1","target":"docs/design/tillr-storage.md","fragment":"OQ-TS1"},
			{"kind":"target","target":"docs/b.md","fragment":"Q2"},
			{"kind":"future","extra":1}]},
		{"id":"Q2","state":"open"}]}]}}`
	index, err := parseVantageIndex([]byte(document))
	if err != nil {
		t.Fatalf("a blocked question's index was refused: %v", err)
	}
	questions := index.documents["docs/a.md"]
	want := []string{"legacy-string", "owner-ready-to-run-a-round", "./tillr-storage.md#OQ-TS1", "docs/b.md#Q2", `{"kind":"future","extra":1}`}
	if len(questions) != 2 || strings.Join(questions[0].BlockedOn, "|") != strings.Join(want, "|") {
		t.Fatalf("blockedOn = %q, want %q", questions[0].BlockedOn, want)
	}
}

func TestVantageIndexStageRolesAndMetadata(t *testing.T) {
	raw := `{
		"index": {
			"config": {
				"stages": {
					"IDEA": "open",
					"APPROVED": "ready",
					"FINISHED": "built",
					"ARCHIVED": "done"
				}
			},
			"documents": [
				{"path": "docs/design/sketch.md", "status": "draft", "stage": "idea", "questions": []},
				{"path": "docs/design/decided.md", "status": "accepted", "stage": "APPROVED", "questions": []},
				{"path": "docs/design/built.md", "status": "accepted", "stage": "FINISHED", "questions": []},
				{"path": "docs/design/done.md", "status": "accepted", "stage": "ARCHIVED", "questions": []},
				{"path": "docs/design/unstaged.md", "status": "draft", "stage": "", "questions": []}
			]
		}
	}`
	index, err := parseVantageIndex([]byte(raw))
	if err != nil {
		t.Fatalf("parseVantageIndex: %v", err)
	}
	cases := []struct {
		path   string
		role   string
		status string
		done   bool
	}{
		{"docs/design/sketch.md", StageRoleOpen, "draft", false},
		{"docs/design/decided.md", StageRoleReady, "accepted", false},
		{"docs/design/built.md", StageRoleBuilt, "accepted", false},
		{"docs/design/done.md", StageRoleDone, "accepted", true},
		{"docs/design/unstaged.md", "", "draft", false},
		{"docs/design/unlisted.md", "", "", false},
	}
	for _, tc := range cases {
		role, err := index.StageRole(tc.path)
		if err != nil || role != tc.role {
			t.Fatalf("StageRole(%q) = %q, %v; want %q", tc.path, role, err, tc.role)
		}
		status, err := index.Status(tc.path)
		if err != nil || status != tc.status {
			t.Fatalf("Status(%q) = %q, %v; want %q", tc.path, status, err, tc.status)
		}
		done, err := index.Done(tc.path)
		if err != nil || done != tc.done {
			t.Fatalf("Done(%q) = %v, %v; want %v", tc.path, done, err, tc.done)
		}
	}
}

func TestDesignUnansweredQuestionsIncludesOwnAndDependsOnOQFragments(t *testing.T) {
	raw := `{
		"index": {
			"config": {
				"stages": {"DESIGN": "open", "DECIDED": "ready"}
			},
			"documents": [
				{
					"path": "docs/design/upstream.md",
					"stage": "DESIGN",
					"questions": [
						{"id": "OQ-UP1", "state": "open", "title": "Upstream open"},
						{"id": "OQ-UP2", "state": "answered", "title": "Upstream answered"},
						{"id": "OQ-UP3", "state": "blocked", "title": "Upstream blocked"}
					]
				},
				{
					"path": "docs/design/child.md",
					"stage": "DESIGN",
					"dependsOn": [
						{"raw": "./upstream.md#OQ-UP1", "target": "docs/design/upstream.md", "fragment": "OQ-UP1", "line": 4},
						{"raw": "./upstream.md#OQ-UP2", "target": "docs/design/upstream.md", "fragment": "OQ-UP2", "line": 5},
						{"raw": "./upstream.md#D-UP1", "target": "docs/design/upstream.md", "fragment": "D-UP1", "line": 6},
						{"raw": "./upstream.md", "target": "docs/design/upstream.md", "fragment": "", "line": 7}
					],
					"questions": [
						{"id": "OQ-C1", "state": "open", "title": "Child open"},
						{"id": "OQ-C2", "state": "answered", "title": "Child answered"}
					]
				}
			]
		}
	}`
	index, err := parseVantageIndex([]byte(raw))
	if err != nil {
		t.Fatalf("parseVantageIndex: %v", err)
	}
	got, err := index.DesignUnansweredQuestions("docs/design/child.md")
	if err != nil {
		t.Fatalf("DesignUnansweredQuestions: %v", err)
	}
	if len(got) != 2 || got[0].ID != "OQ-C1" || got[1].ID != "OQ-UP1" {
		t.Fatalf("DesignUnansweredQuestions = %+v, want [OQ-C1 OQ-UP1]", got)
	}
	deps, err := index.DependsOn("docs/design/child.md")
	if err != nil || len(deps) != 4 {
		t.Fatalf("DependsOn = %+v, %v", deps, err)
	}

	root := t.TempDir()
	writeDocument(t, root, "docs/design/child.md")
	vq := Preloaded(root, index)
	vqRole, err := vq.StageRole("docs/design/child.md")
	if err != nil || vqRole != StageRoleOpen {
		t.Fatalf("VantageQuestions.StageRole = %q, %v", vqRole, err)
	}
	vqUnanswered, err := vq.DesignUnansweredQuestions("docs/design/child.md")
	if err != nil || len(vqUnanswered) != 2 {
		t.Fatalf("VantageQuestions.DesignUnansweredQuestions = %+v, %v", vqUnanswered, err)
	}
}

func TestVantageIndexMethodsFailClosedOnZeroOrRefusedIndex(t *testing.T) {
	var zero VantageIndex
	if _, err := zero.StageRole("docs/a.md"); err == nil {
		t.Fatal("zero StageRole should error")
	}
	if _, err := zero.Status("docs/a.md"); err == nil {
		t.Fatal("zero Status should error")
	}
	if _, err := zero.DependsOn("docs/a.md"); err == nil {
		t.Fatal("zero DependsOn should error")
	}
	if _, err := zero.DesignUnansweredQuestions("docs/a.md"); err == nil {
		t.Fatal("zero DesignUnansweredQuestions should error")
	}

	refused := ParseIndex([]byte(`{"index":{"refused":true,"documents":[]}}`), "refused-fixture")
	vq := Preloaded(t.TempDir(), refused)
	if _, err := vq.StageRole("docs/a.md"); err == nil {
		t.Fatal("refused StageRole should error")
	}
	if _, err := vq.Status("docs/a.md"); err == nil {
		t.Fatal("refused Status should error")
	}
	if _, err := vq.DependsOn("docs/a.md"); err == nil {
		t.Fatal("refused DependsOn should error")
	}
	if _, err := vq.DesignUnansweredQuestions("docs/a.md"); err == nil {
		t.Fatal("refused DesignUnansweredQuestions should error")
	}
}

func TestVantageQuestionsLoadedIndex(t *testing.T) {
	var nilSource *VantageQuestions
	if _, ok := nilSource.LoadedIndex(); ok {
		t.Fatal("nil VantageQuestions reported a loaded index")
	}
	unread := &VantageQuestions{Root: t.TempDir(), Command: []string{"definitely-not-a-tool-7f3a"}}
	if _, ok := unread.LoadedIndex(); ok {
		t.Fatal("unread VantageQuestions reported a loaded index")
	}
	preloaded := Preloaded(t.TempDir(), ParseIndex([]byte(`{"index":{"documents":[]}}`), "preloaded-fixture"))
	idx, ok := preloaded.LoadedIndex()
	if !ok {
		t.Fatal("preloaded VantageQuestions did not report a loaded index")
	}
	if _, err := idx.StageRole("docs/a.md"); err != nil {
		t.Fatalf("preloaded index StageRole: %v", err)
	}
}
