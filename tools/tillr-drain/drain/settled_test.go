package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSettledIdleAttemptIsAnsweredWithRecordedObservations(t *testing.T) {
	root := scratch(t, "settled-drain-")
	log := filepath.Join(root, "report.md")
	if err := os.WriteFile(log, []byte("verification passed\nRESULT: done\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw := runRecord("run-settled", "item-settled", "lane-1", "fixture", run.StateExited, exitCode(-1), log)
	var env record.Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatal(err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	tokens := int64(100)
	rec.Usage = &run.Usage{Tokens: &tokens}
	rec.EndCause = run.EndSettledIdle
	rec.SettledAt = "2026-11-17T00:00:01Z"
	rec.HarnessEndedAt = "2026-11-17T00:00:06Z"
	if class := drain.ClassifyAttempt(rec, "RESULT: done\n"); class != drain.AttemptAnswered {
		t.Fatalf("class: %s", class)
	}
	env.Payload, _ = json.Marshal(rec)
	encoded, _ := json.Marshal(env)
	path := fixture(t, itemRecordSeated("item-settled", "in-queue", "build"), string(encoded))
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		got, found, err := run.StatusRecord(r, "run-settled")
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("run missing")
		}
		var status struct {
			Run run.Record `json:"run"`
		}
		json.Unmarshal(got.Payload, &status)
		if status.Run.EndCause != rec.EndCause || status.Run.SettledAt != rec.SettledAt || status.Run.HarnessEndedAt != rec.HarnessEndedAt {
			t.Fatalf("record facts: %+v", status.Run)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
