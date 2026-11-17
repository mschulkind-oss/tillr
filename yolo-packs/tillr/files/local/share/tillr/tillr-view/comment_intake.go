package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/mschulkind-oss/tillr/store"
)

// Intake rows are read-only and optional on stores created before comments.
func waitingCommentIntake(r *store.Reader) ([]waitingRow, error) {
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
