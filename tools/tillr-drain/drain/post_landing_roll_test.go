package drain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-roll/roll"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRollHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ROLL_HELPER") != "post-landing" {
		return
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(root, "bin")
	storePath := os.Getenv("ROLL_FIXTURE_STORE")

	var steps []string
	deps := &roll.Deps{
		Stepped: func(step string, fn func() error) error {
			steps = append(steps, step)
			if step == "checkout" {
				return nil
			}
			return fn()
		},
		TestCommit: func(root, source, work, tip, store string, old map[string]roll.BuildFact, buildEnv []string) error {
			return errors.New("roll suite should belong to after-merge proof")
		},
		Run: func(args []string, opts roll.RunOpts) (roll.CmdResult, error) {
			if len(args) >= 2 && args[0] == "go" && args[1] == "build" {
				target := ""
				for i := 0; i+1 < len(args); i++ {
					if args[i] == "-o" {
						target = args[i+1]
						break
					}
				}
				if target == "" {
					t.Fatalf("missing -o in go build args: %v", args)
				}
				if err := os.WriteFile(target, []byte("build "+filepath.Base(target)), 0o755); err != nil {
					return roll.CmdResult{}, err
				}
				return roll.CmdResult{}, nil
			}
			if (len(args) == 2 && args[1] == "--help") || (len(args) >= 3 && args[1] == "loop" && args[2] == "--check") {
				info, err := os.Stat(args[0])
				if err != nil || !info.Mode().IsRegular() {
					t.Fatalf("expected regular file for %v: %v", args, err)
				}
				return roll.CmdResult{}, nil
			}
			return roll.Run(args, opts, nil)
		},
	}

	rc, err := roll.RollCommit(root, live, "landed", true, storePath, deps)
	if err != nil || rc != 0 {
		t.Fatalf("RollCommit(landed) = (%d, %v), want (0, nil)", rc, err)
	}
	if !slices.Contains(steps, "loop check") {
		t.Fatalf("expected 'loop check' in steps %v", steps)
	}
	got, err := os.ReadFile(filepath.Join(live, "tillr-drain"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "build tillr-drain" {
		t.Fatalf("live/tillr-drain = %q, want %q", string(got), "build tillr-drain")
	}

	if err := os.Unsetenv("TILLR_ROLL_VERIFICATION"); err != nil {
		t.Fatal(err)
	}
	if _, err := roll.RollCommit(root, live, "manual", true, storePath, deps); err == nil || !strings.Contains(err.Error(), "roll suite should belong to after-merge proof") {
		t.Fatalf("expected after-merge proof error for standalone roll, got %v", err)
	}
	os.Exit(0)
}

func TestAfterMergeLandingRollInstallsWithoutRepeatingProof(t *testing.T) {
	l, _, commit := actionFixture(t)
	ctx := context.Background()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROLL_FIXTURE_STORE", l.cfg.Store)
	l.gatePolicy.LandingStrategy = "after-merge"
	// Exercise the actual roll orchestration. Replace the external build and smoke
	// commands with fixtures; attempting a suite would fail instead of installing.
	l.cfg.PostLanding.Roll = settings.Roll{
		Paths:   []string{"tools/tillr*/"},
		Command: fmt.Sprintf("GO_WANT_ROLL_HELPER=post-landing %q -test.run='^TestRollHelperProcess$'", exe),
	}
	l.afterLanding(ctx, commit("tools/tillr-drain/change"))
	waitAction(t, &l.rollAction)
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
