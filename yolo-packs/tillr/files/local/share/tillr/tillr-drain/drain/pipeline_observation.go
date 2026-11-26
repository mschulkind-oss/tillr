package drain

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Pipeline observations are separate from completed command spans: an unfinished
// command has no exit code or duration, but its start must be readable immediately.
type pipelineObservation struct {
	Step         string `json:"step"`
	StartedAtMS  int64  `json:"started_at_ms"`
	FinishedAtMS *int64 `json:"finished_at_ms"`
}

func pipelineStart(root, runID, step string) (pipelineObservation, error) {
	observation := pipelineObservation{Step: step, StartedAtMS: time.Now().UnixMilli()}
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
	return observation, appendRunLine(root, runID, "pipeline.jsonl", observation)
}

func pipelineFinish(root, runID string, observation pipelineObservation) error {
	if root == "" {
		return nil
	}
	finished := time.Now().UnixMilli()
	observation.FinishedAtMS = &finished
	return appendRunLine(root, runID, "pipeline.jsonl", observation)
}

// Closing is idempotent: replacement settlement supplies the missing finish, but
// a recorded finish remains the original command clock.
func pipelineCloseGraph(root, runID string) error {
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
	return pipelineFinish(root, runID, observation)
}

func lastGraphObservation(root, runID string) (pipelineObservation, error) {
	raw, err := os.ReadFile(filepath.Join(RunDir(root, runID), "pipeline.jsonl"))
	if os.IsNotExist(err) {
		return pipelineObservation{}, nil
	}
	if err != nil {
		return pipelineObservation{}, err
	}
	var last pipelineObservation
	lines := bytes.Split(raw, []byte{'\n'})
	for _, line := range lines[:len(lines)-1] {
		if len(line) == 0 {
			continue
		}
		var observation pipelineObservation
		if err := json.Unmarshal(line, &observation); err != nil {
			return last, err
		}
		if observation.Step == "graph" {
			last = observation
		}
	}
	return last, nil
}
