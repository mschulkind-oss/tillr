package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"

	_ "modernc.org/sqlite"
)

// A fresh fixture has exactly the tables the migration creates, and the counts are
// the rows those tables hold: two meta rows (the fixture mark and the applied-schema
// record the next open reads instead of migrating) and one migration row.
// A reader that answers an empty map, or that counts sqlite's own bookkeeping tables,
// fails here.
func TestTableCountsNamesEveryTableAndItsRows(t *testing.T) {
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()

	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for table, want := range map[string]int{"meta": 2, "schema_migrations": 1, "events": 0} {
		got, ok := counts[table]
		if !ok {
			t.Fatalf("table %q is missing from the counts: %v", table, counts)
		}
		if got != want {
			t.Fatalf("table %q holds %d rows, want %d", table, got, want)
		}
	}
	if _, ok := counts["sqlite_sequence"]; ok {
		t.Fatalf("the counts include sqlite's own bookkeeping table: %v", counts)
	}
}

// An empty store's head is 0, not an error: "no events yet" is an answer a caller
// compares against, and a caller that cannot tell it from a failure cannot decide
// whether a snapshot is current.
func TestEventHeadSeqIsZeroForAStoreWithNoEvents(t *testing.T) {
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()

	head, err := s.EventHeadSeq()
	if err != nil {
		t.Fatalf("reading the head seq failed: %v", err)
	}
	if head != 0 {
		t.Fatalf("an empty store's head seq is %d, want 0", head)
	}
}

// Seeding a fixture with no inner records appends exactly the one marker event, so
// the head is 1 and the event is readable with its payload decoded. `since` is
// exclusive, which is what makes `--since <head>` a tail rather than a repeat.
func TestEventsReadsTheAppendedEventAndSinceIsExclusive(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	if err := s.Seed(ctx, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`)); err != nil {
		t.Fatalf("seeding the fixture failed: %v", err)
	}

	head, err := s.EventHeadSeq()
	if err != nil {
		t.Fatalf("reading the head seq failed: %v", err)
	}
	if head != 1 {
		t.Fatalf("the head seq after one append is %d, want 1", head)
	}

	all, err := s.Events(ctx, 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("reading from 0 returned %d events, want 1", len(all))
	}
	if all[0].Kind != "fixture.seed" {
		t.Fatalf("the event's kind is %q", all[0].Kind)
	}
	// The seq is the reader's cursor: without it a caller cannot resume, and
	// `--since` has nothing to advance to.
	if all[0].Seq != 1 {
		t.Fatalf("the event's seq is %d, want 1", all[0].Seq)
	}
	var payload map[string]any
	if err := json.Unmarshal(all[0].Payload, &payload); err != nil {
		t.Fatalf("the event's payload is not a JSON object: %v", err)
	}
	if payload["records"] != float64(0) {
		t.Fatalf("the event's payload is %v", payload)
	}
	if all[0].At.IsZero() {
		t.Fatal("the event's timestamp was not decoded")
	}

	tail, err := s.Events(ctx, head)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	if len(tail) != 0 {
		t.Fatalf("reading from the head returned %d events, want 0", len(tail))
	}
}

// A store that cannot be read is not intact, so the falsifier is a corrupted copy:
// asserting "ok" on a store nothing has damaged would pass for a reader that always
// answers "ok".
func TestIntegrityDoesNotReportACorruptedStoreAsOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	if err := s.Seed(context.Background(), []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`)); err != nil {
		t.Fatalf("seeding the fixture failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}
	// The copy below is of the database file alone, so the log is copied into it first,
	// through the supported face: no program's own work checkpoints.
	if _, err := store.Checkpoint(context.Background(), path); err != nil {
		t.Fatalf("checkpointing the store before copying it failed: %v", err)
	}

	corrupt := filepath.Join(dir, "corrupt.sqlite3")
	copyFile(t, path, corrupt)
	f, err := os.OpenFile(corrupt, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(make([]byte, 512), 4096); err != nil {
		f.Close()
		t.Fatalf("corrupting the copy failed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := store.OpenRead(context.Background(), corrupt)
	if err != nil {
		// A store that will not open is not intact either; the point of the case is
		// that "ok" is not the answer.
		return
	}
	defer r.Close()
	got, err := r.Integrity()
	if err != nil {
		return
	}
	if got == "ok" {
		t.Fatal("a corrupted store reported integrity ok")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
}

// A report built from separate reads describes no moment: a writer appending between
// the events read and the head read yields a head greater than the file's last seq.
// The snapshot is one transaction, so the write that lands between the two reads is
// invisible to both of them.
func TestTheSnapshotDescribesOneMoment(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	if err := s.Seed(ctx, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`)); err != nil {
		t.Fatalf("seeding the fixture failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}
	// Store.Open selects WAL, letting a writer commit between these reads.

	reader, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer reader.Close()
	writer, err := store.Open(ctx, path, false)
	if err != nil {
		t.Fatalf("opening the store for writing failed: %v", err)
	}
	defer writer.Close()

	var (
		events []event.Event
		head   int64
		counts map[string]int
	)
	err = reader.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		if events, err = r.Events(0); err != nil {
			return err
		}
		// The write lands between the two reads, and commits: WAL lets it.
		if err := writer.Seed(ctx, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`)); err != nil {
			return err
		}
		if head, err = r.EventHeadSeq(); err != nil {
			return err
		}
		counts, err = r.TableCounts()
		return err
	})
	if err != nil {
		t.Fatalf("the snapshot failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the snapshot read no events")
	}
	last := events[len(events)-1].Seq
	if head != last {
		t.Fatalf("event_head_seq is %d but the last event in the file is %d", head, last)
	}
	if counts["events"] != len(events) {
		t.Fatalf("table_counts.events is %d but the file holds %d events", counts["events"], len(events))
	}
	// The write really did land during the snapshot: a later read sees it. Without
	// this the test would pass for a snapshot that simply read nothing new.
	after, err := reader.EventHeadSeq()
	if err != nil {
		t.Fatalf("reading the head after the snapshot failed: %v", err)
	}
	if after != last+1 {
		t.Fatalf("the head after the snapshot is %d, want %d: the write did not land between the reads", after, last+1)
	}
}

// The monolith's rename-temp is not a logical table: a forensic check against a store
// that died mid-rebuild would otherwise report it as one.
func TestTableCountsExcludesTheMonolithsRenameTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}
	execSQL(t, path, `CREATE TABLE items_v4 (id INTEGER PRIMARY KEY)`)

	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer r.Close()
	counts, err := r.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	if _, ok := counts["items_v4"]; ok {
		t.Fatalf("the counts include the monolith's rename-temp: %v", counts)
	}
}

// `_` is a LIKE wildcard, so `NOT LIKE 'sqlite_%'` also excludes a table whose name
// merely starts with "sqlite" and a different character. sqlite reserves the
// "sqlite_" prefix, not the word, so such a table is legal and is store state.
func TestTableCountsKeepsATableWhoseNameMerelyStartsWithSqlite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}
	execSQL(t, path, `CREATE TABLE sqliteXtra (id INTEGER PRIMARY KEY)`)

	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer r.Close()
	counts, err := r.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	if _, ok := counts["sqliteXtra"]; !ok {
		t.Fatalf("the counts dropped a table whose name merely starts with sqlite: %v", counts)
	}
}

// A forensic log carries the payload it was given. Decoding into `any` routes every
// number through float64, so an integer above 2^53 comes back changed -- silently, in
// the one artifact a reader trusts to say what happened.
func TestEventsCarriesThePayloadVerbatim(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	bundle := []byte(`{"kind":"fixture.seed","payload":{"records":[
		{"record_version":1,"kind":"fixture.seed","id":"seed-1",
		 "producer":{"system":"tillr","utility":"store","version":"0.1.0"},
		 "created_at":"2026-11-06T00:00:00Z","payload":{"big":9007199254740993}}]}}`)
	if err := s.Seed(ctx, bundle); err != nil {
		t.Fatalf("seeding the fixture failed: %v", err)
	}
	events, err := s.Events(ctx, 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("reading from 0 returned %d events, want 2", len(events))
	}
	raw, err := json.Marshal(events[1].Payload)
	if err != nil {
		t.Fatalf("re-encoding the payload failed: %v", err)
	}
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("the payload lost precision: %s", raw)
	}
}

// execSQL runs one statement against a store file directly. The store package migrates
// forward only, so a test that needs a shape the migration does not create -- an
// incompatible version, a stray table -- writes it here.
func execSQL(t *testing.T, path, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening %s failed: %v", path, err)
	}
	defer db.Close()
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("running %q failed: %v", statement, err)
	}
}

// A read transaction on a writable handle is a read: it takes no write lock, so a
// writer on the same store commits while it is open. The driver applies the
// connection's transaction lock mode only to a transaction that is not read-only, so
// ReadTx has to pass the read-only option; without it the immediate begin every
// writable handle carries would make every read path hold the store's write lock.
func TestAReadTransactionOnAWritableHandleDoesNotBlockAWriter(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	reader, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	t.Cleanup(func() { reader.Close() })
	// Store.Open selects WAL; only taking a write lock could block this writer.
	writer, err := store.Open(ctx, path, false)
	if err != nil {
		t.Fatalf("opening the writer failed: %v", err)
	}
	t.Cleanup(func() { writer.Close() })

	read := make(chan struct{})
	release := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- reader.ReadTx(ctx, func(r *store.Reader) error {
			if _, err := r.EventHeadSeq(); err != nil {
				return err
			}
			close(read)
			<-release
			return nil
		})
	}()
	<-read
	writeCtx, cancel := context.WithTimeout(ctx, hangGuard)
	defer cancel()
	if err := writer.WriteTx(writeCtx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('written', '1')`)
		return err
	}); err != nil {
		t.Fatalf("a write was blocked by a read transaction on a writable handle: %v", err)
	}
	close(release)
	if err := <-readDone; err != nil {
		t.Fatalf("the read transaction failed: %v", err)
	}
}
