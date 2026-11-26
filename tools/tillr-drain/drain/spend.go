package drain

import (
	"fmt"
	"sort"

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

func (s *SpendTotals) add(rec run.Record) {
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

func (s *SpendTotals) merge(other SpendTotals) {
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

// Rebuild from each run's latest cumulative measurement, not each terminal segment.
func (result *LoopResult) recountSpend() {
	result.StartedSpend, result.CarriedSpend = SpendTotals{}, SpendTotals{}
	result.SpendUnknownExample, result.CostUnknownExample = "", ""
	ids := make([]string, 0, len(result.spendRecords))
	for id := range result.spendRecords {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rec := result.spendRecords[id]
		group := &result.StartedSpend
		if result.spendCarried[id] {
			group = &result.CarriedSpend
		}
		group.add(rec)
		if rec.SpendStatus == "not-applicable" {
			continue
		}
		who := rec.RunID + " on " + rec.Backend
		if (rec.Usage == nil || rec.Usage.Tokens == nil) && result.SpendUnknownExample == "" {
			result.SpendUnknownExample = who
		}
		if (rec.Usage == nil || rec.Usage.CostUSD == nil) && result.CostUnknownExample == "" {
			result.CostUnknownExample = who
		}
	}
	result.SpendUSD = result.StartedSpend.CostUSD + result.CarriedSpend.CostUSD
	result.Tokens = result.StartedSpend.Tokens + result.CarriedSpend.Tokens
	result.SpendUnknown = result.StartedSpend.TokensUnreported + result.CarriedSpend.TokensUnreported
	result.CostUnknown = result.StartedSpend.CostUnreported + result.CarriedSpend.CostUnreported
}

func mergeSpendRecords(dst, src *LoopResult) {
	if len(src.spendRecords) == 0 {
		return
	}
	if dst.spendRecords == nil {
		dst.spendRecords = map[string]run.Record{}
	}
	if dst.spendCarried == nil {
		dst.spendCarried = map[string]bool{}
	}
	for id, rec := range src.spendRecords {
		dst.spendRecords[id] = rec
		dst.spendCarried[id] = src.spendCarried[id]
	}
	dst.recountSpend()
}

func (l *Loop) countSettledSpend(result *LoopResult, rec run.Record, origin string) {
	l.mu.Lock()
	if result.spendCarried == nil {
		result.spendCarried = map[string]bool{}
	}
	result.spendCarried[rec.RunID] = origin == "" || origin != l.sessionID
	l.mu.Unlock()
	l.countSpend(result, rec)
}
