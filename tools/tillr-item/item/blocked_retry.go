package item

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// The adapter exit code and retry acknowledgments that tillr-drain's AuthorizeRetry
// enforces when superseding a finished run. Keeping the exact spelling here lets
// `tillr item state` refuse a direct blocked->queue move and print the one command
// `tillr drain retry` will accept, without coupling tillr-item's module graph to drain.
const (
	blockedAdapterWallExit = 124
	blockedAckGateFailed   = "gate-failed"
	blockedAckUnjudged     = "unjudged"
)

func formatBlockedRetryCommand(itemID, runID, ack string) string {
	cmd := fmt.Sprintf("tillr drain retry --item %s --run %s --by <who> --why <reason>", itemID, runID)
	if ack != "" {
		cmd += " --acknowledge " + ack
	}
	return cmd
}

// checkBlockedTransition refuses moving a blocked item back into an active/ready queue
// state when its latest finished run has not been superseded or acquitted. Without a
// retry record, moving only the item state leaves the drain holding the item as
// `run-link-missing` because the run that reported the obstacle was never answered.
func checkBlockedTransition(r *store.Reader, d definition.Definition, itemID, from, to string, allowConflictRelease bool) error {
	if allowConflictRelease || from != d.Blocked || to == d.Blocked || to == d.Closed || d.IsFinished(to) {
		return nil
	}

	conflicts, err := ReadConflicts(r)
	if err != nil {
		return Internalf("reading conflicts for %q: %v", itemID, err)
	}
	if c, ok := conflicts[itemID]; ok && c.Status == "owner" && c.DesignRunID != "" && c.TriageVersion == "" {
		cmd := formatBlockedRetryCommand(itemID, c.DesignRunID, "")
		return Rejectionf("blocked-use-retry", itemID,
			"item %q is blocked with unanswered design run %q; use %s", itemID, c.DesignRunID, cmd)
	}

	hasRuns, err := hasReaderTable(r, "runs")
	if err != nil {
		return Internalf("checking runs table: %v", err)
	}
	if !hasRuns {
		return nil
	}

	var (
		latestRunID string
		backend     string
		runState    string
		exitCode    sql.NullInt64
	)
	err = r.QueryRow(
		`SELECT run_id, backend, state, exit_code
		   FROM runs
		  WHERE item_id = ? AND state IN ('exited', 'failed-to-start', 'timed-out')
		  ORDER BY started_at DESC, event_seq DESC, rowid DESC
		  LIMIT 1`,
		itemID,
	).Scan(&latestRunID, &backend, &runState, &exitCode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return Internalf("reading latest finished run for %q: %v", itemID, err)
	}

	acquitted, err := isRunAcquittedByRepair(r, latestRunID)
	if err != nil {
		return Internalf("checking refusal events for %q: %v", latestRunID, err)
	}
	if acquitted {
		return nil
	}

	superseded, err := isRunSuperseded(r, itemID, latestRunID)
	if err != nil {
		return Internalf("checking retries for %q: %v", itemID, err)
	}
	if superseded {
		return nil
	}

	ack, err := requiredRetryAck(r, latestRunID, backend, runState, exitCode)
	if err != nil {
		return Internalf("checking retry acknowledgment for %q: %v", latestRunID, err)
	}
	cmd := formatBlockedRetryCommand(itemID, latestRunID, ack)
	return Rejectionf("blocked-use-retry", itemID,
		"item %q is blocked with unanswered run %q; use %s", itemID, latestRunID, cmd)
}

func hasReaderTable(r *store.Reader, table string) (bool, error) {
	var count int
	err := r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count)
	return count > 0, err
}

func isRunAcquittedByRepair(r *store.Reader, runID string) (bool, error) {
	var step sql.NullString
	err := r.QueryRow(
		`SELECT json_extract(payload, '$.step')
		   FROM events
		  WHERE kind = 'drain.refusal' AND object_type = 'run' AND object_id = ?
		  ORDER BY seq DESC
		  LIMIT 1`,
		runID,
	).Scan(&step)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return step.Valid && (step.String == "trunk-already-red" || step.String == "tooling-defect"), nil
}

func isRunSuperseded(r *store.Reader, itemID, runID string) (bool, error) {
	hasRetries, err := hasReaderTable(r, "retries")
	if err != nil || !hasRetries {
		return false, err
	}
	var exists bool
	err = r.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM retries WHERE item_id = ? AND supersedes_run_id = ?)`,
		itemID, runID,
	).Scan(&exists)
	return exists, err
}

func requiredRetryAck(r *store.Reader, runID, backend, runState string, exitCode sql.NullInt64) (string, error) {
	var judged bool
	if err := r.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM events WHERE kind = 'gate.verdict' AND object_type = 'run' AND object_id = ?)`,
		runID,
	).Scan(&judged); err != nil {
		return "", err
	}
	if judged {
		return blockedAckGateFailed, nil
	}
	if backend != "command" && (runState == "timed-out" || (runState == "exited" && exitCode.Valid && exitCode.Int64 == blockedAdapterWallExit)) {
		return blockedAckUnjudged, nil
	}
	return "", nil
}
