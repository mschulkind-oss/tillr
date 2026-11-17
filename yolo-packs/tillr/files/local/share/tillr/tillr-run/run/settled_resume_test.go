package run

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSettledResumeCannotUsePriorSegmentReport(t *testing.T) {
	dir := settledScratch(t)
	log := filepath.Join(dir, "stream")
	old := `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"RESULT: done\n"}]}}` + "\n" + `{"type":"agent_settled"}` + "\n"
	current := `{"type":"agent_settled"}` + "\n"
	if err := os.WriteFile(log, []byte(old+current), 0600); err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs("../backends/pi/run-pi")
	if err != nil {
		t.Fatal(err)
	}
	m := settledMonitor{log: log, offset: int64(len(old)), startOffset: int64(len(old)), backendDir: "../backends", adapter: adapter, durable: dir, backend: "pi"}
	if err := m.observe(); err != nil {
		t.Fatal(err)
	}
	if m.settledAt == "" {
		t.Fatal("did not observe current settlement")
	}
	if m.reportReady(context.Background()) {
		t.Fatal("used preceding segment's footer")
	}
}
