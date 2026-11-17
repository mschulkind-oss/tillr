package store

import "testing"

func TestSQLiteTraceSampleQueuedNamesOnlyTheActiveOperation(t *testing.T) {
	observer := privateSQLiteTraceObserver(t, 1)
	span := &sqliteTraceSpan{id: 1, path: "store.db", operation: "session-write", sampled: true}
	observer.active[span.id] = span
	if SQLiteTraceSampleQueued(span.path, span.operation) {
		t.Fatal("selected sample mistaken for a queued snapshot")
	}
	span.sampleQueued = true
	if !SQLiteTraceSampleQueued(span.path, span.operation) {
		t.Fatal("queued snapshot missing for active operation")
	}
	if SQLiteTraceSampleQueued("other.db", span.operation) || SQLiteTraceSampleQueued(span.path, "open") {
		t.Fatal("another path or operation satisfied the sample barrier")
	}
	delete(observer.active, span.id)
	if SQLiteTraceSampleQueued(span.path, span.operation) {
		t.Fatal("finished operation satisfied the active-sample barrier")
	}
}

func TestSQLiteTraceEnqueueReportsItsHandoff(t *testing.T) {
	observer := newSQLiteTraceObserver(1)
	observer.pending.Add(2)
	if !observer.enqueue(sqliteTraceRecord{}) {
		t.Fatal("available queue slot rejected handoff")
	}
	if observer.enqueue(sqliteTraceRecord{}) {
		t.Fatal("dropped record reported a successful handoff")
	}
	if observer.pending.Load() != 1 || observer.droppedRecords.Load() != 1 {
		t.Fatal("dropped handoff changed delivery accounting")
	}
}
