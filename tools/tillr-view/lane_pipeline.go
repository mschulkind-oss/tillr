package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

type pipelineStep struct {
	Step         string `json:"step"`
	StartedAtMS  int64  `json:"started_at_ms"`
	FinishedAtMS *int64 `json:"finished_at_ms"`
}

type laneHistoryRun struct {
	RunID        string `json:"run_id"`
	ItemID       string `json:"item_id"`
	LaneID       string `json:"lane_id"`
	State        string `json:"state"`
	StartedAtMS  int64  `json:"started_at_ms"`
	FinishedAtMS int64  `json:"finished_at_ms"`
}

// Both observations of an invocation share its step and start clock. A repeated
// invocation replaces the previous one; a partial final line is not an observation.
func readLanePipeline(path string, stats *laneReadStats) ([]pipelineStep, string) {
	raw, _, err := laneTail(path)
	if os.IsNotExist(err) {
		return nil, "pipeline timing was not recorded"
	}
	if err != nil {
		return nil, fmt.Sprintf("pipeline timing unreadable: %v", err)
	}
	stats.add(len(raw))
	steps := []pipelineStep{}
	positions := map[string]int{}
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var step pipelineStep
		if err := json.Unmarshal(line, &step); err != nil {
			return nil, "pipeline timing unreadable: invalid observation"
		}
		switch step.Step {
		case "stage", "graph", "gate", "land":
		default:
			continue
		}
		if i, ok := positions[step.Step]; ok {
			steps[i] = step
		} else {
			positions[step.Step] = len(steps)
			steps = append(steps, step)
		}
	}
	return steps, ""
}
