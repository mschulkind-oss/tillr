package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// This file is the board's only source of design-document question state, and it reads
// Vantage's own index rather than parsing the documents here. The board and the planner must
// answer "what is still open" the same way: the index reader lives in the drain's `vantage`
// package, and this face imports it instead of growing a second parser beside it. The
// dash-bullet parser this replaces reported zero questions for the six documents that number
// theirs, which is the disagreement the shared reader exists to end.

// question is one question a design document carries, as Vantage's index reports it. The
// identifier and the text are the board's own spelling of the index's `id` and `title`; the
// rest is carried through so a reader of the payload sees the index's answer, not a summary
// this face invented.
type question struct {
	QuestionID string   `json:"question_id"`
	Text       string   `json:"text"`
	State      string   `json:"state"`
	Preference bool     `json:"preference,omitempty"`
	Leaning    string   `json:"leaning,omitempty"`
	BlockedOn  []string `json:"blocked_on,omitempty"`
}

// The three document-level answers, kept apart on purpose. `questions-ok` is the tool's
// list (which may be empty: a finished design). `questions-unknown` is the tool being
// absent, failing, or timing out. `design-doc-missing` is the tree's answer that the item
// names a file that is not there, and it survives the tool's absence because no tool can
// change it. Unknown must never be read as "no questions": a finished design and an
// unreadable index dispatch differently.
const (
	questionsOK      = "questions-ok"
	questionsUnknown = "questions-unknown"
	designDocMissing = "design-doc-missing"
)

// errDesignDocMissing is the error for a design_doc reference that names a document which
// is not there. It is a named error so the document route refuses with a rule rather than
// answering an empty document: an empty list is indistinguishable from a finished design,
// and the two need different remedies.
var errDesignDocMissing = errors.New("the design document is not there")

// errDocumentOutsideRoot is the refusal for a relative design_doc that resolves outside the
// root the face was given. An absolute path is taken as the operator configured it, so it is
// not this face's to contain; a relative one is a name inside the root or it is a bug.
var errDocumentOutsideRoot = errors.New("design document resolves outside the served root")

// resolveDocument answers the path a design_doc names, resolving a relative one against
// root and refusing one that climbs out of it. Both the question source and the document
// route go through it, so the two cannot disagree about which file a reference names.
func resolveDocument(root, doc string) (string, error) {
	if filepath.IsAbs(doc) {
		return doc, nil
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(base, doc))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", errDocumentOutsideRoot, doc)
	}
	return path, nil
}

// readDocument answers one design document's bytes and the path they came from. A document
// that is not there is errDesignDocMissing rather than a bare file error, because a
// reference to a file a reader cannot see is the case the faces must describe.
func readDocument(root, doc string) ([]byte, string, error) {
	path, err := resolveDocument(root, doc)
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", fmt.Errorf("%w: %s", errDesignDocMissing, doc)
		}
		return nil, "", err
	}
	return raw, path, nil
}

// questionAnswer is one document's answer: the questions the index reports, and the state
// that names which of the three answers this is. Questions is nil for the two answers that
// are not the tool's list, because a caller that rendered nil as "none open" would be the
// mistake this type exists to prevent.
type questionAnswer struct {
	Questions []question
	State     string
}

// readQuestions answers a design document's questions from the source. It does not return
// an error: the tool's silence and the tree's absence are answers, not failures of the
// report, and a caller that turned either into a refusal would lose the distinction the
// three states exist to keep.
func readQuestions(source vantage.QuestionSource, doc string, excluded ...string) questionAnswer {
	if doc == "" {
		return questionAnswer{}
	}
	if source == nil {
		return questionAnswer{State: questionsUnknown}
	}
	answer, err := source.Document(doc)
	if err != nil {
		// The tool is absent, failed, or timed out. The error text is the tool's own; the
		// state is the answer, and it is never read as "no questions".
		return questionAnswer{State: questionsUnknown}
	}
	if !answer.Present {
		return questionAnswer{State: designDocMissing}
	}
	// The index reports every question with its state. The board's question list is the
	// open ones: a settled question is an answer, not a question, and counting it would
	// hold a finished design open.
	questions := make([]question, 0, len(answer.Questions))
	for _, q := range vantage.WithoutQuestions(answer.Questions, excluded) {
		if q.State == vantage.QuestionAnswered {
			continue
		}
		questions = append(questions, questionOf(q))
	}
	return questionAnswer{Questions: questions, State: questionsOK}
}

// questionOf spells one index question in the board's payload.
func questionOf(q vantage.Question) question {
	return question{
		QuestionID: q.ID,
		Text:       questionText(q),
		State:      q.State,
		Preference: q.Preference,
		Leaning:    q.Leaning,
		BlockedOn:  q.BlockedOn,
	}
}

// questionText is the index's title without the identifier it begins with. The index's
// title is `OQ-1: the question`; the board carries the identifier separately, so the text is
// the question itself.
func questionText(q vantage.Question) string {
	text := strings.TrimSpace(q.Title)
	if q.ID == "" {
		return text
	}
	if rest, found := strings.CutPrefix(text, q.ID); found {
		return strings.TrimSpace(strings.TrimLeft(rest, ":"))
	}
	return text
}

// openQuestionCount answers the count a document carries when the index answered, and nil
// when it did not: a null count is "unknown", never a zero the reader could mistake for a
// finished design.
func openQuestionCount(answer questionAnswer) *int {
	if answer.State != questionsOK {
		return nil
	}
	count := len(answer.Questions)
	return &count
}

// questionItem is one item the board lists because its design document still asks
// something. The questions are Vantage's index answer; Hold is the planner's own latest
// reason for holding the item, so the list, the rail and the header can be read against one
// another rather than believed separately.
type questionItem struct {
	ItemID         string     `json:"item_id"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	DesignDoc      string     `json:"design_doc"`
	Questions      []question `json:"questions"`
	QuestionsState string     `json:"questions_state"`
	// Hold is the loop's latest question-hold reason for the item, or empty when its latest
	// decision was something else. An item in the list with an empty Hold is one whose
	// question opened after the planner's last tick: the list is the index's, and the
	// planner has not answered it yet.
	Hold string `json:"hold,omitempty"`
}

// questionsCounts is the list's own arithmetic: the items with an open question, the total
// number of questions, and how many of those items the planner already holds. Holds counts
// the planner's question holds across the list and the two other answers, which is the same
// union as the header's QuestionHolds; the two differ exactly when a question opened
// between ticks.
type questionsCounts struct {
	Items     int `json:"items"`
	Questions int `json:"questions"`
	Holds     int `json:"holds"`
}

// questionsData is `GET /api/v1/questions`: the board's one place for the questions the
// owner still owes a ruling. Unknown and Missing are the two other answers the question
// source can give, kept apart from the list because neither is "no questions": one is an
// index that could not be read, the other a document the tree does not hold.
type questionsData struct {
	Items   []questionItem  `json:"items"`
	Unknown []questionItem  `json:"unknown"`
	Missing []questionItem  `json:"missing"`
	Counts  questionsCounts `json:"counts"`
	// State is questions-ok when the index answered, and questions-unknown when it did not.
	// An empty list under questions-unknown is not a finished design.
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
}

// questionsPayload answers the board's question list. It reads the index once per request —
// the server's vantageCache (vantagecache.go), which reuses the last index while the tree's
// Markdown is unchanged — and asks each item's document in queue order.
// The store's own dispatch rows supply the planner's hold, so the list can say which items
// the loop already holds and which questions opened after its last tick.
func (s *server) questionsPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	items, err := readItems(r)
	if err != nil {
		return nil, unreadable(err)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return rankBefore(items[i].Rank, items[i].ItemID, items[j].Rank, items[j].ItemID)
	})
	dispatches, _ := readAllDispatches(r)
	holds := questionHolds(dispatches)
	source := s.vantage.source(req.Context())
	out := questionsData{
		Items:   []questionItem{},
		Unknown: []questionItem{},
		Missing: []questionItem{},
		State:   questionsOK,
	}
	for _, it := range items {
		if it.DesignDoc == "" {
			continue
		}
		answer := readQuestions(source, it.DesignDoc)
		entry := questionItemOf(it, answer, holds[it.ItemID], questionLinkPath(s.root, it.DesignDoc))
		switch answer.State {
		case questionsOK:
			if len(answer.Questions) == 0 {
				continue
			}
			out.Items = append(out.Items, entry)
			out.Counts.Questions += len(entry.Questions)
			if entry.Hold != "" {
				out.Counts.Holds++
			}
		case questionsUnknown:
			out.Unknown = append(out.Unknown, entry)
		case designDocMissing:
			out.Missing = append(out.Missing, entry)
		}
	}
	out.Counts.Items = len(out.Items)
	// Holds is the planner's count across every answer, not only the list: the header's
	// QuestionHolds is the same union, so a reader comparing the two is comparing one fact.
	for _, group := range [][]questionItem{out.Unknown, out.Missing} {
		for _, entry := range group {
			if entry.Hold != "" {
				out.Counts.Holds++
			}
		}
	}
	if len(out.Unknown) > 0 {
		// The index is the one answer that is global: when it could not be read, every
		// document in the tree is unknown, and an empty list is not a finished design.
		out.State = questionsUnknown
		out.Note = "the question index could not be read, so this list is not a claim that no questions are open"
	}
	return out, nil
}

// questionItemOf spells one item and its answer in the list's payload. linkPath is the
// root-relative spelling a Vantage URL takes, so an absolute design_doc the operator
// configured still answers a link the viewer can resolve.
func questionItemOf(it storeItem, answer questionAnswer, hold, linkPath string) questionItem {
	questions := answer.Questions
	if questions == nil {
		questions = []question{}
	}
	return questionItem{
		ItemID:         it.ItemID,
		Title:          it.Title,
		State:          it.State,
		DesignDoc:      linkPath,
		Questions:      questions,
		QuestionsState: answer.State,
		Hold:           hold,
	}
}

// questionLinkPath answers the path a Vantage link spells for an item's design_doc: the
// same root-relative spelling the document route and the pane use, so the list's links and
// the pane's link name one document. A reference that cannot be resolved answers itself.
func questionLinkPath(root, doc string) string {
	resolved, err := resolveDocument(root, doc)
	if err != nil {
		return doc
	}
	return documentLinkPath(root, resolved)
}

// questionHold is the set of hold reasons that are the question source's answers rather than
// the store's own machinery. The loop writes one of these as a decision's reason, and the
// board reads that reason back instead of re-running the index on every snapshot.
var questionHold = map[string]bool{
	"question-open":      true,
	"questions-unknown":  true,
	"design-doc-missing": true,
}

// questionHolds answers each item's latest question hold, from the loop's decisions oldest
// first. A later decision of any other kind clears the hold: the planner has answered the
// item again, and the latest answer is the one the rail must show.
func questionHolds(dispatches []storeDispatch) map[string]string {
	holds := map[string]string{}
	for _, d := range dispatches {
		if questionHold[d.Reason] {
			holds[d.ItemID] = d.Reason
			continue
		}
		delete(holds, d.ItemID)
	}
	return holds
}

// questionHoldOf answers one item's latest question hold from its own decisions, which the
// item route reads by item id rather than through the whole board's dispatch table.
func questionHoldOf(dispatches []storeDispatch) string {
	hold := ""
	for _, d := range dispatches {
		if questionHold[d.Reason] {
			hold = d.Reason
			continue
		}
		hold = ""
	}
	return hold
}
