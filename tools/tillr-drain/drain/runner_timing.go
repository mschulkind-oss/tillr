package drain

import (
	"context"
	"strings"
)

// runnerProofPaths recognizes changes whose judge must prove itself before merge.
// The judge is the gate runner, the declared checks and the recipes that start them.
func runnerProofPaths(paths string) bool {
	for _, path := range strings.Split(paths, "\x00") {
		if path == ".justfile" || strings.HasPrefix(path, "tools/tillr-gate/") || strings.HasPrefix(path, "tools/gate/") || strings.HasPrefix(path, "yolo-packs/tillr/files/local/share/tillr/tillr-gate/") {
			return true
		}
	}
	return false
}

func (l *Loop) runnerProofChange(ctx context.Context, c Candidate) (bool, error) {
	paths, err := l.git(ctx, c.Worktree, "diff", "--name-only", "--no-renames", "-z", c.BaseSHA, c.HeadSHA)
	return runnerProofPaths(paths), err
}

func (t *PassTiming) addGateTime(ms int64, runner bool) {
	if runner {
		t.RunnerGates++
		t.RunnerGateMS += ms
	} else {
		t.Gates++
		t.GateMS += ms
		t.GateMaxMS = max(t.GateMaxMS, ms)
	}
}

func (t *PassTiming) addLandTime(ms int64, runner bool) {
	if runner {
		t.RunnerLandMS += ms
	} else {
		t.LandMS += ms
	}
}
