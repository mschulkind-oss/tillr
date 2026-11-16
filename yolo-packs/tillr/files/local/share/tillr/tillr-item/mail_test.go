package main_test

import (
	"path/filepath"
	"testing"
)

type mailInput struct {
	MailID      string `json:"mail_id"`
	FromSession string `json:"from_session"`
	ToTarget    string `json:"to_target"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
}

type mailOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		MailID      string `json:"mail_id"`
		FromSession string `json:"from_session"`
		ToTarget    string `json:"to_target"`
		Subject     string `json:"subject"`
		CreatedAt   string `json:"created_at"`
		AckedAt     string `json:"acked_at"`
		AckedBy     string `json:"acked_by"`
		Revision    int    `json:"revision"`
		Delivery    string `json:"delivery"`
		Rule        string `json:"rule"`
	} `json:"payload"`
}

type mailList struct {
	Kind    string `json:"kind"`
	Payload struct {
		Deliveries []struct {
			MailID   string `json:"mail_id"`
			ToTarget string `json:"to_target"`
		} `json:"deliveries"`
	} `json:"payload"`
}

func mail(id, to string) mailInput {
	return mailInput{MailID: id, FromSession: "s-1", ToTarget: to, Subject: "the item face landed", Body: "read the brief"}
}

// Mail is recorded as intent and nothing is sent: the record says so, and the module
// imports no network package (the gate below proves the second half).
func TestMailSendRecordsIntentAndSendsNothing(t *testing.T) {
	db := filepath.Join(scratch(t, "mail"), "board.sqlite3")
	code, out := run(t, "mail", "send", "--store", db,
		"--input", input(t, envelope(t, "mail.delivery", "rec-1", mail("mail-1", "agent-a"))),
		"--now", "2026-11-10T00:00:00Z")
	if code != 0 {
		t.Fatalf("mail send exited %d: %s", code, out)
	}
	var got mailOut
	decodeAs(t, out, &got)
	if got.Kind != "mail.delivery" {
		t.Fatalf("the face emitted kind %q", got.Kind)
	}
	if got.Payload.Delivery != "intent" {
		t.Fatalf("the delivery is %q, want intent", got.Payload.Delivery)
	}
	if got.Payload.AckedAt != "" || got.Payload.Revision != 1 {
		t.Fatalf("a new delivery carries acked_at %q / revision %d", got.Payload.AckedAt, got.Payload.Revision)
	}
	if got.Payload.CreatedAt != "2026-11-10T00:00:00Z" {
		t.Fatalf("created_at is %q, want the injected clock", got.Payload.CreatedAt)
	}
	code, out = run(t, "mail", "list", "--store", db, "--to", "agent-a")
	if code != 0 {
		t.Fatalf("mail list exited %d: %s", code, out)
	}
	var list mailList
	decodeAs(t, out, &list)
	if len(list.Payload.Deliveries) != 1 || list.Payload.Deliveries[0].MailID != "mail-1" {
		t.Fatalf("the target holds %d deliveries", len(list.Payload.Deliveries))
	}
}

// Acking is terminal for one delivery: a second ack is a negative decision with a
// rule, so a caller cannot believe it acknowledged something twice.
func TestMailAckMarksTheDeliveryAndRefusesASecondAck(t *testing.T) {
	db := filepath.Join(scratch(t, "mail-ack"), "board.sqlite3")
	if code, out := run(t, "mail", "send", "--store", db,
		"--input", input(t, envelope(t, "mail.delivery", "rec-1", mail("mail-1", "agent-a"))),
		"--now", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("mail send exited %d: %s", code, out)
	}
	code, out := run(t, "mail", "ack", "--store", db, "--mail", "mail-1", "--by", "s-2", "--now", "2026-11-10T01:00:00Z")
	if code != 0 {
		t.Fatalf("mail ack exited %d: %s", code, out)
	}
	var got mailOut
	decodeAs(t, out, &got)
	if got.Payload.AckedBy != "s-2" || got.Payload.AckedAt != "2026-11-10T01:00:00Z" || got.Payload.Revision != 2 {
		t.Fatalf("the ack carries by %q / at %q / revision %d",
			got.Payload.AckedBy, got.Payload.AckedAt, got.Payload.Revision)
	}
	code, out = run(t, "mail", "ack", "--store", db, "--mail", "mail-1", "--by", "s-3", "--now", "2026-11-10T02:00:00Z")
	if code != 1 {
		t.Fatalf("a second ack exited %d, want 1: %s", code, out)
	}
	decodeAs(t, out, &got)
	if got.Payload.Rule != "mail-already-acked" {
		t.Fatalf("the second ack refused with rule %q", got.Payload.Rule)
	}
}

// The target filter is what makes a mailbox a mailbox: an agent reads its own
// deliveries, not every delivery on the board.
func TestMailListShowsOnlyTheTargetsDeliveries(t *testing.T) {
	db := filepath.Join(scratch(t, "mail-list"), "board.sqlite3")
	for _, m := range []mailInput{mail("mail-1", "agent-a"), mail("mail-2", "agent-b")} {
		if code, out := run(t, "mail", "send", "--store", db,
			"--input", input(t, envelope(t, "mail.delivery", "rec-"+m.MailID, m)),
			"--now", "2026-11-10T00:00:00Z"); code != 0 {
			t.Fatalf("sending %s exited %d: %s", m.MailID, code, out)
		}
	}
	code, out := run(t, "mail", "list", "--store", db, "--to", "agent-a")
	if code != 0 {
		t.Fatalf("mail list exited %d: %s", code, out)
	}
	var list mailList
	decodeAs(t, out, &list)
	if len(list.Payload.Deliveries) != 1 || list.Payload.Deliveries[0].MailID != "mail-1" {
		t.Fatalf("agent-a holds %d deliveries", len(list.Payload.Deliveries))
	}
	code, out = run(t, "mail", "list", "--store", db)
	if code != 0 {
		t.Fatalf("mail list exited %d: %s", code, out)
	}
	decodeAs(t, out, &list)
	if len(list.Payload.Deliveries) != 2 {
		t.Fatalf("the unfiltered list holds %d deliveries, want 2", len(list.Payload.Deliveries))
	}
}
