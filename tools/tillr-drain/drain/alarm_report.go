package drain

import (
	"context"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/store"
)

func alarmPolicy(p *Policy, since time.Time) *alarms.Policy {
	if p == nil {
		return nil
	}
	out := &alarms.Policy{Backends: make(map[string]alarms.Backend, len(p.Backends)), Since: since}
	for name, b := range p.Backends {
		out.Backends[name] = alarms.Backend{Quota: b.Quota, Reserve: b.Reserve}
	}
	return out
}

// Observing alarms never decides whether a pass runs or succeeds. Keep the last
// observed set only to report transitions; the store is always the source.
func (l *Loop) reportAlarms(ctx context.Context, at time.Time) {
	if ctx.Err() != nil {
		return
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	var active []alarms.Alarm
	if err == nil {
		defer s.Close()
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			active, err = alarms.Read(r, at, alarmPolicy(l.currentPolicy(), l.policyState.At))
			return err
		})
	}
	if err != nil {
		l.reportf("loop: could not read alarms: %v\n", err)
		return
	}
	l.reportAlarmChanges(active)
}

func (l *Loop) reportAlarmChanges(active []alarms.Alarm) {
	prior := map[string]bool{}
	next := map[string]bool{}
	for _, name := range l.reportedAlarms {
		prior[name] = true
	}
	names := make([]string, 0, len(active))
	for _, a := range active {
		key := a.Key()
		names = append(names, key)
		next[key] = true
		if !prior[key] {
			l.reportf("loop: alarm started: %s\n", a)
		}
	}
	for _, name := range l.reportedAlarms {
		if !next[name] {
			l.reportf("loop: alarm cleared: %s\n", name)
		}
	}
	if len(names) > 0 || len(l.reportedAlarms) > 0 {
		l.reportf("loop: pass alarms [%s]\n", strings.Join(names, ", "))
	}
	l.reportedAlarms = names
}
