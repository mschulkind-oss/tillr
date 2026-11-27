package store

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/notify"
)

func TestFramesForEventMapping(t *testing.T) {
	itemID := "item-7"
	runID := "run-7"
	at := time.UnixMilli(1763936280000).UTC()

	cases := []struct {
		name       string
		ev         event.Event
		seq        int64
		wantTopics []string
		wantItemID string
		wantRunID  string
	}{
		{
			name: "item add maps to queue.changed",
			ev: event.Event{
				Kind:       "item.add",
				ObjectType: "item",
				ObjectID:   &itemID,
				At:         at,
			},
			seq:        10,
			wantTopics: []string{notify.TopicQueueChanged},
			wantItemID: itemID,
		},
		{
			name: "item terminal maps to queue.changed and item.ended",
			ev: event.Event{
				Kind:       "item.abandoned",
				ObjectType: "item",
				ObjectID:   &itemID,
				At:         at,
			},
			seq:        11,
			wantTopics: []string{notify.TopicQueueChanged, notify.TopicItemEnded},
			wantItemID: itemID,
		},
		{
			name: "drain assignment maps to run.assigned",
			ev: event.Event{
				Kind:       "drain.started",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7"}`),
				At:         at,
			},
			seq:        12,
			wantTopics: []string{notify.TopicRunAssigned},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "drain staged maps to run.staged",
			ev: event.Event{
				Kind:       "drain.staged",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7"}`),
				At:         at,
			},
			seq:        13,
			wantTopics: []string{notify.TopicRunStaged},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "run allocate maps to run.started",
			ev: event.Event{
				Kind:       "run.allocate",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7"}`),
				At:         at,
			},
			seq:        14,
			wantTopics: []string{notify.TopicRunStarted},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "run finish maps to run.exited and agent.slot-freed",
			ev: event.Event{
				Kind:       "run.finish",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7","state":"exited"}`),
				At:         at,
			},
			seq:        15,
			wantTopics: []string{notify.TopicRunExited, notify.TopicAgentSlotFreed},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "green gate verdict including pass-inherited maps to gate.finished and gate.passed",
			ev: event.Event{
				Kind:       "gate.verdict",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7","result":"pass-inherited"}`),
				At:         at,
			},
			seq:        16,
			wantTopics: []string{notify.TopicGateFinished, notify.TopicGatePassed},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "red gate verdict maps to gate.finished only",
			ev: event.Event{
				Kind:       "gate.verdict",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7","result":"fail"}`),
				At:         at,
			},
			seq:        17,
			wantTopics: []string{notify.TopicGateFinished},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "candidate ready maps to candidate.ready",
			ev: event.Event{
				Kind:       "drain.candidate",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7"}`),
				At:         at,
			},
			seq:        18,
			wantTopics: []string{notify.TopicCandidateReady},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "candidate gated maps to gate.passed",
			ev: event.Event{
				Kind:       "drain.gated",
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    json.RawMessage(`{"item_id":"item-7"}`),
				At:         at,
			},
			seq:        19,
			wantTopics: []string{notify.TopicGatePassed},
			wantItemID: itemID,
			wantRunID:  runID,
		},
		{
			name: "landing maps to trunk.moved and queue.changed",
			ev: event.Event{
				Kind:    "land.landing",
				Payload: json.RawMessage(`{"item_id":"item-7","run_id":"run-7"}`),
				At:      at,
			},
			seq:        19,
			wantTopics: []string{notify.TopicTrunkMoved, notify.TopicQueueChanged},
			wantItemID: itemID,
			wantRunID:  runID,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := framesForEvent(tc.ev, tc.seq)
			gotTopics := make([]string, len(got))
			for i, f := range got {
				gotTopics[i] = f.Topic
				if f.ItemID != tc.wantItemID || f.RunID != tc.wantRunID || f.Seq != tc.seq || f.AtMS != at.UnixMilli() {
					t.Fatalf("frame[%d] = %+v, want item=%q run=%q seq=%d at=%d",
						i, f, tc.wantItemID, tc.wantRunID, tc.seq, at.UnixMilli())
				}
			}
			if !reflect.DeepEqual(gotTopics, tc.wantTopics) {
				t.Fatalf("topics = %v, want %v", gotTopics, tc.wantTopics)
			}
		})
	}
}

func TestSuppressNotifySkipsWork(t *testing.T) {
	n := txNotifyState{mutated: true, suppress: true}
	if n.hasWork() {
		t.Fatal("suppressed txNotifyState reported work")
	}
}
