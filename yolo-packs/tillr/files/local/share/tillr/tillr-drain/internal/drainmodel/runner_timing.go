package drainmodel

import "strings"

// RunnerProofPaths recognizes changes whose judge must prove itself before merge.
// The judge is the gate runner, the declared checks and the recipes that start them.
func RunnerProofPaths(paths string) bool {
	for _, path := range strings.Split(paths, "\x00") {
		if path == ".justfile" || strings.HasPrefix(path, "tools/tillr-gate/") || strings.HasPrefix(path, "tools/gate/") || strings.HasPrefix(path, "yolo-packs/tillr/files/local/share/tillr/tillr-gate/") {
			return true
		}
	}
	return false
}

func (t *PassTiming) AddGateTime(ms int64, runner bool) {
	if runner {
		t.RunnerGates++
		t.RunnerGateMS += ms
	} else {
		t.Gates++
		t.GateMS += ms
		t.GateMaxMS = max(t.GateMaxMS, ms)
	}
}

func (t *PassTiming) AddLandTime(ms int64, runner bool) {
	if runner {
		t.RunnerLandMS += ms
	} else {
		t.LandMS += ms
	}
}
