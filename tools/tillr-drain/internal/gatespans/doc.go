// tillr-arch: layer=2 imports=drainmodel,procguard,boundary,storelog,runhistory

// Package gatespans parses gate and heavy-proof span/timing records,
// extracts stalled-gate watchdog observations, adapts heavyrun proof steps,
// reads legacy check watches, and runs case-scoped Go test replays.
package gatespans
