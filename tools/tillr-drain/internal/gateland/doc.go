// tillr-arch: layer=2 imports=storelog

// Package gateland holds the standalone outside-pass gate (Step 4) and
// FIFO land (Step 5) coordinator state, wake/fallback-timer run loop,
// candidate eligibility predicates, FIFO sorting, and store-reader helpers
// for Scheduling Slice 4.
package gateland
