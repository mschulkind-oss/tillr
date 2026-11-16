package main_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

// designWithQuestions is a design document the fixture items point at. Its questions now
// come from Vantage's index, and the document is the file the tree must hold for the
// index answer to be `questions-ok`: the bullet before the open-questions section is the
// negative control for a reader that would count it as a question.
const designWithQuestions = `# A design

## 4. Something else

- **OQ-0** — this bullet is outside the open-questions section

## 5. Open questions

- **OQ-1** — does the assembly read this?
- **OQ-2** — and this one?
`

// designIdentifierFirst is the identifier-first declaration form the repository's own
// design documents use. The board renders the index's title with the identifier stripped,
// so this fixture and the index's title spell one question.
const designIdentifierFirst = `# A design

## 1. Open questions

- **OQ-1:** Is the first question read?
- **OQ-2:** And the second one, with a [link](#1-open-questions) in it?
`

// Both declaration forms answer with the question, not with the identifier. The question
// text now comes from Vantage's index, which reports the identifier-first form; the index is
// what makes the two forms one answer rather than a parser's guess.
func TestStatusReadsTheIdentifierFirstDeclarationForm(t *testing.T) {
	root, doc := designDoc(t, designIdentifierFirst)
	index := `{"index":{"documents":[{"path":"design.md","questions":[
		{"id":"OQ-1","state":"open","title":"OQ-1: Is the first question read?"},
		{"id":"OQ-2","state":"open","title":"OQ-2: And the second one, with a [link](#1-open-questions) in it?"}]}]}}`
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", doc)),
	)
	code, out := runView(t, "status", "--records", path, "--item", "item_042", "--root", root,
		"--questions-cmd", stubQuestionsIndex(t, index))
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if len(status.Payload.Questions) != 2 {
		t.Fatalf("the report carries %d open questions, want 2: %s", len(status.Payload.Questions), out)
	}
	if status.Payload.Questions[0].Text != "Is the first question read?" {
		t.Fatalf("the first question reads %q", status.Payload.Questions[0].Text)
	}
	// The text is the index's title, and the index carries the question's own markdown:
	// the board does not rewrite the source it renders.
	if status.Payload.Questions[1].Text != "And the second one, with a [link](#1-open-questions) in it?" {
		t.Fatalf("the second question reads %q", status.Payload.Questions[1].Text)
	}
}

// recordOf builds one record of a kind with the payload the test needs. The generic
// envelope helper in main_test.go carries a fixed payload, and the assembly reads the
// payload, so these tests build their own.
func recordOf(t *testing.T, kind, id string, subject *record.Subject, payload any) record.Envelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encoding the %s payload: %v", kind, err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          kind,
		ID:            id,
		Producer:      record.Producer{System: record.System, Utility: "item", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		Subject:       subject,
		Payload:       raw,
	}
}

// itemPayloadOf is an item.record payload: design §5.3's field list.
func itemPayloadOf(id, designDoc string) map[string]any {
	return map[string]any{
		"item_id":    id,
		"title":      "the item",
		"body_ref":   "body.md",
		"state":      "in-progress",
		"criteria":   []map[string]any{{"criterion_id": "c1", "text": "it works", "state": "open"}},
		"blocked_by": []string{},
		"design_doc": designDoc,
		"updated_at": "2026-11-10T12:00:00Z",
		"event_seq":  7,
	}
}

// candidatePayload is a land.candidate payload: design §5.4's field list.
func candidatePayload(id, itemID, treeSHA string) map[string]any {
	return map[string]any{
		"candidate_id": id,
		"item_id":      itemID,
		"base_ref":     "refs/heads/master",
		"base_sha":     "base_sha",
		"head_sha":     "head_" + id,
		"tree_sha":     treeSHA,
		"state":        "enqueued",
		"enqueued_at":  "2026-11-10T12:00:00Z",
	}
}

// attemptPayload is a land.attempt payload: design §5.4's field list.
func attemptPayload(id, candidateID, result string) map[string]any {
	return map[string]any{
		"attempt_id":   id,
		"candidate_id": candidateID,
		"verdict_id":   "verdict_" + id,
		"result":       result,
		"reason":       "verdict-failed",
		"trunk_before": "trunk_sha",
		"trunk_after":  "trunk_sha",
	}
}

// verdictPayload is a gate.verdict payload: design §5.7's field list.
func verdictPayload(id, treeSHA, result string) map[string]any {
	return map[string]any{
		"verdict_id":      id,
		"plan_id":         "plan_1",
		"registry_digest": "digest",
		"base_sha":        "base_sha",
		"tree_sha":        treeSHA,
		"result":          result,
		"checks": []map[string]any{
			{"check_id": "c1", "result": result, "duration_ms": 12, "log_ref": "log.txt"},
		},
	}
}

// runPayload is a run.record payload: design §5.6's field list.
func runPayload(id, laneID string) map[string]any {
	return map[string]any{
		"run_id":        id,
		"lane_id":       laneID,
		"backend":       "muse",
		"state":         "exited",
		"exit_code":     0,
		"started_at":    "2026-11-10T12:00:00Z",
		"finished_at":   "2026-11-10T12:00:01Z",
		"event_seq":     3,
		"artifact_refs": []map[string]any{{"kind": "log", "ref": "run.log"}},
	}
}

// runRequestPayload is a run.request payload: the only record in the stream that names
// both the item a run was dispatched for and the lane it ran on.
func runRequestPayload(runID, itemID, laneID string) map[string]any {
	return map[string]any{
		"run_id":      runID,
		"item_id":     itemID,
		"lane_id":     laneID,
		"command":     []string{"true"},
		"working_dir": ".",
		"timeout_ms":  0,
		"env_refs":    []string{},
	}
}

// designDoc writes a design document into a scratch root and answers the root and the
// document's name relative to it, which is the form an item's design_doc carries.
func designDoc(t *testing.T, body string) (root, doc string) {
	t.Helper()
	root = t.TempDir()
	doc = "design.md"
	if err := os.WriteFile(filepath.Join(root, doc), []byte(body), 0o644); err != nil {
		t.Fatalf("writing the design document: %v", err)
	}
	return root, doc
}

// assembledStatus is the part of a view.status record these tests read: the moment and
// the subject's presence, plus the assembled subject.
type assembledStatus struct {
	Kind    string `json:"kind"`
	Payload struct {
		Found *bool `json:"found"`
		Item  *struct {
			ItemID    string `json:"item_id"`
			State     string `json:"state"`
			DesignDoc string `json:"design_doc"`
			Criteria  []struct {
				CriterionID string `json:"criterion_id"`
			} `json:"criteria"`
		} `json:"item"`
		Questions []struct {
			QuestionID string `json:"question_id"`
			Text       string `json:"text"`
		} `json:"questions"`
		QuestionsSource string `json:"questions_source"`
		QuestionsState  string `json:"questions_state"`
		Items           []struct {
			ItemID   string `json:"item_id"`
			Title    string `json:"title"`
			State    string `json:"state"`
			Criteria []struct {
				CriterionID string `json:"criterion_id"`
			} `json:"criteria"`
			BlockedBy []string `json:"blocked_by"`
		} `json:"items"`
		Candidates []struct {
			Candidate struct {
				CandidateID string `json:"candidate_id"`
				TreeSHA     string `json:"tree_sha"`
			} `json:"candidate"`
			LatestAttempt *struct {
				AttemptID string `json:"attempt_id"`
				Result    string `json:"result"`
			} `json:"latest_attempt"`
			LatestVerdict *struct {
				VerdictID string `json:"verdict_id"`
				TreeSHA   string `json:"tree_sha"`
			} `json:"latest_verdict"`
		} `json:"candidates"`
		Runs []struct {
			RunID    string `json:"run_id"`
			ItemID   string `json:"item_id"`
			LaneID   string `json:"lane_id"`
			State    string `json:"state"`
			ExitCode *int   `json:"exit_code"`
		} `json:"runs"`
		Gate *struct {
			LastMS   *int64 `json:"last_ms"`
			BudgetMS *int64 `json:"budget_ms"`
			Trend    []struct {
				TickID     string `json:"tick_id"`
				GateMS     int64  `json:"gate_ms"`
				BudgetMS   int64  `json:"budget_ms"`
				OverBudget bool   `json:"over_budget"`
			} `json:"trend"`
		} `json:"gate"`
		Counts *struct {
			ByState      map[string]int `json:"by_state"`
			Dispatchable int            `json:"dispatchable"`
			Blocked      int            `json:"blocked"`
			RunsLive     int            `json:"runs_live"`
		} `json:"counts"`
	} `json:"payload"`
}

func decodeAssembled(t *testing.T, out []byte) assembledStatus {
	t.Helper()
	var status assembledStatus
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatalf("status did not emit a record: %v\n%s", err, out)
	}
	return status
}

// fieldIsNull answers whether a payload field is JSON null rather than an empty list.
// A decoded slice cannot tell the two apart, and they are different answers: null is
// "there is no such component", [] is "there is one and it is empty".
func fieldIsNull(t *testing.T, out []byte, field string) bool {
	t.Helper()
	var env struct {
		Payload map[string]json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("status did not emit a record: %v\n%s", err, out)
	}
	raw, ok := env.Payload[field]
	if !ok {
		t.Fatalf("the payload carries no %q field: %s", field, out)
	}
	return string(bytes.TrimSpace(raw)) == "null"
}

// runViewStderr runs one face and answers its exit code, stdout and stderr. A refusal
// that names what it refused says so on stderr, so a test of the refusal needs it.
func runViewStderr(t *testing.T, args ...string) (int, []byte, string) {
	t.Helper()
	cmd := exec.Command(viewBin, args...)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), out, stderr.String()
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, out, stderr.String()
}

// The full assembly: the item record, the open questions read from the design document
// it points at, its candidates each with the latest land.attempt and the latest
// gate.verdict binding its tree, and its runs.
//
// Negative control: the second candidate has neither an attempt nor a verdict, so a
// face that attached the first candidate's records to every candidate fails below.
func TestStatusAssemblesTheSubjectFromRecords(t *testing.T) {
	root, doc := designDoc(t, designWithQuestions)
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", doc)),
		recordOf(t, "land.candidate", "r-2", &record.Subject{Kind: "candidate", ID: "cand_1"},
			candidatePayload("cand_1", "item_042", "tree_1")),
		recordOf(t, "land.candidate", "r-3", &record.Subject{Kind: "candidate", ID: "cand_2"},
			candidatePayload("cand_2", "item_042", "tree_2")),
		recordOf(t, "land.attempt", "r-4", &record.Subject{Kind: "candidate", ID: "cand_1"},
			attemptPayload("attempt_1", "cand_1", "isolated")),
		recordOf(t, "gate.verdict", "r-5", &record.Subject{Kind: "candidate", ID: "cand_1"},
			verdictPayload("verdict_1", "tree_1", "fail")),
		recordOf(t, "run.request", "r-6", &record.Subject{Kind: "run", ID: "run_1"},
			runRequestPayload("run_1", "item_042", "lane_1")),
		recordOf(t, "run.record", "r-7", &record.Subject{Kind: "run", ID: "run_1"},
			runPayload("run_1", "lane_1")),
		recordOf(t, "run.record", "r-8", &record.Subject{Kind: "run", ID: "run_2"},
			runPayload("run_2", "lane_1")),
	)

	index := `{"index":{"documents":[{"path":"design.md","questions":[
		{"id":"OQ-1","state":"open","title":"OQ-1: does the assembly read this?"},
		{"id":"OQ-2","state":"open","title":"OQ-2: and this one?"}]}]}}`
	code, out := runView(t, "status", "--records", path, "--item", "item_042", "--root", root,
		"--questions-cmd", stubQuestionsIndex(t, index))
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if status.Payload.Found == nil || !*status.Payload.Found {
		t.Fatalf("a subject the stream carries reported found=%v", status.Payload.Found)
	}

	if status.Payload.Item == nil {
		t.Fatal("the report carries no item record")
	}
	if status.Payload.Item.ItemID != "item_042" || status.Payload.Item.State != "in-progress" {
		t.Fatalf("the item record is %+v", status.Payload.Item)
	}
	if len(status.Payload.Item.Criteria) != 1 {
		t.Fatalf("the item record carries %d criteria, want 1", len(status.Payload.Item.Criteria))
	}

	if len(status.Payload.Questions) != 2 {
		t.Fatalf("the report carries %d open questions, want 2: %s", len(status.Payload.Questions), out)
	}
	if status.Payload.Questions[0].QuestionID != "OQ-1" || status.Payload.Questions[1].QuestionID != "OQ-2" {
		t.Fatalf("the questions are %+v", status.Payload.Questions)
	}
	// The text is the question, not the identifier: the bold run names the question and
	// the statement follows it.
	if status.Payload.Questions[0].Text != "does the assembly read this?" {
		t.Fatalf("the first question reads %q", status.Payload.Questions[0].Text)
	}
	if status.Payload.QuestionsSource != doc {
		t.Fatalf("questions_source is %q, want %q", status.Payload.QuestionsSource, doc)
	}

	if len(status.Payload.Candidates) != 2 {
		t.Fatalf("the report carries %d candidates, want 2: %s", len(status.Payload.Candidates), out)
	}
	first, second := status.Payload.Candidates[0], status.Payload.Candidates[1]
	if first.Candidate.CandidateID != "cand_1" || second.Candidate.CandidateID != "cand_2" {
		t.Fatalf("the candidates are %q and %q, want stream order",
			first.Candidate.CandidateID, second.Candidate.CandidateID)
	}
	if first.LatestAttempt == nil || first.LatestAttempt.AttemptID != "attempt_1" {
		t.Fatalf("cand_1's latest attempt is %+v", first.LatestAttempt)
	}
	if first.LatestVerdict == nil || first.LatestVerdict.VerdictID != "verdict_1" {
		t.Fatalf("cand_1's latest verdict is %+v", first.LatestVerdict)
	}
	if second.LatestAttempt != nil || second.LatestVerdict != nil {
		t.Fatalf("cand_2 carries records it has none of: %+v %+v",
			second.LatestAttempt, second.LatestVerdict)
	}

	if len(status.Payload.Runs) != 2 {
		t.Fatalf("the report carries %d runs, want 2: %s", len(status.Payload.Runs), out)
	}
	if status.Payload.Runs[0].RunID != "run_1" || status.Payload.Runs[1].RunID != "run_2" {
		t.Fatalf("the runs are %+v", status.Payload.Runs)
	}
}

// "Latest" is stream order, not created_at: the stream is the order the records were
// emitted, and two records can share a timestamp.
//
// Negative control: the two attempts and the two verdicts in this fixture carry the
// same created_at, so a face that ordered by timestamp cannot pick the later one.
func TestStatusReportsTheLatestAttemptAndVerdict(t *testing.T) {
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", "")),
		recordOf(t, "land.candidate", "r-2", &record.Subject{Kind: "candidate", ID: "cand_1"},
			candidatePayload("cand_1", "item_042", "tree_1")),
		recordOf(t, "land.attempt", "r-3", &record.Subject{Kind: "candidate", ID: "cand_1"},
			attemptPayload("attempt_1", "cand_1", "refused")),
		recordOf(t, "land.attempt", "r-4", &record.Subject{Kind: "candidate", ID: "cand_1"},
			attemptPayload("attempt_2", "cand_1", "landed")),
		recordOf(t, "gate.verdict", "r-5", &record.Subject{Kind: "candidate", ID: "cand_1"},
			verdictPayload("verdict_1", "tree_1", "fail")),
		recordOf(t, "gate.verdict", "r-6", &record.Subject{Kind: "candidate", ID: "cand_1"},
			verdictPayload("verdict_2", "tree_1", "pass")),
	)

	code, out := runView(t, "status", "--records", path, "--item", "item_042")
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if len(status.Payload.Candidates) != 1 {
		t.Fatalf("the report carries %d candidates, want 1", len(status.Payload.Candidates))
	}
	candidate := status.Payload.Candidates[0]
	if candidate.LatestAttempt == nil || candidate.LatestAttempt.AttemptID != "attempt_2" {
		t.Fatalf("the latest attempt is %+v, want attempt_2", candidate.LatestAttempt)
	}
	if candidate.LatestVerdict == nil || candidate.LatestVerdict.VerdictID != "verdict_2" {
		t.Fatalf("the latest verdict is %+v, want verdict_2", candidate.LatestVerdict)
	}
}

// A verdict binds a tree, not a candidate id: that is what §5.7's tree_sha is for, and
// it is why a verdict can be reused across candidates with the same tree.
//
// Negative control: the second candidate's tree is the first's, so a face that keyed
// verdicts by candidate id would report null for it.
func TestStatusBindsAVerdictToATreeNotACandidate(t *testing.T) {
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", "")),
		recordOf(t, "land.candidate", "r-2", &record.Subject{Kind: "candidate", ID: "cand_1"},
			candidatePayload("cand_1", "item_042", "tree_shared")),
		recordOf(t, "land.candidate", "r-3", &record.Subject{Kind: "candidate", ID: "cand_2"},
			candidatePayload("cand_2", "item_042", "tree_shared")),
		recordOf(t, "gate.verdict", "r-4", &record.Subject{Kind: "candidate", ID: "cand_1"},
			verdictPayload("verdict_1", "tree_shared", "pass")),
	)

	code, out := runView(t, "status", "--records", path, "--item", "item_042")
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if len(status.Payload.Candidates) != 2 {
		t.Fatalf("the report carries %d candidates, want 2", len(status.Payload.Candidates))
	}
	for _, candidate := range status.Payload.Candidates {
		if candidate.LatestVerdict == nil || candidate.LatestVerdict.VerdictID != "verdict_1" {
			t.Fatalf("%s reports verdict %+v, want verdict_1",
				candidate.Candidate.CandidateID, candidate.LatestVerdict)
		}
	}
}

// An absent component is a fact about the candidate, not an error: null, and the face
// still exits 0. An item with no design_doc has no questions, and that is null too —
// not an empty list, which would be indistinguishable from a finished design.
//
// Negative control: the same face over a candidate that does have an attempt reports
// it (TestStatusAssemblesTheSubjectFromRecords), so this is not a face that never
// attaches anything.
func TestStatusReportsAbsentComponentsAsNull(t *testing.T) {
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", "")),
		recordOf(t, "land.candidate", "r-2", &record.Subject{Kind: "candidate", ID: "cand_1"},
			candidatePayload("cand_1", "item_042", "tree_1")),
	)

	code, out := runView(t, "status", "--records", path, "--item", "item_042")
	if code != 0 {
		t.Fatalf("want exit 0 for a candidate with no attempt, got %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if len(status.Payload.Candidates) != 1 {
		t.Fatalf("the report carries %d candidates, want 1", len(status.Payload.Candidates))
	}
	candidate := status.Payload.Candidates[0]
	if candidate.LatestAttempt != nil || candidate.LatestVerdict != nil {
		t.Fatalf("a candidate with no records reports %+v %+v",
			candidate.LatestAttempt, candidate.LatestVerdict)
	}
	if !fieldIsNull(t, out, "questions") {
		t.Fatalf("an item with no design_doc reports questions as a list, not null: %s", out)
	}
	if status.Payload.QuestionsSource != "" {
		t.Fatalf("questions_source is %q, want empty", status.Payload.QuestionsSource)
	}
}

// boardStore builds a store holding a board: two items, the second blocked by the first,
// one candidate with its landing attempt, and one run. The owner tables come from the
// serve face's fixture schema (testdata/serve-fixture.sql) — this module reads a store,
// so it must not link a writer to have one to read.
func boardStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating the fixture failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture failed: %v", err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		t.Fatalf("reading the fixture schema: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening the fixture to seed it: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("creating the fixture tables: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES ('item-board-01', 'the first board item', 'items/01.md', 'in-queue', '', 1000)`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES ('item-board-02', 'the second board item', 'items/02.md', 'blocked', '', 2000)`,
		`INSERT INTO item_criteria (item_id, criterion_id, text, state)
		 VALUES ('item-board-01', 'ac-1', 'it works', 'pending')`,
		`INSERT INTO item_edges (item_id, blocked_by) VALUES ('item-board-02', 'item-board-01')`,
		`INSERT INTO candidates (candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at)
		 VALUES ('cand-board-1', 'item-board-01', 'main', 'base1', 'head1', 'tree1', 'enqueued', 2500)`,
		`INSERT INTO landing_attempts (attempt_id, candidate_id, verdict_id, result, reason, trunk_before, trunk_after)
		 VALUES ('attempt-board-1', 'cand-board-1', 'verdict-1', 'landed', '', 'trunk-a', 'trunk-b')`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at)
		 VALUES ('run-board-1', 'item-board-01', 'lane-1', 'pi', 'exited', 0, 1000, 2000)`,
		`INSERT INTO drain_passes (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
		   gate_budget_ms, gate_over_budget) VALUES ('tick-status-1', 1000, 10, 20000, 71600, 50, 330000, 0)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seeding the fixture with %q: %v", stmt[:40], err)
		}
	}
	return path
}

// rankedStore builds a store holding both kinds of row: three ranked items and two the
// store never ranked. The insertion order, the id order and the rank order are three
// different orders on purpose — an order that survives any one of them is not the order
// the queue asked for. It is the same fixture the snapshot suite seeds
// (`the_board_is_ordered_by_the_queue_rank`), read through the other assembly.
func rankedStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ranked.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating the fixture failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture failed: %v", err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		t.Fatalf("reading the fixture schema: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening the fixture to seed it: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("creating the fixture tables: %v", err)
	}
	// The fixture's copy of the item DDL predates schema 5's rank column.
	for _, stmt := range []string{
		`ALTER TABLE items ADD COLUMN rank TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, rank)
		 VALUES ('item-seed-06', 'sixth', 'items/06.md', 'in-queue', '', 6000, '')`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, rank)
		 VALUES ('item-seed-02', 'second', 'items/02.md', 'in-queue', '', 2000, 'b')`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, rank)
		 VALUES ('item-seed-04', 'fourth', 'items/04.md', 'in-queue', '', 4000, 'a')`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, rank)
		 VALUES ('item-seed-08', 'eighth', 'items/08.md', 'in-queue', '', 8000, 'am')`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, rank)
		 VALUES ('item-seed-01', 'first', 'items/01.md', 'blocked', '', 1000, '')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seeding the fixture with %q: %v", stmt[:40], err)
		}
	}
	return path
}

// The board report orders ranked items first, ascending as plain strings, then the
// unranked ones by id — the same rule `serve.go` emits the snapshot in and the drain's
// planner decides in (`plan`). One rule, three readers: a board sorted by one and
// dispatched by another is a queue whose order nobody can explain.
//
// Negative control: the insertion order (06, 02, 04, 08, 01) and the plain id order are
// both different from the answer below.
func TestStatusOrdersTheBoardByRankThenUnranked(t *testing.T) {
	path := rankedStore(t)

	code, out := runView(t, "status", "--store", path)
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	want := []string{"item-seed-04", "item-seed-08", "item-seed-02", "item-seed-01", "item-seed-06"}
	got := make([]string, 0, len(status.Payload.Items))
	for _, item := range status.Payload.Items {
		got = append(got, item.ItemID)
	}
	if len(got) != len(want) {
		t.Fatalf("the board carries %v, want %v: %s", got, want, out)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the board's order is %v, want rank ascending with the unranked rows last by id %v: %s",
				got, want, out)
		}
	}
}

// countsStore builds a store whose four header figures are distinct and countable by hand:
// one working item, three the projection renders blocked (one of them by a blocker that
// has already landed), one landed item and one live run. Nothing here reads the counts
// back out of the code under test: the numbers live in the test below.
func countsStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "counts.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating the fixture failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture failed: %v", err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		t.Fatalf("reading the fixture schema: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening the fixture to seed it: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("creating the fixture tables: %v", err)
	}
	for _, stmt := range []string{
		// Working: in-queue, its candidate is still enqueued and its run is still live.
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES ('item-a', 'working', 'items/a.md', 'in-queue', '', 1000)`,
		// Blocked by an unmet blocker: item-a is in-queue, so the edge is not met.
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES ('item-b', 'blocked by an unmet blocker', 'items/b.md', 'in-queue', '', 2000)`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES ('item-c', 'landed', 'items/c.md', 'landed', '', 3000)`,
		// Blocked by a met blocker: the projection renders any blocked_by edge as blocked,
		// while the drain planner would still dispatch it. That divergence is the point.
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES ('item-d', 'blocked by a met blocker', 'items/d.md', 'in-queue', '', 4000)`,
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES ('item-e', 'the store marks it blocked', 'items/e.md', 'blocked', '', 5000)`,
		`INSERT INTO item_edges (item_id, blocked_by) VALUES ('item-b', 'item-a')`,
		`INSERT INTO item_edges (item_id, blocked_by) VALUES ('item-d', 'item-c')`,
		`INSERT INTO candidates (candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at)
		 VALUES ('cand-a', 'item-a', 'main', 'base-a', 'head-a', 'tree-a', 'enqueued', 1500)`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at)
		 VALUES ('run-a', 'item-a', 'lane-a', 'pi', 'running', NULL, 1000, 0)`,
		`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at)
		 VALUES ('run-c', 'item-c', 'lane-c', 'pi', 'exited', 0, 3000, 4000)`,
		// The item state events a real apply writes. Without them every item's history is
		// unknown rather than never-run, and the planner holds all five.
		`INSERT INTO events (ts, kind, object_type, object_id, payload)
		 VALUES (1000, 'item.record', 'item', 'item-a', '{"state":"in-queue"}')`,
		`INSERT INTO events (ts, kind, object_type, object_id, payload)
		 VALUES (2000, 'item.record', 'item', 'item-b', '{"state":"in-queue"}')`,
		`INSERT INTO events (ts, kind, object_type, object_id, payload)
		 VALUES (3000, 'item.record', 'item', 'item-c', '{"state":"landed"}')`,
		`INSERT INTO events (ts, kind, object_type, object_id, payload)
		 VALUES (4000, 'item.record', 'item', 'item-d', '{"state":"in-queue"}')`,
		`INSERT INTO events (ts, kind, object_type, object_id, payload)
		 VALUES (5000, 'item.record', 'item', 'item-e', '{"state":"blocked"}')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seeding the fixture with %q: %v", stmt[:40], err)
		}
	}
	return path
}

// The CLI answers the header's four figures for a store, and the numbers are the fixture's
// own: one working item, three the projection renders blocked, one landed, one live run,
// and one the drain planner would dispatch -- item-d, behind a landed blocker. item-a is
// not dispatchable: it has run already, so a second dispatch is a retry nobody authorized.
//
// Negative control: a subject report names one item, not the board, so it carries no
// counts.
func TestStatusCountsTheBoardFromTheStore(t *testing.T) {
	path := countsStore(t)

	code, out := runView(t, "status", "--store", path)
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if status.Payload.Counts == nil {
		t.Fatalf("the board report carries no counts: %s", out)
	}
	want := map[string]int{"working": 1, "blocked": 3, "landed": 1}
	if len(status.Payload.Counts.ByState) != len(want) {
		t.Fatalf("by_state is %v, want %v", status.Payload.Counts.ByState, want)
	}
	for state, n := range want {
		if status.Payload.Counts.ByState[state] != n {
			t.Fatalf("by_state[%s] is %d, want %d: %v", state,
				status.Payload.Counts.ByState[state], n, status.Payload.Counts.ByState)
		}
	}
	if status.Payload.Counts.Dispatchable != 1 {
		t.Fatalf("dispatchable is %d, want 1 (item-d): %s", status.Payload.Counts.Dispatchable, out)
	}
	if status.Payload.Counts.Blocked != 3 {
		t.Fatalf("blocked is %d, want 3 (item-b, item-d, item-e): %s", status.Payload.Counts.Blocked, out)
	}
	if status.Payload.Counts.RunsLive != 1 {
		t.Fatalf("runs_live is %d, want 1 (run-a): %s", status.Payload.Counts.RunsLive, out)
	}

	code, out = runView(t, "status", "--store", path, "--item", "item-a")
	if code != 0 {
		t.Fatalf("the subject report exited %d: %s", code, out)
	}
	if subject := decodeAssembled(t, out); subject.Payload.Counts != nil {
		t.Fatalf("a subject report carries the board's counts: %+v", subject.Payload.Counts)
	}
}

// A report that names no subject is a report about the board: the store holds two items,
// a candidate and a run, and every one of them is in the record. A null component here is
// indistinguishable from a store that holds nothing, which is what the face answered
// before it read its own rows (the drain acceptance's §4.8).
//
// Negative control: the same store with --item item-board-01 answers one item under
// `item` and no board list, so this is not the face reporting everything twice.
func TestStatusReportsTheBoardWhenNoSubjectIsNamed(t *testing.T) {
	path := boardStore(t)

	code, out := runView(t, "status", "--store", path)
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)

	if len(status.Payload.Items) != 2 {
		t.Fatalf("the board report carries %d items, want the store's 2: %s",
			len(status.Payload.Items), out)
	}
	first := status.Payload.Items[0]
	if first.ItemID != "item-board-01" || first.Title != "the first board item" || first.State != "in-queue" {
		t.Fatalf("the first board item is %+v", first)
	}
	if len(first.Criteria) != 1 || first.Criteria[0].CriterionID != "ac-1" {
		t.Fatalf("the first board item carries criteria %+v", first.Criteria)
	}
	second := status.Payload.Items[1]
	if len(second.BlockedBy) != 1 || second.BlockedBy[0] != "item-board-01" {
		t.Fatalf("the second board item waits on %+v", second.BlockedBy)
	}
	// One item is a subject, not a board: the singular components stay null.
	if !fieldIsNull(t, out, "item") {
		t.Fatalf("a board report answers one item: %s", out)
	}
	if !fieldIsNull(t, out, "questions") {
		t.Fatalf("a board report answers one item's questions: %s", out)
	}

	if len(status.Payload.Candidates) != 1 {
		t.Fatalf("the board report carries %d candidates, want the store's 1: %s",
			len(status.Payload.Candidates), out)
	}
	candidate := status.Payload.Candidates[0]
	if candidate.Candidate.CandidateID != "cand-board-1" {
		t.Fatalf("the board candidate is %q", candidate.Candidate.CandidateID)
	}
	if candidate.LatestAttempt == nil || candidate.LatestAttempt.AttemptID != "attempt-board-1" ||
		candidate.LatestAttempt.Result != "landed" {
		t.Fatalf("the board candidate's latest attempt is %+v", candidate.LatestAttempt)
	}

	if len(status.Payload.Runs) != 1 {
		t.Fatalf("the board report carries %d runs, want the store's 1: %s",
			len(status.Payload.Runs), out)
	}
	run := status.Payload.Runs[0]
	if run.RunID != "run-board-1" || run.State != "exited" || run.ExitCode == nil || *run.ExitCode != 0 {
		t.Fatalf("the board run is %+v", run)
	}

	// The gate's own wall time rides the board report: the tick's number and its trend.
	if status.Payload.Gate == nil {
		t.Fatalf("the board report carries no gate measurement: %s", out)
	}
	if status.Payload.Gate.LastMS == nil || *status.Payload.Gate.LastMS != 71600 {
		t.Fatalf("the board gate's last measurement is %+v, want 71600ms", status.Payload.Gate.LastMS)
	}
	if status.Payload.Gate.BudgetMS == nil || *status.Payload.Gate.BudgetMS != 330000 {
		t.Fatalf("the board gate's budget is %+v, want 330000ms", status.Payload.Gate.BudgetMS)
	}
	if len(status.Payload.Gate.Trend) != 1 || status.Payload.Gate.Trend[0].TickID != "tick-status-1" {
		t.Fatalf("the board gate's trend is %+v", status.Payload.Gate.Trend)
	}

	// The negative control: the narrower report over the same store.
	code, out = runView(t, "status", "--store", path, "--item", "item-board-01")
	if code != 0 {
		t.Fatalf("the subject report exited %d: %s", code, out)
	}
	subject := decodeAssembled(t, out)
	if subject.Payload.Item == nil || subject.Payload.Item.ItemID != "item-board-01" {
		t.Fatalf("the subject report carries item %+v", subject.Payload.Item)
	}
	if len(subject.Payload.Items) != 0 {
		t.Fatalf("a subject report repeats the board: %+v", subject.Payload.Items)
	}
	// A subject report carries no gate: the gate is a property of the tick, and one item is
	// not the tick.
	if subject.Payload.Gate != nil {
		t.Fatalf("a subject report carries the board's gate summary: %+v", subject.Payload.Gate)
	}
}

// The board report is the same shape in records mode: the stream holds two items, a
// candidate whose verdict binds its tree, and a run, and the report carries all of them.
//
// Negative control: the second item.record repeats item_042, so a face that appended
// every item.record instead of the newest per item would report three items.
func TestStatusReportsTheBoardFromARecordStream(t *testing.T) {
	stale := itemPayloadOf("item_042", "")
	stale["state"] = "ready"
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", "")),
		recordOf(t, "item.record", "r-2", &record.Subject{Kind: "item", ID: "item_043"},
			itemPayloadOf("item_043", "")),
		recordOf(t, "item.record", "r-3", &record.Subject{Kind: "item", ID: "item_042"}, stale),
		recordOf(t, "land.candidate", "r-4", &record.Subject{Kind: "candidate", ID: "cand_1"},
			candidatePayload("cand_1", "item_042", "tree_1")),
		recordOf(t, "gate.verdict", "r-5", &record.Subject{Kind: "candidate", ID: "cand_1"},
			verdictPayload("verdict_1", "tree_1", "pass")),
		recordOf(t, "run.record", "r-6", &record.Subject{Kind: "run", ID: "run_1"},
			runPayload("run_1", "lane_1")),
	)

	code, out := runView(t, "status", "--records", path)
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)

	if len(status.Payload.Items) != 2 {
		t.Fatalf("the board report carries %d items, want 2: %s", len(status.Payload.Items), out)
	}
	if status.Payload.Items[0].ItemID != "item_042" || status.Payload.Items[1].ItemID != "item_043" {
		t.Fatalf("the board items are %+v", status.Payload.Items)
	}
	if status.Payload.Items[0].State != "ready" {
		t.Fatalf("item_042 reports state %q, want its newest record's ready", status.Payload.Items[0].State)
	}
	if len(status.Payload.Candidates) != 1 {
		t.Fatalf("the board report carries %d candidates, want 1", len(status.Payload.Candidates))
	}
	if candidate := status.Payload.Candidates[0]; candidate.LatestVerdict == nil || candidate.LatestVerdict.VerdictID != "verdict_1" {
		t.Fatalf("the board candidate's verdict is %+v", status.Payload.Candidates[0].LatestVerdict)
	}
	if len(status.Payload.Runs) != 1 || status.Payload.Runs[0].RunID != "run_1" {
		t.Fatalf("the board runs are %+v", status.Payload.Runs)
	}
}

// A subject the stream does not carry is a negative decision, not an error: exit 1,
// and the record is still emitted, because a caller that gets exit 1 and no record
// cannot tell "absent" from "crashed".
//
// Negative control: the same file with --item item_042 exits 0.
func TestStatusExitsOneForASubjectTheStreamDoesNotCarry(t *testing.T) {
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", "")),
	)

	code, out := runView(t, "status", "--records", path, "--item", "item_999")
	if code != 1 {
		t.Fatalf("want exit 1, got %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if status.Kind != "view.status" {
		t.Fatalf("the negative answer emitted kind %q", status.Kind)
	}
	if status.Payload.Found == nil || *status.Payload.Found {
		t.Fatalf("an absent subject reported found=%v", status.Payload.Found)
	}
	if status.Payload.Item != nil {
		t.Fatalf("an absent subject carries an item record: %+v", status.Payload.Item)
	}

	if code, out := runView(t, "status", "--records", path, "--item", "item_042"); code != 0 {
		t.Fatalf("want exit 0 for a subject the stream carries, got %d: %s", code, out)
	}
}

// A design_doc that names a document which is not there is `design-doc-missing`, not an
// empty list: an empty list is indistinguishable from a finished design, and the two need
// different remedies. The state is the answer, and a report that omits it would read as
// "no questions".
//
// Negative control: the same item with the document present and an index that answers is
// `questions-ok`.
func TestStatusReportsAMissingDesignDocument(t *testing.T) {
	root := t.TempDir()
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", "design/absent.md")),
	)

	code, out := runView(t, "status", "--records", path, "--item", "item_042", "--root", root)
	if code != 0 {
		t.Fatalf("want exit 0 for a missing design document, got %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if status.Payload.QuestionsState != "design-doc-missing" {
		t.Fatalf("questions_state is %q, want design-doc-missing: %s", status.Payload.QuestionsState, out)
	}
	if status.Payload.Questions != nil {
		t.Fatalf("a missing design document answered %d questions; want null: %s", len(status.Payload.Questions), out)
	}

	if err := os.MkdirAll(filepath.Join(root, "design"), 0o755); err != nil {
		t.Fatalf("creating the design directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "design", "absent.md"), []byte(designWithQuestions), 0o644); err != nil {
		t.Fatalf("writing the design document: %v", err)
	}
	index := `{"index":{"documents":[{"path":"design/absent.md","questions":[]}]}}`
	code, out = runView(t, "status", "--records", path, "--item", "item_042", "--root", root,
		"--questions-cmd", stubQuestionsIndex(t, index))
	if code != 0 {
		t.Fatalf("want exit 0 with the document present, got %d: %s", code, out)
	}
	status = decodeAssembled(t, out)
	if status.Payload.QuestionsState != "questions-ok" {
		t.Fatalf("questions_state is %q, want questions-ok: %s", status.Payload.QuestionsState, out)
	}
}

// A document that is in the tree and an index that cannot answer is `questions-unknown`,
// never "no questions": the report says so and carries a null list. The tool being absent
// and the tool failing are the same answer for the same reason.
//
// Negative control: the same item with an index that answers is questions-ok with a
// question list (TestStatusAssemblesTheSubjectFromRecords).
func TestStatusReportsUnknownQuestionsWhenTheIndexCannotAnswer(t *testing.T) {
	for name, command := range map[string]string{
		"absent": "definitely-not-a-tool-7f3a",
		"fails":  "false",
	} {
		t.Run(name, func(t *testing.T) {
			root, doc := designDoc(t, designWithQuestions)
			path := recordsFile(t,
				recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
					itemPayloadOf("item_042", doc)),
			)
			code, out := runView(t, "status", "--records", path, "--item", "item_042", "--root", root,
				"--questions-cmd", command)
			if code != 0 {
				t.Fatalf("status exited %d: %s", code, out)
			}
			status := decodeAssembled(t, out)
			if status.Payload.QuestionsState != "questions-unknown" {
				t.Fatalf("questions_state is %q, want questions-unknown: %s", status.Payload.QuestionsState, out)
			}
			if status.Payload.Questions != nil {
				t.Fatalf("an unreadable index answered %d questions; want null: %s", len(status.Payload.Questions), out)
			}
		})
	}
}

// The open questions are read from the document, not from the record: the board stores
// no question state, so the document is the only copy. A record whose payload carries
// a stale list must report the document's current questions.
//
// Negative control: the stale list is in the payload, so a face that copied it fails.
func TestStatusReadsQuestionsFromTheDocumentNotTheRecord(t *testing.T) {
	root, doc := designDoc(t, designWithQuestions)
	payload := itemPayloadOf("item_042", doc)
	payload["questions"] = []map[string]any{{"question_id": "OQ-STALE", "text": "a stale question"}}
	payload["questions_source"] = "somewhere/else.md"

	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"}, payload),
	)

	index := `{"index":{"documents":[{"path":"design.md","questions":[
		{"id":"OQ-1","state":"open","title":"OQ-1: does the assembly read this?"},
		{"id":"OQ-2","state":"open","title":"OQ-2: and this one?"}]}]}}`
	code, out := runView(t, "status", "--records", path, "--item", "item_042", "--root", root,
		"--questions-cmd", stubQuestionsIndex(t, index))
	if code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}
	status := decodeAssembled(t, out)
	if len(status.Payload.Questions) != 2 {
		t.Fatalf("the report carries %d questions, want the index's 2: %s",
			len(status.Payload.Questions), out)
	}
	for _, question := range status.Payload.Questions {
		if question.QuestionID == "OQ-STALE" {
			t.Fatalf("the report copied the record's stale question: %+v", status.Payload.Questions)
		}
	}
	if status.Payload.QuestionsSource != doc {
		t.Fatalf("questions_source is %q, want the document %q", status.Payload.QuestionsSource, doc)
	}
}

// view writes nothing: it is safe to point at a live source while writers run, so the
// records file is byte-identical after the run.
//
// Negative control: the same file read twice answers the same bytes, so a face that
// rewrote it in place would fail the comparison.
func TestStatusWritesNothing(t *testing.T) {
	root, doc := designDoc(t, designWithQuestions)
	path := recordsFile(t,
		recordOf(t, "item.record", "r-1", &record.Subject{Kind: "item", ID: "item_042"},
			itemPayloadOf("item_042", doc)),
		recordOf(t, "land.candidate", "r-2", &record.Subject{Kind: "candidate", ID: "cand_1"},
			candidatePayload("cand_1", "item_042", "tree_1")),
	)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the records file: %v", err)
	}

	if code, out := runView(t, "status", "--records", path, "--item", "item_042", "--root", root); code != 0 {
		t.Fatalf("status exited %d: %s", code, out)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-reading the records file: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the records file changed: %d bytes before, %d after", len(before), len(after))
	}
}
