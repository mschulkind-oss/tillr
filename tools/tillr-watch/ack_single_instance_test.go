package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchAckSingleInstanceWakeAndItemRefusal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, dir := fixture(t, false)
	seedCLIItems(t, s, map[string]string{"item-open": "in-queue"})

	invoke := func(args ...string) (int, string, string) {
		var out, stderr bytes.Buffer
		code := run(ctx, append([]string{args[0], "--store", s.Path()}, args[1:]...), &out, &stderr)
		return code, out.String(), stderr.String()
	}

	// Help text must not advertise --item on ack.
	var helpOut, helpErr bytes.Buffer
	if code := run(ctx, []string{"ack", "--help"}, &helpOut, &helpErr); code != 0 || helpErr.Len() != 0 {
		t.Fatalf("ack --help: code=%d err=%q", code, &helpErr)
	}
	for _, line := range strings.Split(helpOut.String(), "\n") {
		if strings.Contains(line, "tillr watch ack ") && strings.Contains(line, "--item") {
			t.Fatalf("ack help line still advertises --item: %q", line)
		}
	}

	// Establish rule baselines before seeding facts.
	if code, evalOut, evalErr := invoke("eval", "--once", "--rules", dir); code != 0 || evalErr != "" {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, evalOut, evalErr)
	}

	// Open an alert for subject "alpha".
	fact(t, s, `{"item_id":"alpha"}`)
	fact(t, s, `{"item_id":"alpha"}`)
	code, firstOut, firstErr := invoke("wait", "--rules", dir, "--timeout", "2s")
	if code != 0 || firstErr != "" {
		t.Fatalf("initial wait: code=%d out=%q err=%q", code, firstOut, firstErr)
	}
	through1 := cursor(t, firstOut)
	var alertID string
	faceRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='alpha'`, &alertID)

	// ack --item must be refused with guidance toward item-linked suppression, without mutating store or head seq.
	beforeHead, err := s.EventHeadSeq()
	if err != nil {
		t.Fatal(err)
	}
	for _, itemArg := range []string{"--item=item-open", "--item="} {
		code, out, diag := invoke("ack", alertID, "--by", "operator", itemArg)
		if code != 1 || out != "" || !strings.Contains(diag, "--item") || !(strings.Contains(diag, "track") || strings.Contains(diag, "silence")) {
			t.Fatalf("ack %s: code=%d out=%q diag=%q", itemArg, code, out, diag)
		}
		afterHead, err := s.EventHeadSeq()
		if err != nil || afterHead != beforeHead {
			t.Fatalf("ack %s mutated event head: %d -> %d (%v)", itemArg, beforeHead, afterHead, err)
		}
	}

	// Plain single-instance ack succeeds and records acked_seq / acked_fingerprint.
	code, ackOut, ackErr := invoke("ack", alertID, "--by", "operator", "--note", "checking instance 1")
	if code != 0 || ackErr != "" || ackOut != fmt.Sprintf("alert %s acknowledged\n", alertID) {
		t.Fatalf("ack: code=%d out=%q err=%q", code, ackOut, ackErr)
	}
	var ackedSeq sql.NullInt64
	var ackedFP sql.NullString
	faceRead(t, s, `SELECT acked_seq, acked_fingerprint FROM alert_episodes WHERE alert_id='`+alertID+`'`, &ackedSeq, &ackedFP)
	if !ackedSeq.Valid || !ackedFP.Valid {
		t.Fatalf("expected acked_seq and acked_fingerprint after ack, got seq=%+v fp=%+v", ackedSeq, ackedFP)
	}

	// Start a live background wait (re-armed with --seen-through through1).
	type waitRes struct {
		code     int
		out, err string
	}
	waitDone := make(chan waitRes, 1)
	go func() {
		c, o, e := invoke("wait", "--rules", dir, "--seen-through", fmt.Sprint(through1))
		waitDone <- waitRes{c, o, e}
	}()
	awaitAttached(t, s)

	// Unrelated single refusal on another subject does NOT wake wait.
	fact(t, s, `{"item_id":"other-single"}`)
	select {
	case r := <-waitDone:
		t.Fatalf("wait woke on unrelated single refusal while alpha had no new instance: %+v", r)
	case <-time.After(120 * time.Millisecond):
	}

	// A new matching event for "alpha" immediately reopens the untracked acknowledged episode and wakes wait!
	fact(t, s, `{"item_id":"alpha"}`)
	var woke waitRes
	select {
	case woke = <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not wake when a new matching event arrived after single-instance ack")
	}
	if woke.code != 0 || woke.err != "" || !strings.Contains(woke.out, "[alert "+alertID+" refusals alpha") {
		t.Fatalf("unexpected wake output: %+v", woke)
	}
	var cause, state string
	faceRead(t, s, `SELECT state FROM alert_episodes WHERE alert_id='`+alertID+`'`, &state)
	faceRead(t, s, `SELECT json_extract(payload,'$.cause') FROM events WHERE kind='watch.alert.reopened' AND object_id='`+alertID+`' ORDER BY seq DESC LIMIT 1`, &cause)
	if state != "open" || cause != "new-instance" {
		t.Fatalf("after wake: state=%q cause=%q", state, cause)
	}

	// Ack again, then let a short-window rule clear -> untracked acknowledged episode resolves with condition-cleared.
	shortRule := strings.Replace(testRule, `window="1h"`, `window="1s"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "nested", "refusals.toml"), []byte(shortRule), 0600); err != nil {
		t.Fatal(err)
	}
	// Seed fresh events at old timestamp so window="1s" sees !firing on next eval.
	s2, dir2 := fixture(t, false)
	if err := os.WriteFile(filepath.Join(dir2, "nested", "refusals.toml"), []byte(shortRule), 0600); err != nil {
		t.Fatal(err)
	}
	if code, _, e2 := run(ctx, []string{"eval", "--once", "--store", s2.Path(), "--rules", dir2}, &bytes.Buffer{}, &bytes.Buffer{}), "", ""; code != 0 {
		t.Fatalf("s2 init eval: %d %s", code, e2)
	}
	past := time.Now().Add(-500 * time.Millisecond)
	for range 2 {
		if err := s2.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{At: past, Kind: "drain.refusal", Payload: []byte(`{"item_id":"expiring"}`)})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if code := run(ctx, []string{"eval", "--once", "--store", s2.Path(), "--rules", dir2}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("s2 open eval failed")
	}
	var expID string
	faceRead(t, s2, `SELECT alert_id FROM alert_episodes WHERE subject_key='expiring'`, &expID)
	if code := run(ctx, []string{"ack", "--store", s2.Path(), expID, "--by", "operator"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("s2 ack failed")
	}
	time.Sleep(600 * time.Millisecond)
	if code := run(ctx, []string{"eval", "--once", "--store", s2.Path(), "--rules", dir2}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("s2 clear eval failed")
	}
	var expState, expReason string
	faceRead(t, s2, `SELECT state, resolved_reason FROM alert_episodes WHERE alert_id='`+expID+`'`, &expState, &expReason)
	if expState != "resolved" || expReason != "condition-cleared" {
		t.Fatalf("expected condition-cleared on untracked acked CLI episode, got state=%q reason=%q", expState, expReason)
	}
}
