package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-drain/vantage"
)

// The planner's question reader lives in .../vantage, not here, because the board and the
// planner must answer "what is still open" from one reader: a second parser beside this one
// is a second answer, and documents that number their questions already produced one
// disagreement. The aliases below keep the planner's own vocabulary — `Question`,
// `QuestionSource`, `VantageQuestions` — reading here as it always has, while the code that
// runs `vantage-check index --format json` and parses it has one home.

// Question is one question Vantage's index reports for a design document.
type Question = vantage.Question

// DocumentQuestions is one design document's answer: whether the tree holds it and what the
// index reports it asks.
type DocumentQuestions = vantage.DocumentQuestions

// QuestionSource answers, for a design document an item names, whether the tree holds it and
// what questions it carries.
type QuestionSource = vantage.QuestionSource

// VantageQuestions is the production QuestionSource: the tree decides whether a document is
// there, and Vantage's index decides what it asks.
type VantageQuestions = vantage.VantageQuestions

// VantageIndex is what `vantage-check index --format json` reported for one tree.
type VantageIndex = vantage.VantageIndex

// QuestionAnswered is the one state that settles a question.
const QuestionAnswered = vantage.QuestionAnswered

// IndexTimeout bounds the index command.
const IndexTimeout = vantage.IndexTimeout

// DefaultIndexCommand is the program that answers Vantage's index.
var DefaultIndexCommand = vantage.DefaultIndexCommand

// ReadVantageIndex runs the index command over root and answers the questions it reports.
func ReadVantageIndex(ctx context.Context, root string, command []string) VantageIndex {
	return vantage.ReadVantageIndex(ctx, root, command)
}

// firstUnanswered answers the first question in the document's own order that is not
// settled, and whether there is one.
func firstUnanswered(questions []Question) (Question, bool) {
	return vantage.FirstUnanswered(questions)
}
