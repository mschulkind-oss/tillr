package drain

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRefusedCandidateSizeUsesRecordedCommits(t *testing.T) {
	f, l := passFixture(t, "item-a")
	root := f.root
	const packFile = "yolo-packs/tillr/files/local/share/tillr/tillr/main.go"
	write := func(path, content string) {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, path, content)
	}
	commit := func(message string) string {
		runGit(t, root, "add", "--all")
		runGit(t, root, "commit", "-qm", message)
		return runGit(t, root, "rev-parse", "HEAD")
	}
	write("old\tname\n", "rename me\n")
	write("removed", "delete me\n")
	base := commit("measurement base")
	write(packFile, "generated\ncopy\n")
	packHead := commit("only generated pack")
	runGit(t, root, "mv", "old\tname\n", "new\nname\t")
	runGit(t, root, "rm", "removed")
	write("source\twith\nwhitespace", "one\ntwo\n")
	write("binary", "\x00\xff")
	write("yolo-packs/tillr/pack.json", "tracked configuration\n")
	head := commit("candidate")
	write("later", "committed after judged head\n")
	commit("unrelated movement")
	write("staged", "index only\n")
	runGit(t, root, "add", "staged")
	write("unstaged", "worktree only\n")

	c := Candidate{ItemID: "item-a", LaneID: "lane-1", Worktree: root, BaseSHA: base, HeadSHA: head, VerifyReplan: true, RefusalDetail: "unchanged detail"}
	assertRefusal := func(id, step, want string) {
		t.Helper()
		c.RunID = id
		if err := l.recordRefusal(context.Background(), c, step); err != nil {
			t.Fatal(err)
		}
		p := candidateSizeEvent(t, l, id)
		assertCandidateSize(t, p, want)
		if step == RefusalVerifyNotDone && (string(p["detail"]) != `"unchanged detail"` || string(p["replan"]) != "true") {
			t.Errorf("refusal changed: %s", p)
		}
		if want != "" && p["candidate_size_error"] != nil {
			t.Errorf("unexpected measurement error: %s", p["candidate_size_error"])
		}
		if id == "unavailable" && !strings.Contains(string(p["candidate_size_error"]), "missing-base") {
			t.Errorf("missing diagnostic for unavailable measurement: %s", p)
		}
		if id == "fence" && p["candidate_size_error"] != nil {
			t.Errorf("unrelated refusal tried to measure: %s", p)
		}
	}
	assertRefusal("candidate", RefusalVerifyNotDone, `{"files":5,"added_lines":3,"file_limit":27,"added_line_limit":1200}`)
	c.HeadSHA = packHead
	assertRefusal("pack-only", RefusalVerifyNotDone, `{"files":0,"added_lines":0,"file_limit":27,"added_line_limit":1200}`)

	// The same path in an independent fork is source, not our generated copy.
	root = t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "commit", "--allow-empty", "-qm", "fork base")
	c.BaseSHA = runGit(t, root, "rev-parse", "HEAD")
	write(packFile, "fork source\n")
	c.HeadSHA = commit("fork candidate")
	c.Worktree, c.BaseRef = root, "refs/heads/patch/test"
	assertRefusal("fork", RefusalVerifyNotDone, `{"files":1,"added_lines":1,"file_limit":27,"added_line_limit":1200}`)

	c.BaseSHA = "missing-base"
	assertRefusal("unavailable", RefusalVerifyNotDone, "")
	assertRefusal("fence", RefusalFence, "")
}

func TestVerifyCandidateSizeDoesNotChangeReturn(t *testing.T) {
	for _, reason := range []string{verifyFinding, "plan-wrong: the pieces cannot cover c2"} {
		replan := strings.HasPrefix(reason, PlanWrongPrefix)
		name := "build"
		if replan {
			name = "replan"
		}
		t.Run(name, func(t *testing.T) {
			l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
				return &verifyReturnSteps{workflowSteps: w, verdict: reason}
			})
			writeForkFile(t, filepath.Join(f.root, ".tillr.toml"), "[candidate_size]\nfile_limit = 9\nadded_line_limit = 80\n")
			cfg, err := settings.Read(f.root)
			if err != nil {
				t.Fatal(err)
			}
			l.cfg.CandidateSizeLimits = &cfg.CandidateSize
			runVerifyReturn(t, f, l, s, func() bool { return s.lands >= 1 })
			wantPlans, wantRepairs := 1, 1
			if replan {
				wantPlans, wantRepairs = 2, 0
			}
			if s.plans != wantPlans || len(s.repairs) != wantRepairs || s.verifies != 2 || s.gates != 1 || s.lands != 1 {
				t.Fatalf("plans=%d repairs=%d verifies=%d gates=%d lands=%d", s.plans, len(s.repairs), s.verifies, s.gates, s.lands)
			}
			if !replan && (s.repairHead[0] != s.verified || !strings.Contains(s.repairText[0], reason)) {
				t.Fatal("repair did not receive the refused head and original finding")
			}
			p := candidateSizeEvent(t, l, s.verifiedRun)
			assertCandidateSize(t, p, `{"files":1,"added_lines":1,"file_limit":9,"added_line_limit":80}`)
			if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='drain.refusal' AND json_type(payload, '$.candidate_size') IS NOT NULL`); n != 1 {
				t.Errorf("size observations = %d, want one for the refused candidate only", n)
			}
		})
	}
}

func assertCandidateSize(t *testing.T, p map[string]json.RawMessage, want string) {
	t.Helper()
	raw := p["candidate_size"]
	if want == "" {
		if raw != nil {
			t.Errorf("unexpected size: %s", raw)
		}
		return
	}
	var got, expected map[string]int64
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil || !maps.Equal(got, expected) {
		t.Errorf("size = %s, %v; want %s", raw, err, want)
	}
}

func candidateSizeEvent(t *testing.T, l *Loop, runID string) map[string]json.RawMessage {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := l.readStore(context.Background(), func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=?`, RefusalKind, runID).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &payload)
	}); err != nil {
		t.Fatal(err)
	}
	return payload
}
