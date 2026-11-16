package store_test

// The pilot: the substrate's domain-access pattern proven on item's tables, the
// tables its first consumer needs (lanes §1). The DDL here is a *pilot* and is
// deliberately not compiled into the store package: ITEM-1 registers the real item
// DDL from the item module's own init(), and a store the substrate alone created must
// have no items table. What this file proves is the pattern — per-owner DDL
// registration, a write transaction that appends the transition's event, a domain-row
// read surface, and one record→row mapping shared by the write face and the seed
// materializer.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// itemPilotDDL is item's three tables (design §5.1's logical ownership table), with
// the foreign keys that make a row unable to name an item that does not exist.
const itemPilotDDL = `
CREATE TABLE IF NOT EXISTS items (
  item_id    TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  body_ref   TEXT,
  state      TEXT NOT NULL,
  design_doc TEXT,
  updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS item_criteria (
  item_id      TEXT NOT NULL REFERENCES items(item_id),
  criterion_id TEXT NOT NULL,
  text         TEXT NOT NULL,
  state        TEXT NOT NULL,
  PRIMARY KEY (item_id, criterion_id)
);

CREATE TABLE IF NOT EXISTS item_edges (
  item_id    TEXT NOT NULL REFERENCES items(item_id),
  blocked_by TEXT NOT NULL REFERENCES items(item_id),
  PRIMARY KEY (item_id, blocked_by)
);
`

// itemPilotStates is the writable state vocabulary the pilot accepts. A value outside
// it is malformed input, which is the rule the seed path must not be able to bypass.
var itemPilotStates = map[string]bool{
	"not-started": true, "in-progress": true, "blocked": true, "in-queue": true,
	"landed": true, "resolved-elsewhere": true, "closed": true,
}

// itemPilotPayload is the `item.record` payload the pilot reads and writes.
type itemPilotPayload struct {
	ItemID    string   `json:"item_id"`
	Title     string   `json:"title"`
	BodyRef   string   `json:"body_ref"`
	State     string   `json:"state"`
	BlockedBy []string `json:"blocked_by"`
	DesignDoc string   `json:"design_doc"`
	UpdatedAt string   `json:"updated_at"`
	EventSeq  int64    `json:"event_seq,omitempty"`
	Criteria  []struct {
		CriterionID string `json:"criterion_id"`
		Text        string `json:"text"`
		State       string `json:"state"`
	} `json:"criteria"`
}

func init() {
	if err := store.RegisterDDL("item", itemPilotDDL); err != nil {
		panic(err)
	}
	if err := event.Register("item.add", "item"); err != nil {
		panic(err)
	}
	// The one record→row mapping, registered as the seed materializer. The write face
	// below calls the same function, so a fixture cannot construct a state the write
	// face would reject (lanes §1, OQ-L2).
	if err := store.RegisterMaterializer("item.record", "item", applyItemPilot); err != nil {
		panic(err)
	}
}

// applyItemPilot is item's record→row mapping: it validates the record, writes the
// rows, appends the transition's event in the same transaction, and answers the
// record as stored. It is both the write face and the seed materializer.
func applyItemPilot(ctx context.Context, tx *store.Tx, env record.Envelope) (record.Envelope, error) {
	var payload itemPilotPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return record.Envelope{}, fmt.Errorf("item: reading the record payload: %w", err)
	}
	if payload.ItemID == "" {
		return record.Envelope{}, fmt.Errorf("item: a record needs an item_id")
	}
	if !itemPilotStates[payload.State] {
		return record.Envelope{}, fmt.Errorf("item: %q is not a writable state", payload.State)
	}
	for _, blockedBy := range payload.BlockedBy {
		var found int
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM items WHERE item_id = ?`, blockedBy).Scan(&found); err != nil {
			return record.Envelope{}, fmt.Errorf("item: reading the blocking item: %w", err)
		}
		if found == 0 {
			return record.Envelope{}, fmt.Errorf("item: edge to a missing item %q", blockedBy)
		}
	}
	if _, err := tx.Exec(
		`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(item_id) DO UPDATE SET
		   title = excluded.title, body_ref = excluded.body_ref, state = excluded.state,
		   design_doc = excluded.design_doc, updated_at = excluded.updated_at`,
		payload.ItemID, payload.Title, payload.BodyRef, payload.State, payload.DesignDoc, env.CreatedAt.UnixMilli(),
	); err != nil {
		return record.Envelope{}, fmt.Errorf("item: writing the item: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM item_criteria WHERE item_id = ?`, payload.ItemID); err != nil {
		return record.Envelope{}, fmt.Errorf("item: clearing the criteria: %w", err)
	}
	for _, criterion := range payload.Criteria {
		if _, err := tx.Exec(
			`INSERT INTO item_criteria (item_id, criterion_id, text, state) VALUES (?, ?, ?, ?)`,
			payload.ItemID, criterion.CriterionID, criterion.Text, criterion.State,
		); err != nil {
			return record.Envelope{}, fmt.Errorf("item: writing a criterion: %w", err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM item_edges WHERE item_id = ?`, payload.ItemID); err != nil {
		return record.Envelope{}, fmt.Errorf("item: clearing the edges: %w", err)
	}
	for _, blockedBy := range payload.BlockedBy {
		if _, err := tx.Exec(
			`INSERT INTO item_edges (item_id, blocked_by) VALUES (?, ?)`, payload.ItemID, blockedBy,
		); err != nil {
			return record.Envelope{}, fmt.Errorf("item: writing an edge: %w", err)
		}
	}
	payload.UpdatedAt = env.CreatedAt.UTC().Format(time.RFC3339)
	stored, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, fmt.Errorf("item: encoding the stored record: %w", err)
	}
	seq, err := tx.Append(event.Event{
		Kind:       "item.add",
		ObjectType: "item",
		ObjectID:   &payload.ItemID,
		Payload:    stored,
		At:         env.CreatedAt,
	})
	if err != nil {
		return record.Envelope{}, err
	}
	payload.EventSeq = seq
	stored, err = json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, fmt.Errorf("item: encoding the stored record: %w", err)
	}
	env.Payload = stored
	return env, nil
}

// itemPilotEnvelope builds one item.record envelope for the pilot.
func itemPilotEnvelope(t *testing.T, payload itemPilotPayload) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encoding the payload failed: %v", err)
	}
	raw, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          "item.record",
		ID:            "rec-" + payload.ItemID,
		Producer:      record.Producer{System: record.System, Utility: "item", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		Payload:       body,
	})
	if err != nil {
		t.Fatalf("encoding the envelope failed: %v", err)
	}
	return raw
}

// itemPilotSeed wraps records in the fixture.seed bundle Seed reads.
func itemPilotSeed(t *testing.T, records ...[]byte) []byte {
	t.Helper()
	raw := make([]json.RawMessage, len(records))
	for i, r := range records {
		raw[i] = r
	}
	bundle, err := json.Marshal(map[string]any{
		"kind":    "fixture.seed",
		"payload": map[string]any{"records": raw},
	})
	if err != nil {
		t.Fatalf("encoding the seed bundle failed: %v", err)
	}
	return bundle
}

// itemPilotRows reads the three item tables through the domain-row read surface, so
// the assertions below use the same surface a utility's reader would.
func itemPilotRows(t *testing.T, s *store.Store) (items, criteria, edges int) {
	t.Helper()
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		for _, probe := range []struct {
			query string
			into  *int
		}{
			{`SELECT COUNT(*) FROM items`, &items},
			{`SELECT COUNT(*) FROM item_criteria`, &criteria},
			{`SELECT COUNT(*) FROM item_edges`, &edges},
		} {
			if err := r.QueryRow(probe.query).Scan(probe.into); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the item rows failed: %v", err)
	}
	return items, criteria, edges
}

// itemPilotEvents counts the item.add events in the store.
func itemPilotEvents(t *testing.T, s *store.Store) int {
	t.Helper()
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	count := 0
	for _, e := range events {
		if e.Kind == "item.add" {
			count++
		}
	}
	return count
}

// The write face stores the item's rows and appends its event in one transaction, and
// the echo carries the event sequence it produced.
func TestThePilotWriteFaceStoresItemRowsAndItsEvent(t *testing.T) {
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	blocker := itemPilotPayload{ItemID: "item_001", Title: "blocker", State: "not-started"}
	blocked := itemPilotPayload{
		ItemID: "item_002", Title: "blocked", State: "blocked",
		BlockedBy: []string{"item_001"},
	}
	blocked.Criteria = append(blocked.Criteria, struct {
		CriterionID string `json:"criterion_id"`
		Text        string `json:"text"`
		State       string `json:"state"`
	}{CriterionID: "c1", Text: "first", State: "open"})

	var echo record.Envelope
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, payload := range []itemPilotPayload{blocker, blocked} {
			env, err := record.Decode(itemPilotEnvelope(t, payload))
			if err != nil {
				return err
			}
			if echo, err = applyItemPilot(context.Background(), tx, env); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the write face failed: %v", err)
	}
	items, criteria, edges := itemPilotRows(t, s)
	if items != 2 || criteria != 1 || edges != 1 {
		t.Fatalf("the write face stored %d items, %d criteria, %d edges; want 2, 1, 1", items, criteria, edges)
	}
	if got := itemPilotEvents(t, s); got != 2 {
		t.Fatalf("the write face appended %d item.add events, want 2", got)
	}
	var stored itemPilotPayload
	if err := json.Unmarshal(echo.Payload, &stored); err != nil {
		t.Fatalf("reading the echo failed: %v", err)
	}
	if stored.EventSeq == 0 {
		t.Fatal("the echo carries no event sequence")
	}
	if stored.UpdatedAt != "2026-11-10T12:00:00Z" {
		t.Fatalf("the echo's updated_at is %q, want the record's created_at", stored.UpdatedAt)
	}
}

// The seed materializer is the same mapping as the write face, so a fixture built
// from records holds the rows the write face would have written.
func TestThePilotSeedMaterializesTheSameRowsAsTheWriteFace(t *testing.T) {
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	blocker := itemPilotPayload{ItemID: "item_001", Title: "blocker", State: "not-started"}
	blocked := itemPilotPayload{ItemID: "item_002", Title: "blocked", State: "blocked", BlockedBy: []string{"item_001"}}
	if err := s.Seed(context.Background(), itemPilotSeed(t,
		itemPilotEnvelope(t, blocker), itemPilotEnvelope(t, blocked))); err != nil {
		t.Fatalf("seeding the fixture failed: %v", err)
	}
	items, _, edges := itemPilotRows(t, s)
	if items != 2 || edges != 1 {
		t.Fatalf("the seed materialized %d items and %d edges, want 2 and 1", items, edges)
	}
	if got := itemPilotEvents(t, s); got != 2 {
		t.Fatalf("the seed appended %d item.add events, want 2", got)
	}
}

// A fixture cannot construct a state the write face would reject: the seed path runs
// the same mapping, so a record the write face refuses is refused there too, and the
// whole seed rolls back.
//
// The rejected state is an unknown one rather than an edge to a missing item, because
// the DDL's foreign key refuses that edge on its own and the test would then pass for
// a reason other than the one it states.
func TestThePilotSeedCannotConstructAStateTheWriteFaceRejects(t *testing.T) {
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	orphan := itemPilotPayload{ItemID: "item_002", Title: "orphan", State: "abandoned"}
	if err := s.Seed(context.Background(), itemPilotSeed(t, itemPilotEnvelope(t, orphan))); err == nil {
		t.Fatal("the seed materialized a record the write face refuses")
	}
	items, _, edges := itemPilotRows(t, s)
	if items != 0 || edges != 0 {
		t.Fatalf("a refused seed left %d items and %d edges", items, edges)
	}
	if got := itemPilotEvents(t, s); got != 0 {
		t.Fatalf("a refused seed left %d item.add events", got)
	}
}
