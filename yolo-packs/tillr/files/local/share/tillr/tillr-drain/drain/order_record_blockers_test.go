package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestOrderRecordTakenIDRefusesBeforeWorktreeAndRetries(t *testing.T) {
	f, l, s, now := orderFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("INSERT INTO items(item_id,title,state,updated_at) VALUES('order-record','A legitimate item','closed',0)")
		return err
	})
	moveOrder(t, f, *now, "item-b", "first", "cli")
	*now = now.Add(10 * time.Minute)
	before := itemStates(t, f.storePath)
	for _, advance := range []time.Duration{0, 59 * time.Minute, time.Minute} {
		*now = now.Add(advance)
		orderPass(t, l)
	}
	if s.gates != 0 || s.lands != 0 {
		t.Fatal("taken id reached gate or landing")
	}
	if got := runGit(t, f.root, "worktree", "list", "--porcelain"); strings.Contains(got, ".order-record") {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(l.cfg.LaneRoot, ".order-record")); !os.IsNotExist(err) {
		t.Fatalf("created maintenance directory: %v", err)
	}
	if got := itemStates(t, f.storePath); got != before {
		t.Fatalf("refusal changed items: %s", got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		err := tx.QueryRow("SELECT count(*) FROM dispatches WHERE action='order-record-refused' AND reason='id-taken'").Scan(&n)
		if n != 2 {
			t.Errorf("hour retry refusals=%d", n)
		}
		return err
	})
	moveOrder(t, f, *now, "item-a", "first", "cli")
	*now = now.Add(10 * time.Minute)
	orderPass(t, l)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		err := tx.QueryRow("SELECT count(*) FROM dispatches WHERE action='order-record-refused' AND reason='id-taken'").Scan(&n)
		if n != 3 {
			t.Errorf("further move retry refusals=%d", n)
		}
		return err
	})
}

func TestOrderRecordHandEditedReceiptDoesNotBlockMaintenance(t *testing.T) {
	for _, body := range []string{"item-a\nitem-b\n", "# last_hand_move_event_seq: invalid\nitem-a\n", "directory"} {
		t.Run(body, func(t *testing.T) {
			f, l, s, now := orderFixture(t)
			path := filepath.Join(f.root, "docs/order.txt")
			if body == "directory" {
				path = filepath.Join(path, "child")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, path, body)
			runGit(t, f.root, "add", "docs")
			runGit(t, f.root, "commit", "-qm", "hand edit order record")
			moveOrder(t, f, *now, "item-b", "first", "cli")
			*now = now.Add(10 * time.Minute)
			orderPass(t, l)
			if body == "directory" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					var reason string
					err := tx.QueryRow("SELECT reason FROM dispatches WHERE action='order-record-refused'").Scan(&reason)
					if !strings.HasPrefix(reason, "candidate:") {
						t.Errorf("reason=%s", reason)
					}
					return err
				})
			} else {
				record, err := os.ReadFile(path)
				if err != nil || !strings.HasSuffix(string(record), "item-b\nitem-a\n") || s.lands != 1 {
					t.Fatalf("record=%s lands=%d err=%v", record, s.lands, err)
				}
			}
			if _, err := l.pass(context.Background(), &LoopResult{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
