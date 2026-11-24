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
	s             *store.Store
	req           Request
	dir, log      string
	line          int
	logOffset     int64
	timeoutOffset int64
	modelTimeouts int
	recorded      bool
	children      map[string]string
	merged        map[string]childSession
	childOrder    []string
	sessions      map[string]*sessionObservation
	lastError     string
}

type sessionObservation struct {
	offset      int64
	exists      bool
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
	facts.exists = true
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
	RunID         string `json:"run_id"`
	ItemID        string `json:"item_id,omitempty"`
	LaneID        string `json:"lane_id,omitempty"`
	SessionID     string `json:"session_id"`
	Role          string `json:"role,omitempty"`
	StartedAt     *int64 `json:"started_at_ms,omitempty"`
	EndedAt       *int64 `json:"ended_at_ms,omitempty"`
	Turns         *int   `json:"turn_count,omitempty"`
	EndClock      string `json:"end_clock,omitempty"`
	Transcript    string `json:"transcript"`
	closeObserved bool
}

// mergeChild joins two observations of one child transcript: the earliest start, the
// largest turn count, a child-specific close observation over a wrapper end clock and an
// observed end over a derived one (taking the later of two ends of the same rank), and the
// role first recorded. Every rule is commutative and idempotent, so the merge of a fixed
// set of records is the same whatever order they are read in, and merging one of them
// again changes nothing. Partial tool results never erase a clock a live status record
// supplied.
func mergeChild(old, c childSession) childSession {
	if old.Role != "" {
		c.Role = old.Role
	}
	if c.StartedAt == nil || (old.StartedAt != nil && *old.StartedAt < *c.StartedAt) {
		c.StartedAt = old.StartedAt
	}
	if c.Turns == nil || (old.Turns != nil && *old.Turns > *c.Turns) {
		c.Turns = old.Turns
	}
	rank := func(s childSession) int {
		switch s.EndClock {
		case "observed":
			if s.closeObserved {
				return 3
			}
			return 2
		case "derived":
			return 1
		}
		return 0
	}
	switch {
	case old.EndedAt == nil:
	case c.EndedAt == nil,
		rank(old) > rank(c),
		rank(old) == rank(c) && *old.EndedAt > *c.EndedAt:
		c.EndedAt, c.EndClock, c.closeObserved = old.EndedAt, old.EndClock, old.closeObserved
	}
	return c
}

func newWorkerRecords(ctx context.Context, s *store.Store, req Request, dir, log string) (*workerRecords, error) {
	w := &workerRecords{
		s:        s,
		req:      req,
		dir:      dir,
		log:      log,
		children: map[string]string{},
		merged:   map[string]childSession{},
		sessions: map[string]*sessionObservation{},
	}
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT kind,payload FROM events WHERE object_type='run' AND object_id=? AND kind IN ('run.progress','run.child-session','run.children-recorded','run.model-timeout') ORDER BY seq`, req.RunID)
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
				c.closeObserved = c.EndClock == "observed"
				if _, seen := w.merged[c.Transcript]; !seen {
					w.childOrder = append(w.childOrder, c.Transcript)
				}
				w.merged[c.Transcript] = c
				norm, err := json.Marshal(c)
				if err != nil {
					return err
				}
				w.children[c.Transcript] = string(norm)
			case ModelTimeoutKind:
				var mt struct {
					Count int `json:"count"`
				}
				if err := json.Unmarshal([]byte(body), &mt); err != nil {
					return err
				}
				if mt.Count > w.modelTimeouts {
					w.modelTimeouts = mt.Count
				}
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
	if err := w.observeModelTimeouts(ctx); err != nil {
		return err
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
			return w.collect(v, final)
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
		if err := w.collect(v, final); err != nil {
			return err
		}
		w.logOffset += int64(len(raw))
	}
	return w.flushChildren(ctx)
}

func (w *workerRecords) discover(ctx context.Context, v any) error {
	if err := w.collect(v, false); err != nil {
		return err
	}
	return w.flushChildren(ctx)
}

func (w *workerRecords) collect(v any, final bool) error {
	switch value := v.(type) {
	case []any:
		for _, v := range value {
			if err := w.collect(v, final); err != nil {
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
			// Pi's per-writer process terminal is a child-specific close observation.
			if terminal, ok := value["processTerminal"].(map[string]any); ok {
				if instances, ok := terminal["instances"].([]any); ok {
					for _, instance := range instances {
						if writer, ok := instance.(map[string]any); ok && writer["kind"] == "pi-writer" {
							if x, ok := writer["closeObservedAt"].(float64); ok {
								n := int64(x)
								c.EndedAt = &n
								c.EndClock = "observed"
								c.closeObserved = true
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
			ended := terminal || c.EndedAt != nil || final
			old, seen := w.merged[path]
			var facts *sessionObservation
			if !seen || (c.StartedAt == nil && old.StartedAt == nil) || ended {
				var err error
				facts, err = w.sessionFacts(path)
				if err != nil {
					return err
				}
				if facts.started != nil {
					c.StartedAt = facts.started
				}
			}
			// Record turn_count once the child (or parent run) ends rather than appending
			// a store event on every live assistant turn; active readers inspect the live
			// transcript file directly until the terminal child record is written.
			if ended {
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
				if facts != nil && facts.started != nil && (c.Turns == nil || facts.turns > *c.Turns) {
					n := facts.turns
					c.Turns = &n
				}
				if c.EndedAt == nil && terminal && facts != nil && facts.lastMessage != nil {
					// Older foreground history stores only a batch clock. Use this child's
					// last recorded message as a derived end, never the batch completion.
					c.EndedAt = facts.lastMessage
					c.EndClock = "derived"
				}
			}
			// Skip unstarted pending steps that have neither a start clock nor a transcript file yet.
			if seen || ended || c.StartedAt != nil || (facts != nil && facts.exists) {
				if seen {
					c = mergeChild(old, c)
				} else {
					w.childOrder = append(w.childOrder, path)
				}
				w.merged[path] = c
			}
		}
		for _, key := range []string{"steps", "children", "results", "details", "result", "partialResult", "message"} {
			if v, ok := value[key]; ok {
				if err := w.collect(v, final); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (w *workerRecords) flushChildren(ctx context.Context) error {
	type pendingWrite struct {
		path string
		body []byte
	}
	var pending []pendingWrite
	for _, path := range w.childOrder {
		c := w.merged[path]
		body, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if w.children[path] != string(body) {
			pending = append(pending, pendingWrite{path: path, body: body})
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if err := w.s.WriteTx(ctx, func(tx *store.Tx) error {
		now := time.Now().UTC()
		for _, item := range pending {
			if _, err := tx.Append(event.Event{Kind: "run.child-session", ObjectType: "run", ObjectID: &w.req.RunID, Payload: item.body, At: now}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, item := range pending {
		w.children[item.path] = string(item.body)
	}
	return nil
}

func isModelTimeoutError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "request timed out") ||
		strings.Contains(lower, "request timeout") ||
		strings.Contains(lower, "body timeout") ||
		strings.Contains(lower, "und_err_body_timeout")
}

// observeModelTimeouts scans complete JSONL stream lines appended to w.log since
// w.timeoutOffset and records one run.model-timeout event per assistant message_end
// whose error is a request timeout or body timeout.
func (w *workerRecords) observeModelTimeouts(ctx context.Context) error {
	if w.log == "" {
		return nil
	}
	f, err := os.Open(w.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Seek(w.timeoutOffset, io.SeekStart); err != nil {
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
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role         string `json:"role"`
				StopReason   string `json:"stopReason"`
				ErrorMessage string `json:"errorMessage"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &entry) == nil &&
			entry.Type == "message_end" &&
			entry.Message.Role == "assistant" &&
			entry.Message.StopReason == "error" &&
			isModelTimeoutError(entry.Message.ErrorMessage) {
			count := w.modelTimeouts + 1
			payload := map[string]any{
				"run_id": w.req.RunID,
				"error":  strings.TrimSpace(entry.Message.ErrorMessage),
				"count":  count,
			}
			if w.req.ItemID != "" {
				payload["item_id"] = w.req.ItemID
			}
			if w.req.LaneID != "" {
				payload["lane_id"] = w.req.LaneID
			}
			if w.req.Backend != "" {
				payload["backend"] = w.req.Backend
			}
			if w.req.Node != "" {
				payload["node"] = w.req.Node
			}
			if err := w.append(ctx, ModelTimeoutKind, payload); err != nil {
				return err
			}
			w.modelTimeouts = count
		}
		w.timeoutOffset += int64(len(raw))
	}
	return nil
}
