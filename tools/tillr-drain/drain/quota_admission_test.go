package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestQuotaRevalidatedAtClaim(t *testing.T) {
	f, l := passFixture(t, "item-a")
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }
	payload := filepath.Join(f.root, "quota.json")
	writeForkFile(t, payload, `{"windows":[{"kind":"five_hour","fraction":0,"reset_at":null}]}`)
	b := l.cfg.Policy.Backends["fixture"]
	b.Quota = []string{"/bin/cat", payload}
	l.cfg.Policy.Backends["fixture"] = b
	p := mustPlan(t, l)[0]
	at = at.Add(QuotaProbeInterval + time.Second)
	writeForkFile(t, payload, `{"windows":[{"kind":"five_hour","fraction":1,"reset_at":"2099-11-18T00:00:00Z"}]}`)
	ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), at)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("claimed dispatch using an expired reading")
	}
}

func TestQuotaChangedBetweenPlanAndClaim(t *testing.T) {
	f, l := passFixture(t, "item-a")
	enableFixtureQuota(l, "fixture")
	p := mustPlan(t, l)[0]
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE backend_quota SET windows='[{"kind":"five_hour","fraction":1,"reset_at":"2099-11-18T00:00:00Z"}]'`)
		return err
	})
	ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("claimed dispatch after latest reading closed backend")
	}
}

type quotaClosingStager struct {
	Stager
	at      *time.Time
	payload string
}

func (s quotaClosingStager) Stage(ctx context.Context, spec StageSpec) error {
	if err := s.Stager.Stage(ctx, spec); err != nil {
		return err
	}
	*s.at = s.at.Add(QuotaProbeInterval + time.Second)
	return os.WriteFile(s.payload, []byte(`{"windows":[{"kind":"five_hour","fraction":1,"reset_at":"2099-11-18T00:00:00Z"}]}`), 0600)
}

func TestClaimedDispatchStartsAfterQuotaClosesDuringStaging(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }
	payload := filepath.Join(f.root, "quota.json")
	writeForkFile(t, payload, `{"windows":[{"kind":"five_hour","fraction":0,"reset_at":null}]}`)
	b := l.cfg.Policy.Backends["fixture"]
	b.Quota = []string{"/bin/cat", payload}
	l.cfg.Policy.Backends["fixture"] = b
	l.cfg.Stage = quotaClosingStager{l.cfg.Stage, &at, payload}
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if result.Dispatched != 1 {
		t.Fatalf("claimed dispatch was canceled after staging: %+v", result)
	}
}

func TestStartIdleWaitsForQuotaRefreshPublication(t *testing.T) {
	f, l := passFixture(t, "item-a")
	enableFixtureQuota(l, "fixture")
	ctx := context.Background()
	at, policy, tick := l.cfg.Now(), l.currentPolicy(), l.startViewPass()
	var refresh startRefresh
	defer refresh.store.close()
	read := func(prev startSnapshot) startSnapshot {
		t.Helper()
		next, err := refresh.read(ctx, at, policy, tick, f.root, nil, "", prev, 0)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	stale := read(startSnapshot{})
	if !stale.quotaShut["fixture"] {
		t.Fatal("missing quota reading should initially hold the backend")
	}
	view := newStartView()
	view.current.Store(&stale)
	if err := l.refreshQuotas(ctx); err != nil {
		t.Fatal(err)
	}
	if idle, _, err := l.startIdle(ctx, view, nil); err != nil || idle {
		t.Fatalf("idle=%v err=%v with a fresh admitting reading and a stale start view", idle, err)
	}
	fresh := read(stale)
	if _, ok := fresh.nextFreshStart(); !ok {
		t.Fatal("published quota reading did not make the queued item dispatchable")
	}
}

type quotaDesignerSteps struct{ *commentSteps }

func (s *quotaDesignerSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	rec, log, err := s.commentSteps.Run(ctx, req)
	if rec.ExitCode != nil && *rec.ExitCode != 0 {
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE backend_quota SET windows='[{"kind":"five_hour","fraction":1,"reset_at":"2099-11-18T00:00:00Z"}]'`)
			return err
		})
	}
	return rec, log, err
}

func TestFailedDesignerReplacementWaitsForQuotaAcrossRestart(t *testing.T) {
	f, l, steps := commentFixture(t, "")
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	takeFixtureComment(t, f, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	if _, err := l.Run(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(f.root, ".vantage/inbox/*.jsonl"))
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	steps.resumeFailureSession = steps.sessions[0]
	number = 2
	id := takeFixtureComment(t, f, server.URL)
	newLoop := func() *Loop {
		stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
		next := f.loop(t, &quotaDesignerSteps{steps}, stage)
		next.cfg.Policy.Seats = map[string]string{"design": "fixture"}
		next.cfg.Policy.Backends["fixture"] = Backend{Adapter: "fixture", Model: "fixture-model"}
		next.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s\n' '{"index":{"documents":[{"path":"docs/design/example.md","stage":"DESIGN","questions":[]}]}}'`}
		enableFixtureQuota(next, "fixture")
		return next
	}
	l = newLoop()
	if _, err := l.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(steps.sessions) != 2 || steps.sessions[1] != steps.sessions[0] {
		t.Fatalf("expected the first session and its failed resume, with no replacement while quota is closed; got %v", steps.sessions)
	}
	found := false
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == id {
			found = true
			if p.decision.Reason != ReasonBackendWindow {
				t.Fatalf("replacement not durably held: %+v", p.decision)
			}
		}
	}
	if !found {
		t.Fatal("held comment missing")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE backend_quota SET windows='[{"kind":"five_hour","fraction":0,"reset_at":null}]'`)
		return err
	})
	l = newLoop()
	if _, err := l.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(steps.sessions) != 3 || steps.sessions[2] == steps.sessions[0] {
		t.Fatalf("reopening lost fresh replacement: %v", steps.sessions)
	}
}
