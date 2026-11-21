package watch

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWatchBundleElapsedWithOutOfOrderTimestamps(t *testing.T) {
	r := Rule{Guidance: Guidance{Summary: "{{elapsed}}"}}
	c := condition{events: []observation{
		{Seq: 1, TS: 120_000}, {Seq: 2, TS: 60_000}, {Seq: 3, TS: 240_000}, {Seq: 4, TS: 90_000},
	}}
	data, err := makeBundle(r, "subject", "alert", &c)
	if err != nil {
		t.Fatal(err)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	if b.Guidance.Summary != "3m" {
		t.Errorf("elapsed = %q, want 3m", b.Guidance.Summary)
	}
	for i, e := range b.Events {
		if want := int64(4 - i); e.Seq != want {
			t.Errorf("event %d: seq = %d, want %d", i, e.Seq, want)
		}
	}
}

func TestWatchBundleBudgetCutsInOrder(t *testing.T) {
	summary := strings.Repeat("é", bundleBudget)
	r := Rule{ID: "long-guidance", Severity: "warning", Guidance: Guidance{
		Summary: summary,
		Check:   []string{"inspect the record"},
		Do:      []string{"report the finding"},
	}}
	c := condition{events: []observation{{Seq: 1}, {Seq: 2}}}
	data, err := makeBundle(r, "subject", "alert", &c)
	if err != nil || len(data) > bundleBudget {
		t.Fatalf("bundle size %d: %v", len(data), err)
	}
	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	if want := (omission{Events: 2, Check: 1, Do: 1, SummaryCut: true}); b.Omitted != want {
		t.Fatalf("omitted: %+v, want %+v", b.Omitted, want)
	}
	if b.Subject != "subject" || !strings.HasPrefix(summary, b.Guidance.Summary) || b.Guidance.Summary == "" {
		t.Fatalf("subject or summary was cut incorrectly")
	}
	b.Guidance.Summary += "é"
	longer, err := json.Marshal(b)
	if err != nil || len(longer) <= bundleBudget {
		t.Fatalf("another character fits: bundle size %d, error %v", len(longer), err)
	}
}
