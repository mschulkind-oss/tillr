package drain

import (
	"context"
	"strings"
)

// runnerProofPaths recognizes changes whose judge must prove itself before merge.
// Keep this set aligned with GATE_RUNNER_WATCHES and SELF_SUITE in fork-check.py.
func runnerProofPaths(paths string) bool {
	for _, path := range strings.Split(paths, "\x00") {
		if path == "tools/fork-check.py" || path == "tests/fork-check.test.py" || path == ".justfile" || path == "tools/preflight-declarations.json" || strings.HasPrefix(path, "tools/tillr-gate/") || strings.HasPrefix(path, "tools/gate/") || strings.HasPrefix(path, "yolo-packs/tillr/files/local/share/tillr/tillr-gate/") {
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
	}
}

func (t *PassTiming) addLandTime(ms int64, runner bool) {
	if runner {
		t.RunnerLandMS += ms
	} else {
		t.LandMS += ms
	}
}
