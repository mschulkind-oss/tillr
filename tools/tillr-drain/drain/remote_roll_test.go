package drain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func TestRemoteRebaseTriggersRollPaths(t *testing.T) {
	for _, tc := range []struct {
		path string
		roll bool
	}{
		{"tools/tillr-drain/x.txt", true},
		{"unwatched.txt", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			p := newPushFixture(t)
			rolled := filepath.Join(filepath.Dir(p.f.root), "rolled.txt")
			p.l.cfg.PostLanding.Roll = settings.Roll{
				Command: "touch " + rolled,
				Paths:   []string{"tools/tillr-drain/"},
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(p.other, tc.path)), 0o755); err != nil {
				t.Fatal(err)
			}
			remote := p.remoteCommit(t, tc.path, "upstream change\n")
			p.l.afterGreen(context.Background(), p.local[1])
			waitAction(t, &p.l.pushAction, &p.l.rollAction)
			if green := runGit(t, p.f.root, "rev-parse", GreenRef); runGit(t, p.f.root, "merge-base", remote, green) != remote {
				t.Fatal("the replay did not carry green across the upstream commit")
			}
			if _, err := os.Stat(rolled); (tc.roll && err != nil) || (!tc.roll && !os.IsNotExist(err)) {
				t.Fatalf("roll marker: %v; want present = %v", err, tc.roll)
			}
		})
	}
}

func TestRemoteRollSurvivesManualPushProcess(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	rolled := filepath.Join(filepath.Dir(p.f.root), "manual-rolled.txt")
	writeForkFile(t, filepath.Join(p.f.root, ".tillr.toml"), "[push]\nwhat = \"green\"\n[roll]\ncommand = "+strconv.Quote("echo rolled >> "+rolled)+"\npaths = [\"tools/tillr-drain/\"]\n")
	if err := os.MkdirAll(filepath.Join(p.other, "tools", "tillr-drain"), 0o755); err != nil {
		t.Fatal(err)
	}
	remote := p.remoteCommit(t, "tools/tillr-drain/remote.txt", "upstream tool\n")
	root := p.f.root
	// With no publication or tracking pointer, the manual push attempts publication
	// at the common base and gets a refusal, leaving green behind the replay.
	base := runGit(t, root, "rev-parse", "refs/remotes/origin/"+TrunkRef)
	runGit(t, root, "update-ref", GreenRef, base)
	runGit(t, root, "update-ref", "-d", "refs/remotes/origin/"+TrunkRef)
	bin := filepath.Join(t.TempDir(), "tillr-drain")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build push binary: %v\n%s", err, out)
	}
	push := exec.Command(bin, "push", "--root", root, "--store", p.l.cfg.Store)
	out, err := push.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !strings.Contains(string(out), pushKindProving) {
		t.Fatalf("manual push = %v, %s; want proof hold", err, out)
	}
	proved := runGit(t, root, "rev-parse", "HEAD")
	if runGit(t, root, "merge-base", remote, proved) != remote || runGit(t, root, "rev-parse", GreenRef) != base {
		t.Fatal("manual push did not rebase while leaving green at the common base")
	}

	// Start with fresh loop state after the manual command has exited.
	cfg := p.l.cfg
	cfg.PostLanding, err = settings.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	advance := func(tip string) {
		restarted.afterGreen(ctx, tip)
		waitAction(t, &restarted.pushAction, &restarted.rollAction)
	}
	advance("start")
	if _, err := os.Stat(rolled); !os.IsNotExist(err) {
		t.Fatalf("roll ran before proof: %v", err)
	}
	runGit(t, root, "update-ref", GreenRef, proved)
	// Proof, not a successful publication, makes the stored roll due.
	runGit(t, root, "remote", "set-url", "origin", filepath.Join(filepath.Dir(p.remote), "unavailable.git"))
	advance(proved)
	advance(proved)
	if out, err := os.ReadFile(rolled); err != nil || string(out) != "rolled\n" {
		t.Fatalf("deferred roll = %q, %v; want exactly one roll after proof", out, err)
	}
}
