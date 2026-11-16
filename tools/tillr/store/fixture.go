package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
)

// fixtureKey is the meta row that marks a store as a fixture. Only InitFixture
// writes it, so a store created by Open can never be seeded.
const fixtureKey = "fixture"

// ErrLiveStore is returned for a store that already exists and is not already a
// fixture. Marking one would make it seedable, which is the mistake §5.1 makes
// impossible rather than unlikely.
var ErrLiveStore = errors.New("store: refusing to mark an existing store as a fixture")

// InitFixture creates a store and marks it as a fixture. A store that already exists
// is only re-marked when it is already a fixture: stamping the mark onto a live store
// is the second command that could defeat "seeding a live store must be impossible",
// and it is refused before anything is written.
func InitFixture(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); err == nil {
		existing, err := OpenRead(ctx, path)
		if err != nil {
			return nil, err
		}
		isFixture := existing.IsFixture()
		existing.Close()
		if !isFixture {
			return nil, fmt.Errorf("%w: %s", ErrLiveStore, path)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("store: %w", err)
	}
	s, err := Open(ctx, path, true)
	if err != nil {
		return nil, err
	}
	if err := s.WriteMeta(fixtureKey, "1"); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// IsFixture reports whether this store was created as a fixture. A read error answers
// false, which is the safe direction for the one caller that gates a write on it: a
// store whose mark cannot be read is not a store to seed.
func (s *Store) IsFixture() bool {
	value, err := s.Meta(fixtureKey)
	return err == nil && value != nil && *value == "1"
}

// Seed imports a record bundle into a fixture store. Seeding a live store is the
// one mistake that cannot be undone, so the store refuses it rather than leaving
// the check to every caller.
//
// A record whose kind has a registered materializer is materialized through that
// utility's own record→row mapping — the same function its write face calls — so a
// fixture cannot construct a state the write face would reject, and cannot fail to
// construct one it allows (lanes §1, OQ-L2). That mapping appends the transition's
// event itself, in this transaction. A kind with no mapping describes no rows, so the
// seed appends its event and nothing else.
func (s *Store) Seed(ctx context.Context, bundle []byte) error {
	if !s.IsFixture() {
		return errors.New("store: refusing to seed a store that is not a fixture")
	}
	// The bundle is a bare {kind, payload} object, not an envelope: it is the input
	// to a store, not a record a store produced.
	var seed struct {
		Kind    string `json:"kind"`
		Payload struct {
			Records []json.RawMessage `json:"records"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(bundle, &seed); err != nil {
		return fmt.Errorf("store: reading the seed bundle: %w", err)
	}
	if seed.Kind != "fixture.seed" {
		return fmt.Errorf("store: a seed bundle is kind fixture.seed, not %q", seed.Kind)
	}
	payload := seed.Payload
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning the seed: %w", err)
	}
	// The marker event is what makes the fixture.seed registration true rather than
	// decorative: it records that this store was seeded, and how much. It is also the
	// only append the suite exercises on the happy path.
	marker, err := json.Marshal(map[string]int{"records": len(payload.Records)})
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("store: encoding the seed marker: %w", err)
	}
	if _, err := event.Append(ctx, tx, event.Event{
		Kind:    "fixture.seed",
		Payload: marker,
		At:      time.Now().UTC(),
	}); err != nil {
		tx.Rollback()
		return err
	}
	write := &Tx{ctx: ctx, tx: tx}
	for _, raw := range payload.Records {
		seeded, err := record.Decode(raw)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("store: reading a seed record: %w", err)
		}
		if materialize, ok := materializerFor(seeded.Kind); ok {
			if _, err := materialize(ctx, write, seeded); err != nil {
				tx.Rollback()
				return fmt.Errorf("store: materializing a %q record: %w", seeded.Kind, err)
			}
			continue
		}
		if _, err := event.Append(ctx, tx, event.Event{
			Kind:    seeded.Kind,
			Payload: seeded.Payload,
			At:      seeded.CreatedAt,
		}); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing the seed: %w", err)
	}
	return nil
}
