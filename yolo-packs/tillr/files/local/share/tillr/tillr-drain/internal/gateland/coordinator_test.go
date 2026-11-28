package gateland

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestStateTrackingAndWake(t *testing.T) {
	st := NewState()
	if st.AnyRunning() || st.HasRun("r1") || st.HasItem("i1") {
		t.Fatal("fresh State should have no running items or runs")
	}

	st.Gate.MarkStart("i1", "r1")
	if !st.AnyRunning() || !st.HasRun("r1") || !st.HasItem("i1") || st.RunCount() != 1 {
		t.Fatalf("after Gate.MarkStart: running=%v hasRun=%v hasItem=%v count=%d",
			st.AnyRunning(), st.HasRun("r1"), st.HasItem("i1"), st.RunCount())
	}

	st.Land.MarkStart("i2", "r2")
	if st.RunCount() != 2 || !st.HasRun("r2") || !st.HasItem("i2") {
		t.Fatalf("after Land.MarkStart: count=%d", st.RunCount())
	}

	st.Gate.MarkDone("i1", "r1")
	if st.HasRun("r1") || st.HasItem("i1") || !st.AnyRunning() || st.Gate.Completions != 1 {
		t.Fatalf("after Gate.MarkDone: hasRun=%v hasItem=%v running=%v completions=%d",
			st.HasRun("r1"), st.HasItem("i1"), st.AnyRunning(), st.Gate.Completions)
	}

	st.Land.MarkDone("i2", "r2")
	if st.AnyRunning() || st.RunCount() != 0 || st.Land.Completions != 1 {
		t.Fatalf("after Land.MarkDone: running=%v count=%d completions=%d",
			st.AnyRunning(), st.RunCount(), st.Land.Completions)
	}

	SignalWake(nil) // no-op
	SignalWake(st.Gate.Wake)
	SignalWake(st.Gate.Wake) // non-blocking second signal
	select {
	case <-st.Gate.Wake:
	default:
		t.Fatal("expected wake signal on Gate.Wake")
	}
}

func TestEligibilityPredicates(t *testing.T) {
	base := Eligibility{
		HasCandidate: true,
		RunID:        "run-1",
		LaneID:       "lane-1",
	}
	if !GateEligible(base, "") || !GateEligible(base, "run-1") {
		t.Fatal("base un-gated candidate should be gate-eligible")
	}
	if GateEligible(base, "run-2") {
		t.Fatal("mismatched wantRunID should not be gate-eligible")
	}
	if LandEligible(base, "") {
		t.Fatal("un-gated candidate should not be land-eligible")
	}

	gated := base
	gated.Gated = true
	if GateEligible(gated, "") {
		t.Fatal("gated candidate should not be gate-eligible")
	}
	if !LandEligible(gated, "") || !LandEligible(gated, "run-1") {
		t.Fatal("gated candidate should be land-eligible")
	}
	if LandEligible(gated, "run-2") {
		t.Fatal("mismatched wantRunID should not be land-eligible")
	}

	noChange := base
	noChange.HasNoChange = true
	if GateEligible(noChange, "") {
		t.Fatal("no-change candidate should not be gate-eligible")
	}

	repair := gated
	repair.HasRepairHold = true
	if LandEligible(repair, "") {
		t.Fatal("repair-held candidate should not be land-eligible")
	}
}

func TestSortCandidates(t *testing.T) {
	gates := []GateCandidate{
		{ItemID: "b", RunID: "r2"},
		{ItemID: "a", RunID: "r3"},
		{ItemID: "a", RunID: "r1"},
	}
	SortGateCandidates(gates)
	if gates[0].RunID != "r1" || gates[1].RunID != "r3" || gates[2].RunID != "r2" {
		t.Fatalf("unexpected gate order: %+v", gates)
	}

	lands := []LandCandidate{
		{ItemID: "a", RunID: "r2", GatedSeq: 20},
		{ItemID: "z", RunID: "r1", GatedSeq: 10},
		{ItemID: "b", RunID: "r3", GatedSeq: 20},
	}
	SortLandCandidates(lands)
	if lands[0].RunID != "r1" || lands[1].RunID != "r2" || lands[2].RunID != "r3" {
		t.Fatalf("unexpected FIFO land order: %+v", lands)
	}
}

func TestRunLoopWakeAndTimer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wakeCh := make(chan struct{}, 1)
	timerCh := make(chan time.Time, 1)
	var scans atomic.Int32
	scanDone := make(chan int32, 4)

	done := make(chan struct{})
	go func() {
		defer close(done)
		RunLoop(
			ctx,
			time.Hour,
			func(time.Duration) (<-chan time.Time, func()) {
				return timerCh, func() {}
			},
			func() <-chan struct{} { return wakeCh },
			func() {
				scanDone <- scans.Add(1)
			},
		)
	}()

	if n := <-scanDone; n != 1 {
		t.Fatalf("initial scan = %d, want 1", n)
	}
	SignalWake(wakeCh)
	if n := <-scanDone; n != 2 {
		t.Fatalf("wake scan = %d, want 2", n)
	}
	timerCh <- time.Now()
	if n := <-scanDone; n != 3 {
		t.Fatalf("timer scan = %d, want 3", n)
	}
	cancel()
	<-done
}

func TestReadGatedSeqs(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	run1, run2, item1 := "run-1", "run-2", "item-1"
	const (
		gatedKind = "drain.gated"
		otherKind = "drain.other"
	)
	_ = event.Register(gatedKind, "tillr-drain")
	_ = event.Register(otherKind, "tillr-drain")
	var seq1, seq2 int64
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		if _, err = tx.Append(event.Event{Kind: gatedKind, ObjectType: "run", ObjectID: &run1, Payload: []byte(`{}`)}); err != nil {
			return err
		}
		if seq1, err = tx.Append(event.Event{Kind: gatedKind, ObjectType: "run", ObjectID: &run1, Payload: []byte(`{}`)}); err != nil {
			return err
		}
		if _, err = tx.Append(event.Event{Kind: otherKind, ObjectType: "run", ObjectID: &run2, Payload: []byte(`{}`)}); err != nil {
			return err
		}
		if _, err = tx.Append(event.Event{Kind: gatedKind, ObjectType: "item", ObjectID: &item1, Payload: []byte(`{}`)}); err != nil {
			return err
		}
		seq2, err = tx.Append(event.Event{Kind: gatedKind, ObjectType: "run", ObjectID: &run2, Payload: []byte(`{}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var got map[string]int64
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		got, err = ReadGatedSeqs(r, gatedKind)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[run1] != seq1 || got[run2] != seq2 {
		t.Fatalf("ReadGatedSeqs = %v, want %s:%d and %s:%d", got, run1, seq1, run2, seq2)
	}
}
