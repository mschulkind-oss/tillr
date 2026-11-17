package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/mschulkind-oss/tillr/store"
)

// Comment rows are read-only and optional on stores created before comments.
// Pending replies remain visible even after their comment item has closed.
func waitingCommentIntake(r *store.Reader) ([]waitingRow, error) {
	out, err := waitingCommentIntakeRows(r)
	if err != nil {
		return nil, err
	}
	rows, err := r.Query("SELECT item_id,document FROM comment_deliveries WHERE written=0 ORDER BY item_id,run_id")
	if isMissingTable(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, doc string
		if err := rows.Scan(&id, &doc); err != nil {
			return nil, err
		}
		out = append(out, waitingRow{ItemID: id, Class: "waits on work", Where: "reply", Why: "reply delivery waits on served document " + doc, Mover: "restore the landed bytes of " + doc, Since: "—", ClassSource: "store"})
	}
	return out, rows.Err()
}

func waitingCommentIntakeRows(r *store.Reader) ([]waitingRow, error) {
	var first, last string
	var unavailable bool
	err := r.QueryRow("SELECT first_intake,last_answer,unavailable FROM comment_intake WHERE id=1").Scan(&first, &last, &unavailable)
	if isMissingTable(err) || errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []waitingRow{}
	if unavailable {
		if last == "" {
			last = "never"
		}
		out = append(out, waitingRow{ItemID: "comments", Class: "waits on work", Where: "Vantage", Why: "Vantage not answering; last answer: " + last, Mover: "the next comments poll", Since: first, ClassSource: "store"})
	}
	rows, err := r.Query("SELECT document,older FROM comment_intake_documents WHERE older>0 ORDER BY document")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var doc string
		var n int
		if err := rows.Scan(&doc, &n); err != nil {
			return nil, err
		}
		out = append(out, waitingRow{ItemID: doc, Class: "needs you", Where: "comments", Why: fmt.Sprintf("%d pending comments predate first intake", n), Mover: "tillr item comments take --doc " + strconv.Quote(doc) + " --older", Since: first, ClassSource: "store"})
	}
	return out, rows.Err()
}
