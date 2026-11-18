package vantage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// This package is the item command's, board's and planner's shared source of
// design-document question state. It reads Vantage's own index rather than parsing
// the documents here, because all three must answer "what is still open" the same way:
// a second parser in
// this repository is a second answer, and documents that number their questions already
// produced one disagreement (six queued items the dash-bullet parser reported as carrying
// none).

// Question is one question Vantage's index reports for a design document, as the index
// states it.
//
// The planner reads the identifier and the state; the board reads the title and carries
// `preference`, `leaning` and `blockedOn` through to its own payload. All of them are the
// index's fields, decoded once here so the two faces cannot read the same question two
// ways. The item command likewise reads the identifier, title and state here.
type Question struct {
	ID         string   `json:"id"`
	State      string   `json:"state"`
	Title      string   `json:"title"`
	Line       int      `json:"line"`
	Preference bool     `json:"preference"`
	Leaning    string   `json:"leaning"`
	BlockedOn  []string `json:"blockedOn"`
}

// QuestionAnswered is the one state that settles a question. Every other state is
// unanswered, including one Vantage adds later, because the two mistakes are not equal: a
// hold on a settled question costs a person one look, and a dispatch on an open one spends a
// run on work the design has not decided.
const QuestionAnswered = "answered"

// FirstUnanswered answers the first question in the document's own order that is not
// settled, and whether there is one. The index reports a document's questions in line
// order, so "first" is the question nearest the top of the document.
func FirstUnanswered(questions []Question) (Question, bool) {
	for _, question := range questions {
		if question.State != QuestionAnswered {
			return question, true
		}
	}
	return Question{}, false
}

// DocumentQuestions is one design document's answer, and its three answers are kept apart on
// purpose (see QuestionSource).
type DocumentQuestions struct {
	// Present is whether the tree holds the document. A document that is not there is a
	// broken reference, which is a different fact from a design with nothing open.
	Present bool
	// Questions is every question the index reports for the document, in document order.
	Questions []Question
}

// QuestionSource answers, for a design document an item names, whether the tree holds it and
// what questions it carries.
//
// It is the seam the planner reads instead of a parser, and it has three answers rather than
// two: the document is there with questions, the document is there with none, or this source
// cannot say. The last is an error on purpose. A source that answered an empty list for an
// index it could not read would dispatch work whose design is still open, which is the one
// outcome the three-answer shape exists to prevent.
type QuestionSource interface {
	// Document answers one design document. The path is the item's design_doc as the item
	// stores it: relative to the repository, or absolute. An error answers `unknown`.
	Document(path string) (DocumentQuestions, error)
}

// VantageQuestions is the production QuestionSource: the tree decides whether a document is
// there, and Vantage's index decides what it asks.
//
// The index is read on first use rather than at construction, because a tick whose items
// name no design document must not pay for a subprocess that walks the tree — and a
// subprocess per tick is a cost the suite that drives a hundred ticks would otherwise pay a
// hundred times.
type VantageQuestions struct {
	// Root is the repository the items' relative design_doc references resolve against.
	Root string
	// Command is the program that answers the index, as one argv. Empty is
	// DefaultIndexCommand.
	Command []string
	// Context is the tick's context, so a cancelled tick stops the tool. Nil is a background
	// context.
	Context context.Context

	read  bool
	index VantageIndex
}

// DocumentPresence answers only whether the tree holds the document. It never
// starts the questions index, so a presence check does not depend on that tool.
func (v *VantageQuestions) DocumentPresence(path string) (DocumentQuestions, error) {
	if path == "" || v == nil {
		return DocumentQuestions{}, nil
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(v.Root, resolved)
	}
	if _, err := os.Stat(resolved); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// The reference names no file in the tree, so no index can answer for it. This is
			// the tree's answer and it does not need the tool: it stays `design-doc-missing`
			// even when the tool is absent.
			return DocumentQuestions{}, nil
		}
		return DocumentQuestions{}, err
	}
	return DocumentQuestions{Present: true}, nil
}

// Document answers one design document's presence and indexed questions.
func (v *VantageQuestions) Document(path string) (DocumentQuestions, error) {
	present, err := v.DocumentPresence(path)
	if err != nil || !present.Present {
		return present, err
	}
	// The index names documents relative to the root it walked, so an absolute reference is
	// keyed the way the index spells it.
	key := path
	if filepath.IsAbs(key) {
		root, err := filepath.Abs(v.Root)
		if err != nil {
			return DocumentQuestions{}, fmt.Errorf("resolving question index root: %w", err)
		}
		key, err = filepath.Rel(root, key)
		if err != nil {
			return DocumentQuestions{}, fmt.Errorf("resolving question index document: %w", err)
		}
	}
	questions, err := v.questions(key)
	if err != nil {
		return DocumentQuestions{}, err
	}
	return DocumentQuestions{Present: true, Questions: questions}, nil
}

// Done shares the lazy index with question reads; retirement does not start a
// second index subprocess for every pass at the same repository revision.
func (v *VantageQuestions) Done(path string) (bool, error) {
	if _, err := v.questions(path); err != nil {
		return false, err
	}
	return v.index.Done(path)
}

// questions answers one document's questions from the index, reading the index first if this
// is the first document asked about.
func (v *VantageQuestions) questions(path string) ([]Question, error) {
	if !v.read {
		ctx := v.Context
		if ctx == nil {
			ctx = context.Background()
		}
		v.index = ReadVantageIndex(ctx, v.Root, v.Command)
		v.read = true
	}
	return v.index.Questions(path)
}

// errIndexNotRead is the refusal of a VantageIndex that was never read. It exists so the
// zero value cannot be mistaken for an index that answered "no questions".
var errIndexNotRead = errors.New("the question index was never read")

// VantageIndex is what `vantage-check index --format json` reported for one tree: the
// questions each document carries, keyed by the repository-relative path the index names.
//
// The zero value is deliberately not "no questions". An index nobody read refuses, and the
// planner turns that refusal into `questions-unknown` — never into a dispatch.
type VantageIndex struct {
	documents map[string][]Question
	done      map[string]bool
	read      bool
	err       error
}

// Done answers whether Vantage assigned the document's stage the done role.
func (v VantageIndex) Done(path string) (bool, error) {
	if _, err := v.Questions(path); err != nil {
		return false, err
	}
	return v.done[cleanDocumentPath(path)], nil
}

// Questions answers one document's questions, keyed by its repository-relative path. A
// document the index does not report carries no questions: the index answered for the tree,
// and a document it does not list is one it found nothing to ask about.
func (v VantageIndex) Questions(path string) ([]Question, error) {
	if v.err != nil {
		return nil, v.err
	}
	if !v.read {
		return nil, errIndexNotRead
	}
	return v.documents[cleanDocumentPath(path)], nil
}

// cleanDocumentPath is the spelling the index and an item's design_doc are compared in: a
// slash-separated, cleaned path, so `./docs/x.md` and `docs/x.md` are one document.
func cleanDocumentPath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

// DefaultIndexCommand is the program that answers Vantage's index, spelled as this
// repository's intended installation invokes it: through uvx, without requiring a
// standalone checker on PATH. Callers may explicitly override the command. A missing
// or failed command produces questions-unknown, never an empty question list.
var DefaultIndexCommand = []string{"uvx", "vantage-check", "index", "--format", "json"}

// IndexTimeout bounds the index command. It is a wall on the tool and not a budget on the
// tick: a tool that has not answered by then is the `questions-unknown` case, and the item is
// held rather than dispatched on a guess. It is generous because the index walks every
// planning document in the tree, which on a repository this size takes seconds.
const IndexTimeout = 2 * time.Minute

// ReadVantageIndex runs the index command over root and answers the questions it reports.
//
// The failure is carried in the value rather than returned beside it because every caller
// has to make the same decision about an unreadable index, and a value that cannot be
// mistaken for "no questions" is what makes that decision impossible to get wrong.
func ReadVantageIndex(ctx context.Context, root string, command []string) VantageIndex {
	out, err := RunIndexCommand(ctx, root, command)
	if err != nil {
		return VantageIndex{err: err}
	}
	return ParseIndex(out, IndexCommandName(command))
}

// IndexCommandName is how a refusal names the index command: the argv as one line, the
// default when none was given.
func IndexCommandName(command []string) string {
	if len(command) == 0 {
		command = DefaultIndexCommand
	}
	return strings.Join(command, " ")
}

// RunIndexCommand runs the index command over root and answers its raw JSON. It is the
// half of ReadVantageIndex a caller that keeps the document — the observatory's index cache,
// which hands one walk to several readers — needs on its own.
func RunIndexCommand(ctx context.Context, root string, command []string) ([]byte, error) {
	named := IndexCommandName(command)
	if len(command) == 0 {
		command = DefaultIndexCommand
	}
	ctx, cancel := context.WithTimeout(ctx, IndexTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" && ctx.Err() != nil {
			detail = ctx.Err().Error()
		}
		return nil, fmt.Errorf("running %s: %w: %s", named, err, detail)
	}
	return out, nil
}

// ParseIndex reads an index document a caller already holds. A document that is not an
// index answers the same refusal ReadVantageIndex would, named after where it came from.
func ParseIndex(raw []byte, from string) VantageIndex {
	index, err := parseVantageIndex(raw)
	if err != nil {
		return VantageIndex{err: fmt.Errorf("reading %s: %w", from, err)}
	}
	return index
}

// IndexError is an index that could not be read, carrying why: every question asked of it
// refuses with that error, which the planner and the board answer as `questions-unknown`.
func IndexError(err error) VantageIndex {
	return VantageIndex{err: err}
}

// Preloaded is a VantageQuestions whose index was already read, so asking it a question
// starts no subprocess. The answers are the index's, exactly as if this source had run the
// command itself: a refused index still refuses every document.
func Preloaded(root string, index VantageIndex) *VantageQuestions {
	return &VantageQuestions{Root: root, read: true, index: index}
}

// parseVantageIndex reads the two things the answer needs out of the index document: the
// documents, and the tool's own refusal.
//
// A field that is missing is refused rather than read as empty. `{"index":{}}` is not a tree
// with no documents — it is not an index — and reading it as one would answer "no questions"
// for every item at once, which is exactly the failure this file exists to rule out.
func parseVantageIndex(raw []byte) (VantageIndex, error) {
	var document struct {
		Index *struct {
			Config struct {
				Stages map[string]string `json:"stages"`
			} `json:"config"`
			Refused   bool            `json:"refused"`
			Documents json.RawMessage `json:"documents"`
		} `json:"index"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return VantageIndex{}, err
	}
	if document.Index == nil {
		return VantageIndex{}, errors.New("it carries no index section")
	}
	if document.Index.Refused {
		return VantageIndex{}, errors.New("the tool refused the tree")
	}
	if len(document.Index.Documents) == 0 || string(document.Index.Documents) == "null" {
		return VantageIndex{}, errors.New("it carries no documents")
	}
	var entries []struct {
		Path      string     `json:"path"`
		Stage     string     `json:"stage"`
		Questions []Question `json:"questions"`
	}
	if err := json.Unmarshal(document.Index.Documents, &entries); err != nil {
		return VantageIndex{}, err
	}
	documents := make(map[string][]Question, len(entries))
	done := make(map[string]bool)
	for _, entry := range entries {
		key := cleanDocumentPath(entry.Path)
		documents[key] = entry.Questions
		done[key] = document.Index.Config.Stages[entry.Stage] == "done"
	}
	return VantageIndex{documents: documents, done: done, read: true}, nil
}
