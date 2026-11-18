package drain

import (
	"strings"
	"testing"
	"time"
)

func TestHeavyStatusLineShowsGreenCommitAndProofAge(t *testing.T) {
	status := HeavyStatus{GreenCommit: "abcdef1234567890", GreenProvedAt: time.Now().Add(-2 * time.Hour)}
	line := heavyStatusLine(status)
	if !strings.Contains(line, "green="+shortSHA(status.GreenCommit)) || !strings.Contains(line, "proved_age=2h0m0s") {
		t.Fatal(line)
	}
	status.GreenProvedAt = time.Time{}
	if line := heavyStatusLine(status); !strings.Contains(line, "proved_age=unknown") {
		t.Fatal(line)
	}
}
