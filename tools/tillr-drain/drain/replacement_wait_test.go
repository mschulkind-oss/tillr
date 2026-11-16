package drain

import (
	"context"
	"testing"
)

// A loop stop leaves its run in flight on an unexpiring lease. A replacement loop
// has no local completion for that run, so its claim is work to wait on, not a
// foreign leased lane to stop for: even a draining replacement keeps waiting
// until the run's terminal record lets a later pass settle it.
func TestAReplacementWaitsOnASurvivingRunInsteadOfStopping(t *testing.T) {
	f, l := passFixture(t, "item-a")
	claimRun(t, f, l)
	replacement := f.loop(t, &recordingSteps{}, &itemContentStager{})
	replacement.cfg.ExitWhenDrained = true
	var result LoopResult
	stop, err := replacement.pass(context.Background(), &result)
	if err != nil || stop != "" {
		t.Fatalf("replacement stopped on a surviving run: %q %v (%s)", stop, err, result.Detail)
	}
}
