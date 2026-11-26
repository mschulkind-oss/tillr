package storelog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
)

// PipelineObservation records the start and finish of a pipeline step in pipeline.jsonl.
// Pipeline observations are separate from completed command spans: an unfinished
// command has no exit code or duration, but its start must be readable immediately.
type PipelineObservation struct {
	Step         string `json:"step"`
	StartedAtMS  int64  `json:"started_at_ms"`
	FinishedAtMS *int64 `json:"finished_at_ms"`
}

// PipelineStart appends a started PipelineObservation for step to pipeline.jsonl.
func PipelineStart(root, runID, step string) (PipelineObservation, error) {
	observation := PipelineObservation{Step: step, StartedAtMS: time.Now().UnixMilli()}
	if root == "" {
		return observation, nil
	}
	if step == "graph" {
		previous, err := lastGraphObservation(root, runID)
		if err != nil {
			return observation, err
		}
		if previous.Step != "" {
			observation.StartedAtMS = previous.StartedAtMS
		}
	}
	return observation, drainmodel.AppendRunLine(root, runID, "pipeline.jsonl", observation)
}

// PipelineFinish stamps FinishedAtMS on observation and appends it to pipeline.jsonl.
func PipelineFinish(root, runID string, observation PipelineObservation) error {
	if root == "" {
		return nil
	}
	finished := time.Now().UnixMilli()
	observation.FinishedAtMS = &finished
	return drainmodel.AppendRunLine(root, runID, "pipeline.jsonl", observation)
}

// PipelineCloseGraph idempotently finishes an open "graph" observation if none has finished.
// Closing is idempotent: replacement settlement supplies the missing finish, but
// a recorded finish remains the original command clock.
func PipelineCloseGraph(root, runID string) error {
	if root == "" {
		return nil
	}
	observation, err := lastGraphObservation(root, runID)
	if err != nil {
		return err
	}
	if observation.Step == "" || observation.FinishedAtMS != nil {
		return nil
	}
	return PipelineFinish(root, runID, observation)
}

func lastGraphObservation(root, runID string) (PipelineObservation, error) {
	raw, err := os.ReadFile(filepath.Join(drainmodel.RunDir(root, runID), "pipeline.jsonl"))
	if os.IsNotExist(err) {
		return PipelineObservation{}, nil
	}
	if err != nil {
		return PipelineObservation{}, err
	}
	var last PipelineObservation
	lines := bytes.Split(raw, []byte{'\n'})
	for _, line := range lines[:len(lines)-1] {
		if len(line) == 0 {
			continue
		}
		var observation PipelineObservation
		if err := json.Unmarshal(line, &observation); err != nil {
			return last, err
		}
		if observation.Step == "graph" {
			last = observation
		}
	}
	return last, nil
}
