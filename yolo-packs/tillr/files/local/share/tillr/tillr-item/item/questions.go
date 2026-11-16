package item

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Question is one open question a design document declares: its identifier and its
// text, as the document itself states them.
type Question struct {
	QuestionID string `json:"question_id"`
	Text       string `json:"text"`
}

// ErrDesignDocMissing is the error for a design_doc reference that names a document
// which is not there. It is a named error so the face refuses with a rule rather than
// answering an empty list: an empty list is indistinguishable from a finished design,
// and the two need different remedies.
var ErrDesignDocMissing = errors.New("item: the design document is not there")

// The declaration form this parser reads, which is the form the repository's design
// documents already use: an open-questions section whose items are bolded, linked,
// em-dash-led questions.
var (
	headingPattern    = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	sectionNumber     = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*\.?\s+`)
	boldRunPattern    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	linkPattern       = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)
	linkLabelPattern  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	questionIDPattern = regexp.MustCompile(`OQ-[0-9A-Za-z]+`)
)

// openQuestionsHeading is the section title this parser looks for, after the leading
// section number is stripped.
const openQuestionsHeading = "open questions"

// ReadQuestions answers the open questions a design document declares, read from the
// document itself. The board stores no question state, so the document is the only
// copy and the two cannot drift apart.
//
// A relative doc resolves against root; an absolute one is used as given. The parser
// is a pure function of (root, doc) and returns an error only for a document that
// cannot be read: a document with no open-questions section is a finished design, not
// a failure.
func ReadQuestions(root, doc string) ([]Question, error) {
	path := doc
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, doc)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrDesignDocMissing, doc)
		}
		return nil, err
	}
	return parseQuestions(string(raw)), nil
}

// parseQuestions reads the open-questions section and answers one question per
// top-level list item in it. A bullet outside the section is not a question and an
// OQ- token in prose is not a question: the section is the boundary.
func parseQuestions(document string) []Question {
	questions := []Question{}
	level := 0
	for _, line := range strings.Split(document, "\n") {
		if match := headingPattern.FindStringSubmatch(line); match != nil {
			headingLevel := len(match[1])
			if level != 0 && headingLevel <= level {
				// The section ended at the first heading at or above its own level.
				break
			}
			if level == 0 && isOpenQuestionsHeading(match[2]) {
				level = headingLevel
			}
			continue
		}
		if level == 0 || !strings.HasPrefix(line, "- ") {
			continue
		}
		if question, ok := parseQuestion(line); ok {
			questions = append(questions, question)
		}
	}
	return questions
}

// isOpenQuestionsHeading reports whether a heading's text is the open-questions
// section, after stripping a leading section number.
func isOpenQuestionsHeading(text string) bool {
	return strings.EqualFold(strings.TrimSpace(sectionNumber.ReplaceAllString(strings.TrimSpace(text), "")), openQuestionsHeading)
}

// parseQuestion reads one list item: the identifier is the OQ- token inside the item's
// first bold run, and the text is the question that run declares. Nothing else in the
// item is read.
//
// Two declaration forms are in use, and the bold run's own content decides which. In the
// first the run carries the question (`- **[OQ-1](#anchor) — the question.**`); in the
// second it carries only the identifier and the question follows it (`- **OQ-1:** the
// question`). Reading the run as the text unconditionally answered every question of the
// second form with its own identifier (measured 2026-11-11 against
// docs/design/board-item-state-model.md, whose items are all of that form).
func parseQuestion(line string) (Question, bool) {
	loc := boldRunPattern.FindStringSubmatchIndex(line)
	if loc == nil {
		return Question{}, false
	}
	bold := line[loc[2]:loc[3]]
	id := questionIDPattern.FindString(bold)
	if id == "" {
		return Question{}, false
	}
	// In the first form the identifier's link IS the identifier, so the whole link goes.
	text := trimDeclaration(strings.Replace(linkPattern.ReplaceAllString(bold, ""), id, "", 1))
	if text == "" {
		// The run named the question and did not state it, so the statement follows. A
		// link in the statement keeps its label: it is part of the question's prose.
		text = trimDeclaration(linkLabelPattern.ReplaceAllString(line[loc[1]:], "$1"))
	}
	return Question{QuestionID: id, Text: text}, true
}

// trimDeclaration reduces one fragment of a declaration to the question it states: the
// leading punctuation a declaration puts between the identifier and the question
// stripped, and the surrounding space with it.
func trimDeclaration(fragment string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(fragment), "—:- "))
}
