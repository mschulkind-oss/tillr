package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr/store"
)

// The WAL keeper is one idle, read-only connection to the store that a running Loop
// holds from its first successful session write until Run returns. The name is this
// package's own: it keeps SQLite's write-ahead log (WAL) file in place.
//
// Every other face opens the store, does one thing and closes it. Without the keeper,
// each such close is the store's last connection, and SQLite's last close checkpoints
// the log into the database and deletes it: two disk flushes under SQLite's exclusive
// lock. The next commit then recreates the log and flushes the new file, its directory
// and the commit while holding SQLite's writer, so one slow disk made a writer every
// other connection waited behind until its five-second busy timeout. While the keeper is
// open, no other close is the last one, so none of that happens; commits flush nothing
// (store dsn, synchronous=NORMAL) and checkpoints run as SQLite schedules them.
//
// Idle means it never holds a statement or transaction open: its one read ends before
// openWALKeeper returns. An idle connection holds only SQLite's shared lock, which
// blocks neither a writer nor a checkpoint.
func openWALKeeper(ctx context.Context, path string) (*store.Store, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, err
	}
	// The read maps the log's shared index, which is what attaches the connection to it.
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var n int
		return r.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n)
	}); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// keepWALWhileRunning makes the Loop's first successful session write open the keeper.
// Only Run asks for it, so a Loop driven pass by pass holds no connection between calls.
func (l *Loop) keepWALWhileRunning() {
	l.sessionMu.Lock()
	defer l.sessionMu.Unlock()
	l.keepWAL = true
}

// closeWALKeeper closes the keeper, if one was opened. Run defers it just before
// endSession, so it runs after the session's end record, on every way out of Run,
// including a panic.
func (l *Loop) closeWALKeeper() {
	l.sessionMu.Lock()
	defer l.sessionMu.Unlock()
	if l.walKeeper != nil {
		l.walKeeper.Close()
		l.walKeeper = nil
	}
	l.keepWAL = false
}
