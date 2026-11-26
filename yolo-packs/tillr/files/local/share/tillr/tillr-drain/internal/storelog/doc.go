// tillr-arch: layer=1 imports=drainmodel

// Package storelog owns the drain SQLite schema and column migrations,
// schema-roll staging/activation helpers, busy-retry write transactions,
// WAL checkpoint keeper, loop-pass/loop-ended/settlement-spend/base-replay/judgment
// event-log writers, loop-result envelope writer, dispatches row insert/collision
// helpers, and pipeline/handoff JSONL timing primitives.
package storelog
