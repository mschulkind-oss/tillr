package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchEpisodeFaces(t *testing.T) {
	ctx := context.Background()
	s, dir := fixture(t, false)
	// Guard every domain table installed in the fixture. The tested faces may
	// write only episodes and events; even a no-op UPDATE must not be attempted.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		rows, err := tx.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND (name LIKE '%item%' OR name LIKE '%run%' OR name LIKE '%repair%')`)
		if err != nil {
			return err
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			tables = append(tables, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for i, table := range tables {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				if _, err := tx.Exec(fmt.Sprintf(`CREATE TRIGGER guard_%d_%s BEFORE %s ON %q BEGIN SELECT RAISE(ABORT,'watch must not mutate domain state'); END`, i, op, op, table)); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	call := func(face string, args ...string) (int, string, string) {
		var out, diag bytes.Buffer
		code := run(ctx, append([]string{face, "--store", s.Path()}, args...), &out, &diag)
		return code, out.String(), diag.String()
	}
	refuse := func(msg, face string, args ...string) {
		t.Helper()
		code, out, diag := call(face, args...)
		if code != 1 || out != "" || !strings.Contains(diag, msg) {
			t.Fatalf("%s: code=%d out=%q stderr=%q, want %q", face, code, out, diag, msg)
		}
	}
	success := func(want, face string, args ...string) {
		t.Helper()
		code, out, diag := call(face, args...)
		if code != 0 || out != want || diag != "" {
			t.Fatalf("%s: code=%d out=%q stderr=%q, want %q", face, code, out, diag, want)
		}
	}
	success("", "eval", "--once", "--rules", dir)
	for range 2 {
		fact(t, s, `{"item_id":"one"}`)
	}
	success("", "eval", "--once", "--rules", dir)
	var id string
	faceRead(t, s, `SELECT alert_id FROM alert_episodes`, &id)
	head := func() int64 {
		t.Helper()
		n, err := s.EventHeadSeq()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	refuseDeadline := func(state, by string) {
		t.Helper()
		wantEpisode := "alert " + id + " is " + state
		if by != "" {
			wantEpisode += " by " + by
		}
		for _, tc := range []struct{ until, reason string }{
			{"1m30s", "requires a positive whole-number duration"},
			{"2000-01-01T00:00:00Z", "must be later than the current time"},
		} {
			before := head()
			code, out, diag := call("silence", id, "--until="+tc.until)
			if code != 1 || out != "" || !strings.Contains(diag, tc.reason) || !strings.Contains(diag, wantEpisode) {
				t.Errorf("silence --until=%s: code=%d out=%q stderr=%q, want %q and %q", tc.until, code, out, diag, wantEpisode, tc.reason)
			}
			var gotState, gotBy string
			var until *int64
			var fingerprint *string
			faceRead(t, s, `SELECT state,coalesce(acked_by,''),silence_until_ms,silence_on_fingerprint FROM alert_episodes`, &gotState, &gotBy, &until, &fingerprint)
			if gotState != state || gotBy != by || until != nil || fingerprint != nil || head() != before {
				t.Fatalf("silence --until=%s changed the episode or appended an event", tc.until)
			}
		}
	}
	refuseOptions := func(state, by string) {
		t.Helper()
		wantEpisode := "alert " + id + " is " + state
		if state == "acknowledged" && by != "" {
			wantEpisode += " by " + by
		} else if state == "silenced" && by != "" {
			wantEpisode += " (acknowledged by " + by + ")"
		}
		before := head()
		for _, tc := range []struct {
			face, reason string
			args         []string
		}{
			{"ack", "--by is required", []string{"--store", s.Path(), id, "--by="}},
			{"ack", "flag needs an argument: -by", []string{"--store", s.Path(), id, "--by"}},
			{"ack", "flag needs an argument: -note", []string{"--store", s.Path(), id, "--by=judge", "--note"}},
			{"investigate", "--cause is required", []string{"--store", s.Path(), id, "--by=judge", "--cause=", "--evidence=seq 1"}},
			{"investigate", "flag needs an argument: -cause", []string{"--store", s.Path(), id, "--by=judge", "--cause"}},
			{"silence", "flag needs an argument: -until", []string{"--store", s.Path(), id, "--until"}},
			{"silence", "invalid boolean value", []string{"--store", s.Path(), "--until-change=bad", id}},
			{"ack", "requires --store and one alert ID", []string{"--store", s.Path(), id, "--by=judge", "extra"}},
			{"silence", "requires --store and one alert ID", []string{"--store", s.Path(), id, "--until=1h", "extra"}},
			{"investigate", "requires --store and one alert ID", []string{"--store", s.Path(), id, "--by=judge", "--cause=c", "--evidence=e", "extra"}},
			{"ack", "requires --store and one alert ID", []string{id, "extra", "--store", s.Path(), "--by=judge"}},
			{"silence", "requires --store and one alert ID", []string{"--store", s.Path(), "extra", id, "--until=1h"}},
			{"investigate", "requires --store and one alert ID", []string{"--store", s.Path(), id, "--by=judge", "--cause=c", "--evidence=e", "--", "--extra"}},
			{"investigate", "not defined: -rules", []string{"--store", s.Path(), "--rules", dir, id, "--by=judge", "--cause=c", "--evidence=e"}},
			{"settle", "not defined: -rules", []string{id, "--rules", dir, "--store", s.Path(), "--by=judge", "--outcome=fix-fault", "--item=item-1"}},
			{"silence", "exactly one", []string{"--store", s.Path(), id}},
			{"silence", "exactly one", []string{"--store", s.Path(), id, "--until=1h", "--until-change"}},
			{"silence", "exactly one", []string{"--store", s.Path(), id, "--until-change=false"}},
		} {
			var out, diag bytes.Buffer
			code := run(ctx, append([]string{tc.face}, tc.args...), &out, &diag)
			if code != 1 || out.String() != "" || !strings.Contains(diag.String(), tc.reason) || !strings.Contains(diag.String(), wantEpisode) {
				t.Errorf("%s %v: code=%d out=%q stderr=%q, want %q and %q", tc.face, tc.args, code, out.String(), diag.String(), wantEpisode, tc.reason)
			}
		}
		var gotState, gotBy string
		faceRead(t, s, `SELECT state,coalesce(acked_by,'') FROM alert_episodes`, &gotState, &gotBy)
		if gotState != state || gotBy != by || head() != before {
			t.Fatal("option refusals changed the episode or appended an event")
		}
	}
	start := head()
	refuse("no fingerprint", "silence", id, "--until-change")
	refuseOptions("open", "")
	refuseDeadline("open", "")
	refuse("not found", "ack", "missing", "--by=alice")
	refuse("not defined: -rules", "investigate", id, "--by=alice", "--cause=c", "--evidence=e", "--rules", dir)
	if head() != start {
		t.Fatal("refusals appended events")
	}
	// A failure inserting the event must also roll back the episode update.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_ack BEFORE INSERT ON events WHEN NEW.kind='watch.alert.acknowledged' BEGIN SELECT RAISE(ABORT,'cannot record ack'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	refuse("cannot record ack", "ack", id, "--by=alice")
	var state string
	faceRead(t, s, `SELECT state FROM alert_episodes`, &state)
	if state != "open" || head() != start {
		t.Fatalf("failed event append changed state or head: %s %d", state, head())
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER fail_ack`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		actor     string
		code      int
		out, diag string
	}
	var wg sync.WaitGroup
	outcomes := make(chan outcome, 2)
	for _, actor := range []string{"alice", "bob"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, out, diag := call("ack", id, "--by", actor)
			outcomes <- outcome{actor, code, out, diag}
		}()
	}
	wg.Wait()
	close(outcomes)
	var winner string
	var lost []outcome
	for o := range outcomes {
		if o.code == 0 && o.out == "alert "+id+" acknowledged\n" && o.diag == "" {
			winner = o.actor
		} else {
			lost = append(lost, o)
		}
	}
	if winner == "" || len(lost) != 1 || lost[0].code != 1 || lost[0].out != "" || !strings.Contains(lost[0].diag, "acknowledged") || !strings.Contains(lost[0].diag, winner) {
		t.Fatalf("competing acknowledgements: winner=%q lost=%+v", winner, lost)
	}
	refuse(winner, "ack", id, "--by", winner)
	refuseOptions("acknowledged", winner)
	refuseDeadline("acknowledged", winner)
	refuse("acknowledged by "+winner+" and has no fingerprint; use --until", "silence", id, "--until-change")
	success("alert "+id+" silenced\n", "silence", id, "--until=1h", "--reason=waiting")
	refuseOptions("silenced", winner)
	refuse("silenced (acknowledged by "+winner+")", "ack", id, "--by=bob")
	success("alert "+id+" silenced\n", "silence", "--until=2099-01-01T03:00:00+03:00", "--", id)
	var by string
	var ackedAt, until int64
	var note, fp *string
	faceRead(t, s, `SELECT acked_by,acked_note,acked_at,silence_until_ms,silence_on_fingerprint FROM alert_episodes`, &by, &note, &ackedAt, &until, &fp)
	if by != winner || note != nil || ackedAt == 0 || until != time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() || fp != nil {
		t.Fatalf("unexpected silenced state: %q %v %d %d %v", by, note, ackedAt, until, fp)
	}
	success("alert "+id+" investigated\n", "investigate", id, "--by=bob", "--cause=real fault", "--evidence=seq 1")
	for i, want := range []struct {
		kind   string
		fields map[string]any
	}{
		{"acknowledged", map[string]any{"by": winner, "note": nil}},
		{"silenced", map[string]any{"reason": "waiting"}},
		{"silenced", map[string]any{"until": "2099-01-01T00:00:00Z"}},
		{"investigated", map[string]any{"by": "bob", "cause": "real fault", "evidence": "seq 1"}},
	} {
		var kind, object, idInEvent string
		var raw []byte
		faceRead(t, s, fmt.Sprintf(`SELECT kind,object_type,object_id,payload FROM events WHERE seq=%d`, start+int64(i)+1), &kind, &object, &idInEvent, &raw)
		var p map[string]any
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if kind != "watch.alert."+want.kind || object != "alert" || idInEvent != id || p["alert_id"] != id || p["rule_id"] != "refusals" || p["subject_key"] != "one" || p["severity"] != "warning" {
			t.Fatalf("unexpected event %s %s %s %s", kind, object, idInEvent, raw)
		}
		if _, ok := p["receiver"]; ok {
			t.Fatalf("unexpected receiver in %s", raw)
		}
		for k, v := range want.fields {
			if got, ok := p[k]; !ok || got != v {
				t.Fatalf("want %s=%v in %s", k, v, raw)
			}
		}
	}
}

func faceRead(t *testing.T, s *store.Store, query string, dest ...any) {
	t.Helper()
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error { return rd.QueryRow(query).Scan(dest...) }); err != nil {
		t.Fatal(err)
	}
}

func TestWatchEpisodeFingerprintSilenceAndWait(t *testing.T) {
	ctx := context.Background()
	s, dir := fixture(t, false)
	rule := strings.Replace(testRule, `window="1h"`, "window=\"1h\"\nfingerprint=['step']", 1)
	if err := os.WriteFile(filepath.Join(dir, "nested", "refusals.toml"), []byte(rule), 0600); err != nil {
		t.Fatal(err)
	}
	command := func(out io.Writer, face string, args ...string) {
		t.Helper()
		var diag bytes.Buffer
		if code := run(ctx, append([]string{face, "--store", s.Path()}, args...), out, &diag); code != 0 || diag.Len() != 0 {
			t.Fatalf("%s %v: %d %s", face, args, code, &diag)
		}
	}
	add := func(item, step string) { fact(t, s, fmt.Sprintf(`{"item_id":%q,"step":%q}`, item, step)) }
	command(io.Discard, "eval", "--rules", dir, "--once")
	add("a", "build")
	add("a", "build")
	wait := func() string {
		t.Helper()
		var out bytes.Buffer
		command(&out, "wait", "--rules", dir, "--timeout=2s")
		return out.String()
	}
	first := wait()
	var id string
	var opened, shown int64
	faceRead(t, s, `SELECT alert_id,opened_seq FROM alert_episodes`, &id, &opened)
	faceRead(t, s, `SELECT shown_seq FROM foreground_delivery`, &shown)
	command(io.Discard, "seen", "--through", fmt.Sprint(shown))
	command(io.Discard, "ack", id, "--by=alice", "--note=investigating")
	command(io.Discard, "silence", id, "--until-change", "--reason=known signature")
	var payload string
	faceRead(t, s, `SELECT payload FROM events WHERE kind='watch.alert.silenced'`, &payload)
	var silenced map[string]any
	if err := json.Unmarshal([]byte(payload), &silenced); err != nil || silenced["until_change"] != true || silenced["reason"] != "known signature" {
		t.Fatalf("silence payload: %s (%v)", payload, err)
	}
	add("a", "build")
	command(io.Discard, "eval", "--rules", dir, "--once")
	var state, ackedNote, fp string
	var count int
	faceRead(t, s, `SELECT state,acked_note,condition_fingerprint,observation_count FROM alert_episodes`, &state, &ackedNote, &fp, &count)
	if state != "silenced" || ackedNote != "investigating" || fp == "" || count != 2 {
		t.Fatalf("state=%s note=%s fingerprint=%s count=%d", state, ackedNote, fp, count)
	}
	add("b", "other")
	add("b", "other")
	command(io.Discard, "eval", "--rules", dir, "--once")
	add("a", "smoke")
	second := wait()
	if !strings.Contains(first, "1 pending") || !strings.Contains(second, "2 pending") || !strings.Contains(second, "[alert "+id+" refusals a") || strings.Index(second, "refusals b, warning]") > strings.Index(second, "refusals a, warning]") || !strings.Contains(second, "a has 4 refusals") {
		t.Fatalf("unexpected delivery:\n%s\n%s", first, second)
	}
	var newFP, reopenedID string
	var gotOpened, reopen, gotShown, seen int64
	faceRead(t, s, `SELECT alert_id,opened_seq,condition_fingerprint FROM alert_episodes WHERE subject_key='a' AND episode_num=1 AND acked_by IS NULL AND acked_note IS NULL AND acked_at IS NULL AND silence_until_ms IS NULL AND silence_on_fingerprint IS NULL AND state='open'`, &reopenedID, &gotOpened, &newFP)
	if reopenedID != id {
		t.Fatalf("reopened ID = %s, want %s", reopenedID, id)
	}
	faceRead(t, s, `SELECT seq,json_extract(payload,'$.cause') FROM events WHERE kind='watch.alert.reopened'`, &reopen, &state)
	faceRead(t, s, `SELECT shown_seq,seen_seq FROM foreground_delivery`, &gotShown, &seen)
	if gotOpened != opened || fp == newFP || state != "fingerprint-changed" || gotShown != reopen || seen != shown {
		t.Fatalf("reopening: opened %d/%d fp %s/%s cause %s shown %d/%d seen %d/%d", gotOpened, opened, fp, newFP, state, gotShown, reopen, seen, shown)
	}
}
func TestWatchRefusedMutationsPreserveExistingStore(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "0")
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes(alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,opened_seq,opened_at,last_observed_seq,last_observed_at,bundle_json)
			VALUES ('alrt-existing','refusals','one',1,'open','warning','',1,0,1,0,'{}')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	journal := func(query string) string {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var mode string
		if err := db.QueryRowContext(context.Background(), query).Scan(&mode); err != nil {
			t.Fatal(err)
		}
		return mode
	}
	snapshot := func() []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, tc := range []struct {
		name, want string
		args       []string
	}{
		{"ack", "alert missing not found", []string{"ack", "missing", "--by", "operator"}},
		{"ack-arity", "requires --store and one alert ID", []string{"ack", "missing", "--by=operator", "extra"}},
		{"ack-existing-arity", "alert alrt-existing is open", []string{"ack", "alrt-existing", "--by=operator", "extra"}},
		{"ack-existing-empty", "alert alrt-existing is open", []string{"ack", "alrt-existing", "--by="}},
		{"silence", "alert missing not found", []string{"silence", "missing", "--until-change"}},
		{"silence-mode", "alert missing not found", []string{"silence", "missing"}},
		{"silence-existing-fp", "alert alrt-existing is open", []string{"silence", "alrt-existing", "--until-change"}},
		{"investigate", "alert missing not found", []string{"investigate", "missing", "--by", "op", "--cause", "c", "--evidence", "e"}},
		{"settle", "alert missing not found", []string{"settle", "missing", "--by", "op", "--outcome", "fix-fault", "--item", "item-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if mode := journal("PRAGMA journal_mode=DELETE"); mode != "delete" {
				t.Fatalf("journal mode setup: %q", mode)
			}
			before := snapshot()
			var stdout, stderr bytes.Buffer
			if code := run(ctx, append([]string{tc.args[0], "--store", path}, tc.args[1:]...), &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("refusal: %d\nstdout: %s\nstderr: %s", code, &stdout, &stderr)
			}
			if mode := journal("PRAGMA journal_mode"); mode != "delete" {
				t.Errorf("refusal changed journal mode to %q", mode)
			}
			if !bytes.Equal(before, snapshot()) {
				t.Error("refusal changed database bytes")
			}
		})
	}
}
