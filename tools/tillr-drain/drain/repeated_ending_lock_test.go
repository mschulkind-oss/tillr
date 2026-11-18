package drain

import (
	"database/sql"
	"strings"
	"testing"
)

// The repeat routing reads the store's whole history: every event, every run of the item,
// and the reports on disk. On 2026-11-17 the second-failure rule it replaces did that
// inside markExamined's write transaction and held the store's one write lock for 4.8
// seconds, so every other writer queued behind it and one waited past its busy timeout
// ("database is locked"). The reading belongs before the write: this test writes from a
// second connection, with no busy timeout, at the moment the routing reads, which fails at
// once if the routing holds the write lock and needs no clock to tell.
func TestRepeatRoutingReadsOutsideTheWriteLock(t *testing.T) {
	f, l := designSeatFixture(t)
	first := mustPlan(t, l)[0]
	finishRefused(t, f, l, first, RefusalGate, "", "documents", "the same failure")
	second := mustPlan(t, l)[0]

	other, err := sql.Open("sqlite", "file:"+f.storePath+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	read, competing := 0, error(nil)
	repeatReading = func() {
		read++
		_, competing = other.Exec(`INSERT OR REPLACE INTO meta (key, value) VALUES ('repeat-competitor', 'written')`)
	}
	defer func() { repeatReading = nil }()
	finishRefused(t, f, l, second, RefusalGate, "", "documents", "the same failure")
	if read == 0 {
		t.Fatal("the repeat routing never read the history; the fixture does not reach it")
	}
	if competing != nil {
		t.Fatalf("a writer competing with the repeat routing answered %v: the routing read the history under the write lock", competing)
	}
	if c := itemConflict(t, f); c.Status != "raised" || c.WaitingOn != "design" || !strings.HasPrefix(c.Text, IdenticalRepeatPrefix) {
		t.Fatalf("the routing no longer raises the design conflict: %+v", c)
	}
}
