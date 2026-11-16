package main_test

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

// A tick stamps one correlation id on the run.request it emits and on the run.allocate
// event the run appends when it claims the lane, so the first hop of a run is read by key
// rather than by searching payloads for an item and a timestamp.
//
// The id is the tick's own identity: the clock stamp plus the runs it dispatched, which
// names the item through the run id. The case drives one tick, executes the request it
// handed over, and joins the two rows by that value alone.
func TestATickStampsTheCorrelationIdTheRunAllocateEventCarries(t *testing.T) {
	const now = "2026-11-10T00:00:00Z"
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, now, []string{"lane-1"}, 1, policy(t, "/bin/true"))
	sink := filepath.Join(scratch(t, "sink"), "requests.json")

	onceOK(t, storePath, tickPath, "record-sink", sink)
	requests := readRequests(t, sink)
	if len(requests) != 1 {
		t.Fatalf("the tick handed over %d requests, want 1", len(requests))
	}
	request := requests[0]
	if request.CorrelationID == "" {
		t.Fatalf("the run.request carries no correlation_id: %+v", request)
	}
	if request.CorrelationID != request.Payload.CorrelationID {
		t.Fatalf("the envelope names correlation %q and its payload %q",
			request.CorrelationID, request.Payload.CorrelationID)
	}
	if request.CorrelationID != "gen-1" {
		t.Fatalf("the correlation %q does not name the allocated generation", request.CorrelationID)
	}

	// Execute the request the tick handed over. The run's claim is what appends the
	// run.allocate event, so this is the hop the correlation must survive.
	requestPath := filepath.Join(scratch(t, "records"), "run-request.json")
	writeFile(t, requestPath, string(sinkElement(t, sink, 0)))
	driveOK(t, sibling(t, "run"), "exec", "--store", storePath,
		"--lane-root", filepath.Join(scratch(t, "lanes"), "lanes"), "--input", requestPath, "--now", now)

	var allocated []map[string]any
	for _, e := range eventsOf(t, storePath) {
		if e.Kind != run.AllocateKind {
			continue
		}
		payload := eventPayload(t, e)
		if payload["correlation_id"] == request.CorrelationID {
			allocated = append(allocated, payload)
		}
	}
	if len(allocated) != 1 {
		t.Fatalf("the store holds %d %s events for correlation %q, want 1",
			len(allocated), run.AllocateKind, request.CorrelationID)
	}
	if allocated[0]["run_id"] != request.Payload.RunID || allocated[0]["item_id"] != "item_042" {
		t.Fatalf("the %s event names %v, want run %q for item_042",
			run.AllocateKind, allocated[0], request.Payload.RunID)
	}
}
