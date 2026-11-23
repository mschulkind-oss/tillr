package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
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
	return plannerWhyAt(ctx, program, self, path, root, policy, lanes, "", questionsCmd, index)
}

func plannerWhyAt(ctx context.Context, program plannerProgram, self buildStamp, path, root, policy, lanes, now string, questionsCmd []string, index []byte) (json.RawMessage, []waitingRow, error) {
	args := []string{"why", "--store", path, "--root", root, "--json"}
	if now != "" {
		args = append(args, "--now", now)
	}
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
			return addPreservedHistory(r, answer.Rows)
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
	// Document warnings also appear when the planner has no items to answer.
	// Preserve every planner-only field while extending its display rows.
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	defer s.Close()
	scope, err := designRoadmapScope(root)
	if err != nil {
		return nil, nil, err
	}
	var designs []waitingRow
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		designs, err = waitingDesignWithoutItems(r, scope)
		return err
	}); err != nil {
		return nil, nil, err
	}
	if len(designs) > 0 {
		var document map[string]json.RawMessage
		if err := json.Unmarshal(raw, &document); err != nil {
			return nil, nil, err
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(document["rows"], &rows); err != nil {
			return nil, nil, err
		}
		for _, design := range designs {
			encoded, err := json.Marshal(design)
			if err != nil {
				return nil, nil, err
			}
			rows = append(rows, encoded)
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
		answer.Rows = append(answer.Rows, designs...)
	}
	return json.RawMessage(raw), answer.Rows, nil
}

// askPlanner resolves the planner and asks it, so every caller gets the same refusal for a
// planner that cannot be found as for one that cannot answer.
func askPlanner(ctx context.Context, explicit string, self buildStamp, path, root, policy, lanes string, questionsCmd []string, index []byte) (json.RawMessage, []waitingRow, error) {
	return askPlannerAt(ctx, explicit, self, path, root, policy, lanes, "", questionsCmd, index)
}

func askPlannerAt(ctx context.Context, explicit string, self buildStamp, path, root, policy, lanes, now string, questionsCmd []string, index []byte) (json.RawMessage, []waitingRow, error) {
	program, err := resolvePlanner(explicit)
	if err != nil {
		return nil, nil, err
	}
	return plannerWhyAt(ctx, program, self, path, root, policy, lanes, now, questionsCmd, index)
}

type whyPayloadData struct {
	raw  json.RawMessage
	head int64
}

// whyCache holds the planner's last successful answer and what it was made from. The
// planner re-folds the store's whole event log on every run (about ten seconds on the live
// store, measured 2026-11-22), so a repeat request against an unchanged store reuses it.
// The key is the event head read before the planner ran and a hash of the index handed to
// it: an event written while the planner ran leaves the key behind the answer, so the next
// request asks again rather than serving an older one. A failed answer is never kept.
type whyCache struct {
	mu    sync.Mutex
	valid bool
	head  int64
	index [sha256.Size]byte
	raw   json.RawMessage
}

// documentIndexStatus is what /why says about the Vantage index its questions came from, so
// the page can show that the answer used an index older than the documents.
type documentIndexStatus struct {
	// State is "current", "refreshing" (an older index was used while a rebuild runs),
	// "failed" (an older index was used and the last rebuild failed; none is running) or
	// "unavailable" (no index could be built).
	State        string  `json:"state"`
	AgeMS        float64 `json:"age_ms,omitempty"`
	RefreshError string  `json:"refresh_error,omitempty"`
	Message      string  `json:"message,omitempty"`
}

func indexStatus(look vantageLookup, err error) documentIndexStatus {
	switch {
	case err != nil:
		return documentIndexStatus{State: "unavailable", Message: fmt.Sprintf("The document index could not be built: %v", err)}
	case look.Stale && !look.Refreshing && look.RefreshError != "":
		return documentIndexStatus{State: "failed", AgeMS: look.AgeMS, RefreshError: look.RefreshError,
			Message: fmt.Sprintf("The last document index rebuild failed (%s); this answer uses the index built %s ago. It is rebuilt when a document changes.", look.RefreshError, ageWords(look.AgeMS))}
	case look.Stale:
		msg := fmt.Sprintf("The document index is being refreshed; this answer uses one built %s ago.", ageWords(look.AgeMS))
		if look.RefreshError != "" {
			msg = fmt.Sprintf("The document index is being refreshed after a failed rebuild (%s); this answer uses one built %s ago.", look.RefreshError, ageWords(look.AgeMS))
		}
		return documentIndexStatus{State: "refreshing", AgeMS: look.AgeMS, RefreshError: look.RefreshError, Message: msg}
	}
	return documentIndexStatus{State: "current", AgeMS: look.AgeMS}
}

func ageWords(ms float64) string {
	d := time.Duration(ms * float64(time.Millisecond)).Round(time.Second)
	return d.String()
}

// withDocumentIndex adds the index's status to the planner's answer, keeping every planner
// field and exact number as it came.
func withDocumentIndex(raw json.RawMessage, status documentIndexStatus) (json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	document["document_index"] = encoded
	return json.Marshal(document)
}

func (s *server) whyPayload(_ *store.Reader, r *http.Request) (any, *apiFailure) {
	// The loop's last recorded pass answers when a loop is running and has recorded one
	// (why_pass.go). The planner below is the fallback, and its answer says so.
	fallback := ""
	if raw, head, err := s.whyFromPass(r.Context(), s.clock().UTC()); err == nil {
		phase(r.Context(), "planner", map[string]any{"source": "pass", "ms": 0, "bytes": len(raw)})
		return whyPayloadData{raw: raw, head: head}, nil
	} else {
		var why passFallback
		if !errors.As(err, &why) {
			return nil, failure(codeStoreUnreadable, "why from the last pass: %v", err)
		}
		fallback = why.reason
	}
	// The index is the server's cached walk. When it cannot be read the planner is asked
	// without it and runs the index command itself, so a failure is the planner's own
	// answer (`questions-unknown`) exactly as before the cache existed.
	index, look, ierr := s.vantage.get(r.Context())
	phase(r.Context(), "vantage", look)
	if ierr != nil {
		index = nil
	}
	status := indexStatus(look, ierr)
	// Without the server's index the planner's inputs are not known, so nothing is reused.
	cacheable := index != nil
	var head int64
	var digest [sha256.Size]byte
	if cacheable {
		digest = sha256.Sum256(index)
		if err := s.store.ReadTx(r.Context(), func(rd *store.Reader) error {
			var err error
			head, err = rd.EventHeadSeq()
			return err
		}); err != nil {
			cacheable = false
		}
	}
	// One planner run at a time: a request that arrives during a run waits for it and
	// takes its answer instead of starting a second ten-second fold.
	s.why.mu.Lock()
	defer s.why.mu.Unlock()
	if cacheable && s.why.valid && s.why.head == head && s.why.index == digest {
		phase(r.Context(), "planner", map[string]any{"cache": "hit", "ms": 0, "bytes": len(s.why.raw)})
		raw, err := withDocumentIndex(s.why.raw, status)
		if err == nil {
			raw, err = withWhySource(raw, fallback)
		}
		if err != nil {
			return nil, failure(codeStoreUnreadable, "why index status: %v", err)
		}
		return whyPayloadData{raw: raw, head: whyHead(s.why.raw)}, nil
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
	if cacheable {
		s.why.valid, s.why.head, s.why.index, s.why.raw = true, head, digest, raw
	} else {
		s.why.valid = false
	}
	raw, err = withDocumentIndex(raw, status)
	if err == nil {
		raw, err = withWhySource(raw, fallback)
	}
	if err != nil {
		return nil, failure(codeStoreUnreadable, "why index status: %v", err)
	}
	return whyPayloadData{raw: raw, head: answer.Head}, nil
}

func whyHead(raw json.RawMessage) int64 {
	var answer struct {
		Head int64 `json:"event_head_seq"`
	}
	_ = json.Unmarshal(raw, &answer)
	return answer.Head
}
