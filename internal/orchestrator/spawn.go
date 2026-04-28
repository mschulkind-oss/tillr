package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// ClaudeSpawn is the production SpawnFunc. It builds a `claude -p`
// command with all the right flags and runs it. The persona's
// context file is appended to the system prompt (so the agent sees
// its own accumulated memory at invocation time). The resulting
// JSON is parsed against PersonaJSONSchema.
func ClaudeSpawn(ctx context.Context, opts SpawnOpts) Spawned {
	if opts.DryRunResult != nil {
		return *opts.DryRunResult
	}

	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", opts.JSONSchema,
		"--no-session-persistence",
		"--bare",
	}

	if opts.Persona != "" {
		args = append(args, "--agent", opts.Persona)
	}
	if opts.ContextFilePath != "" {
		args = append(args, "--append-system-prompt-file", opts.ContextFilePath)
	}
	if opts.WorktreeName != "" {
		args = append(args, "--worktree", opts.WorktreeName)
	}

	prompt := fmt.Sprintf(
		"Feature #%d: %s\n\n%s\n\nWhen done, return the structured JSON per the schema.",
		opts.FeatureID, opts.FeatureTitle, opts.FeatureDescription,
	)
	args = append(args, prompt)

	timeout := opts.WorkerTimeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, opts.ClaudeBinary, args...)
	cmd.Dir = opts.ProjectRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	durationMS := time.Since(start).Milliseconds()

	out := Spawned{
		DurationMS: durationMS,
		Stderr:     stderr.String(),
	}

	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			out.ExitCode = exitErr.ExitCode()
		} else {
			out.SpawnError = err.Error()
		}
	}

	// Parse the claude --output-format=json envelope. The exact
	// shape varies slightly by claude version; we extract what we
	// need (assistant message, cost, session_id, model) defensively.
	parseClaudeEnvelope(stdout.Bytes(), &out)
	return out
}

// parseClaudeEnvelope is defensive about claude's JSON shape. It
// looks for known fields without over-coupling to one schema version.
func parseClaudeEnvelope(data []byte, out *Spawned) {
	if len(data) == 0 {
		return
	}
	var env map[string]any
	if err := json.Unmarshal(data, &env); err != nil {
		out.SpawnError = "could not parse claude JSON output: " + err.Error()
		return
	}

	if v, ok := env["session_id"].(string); ok {
		out.SessionID = v
	}
	if v, ok := env["model"].(string); ok {
		out.Model = v
	}
	if v, ok := env["cost_usd"].(float64); ok {
		out.CostUSD = v
	}
	if usage, ok := env["usage"].(map[string]any); ok {
		if t, ok := usage["input_tokens"].(float64); ok {
			out.InputTokens = int64(t)
		}
		if t, ok := usage["output_tokens"].(float64); ok {
			out.OutputTokens = int64(t)
		}
	}

	// The structured output lives at top level under
	// "structured_output" when --json-schema is set, or at
	// "result" / "response" depending on version. Try in order.
	for _, key := range []string{"structured_output", "result", "response"} {
		if raw, ok := env[key]; ok {
			b, _ := json.Marshal(raw)
			var r Result
			if err := json.Unmarshal(b, &r); err == nil && r.Result != "" {
				out.Result = r
				return
			}
		}
	}

	// Fallback: try parsing the whole envelope as a Result (some
	// claude versions put fields at top level when there's no
	// envelope wrapper).
	var fallback Result
	if err := json.Unmarshal(data, &fallback); err == nil && fallback.Result != "" {
		out.Result = fallback
	}
}
