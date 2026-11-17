package drain

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

type stopProbeStager func(StageSpec) error

func (s stopProbeStager) Stage(_ context.Context, spec StageSpec) error { return s(spec) }

func TestStagingFailureRecordsTheItemAndStep(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	loop := f.loop(t, &recordingSteps{}, stubStager{})
	loop.cfg.Stage = stopProbeStager(func(spec StageSpec) error {
		// Simulate a store outage after staging, while restoring this lane.
		loop.laneConditions[spec.LaneID] = &laneCondition{}
		loop.cfg.Store = f.root
		return nil
	})
	var result LoopResult
	_, err := loop.pass(context.Background(), &result)
	if err == nil {
		t.Fatal("expected staging store failure")
	}
	result, _ = faultResult(result, err)
	loop.recordResult(&result)
	raw, readErr := os.ReadFile(LoopResultFile(f.root, result.FirstGeneration, result.LastGeneration))
	if readErr != nil {
		t.Fatal(readErr)
	}
	env, readErr := record.Decode(raw)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var written LoopResult
	if err := json.Unmarshal(env.Payload, &written); err != nil {
		t.Fatal(err)
	}
	if written.StopExplanation == nil || written.StopExplanation.ItemID != "item-a" || written.StopExplanation.Step != "stage" {
		t.Fatalf("lost item-specific stop: %+v", written.StopExplanation)
	}
}

func TestEveryStopWritesItsOwnExplanation(t *testing.T) {
	for _, reason := range []string{StopQueueDrained, StopOperatorStop, StopHostBoundaryViolation, StopGateHarnessError, StopLanesLeased, StopFault} {
		t.Run(reason, func(t *testing.T) {
			root := scratchUnderDurable(t)
			at := time.Unix(1700000000, 0)
			RecordLoopResult(root, at, BuildStamp{}, LoopResult{Stop: reason, Detail: "the recorded cause", StopItemID: "item-a", StopStep: "gate"}, nil)
			raw, err := os.ReadFile(LoopResultFile(root, "", ""))
			if err != nil {
				t.Fatal(err)
			}
			env, err := record.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			var result LoopResult
			if err := json.Unmarshal(env.Payload, &result); err != nil {
				t.Fatal(err)
			}
			if result.StopExplanation == nil {
				t.Fatal("no stop explanation")
			}
			stop := result.StopExplanation
			if stop.AtMS != at.UnixMilli() || stop.ItemID != "item-a" || stop.Step != "gate" || stop.Reason != reason || !strings.Contains(stop.Sentence, "the recorded cause") || stop.Next == "" {
				t.Fatalf("incomplete stop: %+v", stop)
			}
			if strings.Contains(stop.Next, "diagnose the recorded fault") {
				t.Fatal(stop.Next)
			}
		})
	}
}
