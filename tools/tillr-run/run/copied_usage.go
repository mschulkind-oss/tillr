package run

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// CopiedUsage preserves the measurement removed from a run and its attribution.
// Equality is evidence for operator review, not proof that two runs spent alike by mistake.
type CopiedUsage struct {
	RunID       string  `json:"run_id"`
	SourceRunID string  `json:"source_run_id"`
	Tokens      int64   `json:"tokens"`
	CostUSD     float64 `json:"cost_usd"`
	Reason      string  `json:"reason,omitempty"`
}

// DetectCopiedUsage reads one snapshot and lists nonzero pairs matching a strictly
// earlier, distinct run. Each match names the earliest source, so a chain of copies
// never points at a row the batch itself will clear. Equal start times are not earlier.
func DetectCopiedUsage(ctx context.Context, s *store.Store) ([]CopiedUsage, error) {
	var matches []CopiedUsage
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		matches, err = detectCopiedUsage(r)
		return err
	})
	return matches, err
}

func detectCopiedUsage(r *store.Reader) ([]CopiedUsage, error) {
	rows, err := r.Query(`SELECT run_id, source_run_id, usage_tokens, usage_cost_usd FROM (
  SELECT r.run_id, r.started_at, r.usage_tokens, r.usage_cost_usd,
   (SELECT p.run_id FROM runs p WHERE p.started_at < r.started_at
    AND p.usage_tokens = r.usage_tokens AND p.usage_cost_usd = r.usage_cost_usd
    ORDER BY p.started_at, p.run_id LIMIT 1) AS source_run_id
  FROM runs r WHERE r.usage_tokens != 0 AND r.usage_cost_usd != 0
 ) WHERE source_run_id IS NOT NULL ORDER BY started_at, run_id`)
	if err != nil {
		return nil, Internalf("detecting copied usage: %v", err)
	}
	defer rows.Close()
	matches := []CopiedUsage{}
	for rows.Next() {
		var match CopiedUsage
		if err := rows.Scan(&match.RunID, &match.SourceRunID, &match.Tokens, &match.CostUSD); err != nil {
			return nil, Internalf("reading copied usage: %v", err)
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return nil, Internalf("reading copied usage: %v", err)
	}
	return matches, nil
}

// MarkCopiedUsage validates the two rows and clears the recorded usage in the same
// transaction that preserves it in an event. It does not read artifacts or run processes.
func MarkCopiedUsage(ctx context.Context, s *store.Store, runID, sourceRunID, reason string, at time.Time) error {
	if runID == "" || sourceRunID == "" || strings.TrimSpace(reason) == "" {
		return Usagef("mark-copied-usage needs a run, a source run and a nonblank reason")
	}
	if runID == sourceRunID {
		return Usagef("a continued run is the same run: source and corrected run must be distinct")
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return markCopiedUsage(tx, runID, sourceRunID, reason, at)
	})
}

// CorrectCopiedUsage detects and marks all matches under one write lock. Detection
// cannot race a new usage record, and a failed mark rolls back every row and event.
func CorrectCopiedUsage(ctx context.Context, s *store.Store, reason string, at time.Time) ([]CopiedUsage, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, Usagef("copied-usage --apply needs a nonblank reason")
	}
	var matches []CopiedUsage
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		matches, err = detectCopiedUsage(tx.Reader())
		if err != nil {
			return err
		}
		for i := range matches {
			if err := markCopiedUsage(tx, matches[i].RunID, matches[i].SourceRunID, reason, at); err != nil {
				return err
			}
			matches[i].Reason = reason
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func markCopiedUsage(tx *store.Tx, runID, sourceRunID, reason string, at time.Time) error {
	var started, sourceStarted int64
	var tokens, sourceTokens sql.NullInt64
	var cost, sourceCost sql.NullFloat64
	for _, row := range []struct {
		id      string
		started *int64
		tokens  *sql.NullInt64
		cost    *sql.NullFloat64
	}{
		{runID, &started, &tokens, &cost}, {sourceRunID, &sourceStarted, &sourceTokens, &sourceCost},
	} {
		err := tx.QueryRow(`SELECT started_at,usage_tokens,usage_cost_usd FROM runs WHERE run_id=?`, row.id).Scan(row.started, row.tokens, row.cost)
		if err == sql.ErrNoRows {
			return Usagef("run %q is not in the store", row.id)
		}
		if err != nil {
			return Internalf("reading run %q: %v", row.id, err)
		}
	}
	if runID == sourceRunID || sourceStarted >= started {
		return Usagef("source run %q must have started before distinct run %q", sourceRunID, runID)
	}
	if !tokens.Valid || !sourceTokens.Valid || !cost.Valid || !sourceCost.Valid || tokens.Int64 != sourceTokens.Int64 || cost.Float64 != sourceCost.Float64 {
		return Usagef("run %q and source run %q must both report equal tokens and equal cost", runID, sourceRunID)
	}
	body, err := json.Marshal(CopiedUsage{RunID: runID, SourceRunID: sourceRunID, Tokens: tokens.Int64, CostUSD: cost.Float64, Reason: reason})
	if err != nil {
		return Internalf("encoding copied usage: %v", err)
	}
	seq, err := tx.Append(event.Event{Kind: CopiedUsageKind, ObjectType: "run", ObjectID: &runID, Payload: body, At: at})
	if err != nil {
		return Internalf("appending copied usage: %v", err)
	}
	if _, err := tx.Exec(`UPDATE runs SET usage_tokens=NULL,usage_cost_usd=NULL,event_seq=? WHERE run_id=?`, seq, runID); err != nil {
		return Internalf("clearing copied usage: %v", err)
	}
	return nil
}
