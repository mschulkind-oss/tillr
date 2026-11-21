package drain

import (
	"encoding/json"
	"slices"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const repeatedObstacleMarker = "\n\nPrevious obstacle before the design answer"

func repeatedObstacleActor(c item.Conflict) string {
	if parseObstacle(workerConflictReport(c.Text)).Kind == ObstacleOwner {
		return "owner"
	}
	return "operator"
}

func repeatedAnsweredObstacle(previous, next item.Conflict, current Obstacle) bool {
	if previous.Status != "closed" || previous.WaitingOn != "build" || previous.QuestionRef != "" ||
		(previous.AnswerHead == "" && previous.DesignRunID == "") || previous.RunID == next.RunID || current.Kind == "" || current.Problem != "" {
		return false
	}
	first := parseObstacle(workerConflictReport(previous.Text))
	if first.Kind != current.Kind || first.Problem != "" {
		return false
	}
	criteria := func(c item.Conflict, o Obstacle) []string {
		if ids := o.List("CRITERIA"); len(ids) != 0 {
			slices.Sort(ids)
			return slices.Compact(ids)
		}
		return c.BlockedCriteria
	}
	if !slices.Equal(criteria(previous, first), criteria(next, current)) {
		return false
	}
	switch current.Kind {
	case ObstacleDefinition:
		// A criterion can have several gaps; only the evidence identifies the one answered.
		return collapseSingleLine(first.Field("EVIDENCE")) == collapseSingleLine(current.Field("EVIDENCE"))
	case ObstacleOwner:
		return collapseSingleLine(first.Field("QUESTION")) == collapseSingleLine(current.Field("QUESTION"))
	default:
		return true
	}
}

// The existing conflict journal records the landed answer and its later consumption
// separately. Count the answer transition, once per conflict, not its closure.
func answeredObstacleCounts(r *store.Reader) (map[string]int, error) {
	rows, err := r.Query(`SELECT payload FROM events WHERE kind=?
 AND json_extract(payload,'$.status')='answered'
 AND COALESCE(json_extract(payload,'$.answer_head'),'')!=''
 AND COALESCE(json_extract(payload,'$.question_ref'),'')=''`, item.ConflictKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	seen := map[[2]string]bool{}
	for rows.Next() {
		var raw []byte
		var c item.Conflict
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		o := parseObstacle(workerConflictReport(c.Text))
		key := [2]string{c.ItemID, c.ConflictID}
		if !seen[key] && o.Kind != "" && o.Problem == "" {
			seen[key] = true
			counts[c.ItemID]++
		}
	}
	return counts, rows.Err()
}
