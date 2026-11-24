package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/mschulkind-oss/tillr/event"
)

// Tx is one write transaction: the state change and the event describing it commit
// together, so a reader never sees one without the other (design §5.1). It is the
// counterpart of Reader — a utility writes and reads its own rows through these two
// and never opens the database itself.
type Tx struct {
	ctx    context.Context
	tx     *sql.Tx
	events *eventCache
	notify txNotifyState
}

type writeObserverKey struct{}

// ObserveWrites observes each WriteTx made with this context, including time
// spent waiting for admission. The observer is called after releasing the writer
// and must be safe for concurrent calls when the context is shared.
func ObserveWrites(ctx context.Context, observe func(start, end time.Time)) context.Context {
	return context.WithValue(ctx, writeObserverKey{}, observe)
}

// WriteTx runs fn inside one write transaction and commits it. An error from fn rolls
// the whole transaction back, so a refused transition leaves no row and no event: the
// two are one transaction, not two writes a caller must remember to pair.
//
// The begin takes the store's write lock: the handle's connection begins IMMEDIATE
// (dsn, open.go), so this read-then-write transaction waits for an in-flight writer
// instead of being answered SQLITE_BUSY at its first write, and a second writer waits
// here rather than failing.
func (s *Store) WriteTx(ctx context.Context, fn func(*Tx) error) error {
	if observe, ok := ctx.Value(writeObserverKey{}).(func(time.Time, time.Time)); ok {
		start := time.Now()
		defer func() { observe(start, time.Now()) }()
	}
	operation := ""
	if os.Getenv("TILLR_SQLITE_TRACE") == "1" {
		if pc, _, _, ok := runtime.Caller(1); ok {
			operation = runtime.FuncForPC(pc).Name()
		}
	}
	var nstate txNotifyState
	if err := s.writeTx(ctx, operation, func(tx *Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		nstate = tx.notify
		return nil
	}); err != nil {
		return err
	}
	s.publishNotify(nstate)
	return nil
}

// writeTx is WriteTx with the traced operation named by the caller, so a checkpoint
// cycle's own writes read as what they are rather than as the function that called them.
// Nothing about checkpoints is decided here: the handle's commits never checkpoint (dsn).
func (s *Store) writeTx(ctx context.Context, operation string, fn func(*Tx) error) (result error) {
	trace := startSQLiteTrace(s.path, operation)
	defer func() { trace.finish(result) }()
	// This process's writers take turns before asking SQLite (writequeue.go), so the
	// busy handler only ever arbitrates between processes.
	leave, waited, err := writeQueueFor(s.path).enter(ctx, s.busyTimeout)
	if err != nil {
		return fmt.Errorf("store: waiting for this process's turn to write: %w", err)
	}
	defer leave()
	if waited {
		trace.record("queue.waited", nil)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning the write transaction: %w", err)
	}
	trace.record("acquired", nil)
	// The I/O guard (ioguard.go) sees this goroutine as a writer until the commit or the
	// rollback has released the lock.
	defer enterWrite()()
	// Rollback after a successful commit answers ErrTxDone, which is the state the
	// transaction is already in; the commit's own error is the one reported.
	defer tx.Rollback()
	if err := fn(&Tx{ctx: ctx, tx: tx, events: s.events}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: committing the write transaction: %w", err)
	}
	return nil
}

// Append writes one event inside this transaction, through the substrate's append
// primitive. An unregistered kind is refused, and the refusal takes the transaction's
// rows with it.
func (t *Tx) Append(e event.Event) (int64, error) {
	seq, err := event.Append(t.ctx, t.tx, e)
	if err != nil {
		return 0, err
	}
	t.notify.appended = append(t.notify.appended, appendedEvent{ev: e, seq: seq})
	return seq, nil
}

// Exec runs one statement inside this transaction.
func (t *Tx) Exec(query string, args ...any) (sql.Result, error) {
	res, err := t.tx.ExecContext(t.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	t.notify.mutated = true
	return res, nil
}

// Query runs one query inside this transaction.
func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(t.ctx, query, args...)
}

// QueryRow runs one query inside this transaction, answering at most one row.
func (t *Tx) QueryRow(query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(t.ctx, query, args...)
}

// Reader answers a read surface over this transaction, so a write face reads the
// state it is about to change in the same transaction that changes it. A reader built
// over the store's own handle instead would not see the transaction's uncommitted
// rows, and the read and the write would describe two different moments.
func (t *Tx) Reader() *Reader { return &Reader{ctx: t.ctx, q: t.tx, events: t.events} }
