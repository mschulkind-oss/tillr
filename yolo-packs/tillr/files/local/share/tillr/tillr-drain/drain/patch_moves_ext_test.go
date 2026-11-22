package drain_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAcknowledgePatchMoveFaceRecordsOwnerAndTime(t *testing.T) {
	path := filepath.Join(scratch(t, "patch-moves"), "state.db")
	fixtureAt(t, path, itemRecord("item_041", "in-queue"))
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		id := "observing-run"
		seq, err = tx.Append(event.Event{Kind: drain.BoundaryCheckKind, ObjectType: "run", ObjectID: &id, At: time.Now(), Payload: []byte(`{"clean":true,"runs":[{"run_id":"observing-run"}],"patch_moves":[{"ref":"refs/heads/patch/pi","old":"old","new":"new"}]}`)})
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"acknowledge-patch-move", "--store", path, "--observation", fmt.Sprint(seq), "--fork", "pi", "--by", "owner"}
	if code, out := invoke(t, args[:len(args)-2]...); code != 2 || !strings.Contains(string(out), "--by <owner>") {
		t.Fatalf("missing owner: %d %s", code, out)
	}
	if code, out := invoke(t, args...); code != 0 || !strings.Contains(string(out), "acknowledged patch/pi") {
		t.Fatalf("ack: %d %s", code, out)
	}
	if code, out := invoke(t, args...); code != 2 || !strings.Contains(string(out), "no unacknowledged patch move") {
		t.Fatalf("repeated ack: %d %s", code, out)
	}
	s, err = store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		holds, err := patchmoves.Pending(r)
		if err != nil {
			return err
		}
		if len(holds) != 0 {
			t.Fatalf("holds remain: %v", holds)
		}
		var by, at string
		if err := r.QueryRow(`SELECT json_extract(payload,'$.by'),json_extract(payload,'$.acknowledged_at') FROM events WHERE kind=?`, patchmoves.AcknowledgedKind).Scan(&by, &at); err != nil {
			return err
		}
		if by != "owner" {
			t.Fatal(by)
		}
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			t.Fatal(err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(filepath.Dir(path), "absent.db")
	args[2] = missing
	if code, _ := invoke(t, args...); code == 0 {
		t.Fatal("missing store acknowledged")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("acknowledgment created a store: %v", err)
	}
}
