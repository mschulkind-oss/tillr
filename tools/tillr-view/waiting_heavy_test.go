package main

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
)

func TestWaitingHeavyProofNamesGreenCommitAndAge(t *testing.T) {
	now := time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)
	proof := heavy.HeavyStatus{GreenCommit: "abcdef1234567890", GreenProvedAt: now.Add(-2 * time.Hour)}
	row := heavyProofWaitingRow(proof, now)
	if !strings.Contains(row.Why, "abcdef123456") || !strings.Contains(row.Why, "2h0m0s ago") {
		t.Fatalf("row=%+v", row)
	}
	proof.GreenProvedAt = time.Time{}
	if row = heavyProofWaitingRow(proof, now); !strings.Contains(row.Why, "unknown") {
		t.Fatalf("old proof invents an age: %+v", row)
	}
}
