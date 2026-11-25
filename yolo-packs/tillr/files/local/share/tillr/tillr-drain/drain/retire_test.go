package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPlanNeedsOnlyPolicyAndStore(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle')`)
		return err
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answers, err := Plan(r, Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(answers) != 1 {
			t.Fatalf("answers: %v", answers)
		}
		var d Decision
		if err := json.Unmarshal(answers[0].Payload, &d); err != nil {
			return err
		}
		if d.From != "queued" || d.To != "running" {
			t.Fatalf("position/transition: %+v", d)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStopDetailSeparatesHoldsAndDeferrals(t *testing.T) {
	got := heldDetail([]Decision{
		{ItemID: "blocked", Reason: ReasonBlocked},
		{ItemID: "waiting", Reason: ReasonNoLane},
		{ItemID: "limited", Reason: ReasonAgentLimit, DeferredBy: "run-active-a1"},
		{ItemID: "fork", Reason: ReasonForkHeld, DeferredBy: "owner"},
	})
	for _, want := range []string{"1 held item(s): blocked", "3 deferred item(s): waiting", "limited (agent-limit behind run-active-a1)", "fork (fork-held behind owner)"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}

func TestUnusablePolicyFaultWritesNoPolicyHold(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy = nil
	result, err := l.Run(context.Background())
	if err == nil || result.Stop != StopFault {
		t.Fatalf("%+v / %v", result, err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE reason = 'no-policy'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("recorded %d policy holds", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTwoLoopsInOneSecondKeepBothGenerationReports(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?; INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES ('lane-1','leased','foreign',1)`, StateBlocked)
		return err
	})
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state = 'leased', run_id = 'foreign-2', leased_until = 1 WHERE lane_id = 'lane-1'`)
		return err
	})
	_, second := passFixtureForExisting(t, f)
	second.cfg.Now = l.cfg.Now
	if _, err := second.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(LoopDir(f.root), "drain-*.json"))
	if err != nil || len(files) != 2 {
		t.Fatalf("reports %v / %v", files, err)
	}
	for i, file := range files {
		want := []string{"drain-1-1.json", "drain-2-2.json"}[i]
		if filepath.Base(file) != want {
			t.Errorf("file %s, want %s", file, want)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		env, err := record.Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		if env.ID != strings.ReplaceAll(strings.TrimSuffix(want, ".json"), "drain-", "loop-") {
			t.Errorf("record id %s", env.ID)
		}
	}
}

func TestRecoveryRequiresTheBoundWorkerParentAndMaintenanceTree(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-b", "main")
	writeForkFile(t, filepath.Join(root, "candidate.txt"), "worker\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "worker")
	worker := runGit(t, root, "rev-parse", "HEAD")
	workerTree := runGit(t, root, "rev-parse", "HEAD^{tree}")
	writeForkFile(t, filepath.Join(root, "candidate.txt"), "prepared\n")
	runGit(t, root, "commit", "-qam", "maintenance")
	head := runGit(t, root, "rev-parse", "HEAD")
	tree := runGit(t, root, "rev-parse", "HEAD^{tree}")
	loop := &Loop{}
	valid := Candidate{ItemID: "item-target", Worktree: root, HeadSHA: worker, WorkerHeadSHA: worker, RetirementTreeSHA: tree}
	for _, tc := range []struct {
		name      string
		mutate    func(*Candidate)
		wantError string
	}{
		{"valid intent", func(*Candidate) {}, ""},
		{"wrong tree", func(c *Candidate) { c.RetirementTreeSHA = workerTree }, "does not match"},
		{"wrong worker", func(c *Candidate) { c.WorkerHeadSHA = head }, "no completed commit"},
		{"missing tree", func(c *Candidate) { c.RetirementTreeSHA = "" }, "incomplete"},
		{"missing worker", func(c *Candidate) { c.WorkerHeadSHA = "" }, "incomplete"},
		{"fork binding", func(c *Candidate) { c.BaseRef = "refs/heads/patch/fixture" }, "inapplicable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := valid
			tc.mutate(&c)
			got, err := loop.recoverRetirement(context.Background(), c)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("err=%v, want %s", err, tc.wantError)
				}
				return
			}
			if err != nil || got.HeadSHA != head {
				t.Fatalf("recovered=%+v, err=%v", got, err)
			}
		})
	}
	// An extra commit cannot borrow a valid tree binding, even when it is empty.
	runGit(t, root, "commit", "--allow-empty", "-m", "unbound extra commit")
	if _, err := loop.recoverRetirement(context.Background(), valid); err == nil {
		t.Fatal("accepted an unbound parent")
	}
	runGit(t, root, "reset", "--hard", worker)
	if _, err := loop.recoverRetirement(context.Background(), valid); err == nil || !strings.Contains(err.Error(), "no completed commit") {
		t.Fatalf("incomplete intent err=%v", err)
	}
}

func passFixtureForExisting(t *testing.T, f refusalLoopFixture) (refusalLoopFixture, *Loop) {
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	return f, l
}
