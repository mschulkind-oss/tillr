package drain

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
)

func isDesignDecision(d Decision) bool {
	return d.BlockConflict != nil || d.Seat == "design"
}

func isDesignWork(w *pendingWork) bool {
	return w != nil && isDesignDecision(w.Decision)
}

func classifyDecisionAttempt(d Decision, rec run.Record, log string) AttemptClass {
	if isDesignDecision(d) {
		if _, ok := piTransportError(rec); ok {
			return AttemptInfrastructure
		}
	}
	return ClassifyAttempt(rec, log)
}

func classifyWorkAttempt(w *pendingWork, rec run.Record, log string) AttemptClass {
	if isDesignWork(w) {
		if _, ok := piTransportError(rec); ok {
			return AttemptInfrastructure
		}
	}
	return ClassifyAttempt(rec, log)
}

func hasPiTransportError(rec run.Record) bool {
	_, ok := piTransportError(rec)
	return ok
}

// piTransportError reports whether a pi run's own JSONL stream log ended on a
// model or transport error after pi's automatic retries failed, returning the
// error text when it did.
func piTransportError(rec run.Record) (string, bool) {
	if rec.Backend == "command" || rec.State != run.StateExited {
		return "", false
	}
	raw := rawLogTailBytes(rec.ArtifactRefs, 1<<20)
	if raw == "" {
		return "", false
	}
	return parsePiTransportError(raw)
}

func rawLogTailBytes(refs []run.ArtifactRef, maxBytes int64) string {
	for _, ref := range refs {
		if ref.Kind != "log" || ref.Ref == "" {
			continue
		}
		file, err := os.Open(ref.Ref)
		if err != nil {
			return ""
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return ""
		}
		if offset := info.Size() - maxBytes; offset > 0 {
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				return ""
			}
		}
		body, err := io.ReadAll(file)
		if err != nil {
			return ""
		}
		return string(body)
	}
	return ""
}

func parsePiTransportError(raw string) (string, bool) {
	var (
		agentEndError bool
		agentEndMsg   string
		retryFailed   bool
		retryError    string
	)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var ev struct {
			Type       string `json:"type"`
			Success    *bool  `json:"success"`
			FinalError string `json:"finalError"`
			Messages   []struct {
				Role         string `json:"role"`
				StopReason   string `json:"stopReason"`
				ErrorMessage string `json:"errorMessage"`
			} `json:"messages"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "agent_end":
			if len(ev.Messages) > 0 {
				last := ev.Messages[len(ev.Messages)-1]
				if last.Role == "assistant" && last.StopReason == "error" {
					agentEndError = true
					agentEndMsg = strings.TrimSpace(last.ErrorMessage)
					continue
				}
			}
			agentEndError = false
			agentEndMsg = ""
			retryFailed = false
			retryError = ""
		case "auto_retry_end":
			if ev.Success != nil && !*ev.Success {
				retryFailed = true
				retryError = strings.TrimSpace(ev.FinalError)
			} else if ev.Success != nil && *ev.Success {
				retryFailed = false
				retryError = ""
			}
		}
	}
	if !agentEndError || !retryFailed {
		return "", false
	}
	msg := retryError
	if msg == "" {
		msg = agentEndMsg
	}
	if msg == "" {
		msg = "model or transport error"
	}
	return msg, true
}
