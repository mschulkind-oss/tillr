package main

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr/store"
)

// SessionGraceMS allows ordinary scheduling and read latency after the recorded
// next-pass deadline. Beyond five seconds, the view reports a missed heartbeat.
const SessionGraceMS int64 = 5000

// HeartbeatIntervalMS matches the loop's one-second session refresh interval.
const HeartbeatIntervalMS int64 = 1000

type loopSession struct {
	SessionID     string `json:"session_id"`
	StartedAtMS   int64  `json:"started_at_ms"`
	RefreshedAtMS int64  `json:"refreshed_at_ms"`
	NextPassAtMS  int64  `json:"next_pass_at_ms"`
	EndedAtMS     *int64 `json:"ended_at_ms"`
	// Lanes are the lane ids the loop was configured to run (its `--lanes` count plus
	// the express lanes). A loop or store that predates the column sends none.
	Lanes []string `json:"lanes,omitempty"`
}

func readLoopSession(r *store.Reader, now time.Time) (*loopSession, bool, string, error) {
	session, active, note, err := readLoopSessionClocks(r, now)
	if session != nil {
		var raw sql.NullString
		if r.QueryRow(`SELECT lanes FROM drain_sessions WHERE session_id = ?`, session.SessionID).Scan(&raw) == nil && raw.Valid {
			_ = json.Unmarshal([]byte(raw.String), &session.Lanes)
		}
	}
	return session, active, note, err
}

// readConfiguredSessionLanes reads the newest session's configured lane list and a
// cache fingerprint that does not move on 1s heartbeat clock refreshes.
func readConfiguredSessionLanes(r *store.Reader) ([]string, string) {
	var sessionID string
	var raw sql.NullString
	err := r.QueryRow(`SELECT session_id, lanes FROM drain_sessions ORDER BY started_at DESC, rowid DESC LIMIT 1`).Scan(&sessionID, &raw)
	if err != nil || !raw.Valid {
		return nil, ""
	}
	var lanes []string
	if json.Unmarshal([]byte(raw.String), &lanes) != nil || len(lanes) == 0 {
		return nil, ""
	}
	return lanes, sessionID + ":" + raw.String
}

func readLoopSessionClocks(r *store.Reader, now time.Time) (*loopSession, bool, string, error) {
	var session loopSession
	var ended sql.NullInt64
	var rawOwner []byte
	err := r.QueryRow(`SELECT session_id, started_at, refreshed_at, next_pass_at, ended_at, loop_owner
  FROM drain_sessions ORDER BY started_at DESC, rowid DESC LIMIT 1`).Scan(
		&session.SessionID, &session.StartedAtMS, &session.RefreshedAtMS, &session.NextPassAtMS, &ended, &rawOwner)
	if isMissingColumn(err) {
		err = r.QueryRow(`SELECT session_id, started_at, refreshed_at, next_pass_at, ended_at
  FROM drain_sessions ORDER BY started_at DESC, rowid DESC LIMIT 1`).Scan(
			&session.SessionID, &session.StartedAtMS, &session.RefreshedAtMS, &session.NextPassAtMS, &ended)
		rawOwner = nil
	}
	if err == sql.ErrNoRows || isMissingTable(err) {
		return nil, false, "no loop session has been recorded", nil
	}
	if err != nil {
		return nil, false, "the loop session could not be read", err
	}
	if ended.Valid {
		session.EndedAtMS = &ended.Int64
		return &session, false, "the loop session has ended", nil
	}
	if len(rawOwner) > 0 {
		var owner presence.Owner
		if json.Unmarshal(rawOwner, &owner) == nil {
			if presence.SessionOwnerGone(r, readerStorePath(r), owner) {
				endedAt := session.RefreshedAtMS
				session.EndedAtMS = &endedAt
				return &session, false, "the loop session owner is gone", nil
			}
		}
	}
	if now.UnixMilli()-session.RefreshedAtMS > 2*HeartbeatIntervalMS ||
		(now.UnixMilli() > session.NextPassAtMS && now.UnixMilli()-session.NextPassAtMS > SessionGraceMS) {
		return &session, false, "the loop missed its recorded next-pass deadline", nil
	}
	return &session, true, "the loop session heartbeat is current", nil
}
