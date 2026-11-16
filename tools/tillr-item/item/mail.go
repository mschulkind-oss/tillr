package item

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The two delivery states. This utility writes only DeliveryIntent: it records that a
// message is owed and sends nothing, in every mode. A face that reported DeliverySent
// with no transport would be a record asserting a transition that did not happen.
const (
	// DeliveryIntent is a delivery recorded and not sent.
	DeliveryIntent = "intent"
	// DeliverySent is a delivery a transport has carried. No face here writes it.
	DeliverySent = "sent"
)

// mailPayload is the mail.delivery payload: design §5.3's field list, plus the fields
// the store sets and the input never carries.
type mailPayload struct {
	MailID      string `json:"mail_id"`
	FromSession string `json:"from_session"`
	ToTarget    string `json:"to_target"`
	Subject     string `json:"subject"`
	Body        string `json:"body"`
	CreatedAt   string `json:"created_at,omitempty"`
	AckedAt     string `json:"acked_at,omitempty"`
	AckedBy     string `json:"acked_by,omitempty"`
	Revision    int    `json:"revision"`
	Delivery    string `json:"delivery"`
}

// mailListPayload is the record `mail list` emits: one payload per delivery, in
// created-at order, so a caller can diff two mailboxes.
type mailListPayload struct {
	Deliveries []mailPayload `json:"deliveries"`
}

// SendMail records one delivery as intent and appends its event in the same
// transaction. It sends nothing: this package imports no transport, and the gate
// proves it.
func SendMail(ctx context.Context, tx *store.Tx, env record.Envelope) (record.Envelope, error) {
	var payload mailPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return record.Envelope{}, Usagef("reading the mail payload: %v", err)
	}
	if payload.MailID == "" {
		return record.Envelope{}, Usagef("a mail.delivery needs a mail_id")
	}
	if payload.ToTarget == "" {
		return record.Envelope{}, Usagef("a mail.delivery needs a to_target: a delivery to nobody reaches nobody")
	}
	if payload.Subject == "" {
		return record.Envelope{}, Usagef("a mail.delivery needs a subject")
	}
	payload.Delivery = DeliveryIntent
	payload.Revision = 1
	payload.CreatedAt = env.CreatedAt.UTC().Format(time.RFC3339)
	if _, err := tx.Exec(
		`INSERT INTO mail_deliveries
		   (mail_id, from_session, to_target, subject, body, created_at, revision, delivery)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		payload.MailID, payload.FromSession, payload.ToTarget, payload.Subject, payload.Body,
		env.CreatedAt.UnixMilli(), payload.Revision, payload.Delivery,
	); err != nil {
		return record.Envelope{}, Internalf("writing delivery %q: %v", payload.MailID, err)
	}
	if err := appendMailEvent(ctx, tx, MailSendKind, payload.MailID, payload, env.CreatedAt); err != nil {
		return record.Envelope{}, err
	}
	return envelopeOf(MailRecordKind, "mail-"+payload.MailID, env.CreatedAt, payload)
}

// AckMail marks one delivery acknowledged. Acking is terminal for one delivery: a
// second ack is a negative decision with a rule, so a caller cannot believe it
// acknowledged something twice.
func AckMail(ctx context.Context, tx *store.Tx, id, by string, at time.Time) (record.Envelope, error) {
	payload, err := readMail(ctx, tx.Reader(), id)
	if err != nil {
		return record.Envelope{}, err
	}
	if payload.AckedAt != "" {
		return record.Envelope{}, Rejectionf("mail-already-acked", id,
			"mail %q was acknowledged by %q at %s", id, payload.AckedBy, payload.AckedAt)
	}
	payload.AckedAt = at.UTC().Format(time.RFC3339)
	payload.AckedBy = by
	payload.Revision++
	if _, err := tx.Exec(
		`UPDATE mail_deliveries SET acked_at = ?, acked_by = ?, revision = ? WHERE mail_id = ?`,
		at.UnixMilli(), by, payload.Revision, id,
	); err != nil {
		return record.Envelope{}, Internalf("acknowledging delivery %q: %v", id, err)
	}
	if err := appendMailEvent(ctx, tx, MailAckKind, id, payload, at); err != nil {
		return record.Envelope{}, err
	}
	return envelopeOf(MailRecordKind, "mail-"+id, at, payload)
}

// ListMail answers the deliveries to one target, or every delivery when no target is
// named. The target filter is what makes a mailbox a mailbox: an agent reads its own
// deliveries, not every delivery on the board.
func ListMail(ctx context.Context, r *store.Reader, to string) (record.Envelope, error) {
	query := `SELECT mail_id, from_session, to_target, subject, body, created_at,
	                 COALESCE(acked_at, 0), COALESCE(acked_by, ''), revision, delivery
	            FROM mail_deliveries`
	args := []any{}
	if to != "" {
		query += ` WHERE to_target = ?`
		args = append(args, to)
	}
	query += ` ORDER BY created_at, mail_id`
	rows, err := r.Query(query, args...)
	if err != nil {
		return record.Envelope{}, Internalf("reading the deliveries: %v", err)
	}
	defer rows.Close()
	deliveries := []mailPayload{}
	for rows.Next() {
		payload, err := scanMail(rows)
		if err != nil {
			return record.Envelope{}, err
		}
		deliveries = append(deliveries, payload)
	}
	if err := rows.Err(); err != nil {
		return record.Envelope{}, Internalf("reading the deliveries: %v", err)
	}
	return envelopeOf(MailRecordKind, "mailbox-"+to, time.Now().UTC(), mailListPayload{Deliveries: deliveries})
}

// readMail answers one stored delivery row.
func readMail(ctx context.Context, r *store.Reader, id string) (mailPayload, error) {
	row := r.QueryRow(
		`SELECT mail_id, from_session, to_target, subject, body, created_at,
		        COALESCE(acked_at, 0), COALESCE(acked_by, ''), revision, delivery
		   FROM mail_deliveries WHERE mail_id = ?`, id,
	)
	payload, err := scanMail(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mailPayload{}, Rejectionf("not-found", id, "no delivery %q in this store", id)
	}
	return payload, err
}

// scanMail reads one delivery row into its payload, with the stored clocks rendered as
// the record's RFC 3339 spelling and an unacked delivery carrying no ack fields.
func scanMail(row scanner) (mailPayload, error) {
	var (
		payload   mailPayload
		createdAt int64
		ackedAt   int64
	)
	if err := row.Scan(
		&payload.MailID, &payload.FromSession, &payload.ToTarget, &payload.Subject, &payload.Body,
		&createdAt, &ackedAt, &payload.AckedBy, &payload.Revision, &payload.Delivery,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mailPayload{}, err
		}
		return mailPayload{}, Internalf("reading a delivery row: %v", err)
	}
	payload.CreatedAt = time.UnixMilli(createdAt).UTC().Format(time.RFC3339)
	if ackedAt != 0 {
		payload.AckedAt = time.UnixMilli(ackedAt).UTC().Format(time.RFC3339)
	}
	return payload, nil
}

// appendMailEvent appends one mail event inside the caller's transaction.
func appendMailEvent(ctx context.Context, tx *store.Tx, kind, id string, payload mailPayload, at time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return Internalf("encoding the %s event: %v", kind, err)
	}
	if _, err := tx.Append(event.Event{
		Kind:       kind,
		ObjectType: "mail",
		ObjectID:   &id,
		Payload:    body,
		At:         at,
	}); err != nil {
		return Internalf("appending the %s event: %v", kind, err)
	}
	return nil
}
