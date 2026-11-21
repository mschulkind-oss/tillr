package drain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	reasonVerifyTriage    = faults.VerifyTriageReason
	verifyTriageResetKind = "drain.verify-triage.reset"
)

type verifyFindingRecord struct {
	Criterion string `json:"criterion"`
	Reason    string `json:"reason"`
	Snapshot  string `json:"snapshot,omitempty"`
}

func (f verifyFindingRecord) text() string { return f.Criterion + ": " + f.Reason }

type verifyFeedback struct {
	Findings []verifyFindingRecord `json:"findings"`
	Passed   []string              `json:"passed,omitempty"`
}

// A file-scoped finding compares the cited paths, not commit metadata or unrelated
// files. Without explicit file evidence, only equality of the whole judged tree
// proves that the candidate did not change. A failed read cannot prove a repeat.
func (l *Loop) verifyFindingSnapshot(ctx context.Context, c Candidate, tree string, evidence []string) string {
	var paths []string
	for _, locator := range evidence {
		path, ok := strings.CutPrefix(locator, "file:")
		if !ok {
			path, ok = strings.CutPrefix(locator, "path:")
		}
		if !ok {
			return tree
		}
		path, _, _ = strings.Cut(path, ":")
		paths = append(paths, strings.TrimPrefix(path, "./"))
	}
	if len(paths) == 0 {
		return tree
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	out, err := l.git(ctx, c.Worktree, append([]string{"--literal-pathspecs", "ls-tree", "-z", "--full-tree", tree, "--"}, paths...)...)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(paths, "\x00")+"\x00"+out)))
}

func (l *Loop) checkedVerifyFeedback(ctx context.Context, c Candidate, tree string, verdicts []item.JudgeEntry) *verifyFeedback {
	f := &verifyFeedback{}
	for _, v := range verdicts {
		switch v.Verdict {
		case item.VerdictDone:
			f.Passed = append(f.Passed, v.CriterionID)
		case item.VerdictNotDone:
			f.Findings = append(f.Findings, verifyFindingRecord{v.CriterionID, v.Reason, l.verifyFindingSnapshot(ctx, c, tree, v.Evidence)})
		}
	}
	slices.SortFunc(f.Findings, func(a, b verifyFindingRecord) int { return strings.Compare(a.Criterion, b.Criterion) })
	return f
}

// collectVerifyFeedback returns all unanswered findings. A later done verdict
// answers the findings for that criterion; a different not-done finding does not.
// Only a finding actually sent back to this candidate's writer can cause a hold.
// Retry authorizations reset repeat detection, not what the worker needs to read.
func collectVerifyFeedback(r *store.Reader, itemID, runID, writerID string, current *verifyFeedback) (detail, repeat string, err error) {
	var returned string
	err = r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.decision.returned.detail'),'') FROM events
 WHERE kind=? AND object_type='run' AND object_id=? AND json_extract(payload,'$.decision.returned.cause')=?
 ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, writerID, RefusalVerifyNotDone).Scan(&returned)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	rows, err := r.Query(`WITH boundary AS (
 SELECT COALESCE(MAX(seq),0) AS seq FROM events WHERE (kind=:reset AND object_type='item' AND object_id=:item)
 OR (kind=:design AND json_extract(payload,'$.item_id')=:item)
)
SELECT e.payload, e.seq > boundary.seq FROM events e CROSS JOIN boundary
 WHERE e.kind=:refusal AND e.object_type='run' AND e.object_id<>:run
 AND json_extract(e.payload,'$.item_id')=:item AND json_extract(e.payload,'$.step')=:step
 ORDER BY e.seq`, sql.Named("reset", verifyTriageResetKind), sql.Named("design", DesignRetryKind), sql.Named("item", itemID),
		sql.Named("refusal", RefusalKind), sql.Named("run", runID), sql.Named("step", RefusalVerifyNotDone))
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	type unanswered struct {
		verifyFindingRecord
		inCycle bool
	}
	var pending []unanswered
	apply := func(f *verifyFeedback, inCycle, latest bool) {
		pending = slices.DeleteFunc(pending, func(p unanswered) bool { return p.Criterion != "" && slices.Contains(f.Passed, p.Criterion) })
		for _, finding := range f.Findings {
			ix := slices.IndexFunc(pending, func(p unanswered) bool { return p.Criterion == finding.Criterion && p.Reason == finding.Reason })
			if ix < 0 {
				pending = append(pending, unanswered{finding, inCycle})
				continue
			}
			prior := pending[ix]
			if latest && repeat == "" && prior.inCycle && finding.Snapshot != "" && prior.Snapshot == finding.Snapshot &&
				strings.Contains("\n"+returned+"\n", "\n"+prior.text()+"\n") {
				repeat = finding.text()
			}
			pending[ix] = unanswered{finding, inCycle}
		}
	}
	for rows.Next() {
		var raw []byte
		var inCycle bool
		if err := rows.Scan(&raw, &inCycle); err != nil {
			return "", "", err
		}
		var p refusalPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", "", err
		}
		if p.VerifyFeedback != nil {
			apply(p.VerifyFeedback, inCycle, false)
		} else if p.Detail != "" {
			// Older refusals have prose, not criterion snapshots. Send it back
			// intact, but never convict a repeat on that evidence alone.
			apply(&verifyFeedback{Findings: []verifyFindingRecord{{Reason: p.Detail}}}, false, false)
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	apply(current, true, true)
	var lines []string
	for _, p := range pending {
		if p.Criterion == "" {
			lines = append(lines, p.Reason)
		} else {
			lines = append(lines, p.text())
		}
	}
	return strings.Join(lines, "\n"), repeat, nil
}

func (l *Loop) verifyRepeatHold(c Candidate, finding string) ItemFault {
	return ItemFault{ItemID: c.ItemID, RunID: c.RunID, LaneID: c.LaneID, Reason: reasonVerifyTriage,
		Detail: "Repeated verifier finding on unchanged candidate evidence:\n" + finding +
			"\nThe operator moves: " + faults.VerifyTriageCommand(l.cfg.Store, c.ItemID, c.RunID, "")}
}

// legacyVerifyRepeat recognizes the conflicts linked by pre-upgrade triage holds,
// in case an operator retries one before the drain has cleared it.
func legacyVerifyRepeat(c item.Conflict) bool {
	return (c.Status == "raised" || c.Status == "awaiting" || c.Status == "owner") &&
		c.RunID != "" && strings.HasPrefix(c.Text, fmt.Sprintf("%s run %s ended (refused at %s)", IdenticalRepeatPrefix, c.RunID, RefusalVerifyNotDone))
}

// Release count-only holds recorded by older drains; do not manufacture holds
// from historical refusals or clear an actual repeated-finding hold.
func (l *Loop) releaseCountedVerifyHolds(ctx context.Context, s *store.Store) error {
	var found bool
	const query = `WITH latest AS (
 SELECT MAX(seq) AS seq FROM events WHERE kind IN (?,?) AND object_type='item'
 AND json_extract(payload,'$.reason')=? GROUP BY object_id
) SELECT e.payload FROM latest JOIN events e ON e.seq=latest.seq
 WHERE e.kind=? AND json_extract(e.payload,'$.detail') LIKE 'Three verifier refusals:%'`
	args := []any{itemFaultKind, itemFaultClearedKind, reasonVerifyTriage, itemFaultKind}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow("SELECT EXISTS("+query+")", args...).Scan(&found)
	}); err != nil || !found {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		var holds []ItemFault
		for rows.Next() {
			var raw []byte
			var f ItemFault
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &f); err != nil {
				return err
			}
			holds = append(holds, f)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		for _, f := range holds {
			if err := clearItemFault(tx, f, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
}
