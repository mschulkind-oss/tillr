package drain

import (
	"time"

	stoprecord "github.com/mschulkind-oss/tillr-drain/stop"
)

// StopExplanation is the loop's account of its stop, shared with record readers.
type StopExplanation = stoprecord.Explanation

// ExplainStop formats the recorded cause; older result readers can use it without
// substituting a newer pass or guessing which item failed.
func ExplainStop(result LoopResult, at time.Time) *StopExplanation {
	return stoprecord.Explain(result.Stop, result.Detail, result.StopItemID, result.StopStep, at)
}
