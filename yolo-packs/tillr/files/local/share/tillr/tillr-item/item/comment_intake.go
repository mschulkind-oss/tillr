package item

import (
	"encoding/json"
	"time"
)

// ReviewChanged is the latest reviewer write: root creation/edit or a reviewer
// turn's creation/edit. Agent replies do not restart the quiet interval.
func ReviewChanged(c ReviewComment) float64 {
	changed := c.CreatedAt
	if c.EditedAt > changed {
		changed = c.EditedAt
	}
	for _, raw := range c.Reactions {
		var r reviewReaction
		if json.Unmarshal(raw, &r) == nil && r.Actor == "reviewer" {
			if r.Timestamp > changed {
				changed = r.Timestamp
			}
			if r.EditedAt > changed {
				changed = r.EditedAt
			}
		}
	}
	return changed
}

// QuietComments selects one sitting of pending review. Older comments still
// participate in the document-wide quiet interval, even when not selected.
func QuietComments(comments []ReviewComment, first, now time.Time, quiet time.Duration, older bool) ([]ReviewComment, int) {
	pending := []ReviewComment{}
	old := 0
	ready := true
	for _, c := range comments {
		if !PendingReview(c) {
			continue
		}
		changed := ReviewChanged(c)
		if c.BoxOpen || float64(now.UnixNano())/1e9-changed < quiet.Seconds() {
			ready = false
		}
		if changed < float64(first.UnixNano())/1e9 && !older {
			old++
			continue
		}
		pending = append(pending, c)
	}
	if !ready {
		return nil, old
	}
	return pending, old
}
