package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWriteTxPublishesOnlyAfterCommitAndSeesCommittedRowsOnWake(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	storePath := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	reader, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- notify.Serve(ctx, notify.ServeConfig{
			StorePath: storePath,
			IdleExit:  hangGuard,
			Ready:     ready,
		})
	}()
	var sockPath string
	select {
	case sockPath = <-ready:
	case <-time.After(hangGuard):
		t.Fatal("relay did not become ready")
	}
	t.Cleanup(func() { _ = os.Remove(notify.LockPath(sockPath)) })

	sub, err := notify.Subscribe(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	insideCallback := make(chan struct{})
	allowCommit := make(chan struct{})
	writeDone := make(chan error, 1)
	itemID := "item-post-commit"
	var appendedSeq int64

	go func() {
		writeDone <- s.WriteTx(ctx, func(tx *store.Tx) error {
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('notify_probe', 'committed')`); err != nil {
				return err
			}
			seq, err := tx.Append(event.Event{
				Kind:       "item.add",
				ObjectType: "item",
				ObjectID:   &itemID,
				Payload:    json.RawMessage(`{"title":"slice-1a"}`),
			})
			if err != nil {
				return err
			}
			appendedSeq = seq
			tx.Notify(notify.TopicDocsChanged, itemID)
			close(insideCallback)
			<-allowCommit
			return nil
		})
	}()

	<-insideCallback

	// Before Commit returns, no hint may have been published onto the relay.
	preCommitCtx, preCommitCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer preCommitCancel()
	if premature, err := sub.Next(preCommitCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("received frame before WriteTx committed: frame=%+v err=%v", premature, err)
	}

	// Release the transaction to commit.
	close(allowCommit)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}

	readCtx, readCancel := context.WithTimeout(ctx, hangGuard)
	defer readCancel()
	first, err := sub.Next(readCtx)
	if err != nil {
		t.Fatal(err)
	}

	// The moment the wake hint arrives, a reader sees the committed state.
	var gotVal string
	if err := reader.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT value FROM meta WHERE key = 'notify_probe'`).Scan(&gotVal)
	}); err != nil {
		t.Fatalf("reader woken by hint did not see committed row: %v", err)
	}
	if gotVal != "committed" {
		t.Fatalf("meta value = %q, want %q", gotVal, "committed")
	}

	second, err := sub.Next(readCtx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Topic != notify.TopicQueueChanged || first.ItemID != itemID || first.Seq != appendedSeq {
		t.Fatalf("first frame = %+v, want queue.changed for %s seq %d", first, itemID, appendedSeq)
	}
	if second.Topic != notify.TopicDocsChanged || second.ItemID != itemID {
		t.Fatalf("second frame = %+v, want docs.changed for %s", second, itemID)
	}

	// A rolled-back WriteTx publishes nothing.
	boom := errors.New("rollback")
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, _ = tx.Exec(`INSERT INTO meta (key, value) VALUES ('rolled_back', 'no')`)
		tx.Notify(notify.TopicTrunkMoved, "item-rb")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("rolled-back WriteTx err = %v, want %v", err, boom)
	}
	rbCtx, rbCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer rbCancel()
	if leaked, err := sub.Next(rbCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rolled-back WriteTx published frame: %+v (err=%v)", leaked, err)
	}

	cancel()
	<-serveDone
}

func TestWriteTxDerivesTopicsAndSucceedsWithoutRelay(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	storePath := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	sockPath, err := notify.SocketPath(storePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sockPath)

	// With no relay running, WriteTx succeeds without error and without spawning a relay.
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('offline', 'ok')`)
		return err
	}); err != nil {
		t.Fatalf("WriteTx without relay failed: %v", err)
	}
	if _, statErr := os.Stat(sockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("WriteTx without relay created socket %s: %v", sockPath, statErr)
	}

	// Start relay and verify Section 4.3 topic derivations.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- notify.Serve(ctx, notify.ServeConfig{
			StorePath: storePath,
			IdleExit:  hangGuard,
			Ready:     ready,
		})
	}()
	select {
	case <-ready:
	case <-time.After(hangGuard):
		t.Fatal("relay did not become ready")
	}
	t.Cleanup(func() { _ = os.Remove(notify.LockPath(sockPath)) })

	sub, err := notify.Subscribe(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	itemID := "item-42"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Append(event.Event{
			Kind:       "item.add",
			ObjectType: "item",
			ObjectID:   &itemID,
			Payload:    json.RawMessage(`{"state":"done"}`),
		}); err != nil {
			return err
		}
		tx.Notify(notify.TopicCandidateReady, itemID, "run-42")
		tx.Notify(notify.TopicTrunkMoved, itemID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	wantTopics := []string{
		notify.TopicQueueChanged,
		notify.TopicItemEnded,
		notify.TopicCandidateReady,
		notify.TopicTrunkMoved,
	}
	readCtx, readCancel := context.WithTimeout(ctx, hangGuard)
	defer readCancel()
	for _, want := range wantTopics {
		got, err := sub.Next(readCtx)
		if err != nil {
			t.Fatalf("waiting for %s: %v", want, err)
		}
		if got.Topic != want || got.ItemID != "item-42" {
			t.Fatalf("got frame %+v, want topic %s item-42", got, want)
		}
	}

	cancel()
	<-serveDone
}
