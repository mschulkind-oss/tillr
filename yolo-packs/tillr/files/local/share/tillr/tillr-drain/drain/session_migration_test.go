package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestInitializedSessionHeartbeatDoesNotRemigrate(t *testing.T) {
	f, l := passFixture(t)
	if err := l.refreshSession(context.Background(), l.cfg.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TABLE migration_probe(n INTEGER NOT NULL); INSERT INTO migration_probe VALUES(0); CREATE TRIGGER count_migrations BEFORE INSERT ON schema_migrations BEGIN UPDATE migration_probe SET n=n+1; END;`)
		return err
	})
	if err := l.refreshSession(context.Background(), l.cfg.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	l.endSession()
	s, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT n FROM migration_probe`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("initialized heartbeat/end repeated migration %d times", n)
		}
		var ended int
		if err := r.QueryRow(`SELECT count(*) FROM drain_sessions WHERE ended_at IS NOT NULL`).Scan(&ended); err != nil {
			return err
		}
		if ended != 1 {
			t.Errorf("session end not persisted: %d", ended)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
