package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRetiredAutoQAVocabularyRefusedOnNewWrites(t *testing.T) {
	for _, kind := range []string{"actor", "verdict-class", "decision", "successor"} {
		t.Run(kind, func(t *testing.T) {
			db := storedWithCriterion(t, "retired-autoqa", "open")
			var code int
			var out []byte
			var replacements []string
			switch kind {
			case "actor", "verdict-class":
				v := mechanicalVerdict()
				if kind == "actor" {
					v.ActorKind = "autoqa"
					replacements = []string{"mechanical", "agent", "workflow", "actor"}
				} else {
					v.ClaimClass = "qa-verification"
					replacements = []string{"verification-only"}
				}
				code, out = recordVerdict(t, db, v)
			case "decision", "successor":
				d := decision("dec-2", "an automated ruling")
				d.DecidedBy = "autoqa"
				verb := "record"
				args := []string{}
				if kind == "successor" {
					if c, o := run(t, "decision", "record", "--store", db, "--input", input(t, envelope(t, "decision.record", "rec-1", decision("dec-1", "original"))), "--now", issuedAt); c != 0 {
						t.Fatalf("original: %d %s", c, o)
					}
					verb = "supersede"
					args = []string{"--decision", "dec-1"}
				}
				args = append([]string{"decision", verb, "--store", db, "--input", input(t, envelope(t, "decision.record", "rec-2", d)), "--now", issuedAt}, args...)
				code, out = run(t, args...)
				replacements = []string{"owner", "whale"}
			}
			got := assertRefused(t, code, out, "actor-retired")
			for _, replacement := range replacements {
				if !strings.Contains(got.Payload.Reason, replacement) {
					t.Fatalf("no replacement %q in %q", replacement, got.Payload.Reason)
				}
			}
			if got := criterionOf(t, showItem(t, db, "item_042"), "c1"); got.State != "open" {
				t.Fatalf("refusal changed criterion: %+v", got)
			}
			for _, event := range []string{"item.verdict", "item.grant"} {
				if n := eventsOfKind(t, db, event); n != 0 {
					t.Fatalf("refusal appended %s", event)
				}
			}
			wantDecisions := 0
			if kind == "successor" {
				wantDecisions = 1
			}
			if n := eventsOfKind(t, db, "decision.record"); n != wantDecisions {
				t.Fatalf("decision events: %d, want %d", n, wantDecisions)
			}
			if kind == "successor" {
				c, o := run(t, "decision", "list", "--store", db)
				var list decisionList
				decodeAs(t, o, &list)
				if c != 0 || len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecisionID != "dec-1" {
					t.Fatalf("refusal changed original: %d %s", c, o)
				}
			}
		})
	}
}

func TestAutoQAUsesOrdinaryVerdictKinds(t *testing.T) {
	for _, mode := range []string{"mechanical", "agent-selected", "agent-unselected", "agent-self"} {
		t.Run(mode, func(t *testing.T) {
			db := storedWithCriterion(t, "ordinary-autoqa", "open")
			v := mechanicalVerdict()
			v.Actor = "autoqa/runner"
			if mode != "mechanical" {
				v = agentVerdict(t)
				v.Actor = "autoqa/reviewer"
				if mode != "agent-unselected" {
					selectEvaluator(t, db, selectedGraph)
				}
				if mode == "agent-self" {
					v.JudgedActor = v.Actor
				}
			}
			code, out := recordVerdict(t, db, v)
			if mode == "agent-unselected" {
				assertRefused(t, code, out, "evaluator-selection")
				return
			}
			if mode == "agent-self" {
				assertRefused(t, code, out, "self-judging")
				return
			}
			if code != 0 {
				t.Fatalf("verdict: %d %s", code, out)
			}
			p := lifecyclePayload(t, out)
			if p["actor"] != v.Actor || p["actor_kind"] != v.ActorKind || p["claim_class"] != "verification-only" {
				t.Fatalf("identity changed: %v", p)
			}
			if got := criterionOf(t, showItem(t, db, "item_042"), "c1"); got.State != "done" {
				t.Fatalf("criterion: %+v", got)
			}
		})
	}
}

// The SQL fixture was captured with the old writers, not synthesized by the new
// decoders. Compare all legacy record bytes before and after a migrating open.
func legacyAutoQAStore(t *testing.T) string {
	t.Helper()
	db := filepath.Join(scratch(t, "legacy-autoqa"), "board.sqlite3")
	fixture, err := os.ReadFile("testdata/legacy-autoqa.sql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(string(fixture)); err != nil {
		t.Fatal(err)
	}
	return db
}

func legacySnapshot(t *testing.T, db string) []string {
	t.Helper()
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var snapshot []string
	for _, query := range []string{
		`SELECT json_array(seq,ts,kind,actor,seat,session_id,object_type,object_id,revision,payload) FROM events WHERE kind <> 'store.judge-grants-archived' ORDER BY seq`,
		`SELECT json_array(decision_id,text,scope_kind,scope_ref,decided_by,decided_at,status,supersedes,session_id) FROM decisions`,
		`SELECT sql FROM sqlite_master WHERE name = 'decisions'`,
	} {
		rows, err := raw.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			snapshot = append(snapshot, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func TestLegacyAutoQARecordsRemainReadableAndUnchanged(t *testing.T) {
	db := legacyAutoQAStore(t)
	before := legacySnapshot(t, db)
	ctx := context.Background()
	s, err := store.Open(ctx, db, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if got := criterionOf(t, showItem(t, db, "item_042"), "c1"); got.State != "done" {
		t.Fatalf("old verdict state lost: %+v", got)
	}
	code, out := run(t, "decision", "list", "--store", db)
	var list decisionList
	decodeAs(t, out, &list)
	if code != 0 || len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecidedBy != "autoqa" {
		t.Fatalf("legacy decision: %d %s", code, out)
	}
	if after := legacySnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("opening changed legacy records:\nbefore %q\nafter %q", before, after)
	}
	if !strings.Contains(before[len(before)-1], "'autoqa'") {
		t.Fatal("legacy CHECK lost autoqa")
	}
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	var archived string
	if err := raw.QueryRow(`SELECT json_extract(payload,'$.rows[0].claim_classes') FROM events WHERE kind='store.judge-grants-archived'`).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	if archived != "qa-verification" {
		t.Fatalf("historical grant class changed: %q", archived)
	}
	var event string
	if err := raw.QueryRow(`SELECT payload FROM events WHERE kind = 'item.verdict'`).Scan(&event); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	var v verdictInput
	if err := json.Unmarshal([]byte(event), &v); err != nil {
		t.Fatal(err)
	}
	if v.ActorKind != "autoqa" || v.ClaimClass != "qa-verification" {
		t.Fatalf("fixture no longer legacy: %+v", v)
	}
	for _, now := range []string{issuedAt, "2026-11-11T00:00:00Z"} {
		code, out := recordVerdictAt(t, db, agentVerdict(t), now)
		rule := "evaluator-selection"
		assertRefused(t, code, out, rule)
	}
	if after := legacySnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatal("evaluator refusal changed legacy records")
	}
}

func TestLegacyAutoQADecisionCanBeRevokedOrSuperseded(t *testing.T) {
	for _, verb := range []string{"revoke", "supersede"} {
		t.Run(verb, func(t *testing.T) {
			db := legacyAutoQAStore(t)
			args := []string{"decision", verb, "--store", db, "--decision", "dec-1", "--now", issuedAt}
			if verb == "supersede" {
				args = append(args, "--input", input(t, envelope(t, "decision.record", "rec-2", decision("dec-2", "owner replaces QA ruling"))))
			}
			code, out := run(t, args...)
			if code != 0 {
				t.Fatalf("%s: %d %s", verb, code, out)
			}
			want := "revoked"
			if verb == "supersede" {
				want = "superseded"
				if lifecyclePayload(t, out)["decided_by"] != "owner" {
					t.Fatalf("successor: %s", out)
				}
			}
			code, out = run(t, "decision", "list", "--store", db, "--status", want)
			var list decisionList
			decodeAs(t, out, &list)
			if code != 0 || len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecidedBy != "autoqa" || list.Payload.Decisions[0].Status != want {
				t.Fatalf("target: %d %s", code, out)
			}
		})
	}
}
