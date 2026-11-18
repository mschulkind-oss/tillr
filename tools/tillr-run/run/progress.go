package run

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// Worker claims are observations of the worker, never criterion verdicts.
type workerRecords struct {
	s         *store.Store
	req       Request
	dir, log  string
	line      int
	logOffset int64
	recorded  bool
	children  map[string]string
	sessions  map[string]*sessionObservation
	lastError string
}

type sessionObservation struct {
	offset      int64
	started     *int64
	lastMessage *int64
	turns       int
}

func (w *workerRecords) sessionFacts(path string) (*sessionObservation, error) {
	facts := w.sessions[path]
	if facts == nil {
		facts = &sessionObservation{}
		w.sessions[path] = facts
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return facts, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err = f.Seek(facts.offset, io.SeekStart); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(f)
	for {
		raw, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		var entry struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Role      string `json:"role"`
				Timestamp int64  `json:"timestamp"`
			} `json:"message"`
		}
		if err = json.Unmarshal(raw, &entry); err != nil {
			return nil, fmt.Errorf("reading child transcript %s: %w", path, err)
		}
		facts.offset += int64(len(raw))
		if entry.Type == "session" {
			if at, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
				n := at.UnixMilli()
				facts.started = &n
			}
		}
		if entry.Type == "message" {
			if at, err := time.Parse(time.RFC3339Nano, entry.Timestamp); err == nil {
				n := at.UnixMilli()
				facts.lastMessage = &n
			}
		}
		if entry.Type == "message" && entry.Message.Role == "assistant" && (facts.started == nil || entry.Message.Timestamp >= *facts.started) {
			facts.turns++
		}
	}
	return facts, nil
}

type childSession struct {
	RunID      string `json:"run_id"`
	ItemID     string `json:"item_id"`
	LaneID     string `json:"lane_id"`
	SessionID  string `json:"session_id"`
	Role       string `json:"role"`
	StartedAt  *int64 `json:"started_at_ms"`
	EndedAt    *int64 `json:"ended_at_ms"`
	Turns      *int   `json:"turn_count"`
	EndClock   string `json:"end_clock"`
	Transcript string `json:"transcript"`
}

func newWorkerRecords(ctx context.Context, s *store.Store, req Request, dir, log string) (*workerRecords, error) {
	w := &workerRecords{s: s, req: req, dir: dir, log: log, children: map[string]string{}, sessions: map[string]*sessionObservation{}}
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT kind,payload FROM events WHERE object_type='run' AND object_id=? AND kind IN ('run.progress','run.child-session','run.children-recorded') ORDER BY seq`, req.RunID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var kind, body string
			if err := rows.Scan(&kind, &body); err != nil {
				return err
			}
			switch kind {
			case "run.progress":
				var p struct {
					Line int `json:"source_line"`
				}
				if err := json.Unmarshal([]byte(body), &p); err != nil {
					return err
				}
				if p.Line > w.line {
					w.line = p.Line
				}
			case "run.children-recorded":
				w.recorded = true
			case "run.child-session":
				var c childSession
				if err := json.Unmarshal([]byte(body), &c); err != nil {
					return err
				}
				w.children[c.Transcript] = body
			}
		}
		return rows.Err()
	})
	return w, err
}

func (w *workerRecords) append(ctx context.Context, kind string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return w.s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: kind, ObjectType: "run", ObjectID: &w.req.RunID, Payload: body, At: time.Now().UTC()})
		return err
	})
}

// Observation is not execution: a broken recording file never ends the worker.
// Persist failures explicitly and deduplicate repeated samples of the same failure.
func (w *workerRecords) sample(ctx context.Context, final bool) error {
	err := w.observe(ctx, final)
	message := ""
	if err != nil {
		message = err.Error()
	}
	if message != w.lastError {
		if appendErr := w.append(ctx, "run.observation-error", map[string]string{"run_id": w.req.RunID, "item_id": w.req.ItemID, "lane_id": w.req.LaneID, "error": message}); appendErr != nil {
			return appendErr
		}
		w.lastError = message
	}
	return nil
}

func (w *workerRecords) observe(ctx context.Context, final bool) error {
	raw, err := os.ReadFile(filepath.Join(w.dir, "progress"))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading worker progress: %w", err)
	}
	lines := strings.Split(string(raw), "\n")
	if !final || len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		if i+1 <= w.line {
			continue
		}
		criterion := strings.TrimSpace(line)
		if criterion != "" {
			if err := w.append(ctx, "run.progress", map[string]any{"run_id": w.req.RunID, "item_id": w.req.ItemID, "lane_id": w.req.LaneID, "criterion_id": criterion, "source_line": i + 1}); err != nil {
				return err
			}
		}
		w.line = i + 1
	}
	marker, err := os.ReadFile(filepath.Join(w.dir, "child-sessions-recorded"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(marker)) != "pi-subagents-v1" {
		return fmt.Errorf("unsupported child-session discovery marker %q", marker)
	}
	if !w.recorded {
		if err := w.append(ctx, "run.children-recorded", map[string]string{"run_id": w.req.RunID, "item_id": w.req.ItemID, "lane_id": w.req.LaneID, "discovery": "pi-subagents-v1"}); err != nil {
			return err
		}
		w.recorded = true
	}
	// The adapter scopes Pi's runtime root to this run. Async status and foreground
	// history name sessionFile explicitly, including nested and workflow children.
	root := filepath.Join(w.dir, "pi-subagents")
	for _, sub := range []string{"async-subagent-runs", "nested-subagent-runs", "async-subagent-results/foreground-history"} {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.Name() != "status.json" && sub != "async-subagent-results/foreground-history" {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".json") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var v any
			if err = json.Unmarshal(raw, &v); err != nil {
				return fmt.Errorf("reading Pi child metadata %s: %w", path, err)
			}
			return w.discover(ctx, v)
		})
		if err != nil {
			return err
		}
	}
	// Foreground/parallel tool updates and results carry the same sessionFile
	// metadata even when no async status file exists. Read complete stream lines only.
	f, err := os.Open(w.log)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Seek(w.logOffset, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(f)
	for {
		raw, err := reader.ReadBytes('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			w.logOffset += int64(len(raw))
			continue
		}
		if err := w.discover(ctx, v); err != nil {
			return err
		}
		w.logOffset += int64(len(raw))
	}
	return nil
}

func (w *workerRecords) discover(ctx context.Context, v any) error {
	switch value := v.(type) {
	case []any:
		for _, v := range value {
			if err := w.discover(ctx, v); err != nil {
				return err
			}
		}
	case map[string]any:
		path, _ := value["sessionFile"].(string)
		if path != "" && filepath.IsAbs(path) {
			c := childSession{RunID: w.req.RunID, ItemID: w.req.ItemID, LaneID: w.req.LaneID, Transcript: path}
			sum := sha256.Sum256([]byte(path))
			c.SessionID = fmt.Sprintf("%s-child-%x", w.req.RunID, sum[:12])
			c.Role, _ = value["agent"].(string)
			if x, ok := value["startedAt"].(float64); ok {
				n := int64(x)
				c.StartedAt = &n
			}
			if x, ok := value["endedAt"].(float64); ok {
				n := int64(x)
				c.EndedAt = &n
				c.EndClock = "observed"
			}
			if x, ok := value["turnCount"].(float64); ok {
				n := int(x)
				c.Turns = &n
			}
			if usage, ok := value["usage"].(map[string]any); ok && c.Turns == nil {
				if x, ok := usage["turns"].(float64); ok {
					n := int(x)
					c.Turns = &n
				}
			}
			facts, err := w.sessionFacts(path)
			if err != nil {
				return err
			}
			if facts.started != nil {
				c.StartedAt = facts.started
			}
			if c.Turns == nil && facts.started != nil {
				n := facts.turns
				c.Turns = &n
			}
			// Pi's per-writer process terminal is a child-specific close observation.
			if terminal, ok := value["processTerminal"].(map[string]any); ok {
				if instances, ok := terminal["instances"].([]any); ok {
					for _, instance := range instances {
						if writer, ok := instance.(map[string]any); ok && writer["kind"] == "pi-writer" {
							if x, ok := writer["closeObservedAt"].(float64); ok {
								n := int64(x)
								c.EndedAt = &n
								c.EndClock = "observed"
							}
						}
					}
				}
			}
			terminal := false
			switch value["status"] {
			case "completed", "complete", "failed", "stopped":
				terminal = true
			}
			if exit, ok := value["exitCode"].(float64); ok && exit >= 0 {
				terminal = true
			}
			if c.EndedAt == nil && terminal && facts.lastMessage != nil {
				// Older foreground history stores only a batch clock. Use this child's
				// last recorded message as a derived end, never the batch completion.
				c.EndedAt = facts.lastMessage
				c.EndClock = "derived"
			}
			// Partial tool results must not erase clocks supplied by live status records.
			if prev := w.children[path]; prev != "" {
				var old childSession
				_ = json.Unmarshal([]byte(prev), &old)
				if c.Role == "" {
					c.Role = old.Role
				}
				if c.StartedAt == nil {
					c.StartedAt = old.StartedAt
				}
				if c.EndedAt == nil || (old.EndClock == "observed" && c.EndClock == "derived") {
					c.EndedAt = old.EndedAt
					c.EndClock = old.EndClock
				}
				if c.Turns == nil {
					c.Turns = old.Turns
				}
			}
			body, _ := json.Marshal(c)
			if w.children[path] != string(body) {
				if err := w.append(ctx, "run.child-session", c); err != nil {
					return err
				}
				w.children[path] = string(body)
			}
		}
		for _, key := range []string{"steps", "children", "results", "details", "result", "partialResult", "message"} {
			if v, ok := value[key]; ok {
				if err := w.discover(ctx, v); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
