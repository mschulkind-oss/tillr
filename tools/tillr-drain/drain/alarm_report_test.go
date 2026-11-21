package drain

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestBackendAlarmPassReportStartsOnceAndClears(t *testing.T) {
	f, l := passFixture(t, "item-a")
	var log bytes.Buffer
	l.cfg.Report = &log
	backendRun(t, f, "outage", "fixture", 2)
	ctx := context.Background()
	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatal(err)
	}
	l.reportAlarms(ctx, l.cfg.Now())
	l.reportAlarms(ctx, l.cfg.Now())
	if err := ProbeBackend(ctx, f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	l.reportAlarms(ctx, l.cfg.Now())
	l.reportAlarms(ctx, l.cfg.Now())
	text := log.String()
	for want, n := range map[string]int{"alarm started: ALARM backend-closed (fixture)": 1, "pass alarms [backend-closed:fixture]": 2, "alarm cleared: backend-closed:fixture": 1} {
		if got := strings.Count(text, want); got != n {
			t.Errorf("%q: got %d, want %d:\n%s", want, got, n, text)
		}
	}
}

func TestBackendAlarmWhyUsesCurrentHoldAndOperatorClass(t *testing.T) {
	f, l := passFixture(t, "item-a")
	backendRun(t, f, "outage", "fixture", 2)
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		a, err := Why(tx.Reader(), Pass{}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(a.Rows) != 1 || a.Rows[0].Class != "alarm" || len(a.Alarms) != 1 || len(a.Alarms[0].Items) != 1 || a.Alarms[0].Items[0] != "item-a" || len(a.Summary.Alarms) != 1 {
			t.Fatalf("why: %+v", a)
		}
		return nil
	})
}
