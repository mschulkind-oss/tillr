package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWaitingCommentIntake(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE comment_intake(id INTEGER PRIMARY KEY,first_intake TEXT,last_answer TEXT,unavailable INTEGER);
 CREATE TABLE comment_intake_documents(document TEXT PRIMARY KEY,older INTEGER);
 INSERT INTO comment_intake VALUES(1,'2026-11-17T00:00:00Z','2026-11-17T00:01:00Z',1);
 INSERT INTO comment_intake_documents VALUES('docs/design/example.md',2);`)
	for i := 0; i < 3; i++ {
		code, _, raw := h.get("/api/v1/waiting")
		if code != 200 {
			t.Fatalf("waiting %d %s", code, raw)
		}
		var envelope struct {
			Data []waitingRow `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		outages, older := 0, 0
		for _, r := range envelope.Data {
			if r.Where == "Vantage" {
				outages++
				if !strings.Contains(r.Why, "2026-11-17T00:01:00Z") {
					t.Fatalf("missing last answer: %+v", r)
				}
			}
			if r.Where == "comments" {
				older++
				if !strings.Contains(r.Why, "2 pending") || !strings.Contains(r.Mover, "--older") {
					t.Fatalf("older row: %+v", r)
				}
			}
		}
		if outages != 1 || older != 1 {
			t.Fatalf("outage %d older %d: %s", outages, older, raw)
		}
	}
	h.exec("UPDATE comment_intake SET unavailable=0")
	code, _, raw := h.get("/api/v1/waiting")
	if code != 200 || strings.Contains(string(raw), "Vantage not answering") {
		t.Fatalf("recovery %d %s", code, raw)
	}
}
