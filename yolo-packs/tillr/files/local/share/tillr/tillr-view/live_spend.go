package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// liveSpend is what a run's own stream says it has spent so far: its finished turns and
// the tokens and dollars those turns reported. A live run has no store total yet (the
// runner writes `usage_tokens` and `usage_cost_usd` only at its terminal write), so this is
// the only place a live run's spend can be read. The rule is the pi extractor's
// (`extract-report`): only `turn_end` is complete, because `message_start` and
// `message_update` repeat partial usage, so only `turn_end` is counted. Tokens and CostUSD
// are nil when no turn reported them; Note says why a reading is absent.
type liveSpend struct {
	Turns   *int     `json:"turns"`
	Tokens  *int64   `json:"tokens"`
	CostUSD *float64 `json:"cost_usd"`
	Note    string   `json:"note,omitempty"`
}

// loopRecoveryView summarizes in-run loop recoveries pi performed during a session
// (`loop_detected` events with `action: "recovered"`).
type loopRecoveryView struct {
	Count        int    `json:"count"`
	Shape        string `json:"shape,omitempty"`
	TokensBefore int64  `json:"tokens_before"`
	TokensAfter  int64  `json:"tokens_after"`
}

// liveSpendMaxLine bounds one stream line. A pi line carries a whole message, and a tool
// result can be large; a longer line is skipped rather than ending the read.
const liveSpendMaxLine = 64 << 20

// readLiveSpend reads a pi stream log for its finished turns' usage. A missing log is a
// named absence, not zero turns.
func readLiveSpend(path string) liveSpend {
	spend, _ := readLiveRunStream(path)
	return spend
}

// readLiveRunStream reads a pi stream log for its finished turns' usage and any in-run
// loop recoveries. A missing log is a named absence for spend, and nil for recovery.
func readLiveRunStream(path string) (liveSpend, *loopRecoveryView) {
	if path == "" {
		return liveSpend{Note: "no log path for this run"}, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return liveSpend{Note: "the run's log is not there yet"}, nil
	}
	if err != nil {
		return liveSpend{Note: "the run's log is unreadable: " + err.Error()}, nil
	}
	defer f.Close()
	return sumLiveRunStream(f)
}

func sumLiveSpend(r io.Reader) liveSpend {
	spend, _ := sumLiveRunStream(r)
	return spend
}

func sumLiveRunStream(r io.Reader) (liveSpend, *loopRecoveryView) {
	var (
		turns    int
		tokens   int64
		cost     float64
		sawTok   bool
		sawUSD   bool
		recovery *loopRecoveryView
	)
	reader := bufio.NewReaderSize(r, 1<<20)
	turnMarker := []byte(`"type":"turn_end"`)
	loopMarker := []byte(`"loop_detected"`)
	for {
		line, err := readBoundedLine(reader, liveSpendMaxLine)
		if len(line) > 0 {
			if bytes.Contains(line, turnMarker) {
				var e struct {
					Type    string `json:"type"`
					Message struct {
						Usage *struct {
							TotalTokens *int64 `json:"totalTokens"`
							Cost        *struct {
								Total *float64 `json:"total"`
							} `json:"cost"`
						} `json:"usage"`
					} `json:"message"`
				}
				if json.Unmarshal(line, &e) == nil && e.Type == "turn_end" {
					turns++
					if u := e.Message.Usage; u != nil {
						if u.TotalTokens != nil {
							tokens, sawTok = tokens+*u.TotalTokens, true
						}
						if u.Cost != nil && u.Cost.Total != nil {
							cost, sawUSD = cost+*u.Cost.Total, true
						}
					}
				}
			}
			if bytes.Contains(line, loopMarker) {
				var e struct {
					Type         string `json:"type"`
					Action       string `json:"action"`
					Shape        string `json:"shape"`
					TokensBefore int64  `json:"tokensBefore"`
					TokensAfter  int64  `json:"tokensAfter"`
				}
				if json.Unmarshal(line, &e) == nil && e.Type == "loop_detected" && e.Action == "recovered" {
					if recovery == nil {
						recovery = &loopRecoveryView{}
					}
					recovery.Count++
					if e.Shape != "" {
						recovery.Shape = e.Shape
					}
					recovery.TokensBefore = e.TokensBefore
					recovery.TokensAfter = e.TokensAfter
				}
			}
		}
		if err != nil {
			break
		}
	}
	out := liveSpend{Turns: &turns}
	if sawTok {
		out.Tokens = &tokens
	}
	if sawUSD {
		out.CostUSD = &cost
	}
	if !sawUSD {
		out.Note = "no finished turn has reported a cost yet"
	}
	return out, recovery
}

// readBoundedLine answers the next line without its newline, or nil for a line longer
// than limit (which is consumed and skipped). err is io.EOF at the end of the stream.
func readBoundedLine(r *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	over := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !over {
			if len(line)+len(chunk) > limit {
				over, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if over {
			return nil, err
		}
		return bytes.TrimRight(line, "\n"), err
	}
}
