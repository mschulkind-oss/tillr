package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchItemSubjectAlertClosesWhenItemLandsOrCloses(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "state.db")
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	// Copy the live drain-repeated-verifier-refusals, drain-repeated-step-dispatches, and
	// drain-loop-fault rules into a scratch rules dir so single-item, composite-item, and
	// global-subject rules all run through the CLI faces.
	rulesDir := filepath.Join(dir, "rules")
	if err := os.MkdirAll(rulesDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"drain-repeated-verifier-refusals.toml",
		"drain-repeated-step-dispatches.toml",
		"drain-loop-fault.toml",
	} {
		body, err := os.ReadFile(filepath.Join("../../config/tillr/watch", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rulesDir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().UTC()
	setItem := func(itemID, state string) {
		t.Helper()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(
				`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, ?)
				 ON CONFLICT(item_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
				itemID, itemID, state, now.UnixMilli(),
			)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendRefusals := func(itemID string, n int) {
		t.Helper()
		for i := range n {
			runID := fmt.Sprintf("run-%s-%d", itemID, i+1)
			payload, err := json.Marshal(map[string]any{
				"item_id": itemID,
				"run_id":  runID,
				"step":    "verify-not-done",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{
					Kind:       drain.RefusalKind,
					ObjectType: "run",
					ObjectID:   &runID,
					At:         now.Add(-time.Duration(n-i) * time.Second),
					Payload:    payload,
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	invoke := func(args ...string) string {
		t.Helper()
		full := append(args, "--store", storePath, "--rules", rulesDir)
		var out, stderr bytes.Buffer
		if code := run(ctx, full, &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("run(%q): code=%d stdout=%q stderr=%q", full, code, &out, &stderr)
		}
		return out.String()
	}

	const (
		itemClose = "item-request-b478edf600"
		itemLand  = "item-request-e17b5457ac"
		itemComp  = "item-request-comp00001"
		itemOpen  = "item-request-still-open"
	)
	setItem(itemClose, "in-queue")
	setItem(itemLand, "in-queue")
	setItem(itemComp, "in-queue")
	setItem(itemOpen, "in-queue")
	invoke("eval", "--once")

	appendRefusals(itemClose, 5)
	appendRefusals(itemLand, 5)
	appendRefusals(itemOpen, 5)
	for i := range 5 {
		lane := "express-1"
		payload, err := json.Marshal(map[string]any{
			"run_id":     fmt.Sprintf("run-%s-%d", itemComp, i+1),
			"backend":    "pi-verify",
			"seat":       "verify",
			"workflow":   "planned-build@v1",
			"lane_id":    lane,
			"item_id":    itemComp,
			"node":       "verify",
			"started_at": now.UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "run.allocate",
				ObjectType: "lane",
				ObjectID:   &lane,
				At:         now.Add(-time.Duration(5-i) * time.Second),
				Payload:    payload,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       "drain.loop-ended",
			ObjectType: "drain",
			At:         now,
			Payload:    []byte(`{"stop":"fault","detail":"boom"}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	invoke("eval", "--once")

	statusBefore := invoke("status")
	for _, want := range []string{
		fmt.Sprintf(`rule=drain-repeated-verifier-refusals subject=%q state=open`, itemClose),
		fmt.Sprintf(`rule=drain-repeated-verifier-refusals subject=%q state=open`, itemLand),
		fmt.Sprintf(`rule=drain-repeated-step-dispatches subject="%s/verify" state=open`, itemComp),
		fmt.Sprintf(`rule=drain-repeated-verifier-refusals subject=%q state=open`, itemOpen),
		`rule=drain-loop-fault subject="global" state=open`,
	} {
		if !strings.Contains(statusBefore, want) {
			t.Fatalf("status before closing items missing %s:\n%s", want, statusBefore)
		}
	}

	// Acknowledge itemLand and silence itemComp before their items finish, verifying
	// open, acknowledged, and silenced episodes all close when their items finish.
	var landAlertID, compAlertID string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		if err := rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE subject_key = ?`, itemLand).Scan(&landAlertID); err != nil {
			return err
		}
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE subject_key = ?`, itemComp+"/verify").Scan(&compAlertID)
	}); err != nil {
		t.Fatal(err)
	}
	invokeStore := func(args ...string) string {
		t.Helper()
		full := append(args, "--store", storePath)
		var out, stderr bytes.Buffer
		if code := run(ctx, full, &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("run(%q): code=%d stdout=%q stderr=%q", full, code, &out, &stderr)
		}
		return out.String()
	}
	invokeStore("ack", landAlertID, "--by", "operator")
	invokeStore("silence", compAlertID, "--by", "operator", "--item", itemComp, "--reason", "known comp", "--cause", "known comp", "--evidence", "event seq", "--until", "2h")

	// Close itemClose, land itemLand, and close itemComp without modifying their in-window events.
	setItem(itemClose, "closed")
	setItem(itemLand, "landed")
	setItem(itemComp, "closed")

	invoke("eval", "--once")

	statusAfter := invoke("status")
	statusJSON := invoke("status", "--json")
	for _, closedSubject := range []string{itemClose, itemLand, itemComp} {
		if strings.Contains(statusAfter, closedSubject) {
			t.Fatalf("status still lists closed/landed item %s:\n%s", closedSubject, statusAfter)
		}
		if strings.Contains(statusJSON, closedSubject) {
			t.Fatalf("status --json still lists closed/landed item %s:\n%s", closedSubject, statusJSON)
		}
	}
	if !strings.Contains(statusAfter, fmt.Sprintf(`rule=drain-repeated-verifier-refusals subject=%q state=open`, itemOpen)) {
		t.Fatalf("open item alert disappeared from status:\n%s", statusAfter)
	}
	if !strings.Contains(statusAfter, `rule=drain-loop-fault subject="global" state=open`) {
		t.Fatalf("global alert changed in status:\n%s", statusAfter)
	}

	// Verify alert_episodes rows and watch.alert.resolved events carry the exact reasons,
	// and items themselves were untouched.
	for _, tc := range []struct {
		itemID, subjectKey, wantItemState, wantReason string
	}{
		{itemClose, itemClose, "closed", "item-closed"},
		{itemLand, itemLand, "landed", "item-landed"},
		{itemComp, itemComp + "/verify", "closed", "item-closed"},
	} {
		var epState, epReason, curItemState, evReason string
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			if err := rd.QueryRow(`SELECT state, resolved_reason FROM alert_episodes WHERE subject_key = ?`, tc.subjectKey).Scan(&epState, &epReason); err != nil {
				return err
			}
			if err := rd.QueryRow(`SELECT state FROM items WHERE item_id = ?`, tc.itemID).Scan(&curItemState); err != nil {
				return err
			}
			return rd.QueryRow(`SELECT json_extract(payload, '$.reason') FROM events WHERE kind = 'watch.alert.resolved' AND json_extract(payload, '$.subject_key') = ?`, tc.subjectKey).Scan(&evReason)
		}); err != nil {
			t.Fatal(err)
		}
		if epState != "resolved" || epReason != tc.wantReason || evReason != tc.wantReason || curItemState != tc.wantItemState {
			t.Fatalf("%s: ep=(%q,%q) ev=%q item=%q, want resolved/%q/%q", tc.subjectKey, epState, epReason, evReason, curItemState, tc.wantReason, tc.wantItemState)
		}
	}

	// Re-running eval --once while the five in-window events remain does not reopen any closed episode.
	invoke("eval", "--once")
	var totalCloseEpisodes int
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT COUNT(*) FROM alert_episodes WHERE subject_key IN (?, ?, ?)`, itemClose, itemLand, itemComp+"/verify").Scan(&totalCloseEpisodes)
	}); err != nil || totalCloseEpisodes != 3 {
		t.Fatalf("episodes reopened on subsequent eval: count=%d err=%v", totalCloseEpisodes, err)
	}
}
