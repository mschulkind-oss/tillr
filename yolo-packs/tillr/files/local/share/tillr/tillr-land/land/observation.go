package land

import "time"

// LockObservation measures one actual lock hold. Events use the real clock, not
// the injected event clock: acquire to release excludes waiting for the lock.
// The released sample is taken immediately before unlocking, while ownership
// still excludes another landing. Receipt is sampled after SQLite commits.
// This is an observation, not a state transition; it is returned alongside the
// landing after release rather than inserted into the store's event log.
type LockObservation struct {
	RunID  string      `json:"run_id"`
	Events []LockEvent `json:"events"`
}
type LockEvent struct {
	Kind string `json:"kind"`
	AtNS int64  `json:"at_ns"`
}

func (o *LockObservation) note(kind string) {
	if o != nil {
		o.Events = append(o.Events, LockEvent{Kind: kind, AtNS: time.Now().UnixNano()})
	}
}
