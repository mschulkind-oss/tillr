package run_test

import (
	"github.com/mschulkind-oss/tillr-run/run"
	"math"
	"testing"
)

func TestBackendQuotaPolicyValidation(t *testing.T) {
	for _, fraction := range []float64{-.1, 1.1, math.NaN(), math.Inf(1)} {
		b := run.Backend{Adapter: "fixture", Model: "fixture", Reserve: fraction}
		if b.Missing() == "" {
			t.Fatalf("accepted reserve %v", fraction)
		}
	}
	for _, fraction := range []float64{0, .2, 1} {
		b := run.Backend{Adapter: "fixture", Model: "fixture", Reserve: fraction}
		if b.Missing() != "" {
			t.Fatalf("refused reserve %v: %s", fraction, b.Missing())
		}
	}
	b := run.Backend{Adapter: "fixture", Model: "fixture", Quota: []string{""}}
	if b.Missing() == "" {
		t.Fatal("accepted empty command")
	}
}
