// Package stop formats a loop's recorded cause without depending on its execution machinery.
package stop

import (
	"strings"
	"time"
)

// Explanation is the loop's account of why it stopped and who can move it next.
// Readers render this account, rather than inventing guidance from a reason code.
type Explanation struct {
	Reason   string `json:"reason"`
	AtMS     int64  `json:"at_ms"`
	ItemID   string `json:"item_id"`
	Step     string `json:"step"`
	Sentence string `json:"sentence"`
	Next     string `json:"next"`
}

// Explain preserves the recorded cause and context when producing the stop account.
func Explain(reason, detail, itemID, step string, at time.Time) *Explanation {
	if reason == "" {
		return nil
	}
	recordedStep := step
	if step == "" {
		step = "loop"
	}
	detail = strings.Join(strings.Fields(detail), " ")
	next := "Owner resolves the failure described above, then restarts with tillr-drain loop."
	switch reason {
	case "queue-drained":
		if recordedStep == "" {
			step = "plan"
		}
		if detail == "" {
			detail = "no item can be dispatched"
		}
		next = "The loop plans the queue again on restart with tillr-drain loop."
	case "lanes-leased":
		if recordedStep == "" {
			step = "plan"
		}
		if detail == "" {
			detail = "a retained lane has no lease expiry"
		}
		next = "Owner releases the retained lane with tillr run release <lane>, then restarts with tillr-drain loop."
	case "operator-stop":
		if detail == "" {
			detail = "the owner asked it to stop"
		}
		next = "Owner decides when to resume with tillr-drain loop."
	case "host-boundary-violation":
		if detail == "" {
			detail = "a run changed a path outside its permitted boundary"
		}
		next = "Owner inspects and restores the unauthorized changes before any restart; do not retry the offending run."
	case "gate-harness-error":
		if recordedStep == "" {
			step = "gate"
		}
		if detail == "" {
			detail = "the gate could not judge the candidate"
		}
		next = "Owner repairs the gate harness, then restarts with tillr-drain loop."
	case "fault":
		if detail == "" {
			detail = "the loop failed without recording the cause"
		}
	default:
		if detail == "" {
			detail = "the loop recorded an unsupported stop reason: " + reason
		}
		next = "Owner updates the reader for stop reason " + reason + " before restarting."
	}
	return &Explanation{Reason: reason, AtMS: at.UnixMilli(), ItemID: itemID, Step: step,
		Sentence: "The loop stopped because " + strings.TrimRight(detail, ".") + ".", Next: next}
}
