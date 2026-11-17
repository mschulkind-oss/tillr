package main

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

type passSpend struct {
	CostUSD          float64 `json:"cost_usd"`
	Tokens           int64   `json:"tokens"`
	Runs             int     `json:"runs"`
	Unreported       int     `json:"unreported"`
	TokensUnreported int     `json:"tokens_unreported"`
	CostUnreported   int     `json:"cost_unreported"`
}

func (s passSpend) String() string {
	qualifier := ""
	if s.TokensUnreported > 0 || s.CostUnreported > 0 {
		qualifier = "lower bound: "
	}
	return fmt.Sprintf("%s$%f, %d tokens; %d unreported run(s); %d token-unreported, %d cost-unreported", qualifier, s.CostUSD, s.Tokens, s.Unreported, s.TokensUnreported, s.CostUnreported)
}

func readPassSpend(r *store.Reader, decisions []passDecision) (*passSpend, *passSpend, error) {
	exists, err := hasTable(r, "drain_pass_spend")
	if err != nil || !exists {
		return nil, nil, err
	}
	ids := map[string]bool{}
	var args []any
	var marks []string
	for _, d := range decisions {
		if d.passID != "" && !ids[d.passID] {
			ids[d.passID] = true
			args = append(args, d.passID)
			marks = append(marks, "?")
		}
	}
	if len(args) == 0 {
		return nil, nil, nil
	}
	rows, err := r.Query(`SELECT carried_over,COALESCE(SUM(usage_cost_usd),0),COALESCE(SUM(usage_tokens),0),COUNT(*),
 SUM(usage_cost_usd IS NULL AND usage_tokens IS NULL), SUM(usage_tokens IS NULL), SUM(usage_cost_usd IS NULL)
 FROM drain_pass_spend WHERE pass_id IN (`+strings.Join(marks, ",")+`) GROUP BY carried_over`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	started, carried := &passSpend{}, &passSpend{}
	seen := false
	for rows.Next() {
		var sum passSpend
		var carry bool
		if err := rows.Scan(&carry, &sum.CostUSD, &sum.Tokens, &sum.Runs, &sum.Unreported, &sum.TokensUnreported, &sum.CostUnreported); err != nil {
			return nil, nil, err
		}
		seen = true
		if carry {
			*carried = sum
		} else {
			*started = sum
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if !seen {
		return nil, nil, nil
	}
	return started, carried, nil
}
