package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestUnchangedTrunkObservationDoesNotWaitForStoreWriter(t *testing.T) {
	f, l := passFixture(t)
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	locked, release, writerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		writerDone <- st.WriteTx(context.Background(), func(tx *store.Tx) error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	done := make(chan error, 1)
	go func() { _, err := l.observeTrunk(context.Background(), ""); done <- err }()
	var observationErr error
	select {
	case observationErr = <-done:
		close(release)
	case <-time.After(time.Second):
		close(release)
		observationErr = <-done
		t.Errorf("unchanged observation tried to migrate or write behind another store writer")
	}
	if observationErr != nil {
		t.Errorf("observation: %v", observationErr)
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
}
