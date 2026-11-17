package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

func (s *server) writeSnapshot() serverSnapshot {
	s.openMu.Lock()
	defer s.openMu.Unlock()
	return serverSnapshot{StorePath: s.storePath, SchemaVersion: s.schema, GeneratedAt: time.Now().UTC(), Build: s.build, VantageBaseURL: s.vantageBase, WritesEnabled: s.writeEnabled}
}
func (s *server) writeFailure(w http.ResponseWriter, code, message string) {
	f := failure(code, "%s", message)
	f.Snapshot = s.writeSnapshot()
	writeJSON(w, f.status, *f)
}

// placementArg accepts exactly one JSON string member. Reading tokens rather than
// decoding a struct also refuses duplicate keys, nulls and trailing JSON values.
func placementArg(raw []byte) (string, string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", "", fmt.Errorf("expected one placement object")
	}
	if !d.More() {
		return "", "", fmt.Errorf("expected one destination")
	}
	token, err = d.Token()
	if err != nil {
		return "", "", err
	}
	key, ok := token.(string)
	if !ok {
		return "", "", fmt.Errorf("expected destination name")
	}
	var value string
	if err = d.Decode(&value); err != nil {
		return "", "", err
	}
	if value == "" || d.More() {
		return "", "", fmt.Errorf("expected exactly one nonempty destination")
	}
	if _, err = d.Token(); err != nil {
		return "", "", err
	}
	if _, err = d.Token(); err != io.EOF {
		return "", "", fmt.Errorf("trailing JSON")
	}
	if key != "to" && key != "before" && key != "after" || key == "to" && value != "first" && value != "last" {
		return "", "", fmt.Errorf("use to:first, to:last, before:<item>, or after:<item>")
	}
	return "--" + key, value, nil
}
func resolveItemWriter(explicit string) (string, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err == nil {
			err = executableFile(path)
		}
		return path, err
	}
	exe, _ := os.Executable()
	for _, path := range siblingCandidates(exe, "tillr-item") {
		if executableFile(path) == nil {
			return path, nil
		}
	}
	return exec.LookPath("tillr-item")
}

// readWriteRequest is shared by both owner-write routes. The outer routes guard
// enforces read-only first; keep it here too for direct handler callers.
func (s *server) readWriteRequest(w http.ResponseWriter, req *http.Request) ([]byte, bool) {
	if !s.writeEnabled {
		s.writeFailure(w, codeReadOnly, "This server is read-only; restart with --write to save ideas or move items.")
		return nil, false
	}
	if !wsOriginAllowed(req) {
		s.writeFailure(w, codeOriginRefused, "The request's Origin is not allowed.")
		return nil, false
	}
	typ, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || typ != "application/json" {
		s.writeFailure(w, codeContentType, "Declare the body as application/json.")
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, (1<<20)+1))
	if err != nil {
		s.writeFailure(w, codeBadRequest, err.Error())
		return nil, false
	}
	if len(raw) > 1<<20 {
		s.writeFailure(w, codeTooLarge, "The body exceeds 1 MiB.")
		return nil, false
	}
	return raw, true
}

func (s *server) placeHandler(w http.ResponseWriter, req *http.Request) {
	raw, ok := s.readWriteRequest(w, req)
	if !ok {
		return
	}
	flag, value, err := placementArg(raw)
	if err != nil {
		s.writeFailure(w, codeBadRequest, err.Error())
		return
	}
	// Only the child opens a writer; serialize requests without a server transaction
	// spanning the process boundary. Waiting callers can still cancel their requests.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if req.Context().Err() != nil {
		return
	}
	program, err := resolveItemWriter(s.itemFlag)
	if err != nil {
		s.writeFailure(w, codeWriterUnavailable, "Cannot start tillr-item: "+err.Error()+"; build it beside the server or pass --tillr-item.")
		return
	}
	cmd := exec.CommandContext(req.Context(), program, "place", "--store", s.storePath, "--item", req.PathValue("id"), flag, value, "--by", "owner", "--via", "observatory")
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	out, runErr := cmd.Output()
	env, decodeErr := record.Decode(out)
	if decodeErr == nil && env.Kind == "item.rejection" {
		var refusal struct {
			Rule   string `json:"rule"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(env.Payload, &refusal); err == nil {
			switch refusal.Rule {
			case codeUnknownItem, codeNotInOrder, codeStoreBusy:
				s.writeFailure(w, refusal.Rule, refusal.Reason)
				return
			}
		}
	}
	if runErr != nil || decodeErr != nil || env.Kind != "item.place" {
		s.writeFailure(w, codeWriterUnavailable, fmt.Sprintf("tillr-item could not answer placement: %v; %s", runErr, diagnostic.String()))
		return
	}
	var result struct {
		ItemID       string `json:"item_id"`
		Place        int    `json:"place"`
		Count        int    `json:"count"`
		EventSeq     int64  `json:"event_seq"`
		EventHeadSeq int64  `json:"event_head_seq"`
	}
	if err := json.Unmarshal(env.Payload, &result); err != nil || result.ItemID != req.PathValue("id") || result.Place < 1 || result.Count < result.Place {
		s.writeFailure(w, codeWriterUnavailable, "tillr-item returned an invalid placement result.")
		return
	}
	cursor := s.writeSnapshot()
	cursor.EventHeadSeq = result.EventHeadSeq
	writeJSON(w, http.StatusOK, apiResponse{Snapshot: cursor, Data: result})
}
