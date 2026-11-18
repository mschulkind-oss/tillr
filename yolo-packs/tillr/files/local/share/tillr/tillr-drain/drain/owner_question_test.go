package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func ownerIndexCommand(file, id, state string) []string {
	payload := fmt.Sprintf(`{"index":{"documents":[{"path":%q,"questions":[{"id":%q,"state":%q,"title":"Which delivery should we use?"}]}]}}`, file, id, state)
	return []string{"sh", "-c", `printf '%s' "$1"`, "questions", payload}
}

const ownerChoiceReport = "Which delivery should we use?\n\nOptions:\n- A: ship the built-in copy\n- B: download upstream\nNEXT-ACTOR: owner\nRESULT: blocked\n"

type ownerQuestionSteps struct {
	recordingSteps
	t            *testing.T
	f            refusalLoopFixture
	l            *Loop
	gates, lands int
	reject       bool
	document     string
}

func (s *ownerQuestionSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	document := s.document
	if document == "" {
		document = "briefs/item-a.md"
	}
	raw, e := os.ReadFile(filepath.Join(c.Worktree, document))
	if e != nil {
		s.t.Fatal(e)
	}
	_, id, _ := strings.Cut(c.BlockConflict.QuestionRef, "#")
	if id == "" || !strings.Contains(string(raw), "vantage: question id="+id) {
		s.t.Fatal("gate saw no question")
	}
	if s.reject {
		return GateOutcome{Outcome: GateFailed}, nil
	}
	return GateOutcome{Outcome: GatePassed}, nil
}
func (s *ownerQuestionSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	runGit(s.t, s.f.root, "merge", "--ff-only", c.HeadSHA)
	return LandOutcome{Result: LandLanded}, nil
}
func (s *ownerQuestionSteps) Run(_ context.Context, r run.Request) (run.Record, string, error) {
	report := r.Command[len(r.Command)-1]
	rec := run.Record{RunID: r.RunID, ItemID: r.ItemID, LaneID: r.LaneID, Backend: r.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: s.l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: s.l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, nil
}

func TestOwnerReportQuestionGateHoldAnswerAndRelease(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	steps := &ownerQuestionSteps{t: t, f: f, l: l}
	l.cfg.Steps = steps
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	file, id, _ := strings.Cut(c.QuestionRef, "#")
	if steps.gates != 1 || steps.lands != 1 || c.Status != "question" || file != "briefs/item-a.md" || id == "" || c.AnswerHead != "" {
		t.Fatalf("question bypassed design landing: gates=%d lands=%d conflict=%+v", steps.gates, steps.lands, c)
	}
	l.cfg.QuestionsCmd = ownerTreeIndexCommand(file, id, "")
	l.questions = &VantageQuestions{Root: f.root, Command: l.cfg.QuestionsCmd}
	// Vantage's indexed questions and the planner's owner-waiting items must
	// describe exactly the same set of document/question destinations.
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, l.questions)
		if err != nil {
			return err
		}
		fromWhy := []string{}
		for _, row := range answer.Rows {
			if row.Reason == ReasonQuestionOpen {
				fromWhy = append(fromWhy, row.Mover)
				if !strings.Contains(row.Why, "Which delivery") {
					t.Errorf("question invisible: %+v", row)
				}
			}
		}
		doc, err := l.questions.Document("briefs/item-a.md")
		if err != nil {
			return err
		}
		fromIndex := []string{}
		for _, q := range doc.Questions {
			if q.State != QuestionAnswered {
				fromIndex = append(fromIndex, "owner; answer briefs/item-a.md#"+q.ID)
			}
		}
		if !reflect.DeepEqual(fromWhy, fromIndex) || answer.Summary.OwnerWaiting != len(fromIndex) {
			t.Fatalf("owner/index mismatch: %v / %v / %+v", fromWhy, fromIndex, answer.Summary)
		}
		if !strings.Contains(answer.Summary.String(), "owner-waiting 1; next [drain=0, design seat=0, operator=0]") {
			t.Fatalf("summary: %s", answer.Summary.String())
		}
		return nil
	})
	next := decisionFor(t, l, "item-a").decision
	if next.Action != ActionHold || next.Reason != ReasonQuestionOpen || next.QuestionID != id {
		t.Fatalf("not document-held: %+v", next)
	}
	raw, e := os.ReadFile(filepath.Join(f.root, "briefs/item-a.md"))
	if e != nil {
		t.Fatal(e)
	}
	body := strings.Replace(string(raw), "💬 **"+id, "✅ **"+id, 1)
	body = strings.Replace(body, "_(empty — fill in when decided)_", "Ship the built-in copy.", 1)
	writeForkFile(t, filepath.Join(f.root, "briefs/item-a.md"), body)
	runGit(t, f.root, "add", "briefs/item-a.md")
	runGit(t, f.root, "commit", "-qm", "docs: answer the delivery question")
	l.cfg.QuestionsCmd = ownerTreeIndexCommand(file, id, "")
	if _, e := l.refreshDocuments(context.Background(), &LoopResult{}); e != nil {
		t.Fatal(e)
	}
	c, _ = conflictOf(t, f, "item-a")
	if c.Status != "answered" || c.AnswerHead != runGit(t, f.root, "rev-parse", TrunkRef) || c.QuestionHead == c.AnswerHead {
		t.Fatalf("sync did not record the owner answer separately: %+v", c)
	}
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch {
		t.Fatalf("answer did not release on next pass: %+v", p.decision)
	}
	applied, _, e := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if e != nil || !applied {
		t.Fatalf("dispatch: %v %v", applied, e)
	}
	c, _ = conflictOf(t, f, "item-a")
	if c.Status != "closed" {
		t.Fatalf("answered conflict not closed: %+v", c)
	}
}

func TestOwnerQuestionDoesNotAnswerConflictBeforeGatePasses(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	steps := &ownerQuestionSteps{t: t, f: f, l: l, reject: true}
	l.cfg.Steps = steps
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	if steps.gates != 1 || steps.lands != 0 || c.Status == "answered" || c.Status == "closed" {
		t.Fatalf("red question publication answered conflict: %+v gates=%d lands=%d", c, steps.gates, steps.lands)
	}
	raw, e := os.ReadFile(filepath.Join(f.root, "briefs/item-a.md"))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(raw), "vantage: question") {
		t.Fatal("red candidate reached trunk")
	}
}

func TestOwnerQuestionMigrationPublishesOnceAndSkipsStagingReports(t *testing.T) {
	f, l, _, _ := designAnswerFixture(t)
	c, _ := conflictOf(t, f, "item-a")
	c.Status, c.WaitingOn, c.Text = "owner", "owner", ownerChoiceReport
	fixtureWrite(t, f, func(tx *store.Tx) error { return item.RecordConflict(tx, c, l.cfg.Now()) })
	steps := &ownerQuestionSteps{t: t, f: f, l: l}
	l.cfg.Steps = steps
	if e := l.migrateOwnerQuestions(context.Background(), &LoopResult{}); e != nil {
		t.Fatal(e)
	}
	c, _ = conflictOf(t, f, "item-a")
	if c.Status != "question" || c.QuestionRef == "" || c.AnswerHead != "" || steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("migration not gated: %+v %+v", c, steps)
	}
	if d := decisionFor(t, l, "item-a").decision; d.Action != ActionHold || d.Reason != ReasonQuestionOpen {
		t.Fatalf("migration did not hold the item on its question: %+v", d)
	}
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	before := eventCount(t, f.storePath)
	if e := l.migrateOwnerQuestions(context.Background(), &LoopResult{}); e != nil {
		t.Fatal(e)
	}
	if head != runGit(t, f.root, "rev-parse", TrunkRef) || before != eventCount(t, f.storePath) {
		t.Fatal("second conversion wrote something")
	}
	c.Text = "The lane's base lacks the already-landed prerequisite; restage it.\nNEXT-ACTOR: owner\nRESULT: blocked"
	c.Status, c.WaitingOn = "owner", "owner"
	fixtureWrite(t, f, func(tx *store.Tx) error { return item.RecordConflict(tx, c, l.cfg.Now()) })
	before = eventCount(t, f.storePath)
	if e := l.migrateOwnerQuestions(context.Background(), &LoopResult{}); e != nil {
		t.Fatal(e)
	}
	if before != eventCount(t, f.storePath) || steps.gates != 1 {
		t.Fatal("staging report migrated into a question")
	}
}

func TestOwnerQuestionRecoversAfterCommitBeforeCandidateRecord(t *testing.T) {
	for _, backend := range []string{"fixture", "command"} {
		t.Run(backend, func(t *testing.T) {
			f, l, d, base := designAnswerFixture(t)
			original := *d.BlockConflict

			d.BlockConflict = &original
			request := *d.RunRequest
			request.Backend = backend
			d.RunRequest = &request
			before := original
			wrote, err := l.prepareOwnerQuestion(context.Background(), &d, ownerChoiceReport)
			if err != nil || !wrote {
				t.Fatalf("prepare: %v %v", wrote, err)
			}
			owned := runGit(t, l.laneTarget(d.LaneID), "rev-parse", "HEAD")
			expectedRef := d.BlockConflict.QuestionRef
			// The process died before its in-memory conflict or candidate was persisted.
			d.BlockConflict = &before
			steps := &ownerQuestionSteps{t: t, f: f, l: l}
			l.cfg.Steps = steps
			c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
			if c.QuestionRef != expectedRef || c.QuestionRunID != d.RunRequest.RunID || c.QuestionHead != owned || c.AnswerHead != "" || c.DesignRunID != d.RunRequest.RunID || !strings.Contains(c.Text, "Owner question: "+expectedRef) {
				t.Fatalf("recovered commit lost provenance: %+v", c)
			}
			if steps.gates != 1 || steps.lands != 1 || runGit(t, f.root, "rev-parse", TrunkRef) != owned {
				t.Fatal("recovery authored or gated another question commit")
			}
		})
	}
}

func TestOwnerQuestionCommitsAnInterruptedUncommittedDocument(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	path := filepath.Join(l.laneTarget(d.LaneID), "briefs/item-a.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := item.OwnerQuestion(string(raw), d.ItemID, d.RunRequest.RunID, ownerChoiceReport, l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, body) // Previous settlement stopped after writing, before committing.
	steps := &ownerQuestionSteps{t: t, f: f, l: l}
	l.cfg.Steps = steps
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	if c.Status != "question" || c.QuestionRef == "" || c.QuestionHead == base || steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("uncommitted publication not recovered: %+v %+v", c, steps)
	}
}

func TestUncommittedOwnerAnswerDoesNotChangeTheConflict(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	steps := &ownerQuestionSteps{t: t, f: f, l: l}
	l.cfg.Steps = steps
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	file, id, _ := strings.Cut(c.QuestionRef, "#")
	path := filepath.Join(f.root, file)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, strings.Replace(string(raw), "💬 **"+id, "✅ **"+id, 1))
	count := filepath.Join(t.TempDir(), "calls")
	l.cfg.QuestionsCmd = ownerTreeIndexCommand(file, id, count)
	l.questions = &VantageQuestions{Root: f.root, Command: ownerTreeIndexCommand(file, id, "")}
	doc, err := l.questions.Document(file)
	if err != nil || len(doc.Questions) != 1 || doc.Questions[0].State != QuestionAnswered {
		t.Fatalf("checkout index: %+v %v", doc, err)
	}
	before := eventCount(t, f.storePath)
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			writeForkFile(t, path, string(raw))
		}
		if err := l.settleOwnerAnswers(context.Background(), runGit(t, f.root, "rev-parse", TrunkRef)); err != nil {
			t.Fatal(err)
		}
		after, _ := conflictOf(t, f, d.ItemID)
		if after.Status != "question" || after.AnswerHead != "" || eventCount(t, f.storePath) != before {
			t.Fatalf("pass %d recorded checkout answer: %+v", pass, after)
		}
		if d := decisionFor(t, l, d.ItemID).decision; d.Action != ActionHold || d.Reason != ReasonQuestionOpen {
			t.Fatalf("not held: %+v", d)
		}
	}
	if calls, err := os.ReadFile(count); err != nil || string(calls) != "call\n" {
		t.Fatalf("index calls: %q %v", calls, err)
	}
}

func TestOwnerQuestionUsesTheDocumentAndBlockTriageJustCreated(t *testing.T) {
	f, l := triageFixture(t)
	d := findTriagePlan(t, l, "item-request").decision
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	target := l.laneTarget(d.LaneID)
	runGit(t, target, "reset", "--hard", base)
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "docs/request.md\n")
	writeForkFile(t, filepath.Join(target, "docs/request.md"), triageBlock+strings.ReplaceAll(triageBlock, "item-declared", "item-request"))
	steps := &ownerQuestionSteps{t: t, f: f, l: l, document: "docs/request.md"}
	l.cfg.Steps = steps
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	file, id, _ := strings.Cut(c.QuestionRef, "#")
	if c.Status != "question" || file != "docs/request.md" || id == "" || c.AnswerHead != "" || steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("triage question not published: %+v %+v", c, steps)
	}
	l.questions = &VantageQuestions{Root: f.root, Command: ownerIndexCommand(file, id, "open")}
	next := findTriagePlan(t, l, "item-request").decision
	if next.Reason != ReasonQuestionOpen || next.QuestionID != id {
		t.Fatalf("triaged request not question-held: %+v", next)
	}
}

func TestOwnerDecisionWithNoDocumentStaysAConflictAndNamesTheMissingHome(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	d.BlockConflict.BodyRef = ""
	var output bytes.Buffer
	l.cfg.Report = &output
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	if c.Status != "owner" || c.BodyRef != "" {
		t.Fatalf("invented a document: %+v", c)
	}
	if !strings.Contains(output.String(), "item-a has no document") {
		t.Fatalf("report lost item: %s", output.String())
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		row := answer.Rows[0]
		if !strings.Contains(row.Why, "no document exists") || !strings.HasPrefix(row.Mover, "operator;") || answer.Summary.OwnerWaiting != 0 || answer.Summary.OperatorNext != 1 {
			t.Fatalf("unanswerable owner wait: %+v %+v", row, answer.Summary)
		}
		return nil
	})
	before := eventCount(t, f.storePath)
	if err := l.migrateOwnerQuestions(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if eventCount(t, f.storePath) != before || !strings.Contains(output.String(), "item-a unchanged: no document exists") {
		t.Fatalf("undocumented conflict migrated: %s", output.String())
	}
}

func TestAPublishedQuestionHoldsOnlyItsOwnItem(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	steps := &ownerQuestionSteps{t: t, f: f, l: l}
	l.cfg.Steps = steps
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	file, id, _ := strings.Cut(c.QuestionRef, "#")
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, s, "item-b")
	s.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue', design_doc=? WHERE item_id IN ('item-a','item-b')`, file)
		return err
	})
	l.questions = &VantageQuestions{Root: f.root, Command: ownerIndexCommand(file, id, "open")}
	if own := decisionFor(t, l, "item-a").decision; own.Action != ActionHold || own.Reason != ReasonQuestionOpen || own.QuestionID != id {
		t.Fatalf("the question's own item is not held on it: %+v", own)
	}
	if other := decisionFor(t, l, "item-b").decision; other.Action != ActionDispatch {
		t.Fatalf("a question published for item-a held item-b: %+v", other)
	}
	l.questions = &VantageQuestions{Root: f.root, Command: ownerIndexCommand(file, "OQ-1", "open")}
	if other := decisionFor(t, l, "item-b").decision; other.Action != ActionHold || other.Reason != ReasonQuestionOpen || other.QuestionID != "OQ-1" {
		t.Fatalf("the document's own question no longer holds item-b: %+v", other)
	}
}

func TestOwnerWaitingEqualsCommittedIndex(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	l.cfg.Steps = &ownerQuestionSteps{t: t, f: f, l: l}
	c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
	file, id, _ := strings.Cut(c.QuestionRef, "#")
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, s, "item-b")
	s.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue', design_doc=? WHERE item_id IN ('item-a','item-b')`, file)
		return err
	})
	l.cfg.QuestionsCmd = ownerTreeIndexCommand(file, id, "")
	check := func(wantOpen bool) {
		t.Helper()
		tree := t.TempDir()
		archive := filepath.Join(t.TempDir(), "trunk.tar")
		runGit(t, f.root, "archive", "--output="+archive, TrunkRef)
		if out, err := exec.Command("tar", "-xf", archive, "-C", tree).CombinedOutput(); err != nil {
			t.Fatalf("extract trunk: %v: %s", err, out)
		}
		index := &VantageQuestions{Root: tree, Command: l.cfg.QuestionsCmd}
		doc, err := index.Document(file)
		if err != nil || !doc.Present {
			t.Fatalf("index document: %+v, %v", doc, err)
		}
		indexed := map[string]string{}
		for _, q := range doc.Questions {
			if q.State != QuestionAnswered {
				indexed["owner; answer "+file+"#"+q.ID] = d.ItemID
			}
		}
		if (len(indexed) == 1 && indexed["owner; answer "+c.QuestionRef] == d.ItemID) != wantOpen || (!wantOpen && len(indexed) != 0) {
			t.Fatalf("unexpected indexed questions: %v", indexed)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			why, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, index)
			if err != nil {
				return err
			}
			waiting := map[string]string{}
			removed := false
			for _, row := range why.Rows {
				if row.Reason == ReasonQuestionOpen {
					if _, duplicate := waiting[row.Mover]; duplicate {
						t.Fatalf("duplicate answer target: %+v", row)
					}
					waiting[row.Mover] = row.ItemID
				}
				removed = removed || (row.ItemID == d.ItemID && row.Reason == ReasonQuestionRemoved)
			}
			if !reflect.DeepEqual(waiting, indexed) || why.Summary.OwnerWaiting != len(indexed) || removed == wantOpen {
				t.Fatalf("index %v disagrees with waiting %v: %+v", indexed, waiting, why)
			}
			return nil
		})
	}
	check(true)
	writeForkFile(t, filepath.Join(f.root, file), "# Request\n")
	runGit(t, f.root, "add", file)
	runGit(t, f.root, "commit", "-qm", "docs: remove the owner question")
	if _, err := l.refreshDocuments(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func ownerTreeIndexCommand(file, id, count string) []string {
	return []string{"sh", "-c", `[ -z "$3" ] || echo call >> "$3"
 [ -f "$1" ] || { printf '{"index":{"documents":[]}}'; exit; }
 q= l=
 if grep -Fq "✅ **$2:" "$1"; then state=answered
 elif grep -Fq "💬 **$2:" "$1"; then state=open
 else state=; fi
 if [ -n "$state" ]; then q=$(printf '{"id":"%s","state":"%s","title":"Which delivery should we use?"}' "$2" "$state"); fi
 if grep -Fq "| $2 |" "$1"; then l=$(printf '"%s"' "$2"); fi
 printf '{"index":{"documents":[{"path":"%s","questions":[%s],"ledgerIds":[%s]}]}}' "$1" "$q" "$l"`, "questions", file, id, count}
}

func TestOwnerAnswerLedgerEvidence(t *testing.T) {
	for _, tc := range []struct {
		name         string
		open, ledger bool
	}{
		{"compacted", false, true}, {"still open", true, true}, {"removed", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, d, base := designAnswerFixture(t)
			l.cfg.Steps = &ownerQuestionSteps{t: t, f: f, l: l}
			c := settleDesignReport(t, f, l, d, base, ownerChoiceReport)
			file, id, _ := strings.Cut(c.QuestionRef, "#")
			path := filepath.Join(f.root, file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			body := "# Request\n"
			if tc.open {
				body = string(raw)
			}
			if tc.ledger {
				body += "\n## Decision Ledger\n\n| ID | Ruling |\n| -- | -- |\n| " + id + " | Ship the built-in copy. |\n"
			}
			writeForkFile(t, path, body)
			runGit(t, f.root, "add", file)
			runGit(t, f.root, "commit", "-qm", "docs: update owner question")
			l.cfg.QuestionsCmd = ownerTreeIndexCommand(file, id, "")
			if _, err := l.refreshDocuments(context.Background(), &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			after, _ := conflictOf(t, f, d.ItemID)
			next := decisionFor(t, l, d.ItemID).decision
			if tc.ledger && !tc.open {
				if after.Status != "answered" || next.Action != ActionDispatch {
					t.Fatalf("compacted ruling did not release: %+v %+v", after, next)
				}
			} else if !tc.open {
				if after.Status != "question" || after.AnswerHead != "" || after.QuestionRemovedHead != runGit(t, f.root, "rev-parse", TrunkRef) || next.Action != ActionHold || next.Reason != ReasonQuestionRemoved || next.Seat != "operator" {
					t.Fatalf("deleted question did not become an operator hold: %+v %+v", after, next)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					why, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, l.questions)
					if err != nil {
						return err
					}
					if why.Summary.OperatorNext != 1 || why.Summary.OwnerWaiting != 0 {
						t.Fatalf("wrong mover counts: %+v", why.Summary)
					}
					for _, row := range why.Rows {
						if row.ItemID == d.ItemID && row.Mover == "operator; restore "+c.QuestionRef+" under the item's block and land it" && strings.Contains(row.Why, c.QuestionRef) && strings.HasSuffix(row.Why, "the question was removed without a ruling") {
							return nil
						}
					}
					return fmt.Errorf("missing deletion explanation: %+v", why.Rows)
				})
				refresh := func(body string) item.Conflict {
					t.Helper()
					writeForkFile(t, path, body)
					runGit(t, f.root, "add", file)
					runGit(t, f.root, "commit", "-qm", "docs: update removed question")
					if _, err := l.refreshDocuments(context.Background(), &LoopResult{}); err != nil {
						t.Fatal(err)
					}
					current, _ := conflictOf(t, f, d.ItemID)
					return current
				}
				restored := refresh(string(raw))
				if next := decisionFor(t, l, d.ItemID).decision; restored.Status != "question" || restored.QuestionRemovedHead != "" || next.Action != ActionHold || next.Reason != ReasonQuestionOpen || next.QuestionID != id {
					t.Fatalf("restored question did not wait on owner: %+v %+v", restored, next)
				}
				document, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if marker := fmt.Sprintf("<!-- tillr-owner-question item=%q run=%q", d.ItemID, c.QuestionRunID); strings.Count(string(document), marker) != 1 {
					t.Fatalf("expected exactly one origin marker %q in %s", marker, document)
				}
				refresh(body)
				answered := refresh(strings.Replace(string(raw), "💬 **"+id+":", "✅ **"+id+":", 1))
				if next := decisionFor(t, l, d.ItemID).decision; answered.Status != "answered" || answered.QuestionRemovedHead != "" || next.Action != ActionDispatch {
					t.Fatalf("restored ruling did not release item: %+v %+v", answered, next)
				}
			} else if after.Status != "question" || after.AnswerHead != "" || next.Action != ActionHold || next.Reason != ReasonQuestionOpen {
				t.Fatalf("question did not stay held: %+v %+v", after, next)
			}
		})
	}
}
