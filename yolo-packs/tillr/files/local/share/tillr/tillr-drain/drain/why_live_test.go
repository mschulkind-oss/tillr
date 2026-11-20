package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRefreshSessionRecordsLanesForLiveLoopLanes(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1"}
	if err := l.refreshSession(ctx, l.cfg.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.endSession)

	var err error
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got []string
	var live bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		got, live, err = LiveLoopLanes(r, f.storePath, l.cfg.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !live || !reflect.DeepEqual(got, []string{"lane-1", "lane-2", "express-1"}) {
		t.Fatalf("LiveLoopLanes() = (%v, %v), want ([lane-1 lane-2 express-1], true)", got, live)
	}
}

func TestSavedQuestionsWritesAndReusesIndexAtSameTrunk(t *testing.T) {
	root := t.TempDir()
	docPath := filepath.Join(root, "docs", "design.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# Design\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	counter := filepath.Join(t.TempDir(), "calls")
	script := filepath.Join(t.TempDir(), "index.sh")
	body := "#!/bin/sh\nprintf '1\\n' >> " + counter + "\nprintf '%s' '{\"index\":{\"documents\":[{\"path\":\"docs/design.md\",\"questions\":[{\"id\":\"OQ-1\",\"state\":\"open\"}]}]}}'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := []string{script}
	var calls atomic.Int32

	q1 := newSavedQuestions(root, "abc123", cmd, context.Background())
	doc, err := q1.Document("docs/design.md")
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Present || len(doc.Questions) != 1 || doc.Questions[0].ID != "OQ-1" {
		t.Fatalf("first Document: %+v", doc)
	}
	if _, err := os.Stat(QuestionsIndexFile(root)); err != nil {
		t.Fatalf("saved index file missing: %v", err)
	}

	// Second reader at the same trunk SHA reuses the saved file without running script.
	q2 := newSavedQuestions(root, "abc123", cmd, context.Background())
	doc2, err := q2.Document("docs/design.md")
	if err != nil {
		t.Fatal(err)
	}
	if !doc2.Present || len(doc2.Questions) != 1 || doc2.Questions[0].ID != "OQ-1" {
		t.Fatalf("second Document: %+v", doc2)
	}
	rawCalls, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range rawCalls {
		if b == '\n' {
			calls.Add(1)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("index command ran %d times, want 1", calls.Load())
	}
}
