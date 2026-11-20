// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package item

// Question is one unanswered question from Vantage's index. The identifier and title
// keep the item record's existing field names; State preserves the index's answer.
type Question struct {
	QuestionID string `json:"question_id"`
	Text       string `json:"text"`
	State      string `json:"state"`
}
