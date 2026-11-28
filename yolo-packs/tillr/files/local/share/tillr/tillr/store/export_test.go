package store

// SetAfterCheckpoint makes f run inside CheckpointOwner.Checkpoint, after the checkpoint
// and before the owner's own write transaction begins: the window in which a restart of
// the log by any other writer would be possible but for the owner's pin. It answers the
// function that restores the previous hook.
func SetAfterCheckpoint(f func()) func() {
	previous := afterCheckpoint
	afterCheckpoint = f
	return func() { afterCheckpoint = previous }
}

// WritersWaiting answers how many of this process's writers wait for their turn on the
// store at path (writequeue.go).
func WritersWaiting(path string) int64 { return writeQueueFor(path).waiting.Load() }

// SetEventCache turns the committed-event cache on or off for stores opened afterwards,
// and answers the function that restores the previous setting.
func SetEventCache(on bool) func() {
	previous := eventCacheEnabled.Load()
	eventCacheEnabled.Store(on)
	return func() { eventCacheEnabled.Store(previous) }
}

// SetSelectedEventCache turns the narrow selected-event cache on or off for stores opened
// afterwards, and answers the function that restores the previous setting.
func SetSelectedEventCache(on bool) func() {
	previous := narrowCacheEnabled.Load()
	narrowCacheEnabled.Store(on)
	return func() { narrowCacheEnabled.Store(previous) }
}
