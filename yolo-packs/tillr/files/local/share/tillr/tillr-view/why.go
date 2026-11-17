package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// plannerWhy invokes the planner's read-only face, not a second readiness model.
// Keeping the process boundary avoids importing drain's write-schema registrations
// into the read utility. The program is the one resolvePlanner names, never a bare PATH
// lookup, and its answer is refused unless it names this server's build (siblings.go). A
// missing or mismatched planner is an explicit refusal, never a history fallback.
//
// index, when it is not nil, is the Vantage index document the server already holds
// (vantagecache.go): it is handed to the planner on stdin with `--questions-index -`, so the
// planner answers from the same walk instead of starting its own, which was 98% of its time.
// A nil index leaves the planner to run the index command itself, as it always has.
func plannerWhy(ctx context.Context, program plannerProgram, self buildStamp, path, root, policy, lanes string, questionsCmd []string, index []byte) (json.RawMessage, []waitingRow, error) {
	args := []string{"why", "--store", path, "--root", root, "--json"}
	if policy != "" {
		args = append(args, "--policy", policy)
	}
	if lanes != "" {
		args = append(args, "--lanes", lanes)
	}
	if index != nil {
		args = append(args, "--questions-index", "-")
	} else if len(questionsCmd) > 0 {
		args = append(args, "--questions-cmd", strings.Join(questionsCmd, " "))
	}
	cmd := exec.CommandContext(ctx, program.Path, args...)
	if index != nil {
		cmd.Stdin = bytes.NewReader(index)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	start := time.Now()
	raw, err := cmd.Output()
	planned := map[string]any{"ms": msSince(time.Now, start), "bytes": len(raw), "exit": cmd.ProcessState.ExitCode(), "index": "own"}
	if index != nil {
		planned["index"] = "preloaded"
	}
	phase(ctx, "planner", planned)
	if err != nil {
		return nil, nil, &plannerError{Kind: plannerUnavailable,
			Msg: fmt.Sprintf("the read-only planner (tillr-drain why, %s) failed: %v; %s", program, err, strings.TrimSpace(stderr.String()))}
	}
	var answer struct {
		Rows    []waitingRow    `json:"rows"`
		Summary json.RawMessage `json:"summary"`
		Build   *drainBuild     `json:"build"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, nil, &plannerError{Kind: plannerUnavailable,
			Msg: fmt.Sprintf("the planner %s returned invalid why JSON: %v", program, err)}
	}
	if err := checkPlannerBuild(self, answer.Build, program); err != nil {
		return nil, nil, err
	}
	if answer.Rows == nil || len(answer.Summary) == 0 {
		return nil, nil, &plannerError{Kind: plannerUnavailable,
			Msg: fmt.Sprintf("the planner %s answered why without rows or a summary", program)}
	}
	if len(answer.Rows) > 0 {
		s, err := store.OpenRead(ctx, path)
		if err != nil {
			return nil, nil, err
		}
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			conflicts, err := waitingConflicts(r)
			if err != nil {
				return err
			}
			addConflictHistory(answer.Rows, conflicts)
			return nil
		}); err != nil {
			return nil, nil, err
		}
		// Preserve planner-only fields and exact JSON numbers; waitingRow is only
		// the shared display subset, not a replacement for the planner's answer.
		var document map[string]json.RawMessage
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil, nil, err
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(document["rows"], &rows); err != nil {
			return nil, nil, err
		}
		for i := range rows {
			why, err := json.Marshal(answer.Rows[i].Why)
			if err != nil {
				return nil, nil, err
			}
			rows[i]["why"] = why
		}
		encoded, err := json.Marshal(rows)
		if err != nil {
			return nil, nil, err
		}
		document["rows"] = encoded
		raw, err = json.Marshal(document)
		if err != nil {
			return nil, nil, err
		}
	}
	return json.RawMessage(raw), answer.Rows, nil
}

// askPlanner resolves the planner and asks it, so every caller gets the same refusal for a
// planner that cannot be found as for one that cannot answer.
func askPlanner(ctx context.Context, explicit string, self buildStamp, path, root, policy, lanes string, questionsCmd []string, index []byte) (json.RawMessage, []waitingRow, error) {
	program, err := resolvePlanner(explicit)
	if err != nil {
		return nil, nil, err
	}
	return plannerWhy(ctx, program, self, path, root, policy, lanes, questionsCmd, index)
}

type whyPayloadData struct {
	raw  json.RawMessage
	head int64
}

func (s *server) whyPayload(_ *store.Reader, r *http.Request) (any, *apiFailure) {
	// The index is the server's cached walk. When it cannot be read the planner is asked
	// without it and runs the index command itself, so a failure is the planner's own
	// answer (`questions-unknown`) exactly as before the cache existed.
	index, look, ierr := s.vantage.get(r.Context())
	phase(r.Context(), "vantage", look)
	if ierr != nil {
		index = nil
	}
	raw, _, err := askPlanner(r.Context(), s.plannerFlag, s.build, s.storePath, s.root, s.policyRef, s.offeredLanes, s.questionsCmd, index)
	if err != nil {
		// The planner is a program beside this one, not the store and not the drain: its
		// absence says nothing about whether the loop is moving, and the code says so.
		code := codePlannerUnavailable
		var planner *plannerError
		if errors.As(err, &planner) && planner.Kind == plannerMismatch {
			code = codePlannerMismatch
		}
		return nil, failure(code, "%v", err)
	}
	var answer struct {
		Head int64 `json:"event_head_seq"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, failure(codeStoreUnreadable, "why cursor: %v", err)
	}
	return whyPayloadData{raw: raw, head: answer.Head}, nil
}
