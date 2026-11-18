package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

type orderSteps struct {
	recordingSteps
	t                      *testing.T
	root                   string
	store                  string
	gates, lands           int
	gateResult, landReason string
	onGate                 func(Candidate)
}

func (s *orderSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	if c.Worktree == s.root {
		s.t.Fatal("record was written in main checkout")
	}
	if got := runGit(s.t, c.Worktree, "diff", "--name-only", c.BaseSHA, c.HeadSHA); got != "docs/order.txt" {
		s.t.Fatalf("record-only candidate: %q", got)
	}
	if got := runGit(s.t, c.Worktree, "log", "-1", "--format=%s"); got != "docs: record the queue's order" {
		s.t.Fatal(got)
	}
	if s.onGate != nil {
		s.onGate(c)
	}
	result := s.gateResult
	if result == "" {
		result = GatePassed
	}
	return GateOutcome{Outcome: result}, nil
}
func (s *orderSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	if c.BaseRef != "refs/heads/main" || !strings.Contains(strings.Join(landArgs(c), " "), "--base-ref refs/heads/main") {
		s.t.Fatalf("maintenance landing does not explicitly target main: %+v args=%v", c, landArgs(c))
	}
	if s.landReason != "" {
		return LandOutcome{Result: LandRefused, Reason: s.landReason}, nil
	}
	trunk, err := land.OpenTrunk(s.root)
	if err != nil {
		return LandOutcome{}, err
	}
	db, err := store.Open(ctx, s.store, true)
	if err != nil {
		return LandOutcome{}, err
	}
	defer db.Close()
	out, err := land.Land(ctx, db, trunk, land.LandingRequest{ItemID: c.ItemID, RunID: c.RunID, BaseRef: c.BaseRef, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA, At: time.Now(), Verdict: land.Verdict{VerdictID: c.RunID + "-gate", BaseSHA: c.BaseSHA, TreeSHA: runGit(s.t, c.Worktree, "rev-parse", c.HeadSHA+"^{tree}"), Result: land.VerdictPass}})
	return LandOutcome{Result: out.Result, Reason: out.Reason, TrunkAfter: out.TrunkAfter}, err
}
func orderFixture(t *testing.T) (refusalLoopFixture, *Loop, *orderSteps, *time.Time) {
	t.Helper()
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	writeForkFile(t, filepath.Join(f.root, ".tillr.toml"), "[order]\nrecord='docs/order.txt'\nsettle_minutes=10\n")
	runGit(t, f.root, "add", ".tillr.toml")
	runGit(t, f.root, "commit", "-qm", "settings")
	steps := &orderSteps{t: t, root: f.root, store: f.storePath}
	l := f.loop(t, steps, &itemContentStager{})
	now := l.cfg.Now()
	l.cfg.Now = func() time.Time { return now }
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET rank=CASE item_id WHEN 'item-a' THEN 'a' ELSE 'b' END, state='blocked'")
		return err
	})
	return f, l, steps, &now
}
func moveOrder(t *testing.T, f refusalLoopFixture, at time.Time, id, to, via string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Place(context.Background(), tx, id, item.Destination{To: to}, at, "owner", via)
		return err
	})
}
func orderPass(t *testing.T, l *Loop) {
	t.Helper()
	if _, err := l.pass(context.Background(), &LoopResult{}); err != nil {
		t.Fatal(err)
	}
}
func TestOrderRecordPassSettlesAndLandsOnlyTheRecord(t *testing.T) {
	f, l, s, now := orderFixture(t)
	moveOrder(t, f, *now, "item-b", "first", "cli")
	orderPass(t, l)
	if s.gates != 0 {
		t.Fatal("unsettled move gated")
	}
	*now = now.Add(10 * time.Minute)
	orderPass(t, l)
	body, err := os.ReadFile(filepath.Join(f.root, "docs/order.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(body), "item-b\nitem-a\n") || s.gates != 1 || s.lands != 1 {
		t.Fatalf("record=%s gates=%d lands=%d", body, s.gates, s.lands)
	}
	if got := runGit(t, f.root, "status", "--porcelain"); got != "" {
		t.Fatalf("main dirty: %s", got)
	}
	orderPass(t, l)
	if s.gates != 1 {
		t.Fatal("recorded move generated another candidate")
	}
}
func TestOrderRecordImportsAndLandingsDoNotTrigger(t *testing.T) {
	f, l, s, now := orderFixture(t)
	moveOrder(t, f, *now, "item-b", "first", "sync")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state='landed' WHERE item_id='item-a'")
		return err
	})
	*now = now.Add(time.Hour)
	orderPass(t, l)
	if s.gates != 0 {
		t.Fatal("sync or landing generated record")
	}
}
func TestOrderRecordRefusalRetriesAfterHourOrFurtherMove(t *testing.T) {
	for _, step := range []string{"gate", "land"} {
		for _, retry := range []string{"hour", "move"} {
			t.Run(step+"/"+retry, func(t *testing.T) {
				f, l, s, now := orderFixture(t)
				moveOrder(t, f, *now, "item-b", "first", "cli")
				*now = now.Add(10 * time.Minute)
				if step == "gate" {
					s.gateResult = GateFailed
				} else {
					s.landReason = "trunk-worktree-uncommitted"
				}
				orderPass(t, l)
				before := itemStates(t, f.storePath)
				*now = now.Add(59 * time.Minute)
				orderPass(t, l)
				if s.gates != 1 {
					t.Fatal("retry before one hour")
				}
				// Retry state is durable, not a Loop field.
				replacement := f.loop(t, s, &itemContentStager{})
				replacement.cfg.Now = l.cfg.Now
				if retry == "hour" {
					*now = now.Add(time.Minute)
				} else {
					moveOrder(t, f, *now, "item-a", "first", "cli")
					*now = now.Add(10 * time.Minute)
				}
				orderPass(t, replacement)
				if s.gates != 2 {
					t.Fatalf("gates=%d", s.gates)
				}
				if got := itemStates(t, f.storePath); got != before {
					t.Fatalf("refusal changed items: %s -> %s", before, got)
				}
				reader := openFixture(t, f.storePath)
				defer reader.Close()
				if err := reader.ReadTx(context.Background(), func(r *store.Reader) error {
					var n int
					err := r.QueryRow("SELECT count(*) FROM dispatches WHERE action='order-record-refused' AND reason<>''").Scan(&n)
					if n != 2 {
						t.Errorf("refusal decisions=%d", n)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func itemStates(t *testing.T, path string) string {
	t.Helper()
	s := openFixture(t, path)
	defer s.Close()
	result := ""
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query("SELECT item_id,state FROM items ORDER BY item_id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				return err
			}
			result += id + ":" + state + ";"
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestOrderRecordInFlightClaimExcludesSecondLoop(t *testing.T) {
	f, l, s, now := orderFixture(t)
	moveOrder(t, f, *now, "item-b", "first", "cli")
	*now = now.Add(10 * time.Minute)
	s.onGate = func(c Candidate) {
		other := f.loop(t, s, &itemContentStager{})
		other.cfg.Now = l.cfg.Now
		orderPass(t, other)
	}
	orderPass(t, l)
	if s.gates != 1 || s.lands != 1 {
		t.Fatalf("duplicate candidate: gates=%d lands=%d", s.gates, s.lands)
	}
}
