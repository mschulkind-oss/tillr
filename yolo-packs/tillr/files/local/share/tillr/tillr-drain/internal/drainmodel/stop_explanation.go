package drainmodel

import (
	"time"

	stoprecord "github.com/mschulkind-oss/tillr-drain/stop"
)

// StopExplanation is the loop's account of its stop, shared with record readers.
type StopExplanation = stoprecord.Explanation

// ExplainStopFields formats the recorded cause; older result readers can use it without
// substituting a newer pass or guessing which item failed.
func ExplainStopFields(stop, detail, itemID, step string, at time.Time) *StopExplanation {
	return stoprecord.Explain(stop, detail, itemID, step, at)
}
