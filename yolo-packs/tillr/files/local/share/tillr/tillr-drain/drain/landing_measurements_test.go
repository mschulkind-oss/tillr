package drain

import (
	"strings"
	"testing"
)

func TestLandingSummaryMeasuresTheWholeHoldAndTheMedian(t *testing.T) {
	for _, test := range []struct {
		name  string
		holds []int64
		want  string
	}{
		{"odd", []int64{9, 1, 5}, "longest=9ns median=5ns measured=3"},
		{"even", []int64{9, 1, 7, 3}, "longest=9ns median=5ns measured=4"},
		{"absent", nil, "longest=unavailable median=unavailable measured=0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var report strings.Builder
			l := &Loop{cfg: LoopConfig{Report: &report}, landLockHolds: test.holds, landLockUnmeasured: 2, heavyLandingCounts: []int{3, 7}}
			l.reportLandingMeasurements()
			if !strings.Contains(report.String(), test.want) || !strings.Contains(report.String(), "unmeasured=2") || !strings.Contains(report.String(), "landings per heavy run=[3 7]") {
				t.Fatal(report.String())
			}
		})
	}
	observation := &LandObservation{Events: []LandLockEvent{{Kind: "acquired", AtNS: 10}, {Kind: "squash", AtNS: 20}, {Kind: "receipt", AtNS: 50}, {Kind: "released", AtNS: 80}}}
	if got, ok := observation.holdNS(); !ok || got != 70 {
		t.Fatalf("hold=%d complete=%v", got, ok)
	}
}
func TestIncompleteLandingObservationDoesNotInventAHold(t *testing.T) {
	for _, o := range []*LandObservation{nil, {}, {Events: []LandLockEvent{{Kind: "acquired", AtNS: 1}}}, {Events: []LandLockEvent{{Kind: "acquired", AtNS: 9}, {Kind: "released", AtNS: 2}}}, {Events: []LandLockEvent{{Kind: "waiting", AtNS: 1}, {Kind: "released", AtNS: 2}}}} {
		if _, ok := o.holdNS(); ok {
			t.Fatalf("accepted incomplete measurement: %+v", o)
		}
	}
}
