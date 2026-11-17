package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"

	"github.com/mschulkind-oss/tillr/event"
)

// Tx is one write transaction: the state change and the event describing it commit
// together, so a reader never sees one without the other (design §5.1). It is the
// counterpart of Reader — a utility writes and reads its own rows through these two
// and never opens the database itself.
type Tx struct {
	ctx context.Context
	tx  *sql.Tx
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
	operation := ""
	if os.Getenv("TILLR_SQLITE_TRACE") == "1" {
		if pc, _, _, ok := runtime.Caller(1); ok {
			operation = runtime.FuncForPC(pc).Name()
		}
	}
	return s.writeTx(ctx, operation, fn)
}

// writeTx is WriteTx with the traced operation named by the caller, so a checkpoint
// cycle's own writes read as what they are rather than as the function that called them.
// Nothing about checkpoints is decided here: the handle's commits never checkpoint (dsn).
func (s *Store) writeTx(ctx context.Context, operation string, fn func(*Tx) error) (result error) {
	trace := startSQLiteTrace(s.path, operation)
	defer func() { trace.finish(result) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: beginning the write transaction: %w", err)
	}
	trace.record("acquired", nil)
	// Rollback after a successful commit answers ErrTxDone, which is the state the
	// transaction is already in; the commit's own error is the one reported.
	defer tx.Rollback()
	if err := fn(&Tx{ctx: ctx, tx: tx}); err != nil {
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
func (t *Tx) Append(e event.Event) (int64, error) { return event.Append(t.ctx, t.tx, e) }

// Exec runs one statement inside this transaction.
func (t *Tx) Exec(query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(t.ctx, query, args...)
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
func (t *Tx) Reader() *Reader { return &Reader{ctx: t.ctx, q: t.tx} }
