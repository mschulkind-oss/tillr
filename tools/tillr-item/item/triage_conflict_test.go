package item_test

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestOwnerTriageConflictDoesNotInventARun(t *testing.T) {
	s := syncStore(t)
	c := item.Conflict{ConflictID: "triage-request", ItemID: "item-a", Text: "No document is named", Status: "owner", WaitingOn: "owner", TriageVersion: "request-version", BlockedCriteria: []string{}}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('item-a','Request','in-queue',1)`); err != nil {
			return err
		}
		if err := item.RecordConflict(tx, c, syncAt); err != nil {
			return err
		}
		c.Status, c.WaitingOn = "answered", "build"
		if err := item.RecordConflict(tx, c, syncAt); err != nil {
			return err
		}
		c.Status = "closed"
		return item.RecordConflict(tx, c, syncAt)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		cs, err := item.ReadConflicts(r)
		if cs[c.ItemID].RunID != "" {
			t.Errorf("invented run: %+v", cs[c.ItemID])
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c.TriageVersion = ""
	c.Status, c.WaitingOn = "raised", "design"
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { return item.RecordConflict(tx, c, syncAt) }); err == nil {
		t.Fatal("ordinary design conflict admitted without a run")
	}
}
