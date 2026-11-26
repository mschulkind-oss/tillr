package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/store"
)

type recordedBuildFields struct {
	restartVal, restartType   sql.NullString
	stopVal, stopType         sql.NullString
	initialVal, initialType   sql.NullString
	sessionVal, sessionType   sql.NullString
	ownerVal, ownerType       sql.NullString
	relaunchVal, relaunchType sql.NullString
	priorVal, priorType       sql.NullString
}

func readLatestDrainBuildFields(t *testing.T, storePath string) recordedBuildFields {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	var got recordedBuildFields
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT
			json_extract(payload, '$.restart_request'), json_type(payload, '$.restart_request'),
			json_extract(payload, '$.stop_request'), json_type(payload, '$.stop_request'),
			json_extract(payload, '$.initial'), json_type(payload, '$.initial'),
			json_extract(payload, '$.session_id'), json_type(payload, '$.session_id'),
			json_extract(payload, '$.loop_owner'), json_type(payload, '$.loop_owner'),
			json_extract(payload, '$.session_relaunch'), json_type(payload, '$.session_relaunch'),
			json_extract(payload, '$.prior_domain'), json_type(payload, '$.prior_domain')
			FROM events WHERE kind=? AND json_extract(payload, '$.role')=? ORDER BY seq DESC LIMIT 1`,
			restart.BuildKind, restart.RoleDrain).Scan(
			&got.restartVal, &got.restartType,
			&got.stopVal, &got.stopType,
			&got.initialVal, &got.initialType,
			&got.sessionVal, &got.sessionType,
			&got.ownerVal, &got.ownerType,
			&got.relaunchVal, &got.relaunchType,
			&got.priorVal, &got.priorType,
		)
	}); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestDrainBuildStartedRecordsCarriedRestartRequest(t *testing.T) {
	ctx := context.Background()
	h := newRestartHarness(t, []byte("drain build"), nil)
	t.Setenv(restart.RequestEnv, "")
	adopt := func() recordedBuildFields {
		t.Helper()
		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		if h.l.stopInheritedReaper != nil {
			t.Cleanup(h.l.stopInheritedReaper)
		}
		return readLatestDrainBuildFields(t, h.f.storePath)
	}

	first := adopt()
	if first.restartType.Valid {
		t.Fatalf("initial build-started must omit restart_request, got %s %q", first.restartType.String, first.restartVal.String)
	}
	if first.stopType.Valid {
		t.Fatalf("initial build-started must omit stop_request, got %s %q", first.stopType.String, first.stopVal.String)
	}
	if first.initialType.String != "true" {
		t.Fatalf("initial build-started initial = %s %q, want true", first.initialType.String, first.initialVal.String)
	}

	req := h.request(t, 1)[0]
	t.Setenv(restart.RequestEnv, req.ID)
	carried := adopt()
	if carried.restartType.String != "text" || carried.restartVal.String != req.ID {
		t.Fatalf("replacement build-started restart_request = %s %q, want %q", carried.restartType.String, carried.restartVal.String, req.ID)
	}
	if carried.stopType.Valid || carried.initialType.Valid {
		t.Fatalf("replacement build-started must omit stop_request and initial, got stop=%v initial=%v", carried.stopType, carried.initialType)
	}
}

func TestDrainBuildStartedStopProvenanceLifecycle(t *testing.T) {
	ctx := context.Background()
	h := newRestartHarness(t, []byte("drain build"), nil)
	t.Setenv(restart.RequestEnv, "")

	publishView := func() {
		t.Helper()
		if err := restart.PublishBuild(ctx, h.f.storePath, restart.BuildFact{
			Role: restart.RoleView, Revision: "view-rev", Digest: "view-digest",
			Executable: "/bin/tillr-view", PID: 4242, At: stopClock,
		}); err != nil {
			t.Fatal(err)
		}
	}
	adopt := func() recordedBuildFields {
		t.Helper()
		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		if h.l.stopInheritedReaper != nil {
			t.Cleanup(h.l.stopInheritedReaper)
		}
		return readLatestDrainBuildFields(t, h.f.storePath)
	}
	countStopEvents := func() int {
		t.Helper()
		s := openFixture(t, h.f.storePath)
		defer s.Close()
		var n int
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, StopRequestedKind).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A view build on an otherwise empty store must not hide the drain's initial start.
	publishView()
	initial := adopt()
	if initial.initialType.String != "true" || initial.restartType.Valid || initial.stopType.Valid {
		t.Fatalf("initial drain start = %+v, want initial=true only", initial)
	}

	// Next start after an exit without drain.Stop (even before refreshSession writes drain_sessions) is unrequested.
	unrequested := adopt()
	if unrequested.initialType.Valid || unrequested.restartType.Valid || unrequested.stopType.Valid {
		t.Fatalf("unrequested start after fault before refreshSession = %+v, want all provenance omitted", unrequested)
	}

	// Stop when no live local loop is running (only a foreign live loop or nothing) appends no drain.stop-requested.
	if _, err := Stop(ctx, StopConfig{Store: h.f.storePath, Now: func() time.Time { return stopClock }}); err == nil {
		t.Fatal("Stop with no live session unexpectedly succeeded")
	}
	insertSession(t, h.f, "foreign-live", foreignOwner(t), stopClock.Add(time.Minute))
	if _, err := Stop(ctx, StopConfig{Store: h.f.storePath, Now: func() time.Time { return stopClock }}); err == nil {
		t.Fatal("Stop with only foreign session unexpectedly succeeded")
	}
	if n := countStopEvents(); n != 0 {
		t.Fatalf("Stop without live local session appended %d %s events, want 0", n, StopRequestedKind)
	}
	fixtureWrite(t, h.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE drain_sessions SET ended_at = ? WHERE session_id = 'foreign-live'`, stopClock.UnixMilli())
		return err
	})
	stillUnrequested := adopt()
	if stillUnrequested.initialType.Valid || stillUnrequested.restartType.Valid || stillUnrequested.stopType.Valid {
		t.Fatalf("start after no-op Stop = %+v, want all provenance omitted", stillUnrequested)
	}

	// Live local session stopped through drain.Stop appends drain.stop-requested and the next start consumes it once.
	owner, exited := localLoop(t)
	insertSession(t, h.f, "local-live", owner, stopClock.Add(time.Minute))
	if _, err := Stop(ctx, StopConfig{Store: h.f.storePath, EndRuns: true, Now: func() time.Time { return stopClock }}); err != nil {
		t.Fatalf("Stop live local session: %v", err)
	}
	<-exited
	if n := countStopEvents(); n != 1 {
		t.Fatalf("Stop live local session appended %d %s events, want 1", n, StopRequestedKind)
	}

	var objType, objID string
	var payloadRaw []byte
	s := openFixture(t, h.f.storePath)
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT object_type, object_id, payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`, StopRequestedKind).Scan(&objType, &objID, &payloadRaw)
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	var stopPayload struct {
		ID         string    `json:"id"`
		SessionIDs []string  `json:"session_ids"`
		EndRuns    bool      `json:"end_runs"`
		At         time.Time `json:"at"`
	}
	if err := json.Unmarshal(payloadRaw, &stopPayload); err != nil {
		t.Fatal(err)
	}
	if objType != "session" || objID != stopPayload.ID || !strings.HasPrefix(stopPayload.ID, "stop-drain-") {
		t.Fatalf("stop-requested identity = (%q, %q, %+v)", objType, objID, stopPayload)
	}
	if len(stopPayload.SessionIDs) != 1 || stopPayload.SessionIDs[0] != "local-live" || !stopPayload.EndRuns || !stopPayload.At.Equal(stopClock) {
		t.Fatalf("stop-requested payload = %+v", stopPayload)
	}

	// View build between stop and loop start must not consume or mask drain.stop-requested.
	publishView()
	afterStop := adopt()
	if afterStop.stopType.String != "text" || afterStop.stopVal.String != stopPayload.ID || afterStop.restartType.Valid || afterStop.initialType.Valid {
		t.Fatalf("start after drain.Stop = %+v, want stop_request=%q only", afterStop, stopPayload.ID)
	}

	// The stop was consumed: if that restarted loop exits without another drain.Stop, the next start has no stop_request.
	afterCrash := adopt()
	if afterCrash.stopType.Valid || afterCrash.restartType.Valid || afterCrash.initialType.Valid {
		t.Fatalf("start after subsequent unrequested exit = %+v, want all provenance omitted", afterCrash)
	}
}

func TestDrainBuildStartedPreEventSessionFallback(t *testing.T) {
	ctx := context.Background()
	t.Setenv(restart.RequestEnv, "")

	for _, tc := range []struct {
		name     string
		stopFlag int
		wantStop string
	}{
		{"stopped pre-event session", 1, "drain_sessions"},
		{"unstopped pre-event session", 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRestartHarness(t, []byte("drain build"), nil)
			fixtureWrite(t, h.f, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, stop_requested, ended_at) VALUES ('legacy', 10, 20, 30, ?, 40)`, tc.stopFlag)
				return err
			})
			if err := h.l.adoptRestart(ctx); err != nil {
				t.Fatal(err)
			}
			got := readLatestDrainBuildFields(t, h.f.storePath)
			if got.initialType.Valid || got.restartType.Valid {
				t.Fatalf("pre-event session fallback = %+v, want initial and restart_request omitted", got)
			}
			if tc.wantStop == "" {
				if got.stopType.Valid {
					t.Fatalf("unstopped pre-event session set stop_request = %+v", got)
				}
			} else if got.stopType.String != "text" || got.stopVal.String != tc.wantStop {
				t.Fatalf("stopped pre-event session stop_request = %+v, want %q", got, tc.wantStop)
			}
		})
	}
}

func TestDrainBuildStartedRecordsSessionIDAndLoopOwnerBeforeRefresh(t *testing.T) {
	ctx := context.Background()
	h := newRestartHarness(t, []byte("drain build"), nil)
	t.Setenv(restart.RequestEnv, "")

	if err := h.l.adoptRestart(ctx); err != nil {
		t.Fatal(err)
	}
	if h.l.sessionID == "" {
		t.Fatal("adoptRestart did not mint Loop.sessionID")
	}
	got := readLatestDrainBuildFields(t, h.f.storePath)
	if got.sessionType.String != "text" || got.sessionVal.String != h.l.sessionID {
		t.Fatalf("build-started session_id = %s %q, want %q", got.sessionType.String, got.sessionVal.String, h.l.sessionID)
	}
	if got.ownerType.String != "object" {
		t.Fatalf("build-started loop_owner type = %q, want object", got.ownerType.String)
	}
	var recordedOwner presence.Owner
	if err := json.Unmarshal([]byte(got.ownerVal.String), &recordedOwner); err != nil {
		t.Fatal(err)
	}
	here, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	if recordedOwner != *here {
		t.Fatalf("build-started loop_owner = %+v, want %+v", recordedOwner, *here)
	}
	identity, err := processStoreIdentity(h.f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := openForWriting(loopPresencePath(identity, *here)); err != nil || state != streamWritten {
		t.Fatalf("presence file before refreshSession = (%v, %v), want (%v, nil)", state, err, streamWritten)
	}

	firstID := h.l.sessionID
	if err := h.l.refreshSession(ctx, h.l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	if h.l.sessionID != firstID {
		t.Fatalf("refreshSession changed sessionID from %q to %q", firstID, h.l.sessionID)
	}
}

func TestDrainBuildStartedSessionRelaunchProvenance(t *testing.T) {
	ctx := context.Background()
	t.Setenv(restart.RequestEnv, "")

	publishPrior := func(t *testing.T, f refusalLoopFixture, sessionID string, owner *presence.Owner) {
		t.Helper()
		if err := restart.PublishBuild(ctx, f.storePath, restart.BuildFact{
			Role: restart.RoleDrain, Revision: "prior-rev", Digest: "prior-digest",
			Executable: "/bin/tillr-drain", PID: 12345, At: stopClock.Add(-time.Minute),
			SessionID: sessionID, LoopOwner: owner,
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("dead foreign domain with unwritten presence file", func(t *testing.T) {
		h := newRestartHarness(t, []byte("drain build"), nil)
		prev := foreignOwner(t)
		identity, err := processStoreIdentity(h.f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		pf, err := openLoopPresence(identity, prev)
		if err != nil {
			t.Fatal(err)
		}
		if err := pf.Close(); err != nil {
			t.Fatal(err)
		}
		publishPrior(t, h.f, "prior-foreign-dead", &prev)

		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		got := readLatestDrainBuildFields(t, h.f.storePath)
		if got.relaunchType.String != "true" || got.priorVal.String != prev.Domain {
			t.Fatalf("dead foreign domain provenance = %+v, want session_relaunch=true prior_domain=%q", got, prev.Domain)
		}
	})

	t.Run("dead foreign domain across reboot", func(t *testing.T) {
		h := newRestartHarness(t, []byte("drain build"), nil)
		prev := foreignOwner(t)
		prev.Domain = "other-boot:pid:[1]"
		publishPrior(t, h.f, "prior-reboot", &prev)

		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		got := readLatestDrainBuildFields(t, h.f.storePath)
		if got.relaunchType.String != "true" || got.priorVal.String != prev.Domain {
			t.Fatalf("reboot provenance = %+v, want session_relaunch=true prior_domain=%q", got, prev.Domain)
		}
	})

	t.Run("dead foreign domain with ended_at set", func(t *testing.T) {
		h := newRestartHarness(t, []byte("drain build"), nil)
		prev := foreignOwner(t)
		publishPrior(t, h.f, "prior-ended", &prev)
		insertSession(t, h.f, "prior-ended", prev, stopClock.Add(time.Minute))
		fixtureWrite(t, h.f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE drain_sessions SET ended_at = ? WHERE session_id = 'prior-ended'`, stopClock.Add(-time.Second).UnixMilli())
			return err
		})

		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		got := readLatestDrainBuildFields(t, h.f.storePath)
		if got.relaunchType.String != "true" || got.priorVal.String != prev.Domain {
			t.Fatalf("ended foreign session provenance = %+v, want session_relaunch=true prior_domain=%q", got, prev.Domain)
		}
	})

	t.Run("pre-upgrade drain_sessions stale heartbeat fallback", func(t *testing.T) {
		for _, withBuildEvent := range []bool{true, false} {
			name := "pre-event store"
			if withBuildEvent {
				name = "pre-loop_owner build event"
			}
			t.Run(name, func(t *testing.T) {
				h := newRestartHarness(t, []byte("drain build"), nil)
				h.l.cfg.Now = func() time.Time { return stopClock }
				prev := foreignOwner(t)
				if withBuildEvent {
					publishPrior(t, h.f, "", nil)
				}
				staleNextPass := stopClock.Add(-SessionGrace - time.Millisecond)
				insertSession(t, h.f, "legacy-foreign", prev, staleNextPass)

				if err := h.l.adoptRestart(ctx); err != nil {
					t.Fatal(err)
				}
				got := readLatestDrainBuildFields(t, h.f.storePath)
				if got.relaunchType.String != "true" || got.priorVal.String != prev.Domain {
					t.Fatalf("stale fallback provenance = %+v, want session_relaunch=true prior_domain=%q", got, prev.Domain)
				}
			})
		}
	})

	t.Run("same domain unstopped restart stays unrequested", func(t *testing.T) {
		h := newRestartHarness(t, []byte("drain build"), nil)
		here, err := currentLoopOwner()
		if err != nil {
			t.Fatal(err)
		}
		publishPrior(t, h.f, "prior-same-domain", here)

		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		got := readLatestDrainBuildFields(t, h.f.storePath)
		if got.relaunchType.Valid || got.priorType.Valid {
			t.Fatalf("same-domain unstopped restart = %+v, want session_relaunch and prior_domain omitted", got)
		}
	})

	t.Run("prior fault in dead foreign domain stays unrequested", func(t *testing.T) {
		h := newRestartHarness(t, []byte("drain build"), nil)
		prev := foreignOwner(t)
		identity, err := processStoreIdentity(h.f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		pf, err := openLoopPresence(identity, prev)
		if err != nil {
			t.Fatal(err)
		}
		if err := pf.Close(); err != nil {
			t.Fatal(err)
		}
		publishPrior(t, h.f, "prior-faulted", &prev)
		storelog.RecordLoopEnded(h.f.storePath, true, "prior-faulted", "fault", "boom", 1, "", "", stopClock.Add(-time.Second), nil)

		if err := h.l.adoptRestart(ctx); err != nil {
			t.Fatal(err)
		}
		got := readLatestDrainBuildFields(t, h.f.storePath)
		if got.relaunchType.Valid || got.priorType.Valid {
			t.Fatalf("prior faulted foreign domain = %+v, want session_relaunch and prior_domain omitted", got)
		}
	})

	t.Run("live foreign loop collision stays unrequested", func(t *testing.T) {
		t.Run("streamWritten", func(t *testing.T) {
			h := newRestartHarness(t, []byte("drain build"), nil)
			prev := foreignOwner(t)
			identity, err := processStoreIdentity(h.f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			pf, err := openLoopPresence(identity, prev)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pf.Close() })
			publishPrior(t, h.f, "prior-live-written", &prev)

			if err := h.l.adoptRestart(ctx); err != nil {
				t.Fatal(err)
			}
			got := readLatestDrainBuildFields(t, h.f.storePath)
			if got.relaunchType.Valid || got.priorType.Valid {
				t.Fatalf("live foreign loop (streamWritten) = %+v, want session_relaunch and prior_domain omitted", got)
			}
		})

		t.Run("streamAbsent with current heartbeat", func(t *testing.T) {
			h := newRestartHarness(t, []byte("drain build"), nil)
			h.l.cfg.Now = func() time.Time { return stopClock }
			prev := foreignOwner(t)
			publishPrior(t, h.f, "prior-live-heartbeat", &prev)
			insertSession(t, h.f, "prior-live-heartbeat", prev, stopClock.Add(-SessionGrace))

			if err := h.l.adoptRestart(ctx); err != nil {
				t.Fatal(err)
			}
			got := readLatestDrainBuildFields(t, h.f.storePath)
			if got.relaunchType.Valid || got.priorType.Valid {
				t.Fatalf("live foreign loop (current heartbeat) = %+v, want session_relaunch and prior_domain omitted", got)
			}
		})
	})
}
