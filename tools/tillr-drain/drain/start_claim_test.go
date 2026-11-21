package drain

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func claimFixture(t *testing.T, l *Loop) (*startView, startSnapshot, startViewEntry) {
	t.Helper()
	v := newStartView()
	l.startView.Store(v)
	s := proposeFixture(t, l, l.cfg.Now(), startSnapshot{})
	v.current.Store(&s)
	e, ok := s.nextStart()
	if !ok || e.decision.RunRequest == nil || e.decision.LaneID != "lane-1" {
		t.Fatalf("expected a proposal for lane-1, got %+v (ok=%v)", e, ok)
	}
	return v, s, e
}

func TestFastStartHoldBeforeClaimCancelsStart(t *testing.T) {
	for _, change := range []string{"local hold", "fault", "criterion", "occupied lane", "closed backend"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a")
			v, s, e := claimFixture(t, l)
			switch change {
			case "local hold":
				l.holdLocal(ctx, "item-a", ReasonStagingFailed)
			case "fault":
				if err := l.holdItemFault(ctx, Candidate{ItemID: "item-a"}, ReasonDecisionCollision, "fixture fault"); err != nil {
					t.Fatal(err)
				}
			case "criterion":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE item_criteria SET text='amended criterion' WHERE item_id='item-a' AND criterion_id='c1'`)
					return err
				})
			case "closed backend":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO backend_windows VALUES ('fixture','weekly',1,'rejected',?,'refused')`, l.cfg.Now().Add(time.Hour).UnixMilli())
					return err
				})
			case "occupied lane":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := run.ClaimLease(ctx, tx, run.Request{RunID: "foreign", ItemID: "item-other", Backend: "fixture"}, "lane-1", l.cfg.Now())
					return err
				})
			}
			before := eventCount(t, f.storePath)
			ok, work, gen, _, err := l.claimFastStart(ctx, v, s, e, l.cfg.Now())
			if err != nil || ok || work != nil || gen != "" || eventCount(t, f.storePath) != before {
				t.Fatalf("declined claim changed the store or returned a claim: ok=%v work=%v gen=%q err=%v", ok, work, gen, err)
			}
		})
	}
}

func TestClaimFastStartReadsNoWholeLogAndFoldsNothingUnderTheWriteLock(t *testing.T) {
	f, l := passFixture(t, "item-a")
	v, s, e := claimFixture(t, l)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "later trunk")
	workerRecordFlood(t, f, 200)
	deltas, underWrite := 0, 0
	pendingDeltaFoldHook = func() {
		deltas++
		if store.HoldsWrite() {
			underWrite++
		}
	}
	t.Cleanup(func() { pendingDeltaFoldHook = nil })
	wholeBefore, foldsBefore := store.WholeLogReadsUnderWrite(), pendingFolds.Load()
	ok, work, gen, d, err := l.claimFastStart(context.Background(), v, s, e, l.cfg.Now())
	if err != nil || !ok || work == nil || gen == "" || d.ItemID != "item-a" || d.LaneID != "lane-1" || work.BaseSHA != s.tip {
		t.Fatalf("unexpected claim: ok=%v work=%+v gen=%q d=%+v err=%v", ok, work, gen, d, err)
	}
	if n := store.WholeLogReadsUnderWrite() - wholeBefore; n != 0 {
		t.Errorf("whole-log reads under the write lock: %d", n)
	}
	if n := pendingFolds.Load() - foldsBefore; n != 0 {
		t.Errorf("full pending folds during claim: %d", n)
	}
	if deltas == 0 || underWrite != 0 {
		t.Errorf("observed %d pending delta folds, %d under the write lock; want a read-side fold only", deltas, underWrite)
	}
}

func TestClaimFastStartOnlyOneWinner(t *testing.T) {
	for _, tc := range []struct{ name, item, lane string }{
		{"same lane", "item-b", "lane-1"},
		{"same item", "item-a", "lane-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t, "item-a", "item-b")
			l.cfg.Lanes = []string{"lane-1", "lane-2"}
			v, s, first := claimFixture(t, l)
			alternative := s
			alternative.items = []startQueueRow{{id: tc.item}}
			alternative.freeLanes = []string{tc.lane}
			second, ok := alternative.nextStart()
			if !ok || second.decision.ItemID != tc.item || second.decision.LaneID != tc.lane || second.decision.RunRequest == nil {
				t.Fatalf("unexpected competing proposal: %+v ok=%v", second, ok)
			}
			type result struct {
				ok  bool
				err error
			}
			ready, release, results := make(chan struct{}, 2), make(chan struct{}), make(chan result, 2)
			pendingDeltaFoldHook = func() {
				ready <- struct{}{}
				<-release
			}
			t.Cleanup(func() { pendingDeltaFoldHook = nil })
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			var claims sync.WaitGroup
			releaseClaims := sync.OnceFunc(func() { close(release) })
			defer func() { cancel(); releaseClaims(); claims.Wait() }()
			at := l.cfg.Now()
			for _, e := range []startViewEntry{first, second} {
				claims.Go(func() {
					ok, _, _, _, err := l.claimFastStart(ctx, v, s, e, at)
					results <- result{ok, err}
				})
			}
			// Both claims have sampled the same history head before either may write.
			for range 2 {
				select {
				case <-ready:
				case res := <-results:
					t.Fatalf("claim returned before the race: %+v", res)
				case <-ctx.Done():
					t.Fatal("claims did not reach the read boundary")
				}
			}
			releaseClaims()
			wins := 0
			for range 2 {
				select {
				case res := <-results:
					if res.err != nil {
						t.Error(res.err)
					}
					if res.ok {
						wins++
					}
				case <-ctx.Done():
					t.Fatal("claims did not finish")
				}
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				events, err := r.EventsOfKind(0, dispatchStartedKind)
				if err == nil && (wins != 1 || len(events) != 1) {
					t.Errorf("got %d winners and %d dispatches, want one of each", wins, len(events))
				}
				return err
			})
		})
	}
}
