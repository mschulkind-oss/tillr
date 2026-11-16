package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestChangedHoldAnswersDoNotRecordAnUnappliedPass(t *testing.T) {
	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state = 'blocked'`); return err })
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if i == 2 {
			fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state = 'landed'`); return err })
		}
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Second)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n, ended, holds int
		if err := r.QueryRow(`SELECT count(*), count(ended_at), COALESCE(sum(json_extract(decision_counts, '$.hold')), 0) FROM drain_passes`).Scan(&n, &ended, &holds); err != nil {
			return err
		}
		if n != 0 || ended != 0 || holds != 0 {
			t.Errorf("unapplied pass wrote timing rows/counts = %d/%d/%d", n, ended, holds)
		}
		if err := r.QueryRow(`SELECT count(*) FROM dispatches d JOIN drain_passes t ON t.tick_id = d.pass_id`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("unapplied pass wrote %d correlated decisions", n)
		}
		if err := r.QueryRow(`SELECT count(*) FROM drain_sessions`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("unapplied pass wrote %d sessions; heartbeats belong to Run", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPassWithNoItemsRecordsNoAnswer(t *testing.T) {
	f, l := passFixture(t)
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), &LoopResult{}); err != nil {
			t.Fatal(err)
		}
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM drain_passes`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("empty answer recorded %d times, want 0", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
