package drainmodel

import (
	"fmt"

	"github.com/mschulkind-oss/tillr-run/run"
)

// SpendTotals is a sum of reported measurements, never an estimate of missing
// measurements. Unreported counts runs with neither number; the field-specific
// counts also cover partial reports. A sum with a missing field is a lower bound.
type SpendTotals struct {
	CostUSD          float64 `json:"cost_usd"`
	Tokens           int64   `json:"tokens"`
	Runs             int     `json:"runs"`
	Unreported       int     `json:"unreported"`
	TokensUnreported int     `json:"tokens_unreported"`
	CostUnreported   int     `json:"cost_unreported"`
}

func (s *SpendTotals) Add(rec run.Record) {
	s.Runs++
	if rec.SpendStatus == "not-applicable" {
		return
	}
	if rec.Usage == nil {
		s.Unreported++
	}
	if rec.Usage == nil || rec.Usage.Tokens == nil {
		s.TokensUnreported++
	} else {
		s.Tokens += *rec.Usage.Tokens
	}
	if rec.Usage == nil || rec.Usage.CostUSD == nil {
		s.CostUnreported++
	} else {
		s.CostUSD += *rec.Usage.CostUSD
	}
}

func (s *SpendTotals) Merge(other SpendTotals) {
	s.CostUSD += other.CostUSD
	s.Tokens += other.Tokens
	s.Runs += other.Runs
	s.Unreported += other.Unreported
	s.TokensUnreported += other.TokensUnreported
	s.CostUnreported += other.CostUnreported
}

func (s SpendTotals) String() string {
	qualifier := ""
	if s.TokensUnreported > 0 || s.CostUnreported > 0 {
		qualifier = "lower bound: "
	}
	return fmt.Sprintf("%s$%f, %d tokens; %d unreported run(s); %d token-unreported, %d cost-unreported", qualifier, s.CostUSD, s.Tokens, s.Unreported, s.TokensUnreported, s.CostUnreported)
}
