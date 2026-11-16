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
