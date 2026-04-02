package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Exit codes
const (
	ExitSuccess    = 0
	ExitUserError  = 1
	ExitSysError   = 2
)

// CLIError wraps an error with context, hint, and exit code.
type CLIError struct {
	Context  string `json:"error"`
	Cause    error  `json:"-"`
	Hint     string `json:"hint,omitempty"`
	ExitCode int    `json:"-"`
}

func (e *CLIError) Error() string {
	msg := fmt.Sprintf("Error: %s", e.Context)
	if e.Cause != nil {
		msg += fmt.Sprintf(": %s", e.Cause)
	}
	return msg
}

// userError creates a user-facing error (exit code 1) with an optional hint.
func userError(context string, cause error, hint string) *CLIError {
	return &CLIError{
		Context:  context,
		Cause:    cause,
		Hint:     hint,
		ExitCode: ExitUserError,
	}
}

// sysError creates a system error (exit code 2) with an optional hint.
func sysError(context string, cause error, hint string) *CLIError {
	return &CLIError{
		Context:  context,
		Cause:    cause,
		Hint:     hint,
		ExitCode: ExitSysError,
	}
}

// formatError formats an error for output based on whether JSON mode is active.
func formatError(err error) {
	if err == nil {
		return
	}

	cliErr, ok := err.(*CLIError)
	if !ok {
		// Wrap generic errors
		cliErr = &CLIError{
			Context:  err.Error(),
			ExitCode: ExitUserError,
		}
	}

	if jsonOutput {
		out := map[string]string{"error": cliErr.Context}
		if cliErr.Cause != nil {
			out["error"] = fmt.Sprintf("%s: %s", cliErr.Context, cliErr.Cause)
		}
		if cliErr.Hint != "" {
			out["hint"] = cliErr.Hint
		}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(os.Stderr, string(data))
	} else {
		fmt.Fprintln(os.Stderr, cliErr.Error())
		if cliErr.Hint != "" {
			fmt.Fprintf(os.Stderr, "Hint: %s\n", cliErr.Hint)
		}
	}
}

// Common error hints for recovery suggestions
var errorHints = map[string]string{
	"no tillr project found": "Run 'tillr init <name>' to create a new project, or 'tillr onboard' to onboard an existing one.",
	"feature not found":          "Run 'tillr feature list' to see available features.",
	"milestone not found":        "Run 'tillr milestone list' to see available milestones.",
	"discussion not found":       "Run 'tillr discuss list' to see available discussions.",
	"no active cycle":            "Run 'tillr cycle start <type> <feature-id>' to start a cycle.",
	"no pending work items":      "All work items are completed or in progress. Create new features or start cycles.",
	"no active work item":        "Run 'tillr next' to get the next work item.",
	"invalid transition":         "Run 'tillr feature show <id>' to see current status and valid transitions.",
	"already initialized":        "This directory already has a tillr project. Use 'tillr status' to view it.",
}

// hintForError returns a recovery hint based on the error message.
func hintForError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	for pattern, hint := range errorHints {
		if strings.Contains(msg, pattern) {
			return hint
		}
	}
	return ""
}
