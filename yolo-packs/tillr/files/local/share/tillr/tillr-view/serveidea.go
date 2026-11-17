package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/record"
)

// ideaArgs accepts exactly words and place, refusing duplicate/unknown members,
// nulls, and extra JSON. placementArg owns the destination forms for both routes.
func ideaArgs(raw []byte) (string, string, string, error) {
	if !utf8.Valid(raw) {
		return "", "", "", fmt.Errorf("body must be UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", "", "", fmt.Errorf("expected words and place object")
	}
	seen := map[string]bool{}
	var words string
	var place json.RawMessage
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return "", "", "", err
		}
		key, ok := token.(string)
		if !ok || seen[key] || key != "words" && key != "place" {
			return "", "", "", fmt.Errorf("expected exactly words and place")
		}
		seen[key] = true
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return "", "", "", err
		}
		if key == "words" {
			if bytes.Equal(value, []byte("null")) {
				return "", "", "", fmt.Errorf("words must be a string")
			}
			if err := json.Unmarshal(value, &words); err != nil {
				return "", "", "", err
			}
		} else {
			place = value
		}
	}
	if _, err := d.Token(); err != nil {
		return "", "", "", err
	}
	if _, err := d.Token(); err != io.EOF || len(seen) != 2 {
		return "", "", "", fmt.Errorf("expected only words and place")
	}
	flag, value, err := placementArg(place)
	return words, flag, value, err
}

func (s *server) ideaHandler(w http.ResponseWriter, req *http.Request) {
	raw, ok := s.readWriteRequest(w, req)
	if !ok {
		return
	}
	words, flag, value, err := ideaArgs(raw)
	if err != nil {
		s.writeFailure(w, codeBadRequest, err.Error())
		return
	}
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
	cmd := exec.CommandContext(req.Context(), program, "idea", "--store", s.storePath, "--root", s.root, flag, value, "--by", "owner", "--via", "observatory")
	cmd.Stdin = bytes.NewBufferString(words)
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	out, runErr := cmd.Output()
	env, decodeErr := record.Decode(out)
	if decodeErr == nil && env.Kind == "item.rejection" {
		var refusal struct {
			Rule   string `json:"rule"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(env.Payload, &refusal) == nil {
			switch refusal.Rule {
			case codeUnknownItem, codeNotInOrder, codeStoreBusy, codeWordsEmpty, codeWordsNotText, codeWordsTooLarge, codeIntakeNotConfigured, codeIntakeStoreFailed:
				s.writeFailure(w, refusal.Rule, refusal.Reason)
				return
			}
		}
	}
	if runErr != nil || decodeErr != nil || env.Kind != "item.idea" {
		s.writeFailure(w, codeWriterUnavailable, fmt.Sprintf("tillr-item could not save idea: %v; %s", runErr, diagnostic.String()))
		return
	}
	var result struct {
		ItemID       string `json:"item_id"`
		Place        int    `json:"place"`
		Count        int    `json:"count"`
		EventSeq     int64  `json:"event_seq"`
		EventHeadSeq int64  `json:"event_head_seq"`
	}
	if json.Unmarshal(env.Payload, &result) != nil || result.ItemID == "" || result.Place < 0 || result.Count < result.Place {
		s.writeFailure(w, codeWriterUnavailable, "tillr-item returned an invalid idea result.")
		return
	}
	cursor := s.writeSnapshot()
	cursor.EventHeadSeq = result.EventHeadSeq
	writeJSON(w, http.StatusOK, apiResponse{Snapshot: cursor, Data: result})
}
