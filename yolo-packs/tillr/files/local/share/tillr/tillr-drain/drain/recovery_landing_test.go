package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Only the gate is a fixture. Landing, criterion verdicts, item-state changes,
// and lane cleanup go through the production processes in both paths.
type recoveryLandingSteps struct {
	*ProcessSteps
	t *testing.T
}

func (s *recoveryLandingSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	tree := runGit(s.t, c.Worktree, "rev-parse", c.HeadSHA+"^{tree}")
	payload := map[string]any{"verdict_id": "fixture-verdict", "plan_id": "fixture-plan", "registry_digest": "sha256:fixture", "base_sha": c.BaseSHA, "tree_sha": tree, "result": "pass", "checks": []any{}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return GateOutcome{}, err
	}
	body, err := record.Encode(record.Envelope{RecordVersion: record.SupportedVersion, Kind: GateVerdictKind, ID: "fixture-verdict", Producer: record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"}, CreatedAt: s.Now(), Payload: raw})
	if err != nil {
		return GateOutcome{}, err
	}
	path := filepath.Join(RunDir(s.Root, c.RunID), GateVerdictRecord)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return GateOutcome{}, err
	}
	if err := os.WriteFile(path, body, 0644); err != nil {
		return GateOutcome{}, err
	}
	err = appendJudgment(ctx, s.Store, GateVerdictKind, "run", c.RunID, payload, s.Now())
	return GateOutcome{Outcome: GatePassed}, err
}

func TestExamineLandsRealFixtureWithTheLoopsRecords(t *testing.T) {
	root := scratchUnderDurable(t)
	config := filepath.Join(root, "gitconfig")
	writeForkFile(t, config, "[user]\n name = tillr\n email = tillr@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	for _, module := range []string{"tillr-land", "tillr-item"} {
		declareSiblingInputs(t)
		cmd := exec.Command("go", "build", "-o", filepath.Join(root, module), ".")
		cmd.Dir = filepath.Join("..", "..", module)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", module, err, out)
		}
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-loop", "tillr-loop"))
	if err != nil {
		t.Fatal(err)
	}
	var baseline []string
	for _, manual := range []bool{false, true} {
		p := newPendingTerminalFixture(t, true, "")
		seedRefRun(t, p.f, "earlier-attempt", "item-a", 1)
		for _, ref := range []string{"refs/tillr/runs/earlier-attempt", "refs/tillr/runs/" + p.runID, "refs/tillr/held/item-a"} {
			runGit(t, p.f.root, "update-ref", ref, p.head)
		}
		runGit(t, p.f.root, "update-ref", "refs/tillr/recovery/operator", p.base)
		runGit(t, p.f.root, "update-ref", GreenRef, p.base)
		writeForkFile(t, filepath.Join(p.f.root, "sibling.txt"), "concurrent landing\n")
		runGit(t, p.f.root, "add", "sibling.txt")
		runGit(t, p.f.root, "commit", "-qm", "concurrent trunk change")
		l := p.replacement(t, &terminalSteps{})
		steps := &recoveryLandingSteps{t: t, ProcessSteps: &ProcessSteps{Root: p.f.root, Store: p.f.storePath, LaneRoot: l.cfg.LaneRoot, Now: l.cfg.Now, ItemBin: filepath.Join(root, "tillr-item"), LandProgram: "python3 " + adapter + " land --store " + p.f.storePath + " --tillr-land " + filepath.Join(root, "tillr-land"), VerdictProgram: "python3 " + adapter + " verdict"}}
		l.cfg.Steps = steps
		var result LoopResult
		if manual {
			result, err = l.Examine(context.Background(), p.runID)
		} else {
			result, err = l.Run(context.Background())
		}
		if err != nil || result.Landed != 1 {
			t.Fatalf("manual=%v: %+v / %v", manual, result, err)
		}
		if content := runGit(t, p.f.root, "show", "main:base.txt"); content != "candidate" {
			t.Fatalf("not landed: %s", content)
		}
		refs := strings.Fields(runGit(t, p.f.root, "for-each-ref", "--format=%(refname)", "refs/tillr"))
		for _, ref := range refs {
			if ref != GreenRef && ref != "refs/tillr/held/item-a" && ref != "refs/tillr/recovery/operator" &&
				ref != pinRef("earlier-attempt") && ref != pinRef(p.runID) && !strings.HasPrefix(ref, "refs/tillr/trunk-history/") {
				t.Fatalf("drained queue left an obsolete ref: %s (all: %v)", ref, refs)
			}
		}
		for _, ref := range []string{pinRef("earlier-attempt"), pinRef(p.runID)} {
			if got := runGit(t, p.f.root, "rev-parse", ref); got != p.head {
				t.Fatalf("off-trunk pin %s = %s, want %s", ref, got, p.head)
			}
		}
		if got := runGit(t, p.f.root, "rev-parse", "refs/tillr/held/item-a"); got != p.head {
			t.Fatal("held ref changed")
		}
		if got := runGit(t, p.f.root, "rev-parse", "refs/tillr/recovery/operator"); got != p.base {
			t.Fatal("recovery ref changed")
		}
		runGit(t, p.f.root, "cat-file", "-e", p.head)
		if strings.Contains(runGit(t, p.f.root, "rev-list", TrunkRef), p.head) {
			t.Fatal("run commit is an ancestor: fixture did not squash")
		}
		var facts []string
		fixtureWrite(t, p.f, func(tx *store.Tx) error {
			var state, criteria, result, gated, landed string
			if err := tx.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT group_concat(state,',') FROM item_criteria WHERE item_id='item-a'`).Scan(&criteria); err != nil {
				return err
			}
			if err := tx.QueryRow(`SELECT result,gated_tree,landed_tree FROM landings WHERE run_id=?`, p.runID).Scan(&result, &gated, &landed); err != nil {
				return err
			}
			facts = []string{state, criteria, result, gated, landed}
			return nil
		})
		for _, e := range eventsAfter(t, p.f.storePath, 0) {
			switch e.kind {
			case GateVerdictKind, "land.landing", "item.verdict", examinationDoneKind:
				facts = append(facts, e.kind)
			}
		}
		if !manual {
			baseline = facts
		} else if !reflect.DeepEqual(facts, baseline) {
			t.Fatalf("recovery records %v != loop %v", facts, baseline)
		}
	}
}

type maintenanceOverlapSteps struct {
	terminalSteps
	t           *testing.T
	f           refusalLoopFixture
	markerReady chan struct{}
	releaseLand chan struct{}
	onSync      func()
}

func (s *maintenanceOverlapSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	before := runGit(s.t, s.f.root, "rev-parse", TrunkRef)
	runGit(s.t, s.f.root, "commit", "--allow-empty", "-qm", "landed "+c.RunID)
	after := runGit(s.t, s.f.root, "rev-parse", TrunkRef)
	runGit(s.t, s.f.root, "update-ref", "refs/tillr/landed/"+c.RunID, after)
	close(s.markerReady)
	<-s.releaseLand
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO landings (landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after) VALUES (?,?,?,'fixture-verdict','landed',?,?,?,?)`,
			"landing-"+c.RunID, c.ItemID, c.RunID, c.BaseSHA, c.HeadSHA, before, after)
		return err
	})
	return LandOutcome{Result: LandLanded}, nil
}

func (s *maintenanceOverlapSteps) ItemState(_ context.Context, itemID, state, _ string, _ ...item.WaitingOn) error {
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=?, updated_at=updated_at+1 WHERE item_id=?`, state, itemID)
		return err
	})
	return nil
}

func (s *maintenanceOverlapSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	if s.onSync != nil {
		s.onSync()
	}
	return s.terminalSteps.Sync(ctx)
}

func TestDrainedPassSweepsLandedMarkerCompletedDuringMaintenance(t *testing.T) {
	ctx := context.Background()
	p := newPendingTerminalFixture(t, true, "")
	markerReady := make(chan struct{})
	releaseLand := make(chan struct{})
	var l *Loop
	var once sync.Once
	steps := &maintenanceOverlapSteps{
		t:           t,
		f:           p.f,
		markerReady: markerReady,
		releaseLand: releaseLand,
		onSync: func() {
			once.Do(func() {
				// Release the landing after Pass 2's top-of-pass ref sweep has
				// already run, then wait for the examination goroutine to queue
				// its completion before Pass 2's post-maintenance drain.
				close(releaseLand)
				l.examinations.Wait()
			})
		},
	}
	l = p.replacement(t, steps)
	l.syncedSHA = p.base
	var result LoopResult
	if stop, err := l.pass(ctx, &result); err != nil || stop != "" {
		t.Fatalf("first pass: stop=%q err=%v", stop, err)
	}
	select {
	case <-markerReady:
	case <-time.After(hangGuard):
		t.Fatal("examination did not reach landing")
	}
	stop := ""
	for passes := 0; stop == ""; passes++ {
		if passes > 5 {
			t.Fatalf("loop did not drain after %d passes", passes)
		}
		var err error
		stop, err = l.pass(ctx, &result)
		if err != nil {
			t.Fatalf("pass %d: %v", passes+2, err)
		}
	}
	if stop != StopQueueDrained || result.Landed != 1 {
		t.Fatalf("stop=%q landed=%d, want %q and 1", stop, result.Landed, StopQueueDrained)
	}
	marker := "refs/tillr/landed/" + p.runID
	if refExists(t, p.f, marker) {
		t.Fatalf("drained queue left an obsolete ref: %s", marker)
	}
}
