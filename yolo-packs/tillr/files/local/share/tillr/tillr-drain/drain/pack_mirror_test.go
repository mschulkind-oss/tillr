package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

type packMirrorGate struct {
	Steps
	gate func(Candidate) (GateOutcome, error)
}

func (s packMirrorGate) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	return s.gate(c)
}

func writePackFixture(t *testing.T, root, path, body string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, body)
}

func TestGateSeesRegeneratedTillrPackMirror(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	lane := l.laneTarget("lane-1")
	const source = "tools/tillr-drain/"
	const mirror = "yolo-packs/tillr/files/local/share/tillr/tillr-drain/"
	for _, path := range []string{".justfile", "tools/pack-mirror-links.py"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		writePackFixture(t, f.root, path, string(body))
	}
	for path, body := range map[string]string{
		"yolo-packs/tillr/pack.json": "{}\n",
		source + "script":            "old",
		mirror + "script":            "old",
		source + "removed":           "old",
		mirror + "removed":           "old",
	} {
		writePackFixture(t, f.root, path, body)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "pack fixture")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, lane, "reset", "--hard", base)
	writeForkFile(t, filepath.Join(lane, source, "script"), "new")
	if err := os.Chmod(filepath.Join(lane, source, "script"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, source, "README.md"), "[Doc](../../docs/work.md)\n")
	runGit(t, lane, "rm", source+"removed")
	runGit(t, lane, "add", source)
	runGit(t, lane, "commit", "-m", "feat: update drain source only")
	worker := runGit(t, lane, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), source+"\n")
	c := Candidate{ItemID: "item-a", LaneID: "lane-1", RunID: "run-pack-mirror", Worktree: lane, BaseSHA: base, HeadSHA: worker}

	gateStop := errors.New("stop after checking the gate input")
	calls := 0
	l.cfg.Steps = packMirrorGate{Steps: l.cfg.Steps, gate: func(c Candidate) (GateOutcome, error) {
		calls++
		if c.WorkerHeadSHA != worker || c.HeadSHA != runGit(t, lane, "rev-parse", "HEAD") || c.HeadSHA == worker {
			t.Fatalf("gate did not receive a committed maintenance tree bound to the worker: %+v", c)
		}
		if got := runGit(t, lane, "show", c.HeadSHA+":"+mirror+"script"); got != "new" {
			t.Fatalf("gate saw stale mirror: %q", got)
		}
		if got := runGit(t, lane, "ls-tree", c.HeadSHA, mirror+"script"); !strings.HasPrefix(got, "100755 ") {
			t.Fatalf("gate saw wrong mirror mode: %q", got)
		}
		if got := runGit(t, lane, "ls-tree", c.HeadSHA, mirror+"removed"); got != "" {
			t.Fatalf("gate saw deleted source in mirror: %q", got)
		}
		if got := runGit(t, lane, "show", c.HeadSHA+":"+mirror+"README.md"); got != "[Doc](https://github.com/mschulkind-oss/tillr/blob/main/docs/work.md)" {
			t.Fatalf("gate saw wrong mirror link: %q", got)
		}
		if got := runGit(t, lane, "status", "--porcelain"); got != "" {
			t.Fatalf("gate saw uncommitted preparation: %s", got)
		}
		return GateOutcome{}, gateStop
	}}
	if _, _, err := l.verify(ctx, &c, &LoopResult{}, nil); !errors.Is(err, gateStop) || calls != 1 {
		t.Fatalf("first gate: calls=%d, err=%v", calls, err)
	}
	if retired, err := l.retiredOriginal(ctx, c.RunID); retired || err != nil {
		t.Fatalf("mirror-only maintenance falsely recorded retirement: %v, %v", retired, err)
	}
	// Re-enter with the pre-commit intent that a replacement drain loads.
	var saved Candidate
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, candidatePreparedKind, c.RunID).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &saved)
	}); err != nil {
		t.Fatal(err)
	}
	if saved.HeadSHA != worker || saved.RetirementTreeSHA != runGit(t, lane, "rev-parse", c.HeadSHA+"^{tree}") {
		t.Fatalf("missing pre-commit binding: %+v", saved)
	}
	if _, _, err := l.verify(ctx, &saved, &LoopResult{}, nil); !errors.Is(err, gateStop) || calls != 2 || saved.HeadSHA != c.HeadSHA {
		t.Fatalf("recovered gate: calls=%d, head=%s, err=%v", calls, saved.HeadSHA, err)
	}
}
