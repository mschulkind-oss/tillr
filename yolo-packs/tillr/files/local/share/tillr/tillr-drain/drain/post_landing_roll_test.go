package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAfterMergeLandingRollInstallsWithoutRepeatingProof(t *testing.T) {
	declareSiblingInputs(t)
	if _, err := os.ReadFile("../.sibling-inputs/tillr-roll/tillr-roll.py"); err != nil {
		t.Fatal(err)
	}
	l, _, commit := actionFixture(t)
	ctx := context.Background()
	script, err := filepath.Abs(filepath.Join("..", "..", "tillr-roll", "tillr-roll.py"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROLL_SCRIPT", script)
	t.Setenv("ROLL_FIXTURE_STORE", l.cfg.Store)
	l.gatePolicy.LandingStrategy = "after-merge"
	// Exercise the actual roll orchestration. Replace the external build and smoke
	// commands with fixtures; attempting a suite would fail instead of installing.
	l.cfg.PostLanding.Roll = settings.Roll{Paths: []string{"tools/tillr*/"}, Command: `python3 -B - <<'PY'
import os
from pathlib import Path
import runpy

roll = runpy.run_path(os.environ['ROLL_SCRIPT'])['roll_commit']
steps = []
def step(name, fn, *args, **kwargs):
    steps.append(name)
    if name.startswith('build '):
        Path(args[args.index('-o')+1]).write_text(name)
    elif name.startswith('smoke ') or name == 'loop check':
        assert Path(args[0]).is_file(), args
    elif name != 'checkout':
        return fn(*args, **kwargs)

def test(*args):
    raise RuntimeError('roll suite should belong to after-merge proof')

roll.__globals__.update(stepped=step, test_commit=test)
root = Path.cwd()
store = Path(os.environ['ROLL_FIXTURE_STORE'])
assert roll(root, root/'bin', 'landed', True, store) == 0
assert 'loop check' in steps
assert (root/'bin/tillr-drain').read_text() == 'build tillr-drain'
del os.environ['TILLR_ROLL_VERIFICATION']
try:
    roll(root, root/'bin', 'manual', True, store)
except RuntimeError as e:
    assert str(e) == 'roll suite should belong to after-merge proof'
else:
    raise AssertionError('standalone roll skipped its verification')
PY`}
	l.afterLanding(ctx, commit("tools/tillr-drain/change"))
	waitAction(t, func() bool { return !l.rollAction.busy() })
	if _, err := os.Stat(filepath.Join(l.cfg.Root, "bin", "tillr-drain")); err != nil {
		t.Fatalf("roll did not install: %v\n%s", err, l.cfg.Report)
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		state, err := restart.ReadState(r, restart.RoleDrain)
		if err == nil && len(state.Pending) != 1 {
			t.Fatalf("roll did not request a restart: %+v\n%s", state, l.cfg.Report)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
