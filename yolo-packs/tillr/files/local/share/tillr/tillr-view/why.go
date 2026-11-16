package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// plannerWhy invokes the planner's read-only face, not a second readiness model.
// Keeping the process boundary avoids importing drain's write-schema registrations
// into the read utility. A missing binary is an explicit refusal, never a history fallback.
func plannerWhy(ctx context.Context, path, root, policy, lanes string, questionsCmd []string) (json.RawMessage, []waitingRow, error) {
	args := []string{"why", "--store", path, "--root", root, "--json"}
	if policy != "" {
		args = append(args, "--policy", policy)
	}
	if lanes != "" {
		args = append(args, "--lanes", lanes)
	}
	if len(questionsCmd) > 0 {
		args = append(args, "--questions-cmd", strings.Join(questionsCmd, " "))
	}
	cmd := exec.CommandContext(ctx, "tillr-drain", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("the read-only planner (tillr-drain why) failed: %w; %s", err, strings.TrimSpace(stderr.String()))
	}
	var answer struct {
		Rows    []waitingRow    `json:"rows"`
		Summary json.RawMessage `json:"summary"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, nil, fmt.Errorf("the planner returned invalid why JSON: %w", err)
	}
	if answer.Rows == nil || len(answer.Summary) == 0 {
		return nil, nil, fmt.Errorf("the planner why answer lacks rows or summary")
	}
	return json.RawMessage(raw), answer.Rows, nil
}

type whyPayloadData struct {
	raw  json.RawMessage
	head int64
}

func (s *server) whyPayload(_ *store.Reader, r *http.Request) (any, *apiFailure) {
	raw, _, err := plannerWhy(r.Context(), s.storePath, s.root, s.policyRef, s.offeredLanes, s.questionsCmd)
	if err != nil {
		return nil, failure(codeStoreUnreadable, "why could not run the planner: %v", err)
	}
	var answer struct {
		Head int64 `json:"event_head_seq"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, failure(codeStoreUnreadable, "why cursor: %v", err)
	}
	return whyPayloadData{raw: raw, head: answer.Head}, nil
}
