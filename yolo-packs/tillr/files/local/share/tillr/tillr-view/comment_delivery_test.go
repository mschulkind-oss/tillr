package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWaitingNamesPendingCommentDeliveryAfterClosure(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE comment_deliveries(run_id TEXT PRIMARY KEY,item_id TEXT,document TEXT,blob_id TEXT,body TEXT,written INTEGER);
 UPDATE items SET state='closed' WHERE item_id='item-seed-02';
 INSERT INTO comment_deliveries VALUES('reply-run','item-seed-02','docs/design/edited.md','blob','{}',0);`)
	assertRows := func(rows []waitingRow) {
		t.Helper()
		for _, row := range rows {
			if row.ItemID == "item-seed-02" && row.Class == "waits on work" && row.Where == "reply" && strings.Contains(row.Why, "docs/design/edited.md") && strings.Contains(row.Mover, "docs/design/edited.md") {
				return
			}
		}
		t.Fatalf("pending document absent: %+v", rows)
	}
	code, _, raw := h.get("/api/v1/waiting")
	if code != 200 {
		t.Fatalf("waiting %d %s", code, raw)
	}
	var response struct {
		Data []waitingRow `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	assertRows(response.Data)
	// The terminal face reads the same optional rows, even with no intake tables.
	s, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingCommentIntake(r)
		if err == nil {
			assertRows(rows)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.exec(`UPDATE comment_deliveries SET written=1;`)
	_, _, raw = h.get("/api/v1/waiting")
	if strings.Contains(string(raw), "docs/design/edited.md") {
		t.Fatalf("written delivery still waiting: %s", raw)
	}
}

func TestWaitingClassifiesDesignUnderComment(t *testing.T) {
	rule := waitingRuleFor("design-under-comment")
	if rule.class != "waits on work" {
		t.Fatalf("comment hold unclassified: %+v", rule)
	}
}
