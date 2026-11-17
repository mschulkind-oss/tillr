package drain

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// LandObservationFile is the lander's measured events, saved outside the lane's
// gate worktree. Its hold starts at acquisition (not process start or lock wait)
// and ends at the sample immediately before release.
const LandObservationFile = "land.observation.json"

type LandObservation struct {
	RunID  string          `json:"run_id"`
	Events []LandLockEvent `json:"events"`
}
type LandLockEvent struct {
	Kind string `json:"kind"`
	AtNS int64  `json:"at_ns"`
}

func (o *LandObservation) holdNS() (int64, bool) {
	if o == nil || len(o.Events) < 2 {
		return 0, false
	}
	first, last := o.Events[0], o.Events[len(o.Events)-1]
	return last.AtNS - first.AtNS, first.Kind == "acquired" && last.Kind == "released" && last.AtNS >= first.AtNS
}
func (s *ProcessSteps) saveLandObservation(runID string, o *LandObservation) error {
	if o == nil {
		return nil
	} // Older landers explicitly have no measurement.
	if o.RunID != runID {
		return Internalf("land observation for %s names %s", runID, o.RunID)
	}
	if _, ok := o.holdNS(); !ok {
		return Internalf("land observation for %s has no complete hold", runID)
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	dir := RunDir(s.Root, runID)
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, LandObservationFile), append(raw, '\n'), 0644)
}
func (l *Loop) reportLandingMeasurements() {
	l.mu.Lock()
	holds := append([]int64(nil), l.landLockHolds...)
	missing := l.landLockUnmeasured
	l.mu.Unlock()
	sort.Slice(holds, func(i, j int) bool { return holds[i] < holds[j] })
	if len(holds) == 0 {
		l.reportf("loop: land-lock longest=unavailable median=unavailable measured=0 unmeasured=%d\n", missing)
	} else {
		median := holds[len(holds)/2]
		if len(holds)%2 == 0 {
			low := holds[len(holds)/2-1]
			median = low + (median-low)/2
		}
		l.reportf("loop: land-lock longest=%s median=%s measured=%d unmeasured=%d\n", time.Duration(holds[len(holds)-1]), time.Duration(median), len(holds), missing)
	}
	l.heavyMu.Lock()
	counts := append([]int(nil), l.heavyLandingCounts...)
	l.heavyMu.Unlock()
	l.reportf("loop: landings per heavy run=%v\n", counts)
}
func landingMeasurementError(err error) error {
	return fmt.Errorf("recording land observation: %w", err)
}
