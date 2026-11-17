package drain

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestDefaultWaitReportsFiveHoldsOnceAcrossOneHundredIdlePasses(t *testing.T) {
	f, l := passFixture(t, "held-0", "held-1", "held-2", "held-3", "held-4", "held-5", "held-6")
	l.cfg.ExitWhenDrained = false
	l.cfg.StorePollInterval = time.Millisecond
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})
	var report bytes.Buffer
	l.cfg.Report = &report
	var result LoopResult
	// Startup is not an idle pass: sync reports once before waiting begins.
	if _, err := l.refreshDocuments(context.Background(), &result); err != nil {
		t.Fatal(err)
	}
	report.Reset()
	pass := func() {
		t.Helper()
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("default idle pass stopped: %s / %v", stop, err)
		}
	}
	pass()
	first := report.String()
	if strings.Count(first, "\n") != 1 || !strings.Contains(first, "7 held item(s)") || !strings.Contains(first, "waiting for a trigger") {
		t.Fatalf("waiting summary: %q", first)
	}
	for i := 0; i < 7; i++ {
		present := strings.Contains(first, fmt.Sprintf("held-%d (%s)", i, ReasonBlocked))
		if present != (i < 5) {
			t.Fatalf("summary must name only the first five holds: %q", first)
		}
	}
	for i := 0; i < 100; i++ {
		pass()
	}
	if report.String() != first {
		t.Fatalf("100 idle passes added output: %q", report.String()[len(first):])
	}
	// A reason changing beyond the five printed examples is still a changed queue.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'held-6'`, StateLanded)
		return err
	})
	pass()
	if strings.Count(report.String(), "\n") != 2 {
		t.Fatalf("changed hold did not produce a fresh summary: %q", report.String())
	}
}
