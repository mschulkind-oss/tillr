package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWaitingSummarySeparatesDocumentAnswersFromOtherNextActors(t *testing.T) {
	rows := []waitingRow{
		{ItemID: "item-question", Class: "needs you", Mover: "owner; answer docs/feature.md#OQ-1"},
		{ItemID: "item-design", Class: "waits on work", Mover: "design"},
		{ItemID: "item-drain", Class: "waits on work", Mover: "the next pass"},
		{ItemID: "item-operator", Class: "needs you", Mover: "owner"},
	}
	for i := range rows {
		normalizeWaitingMover(&rows[i])
	}
	var out bytes.Buffer
	if err := printWaiting(&out, rows); err != nil {
		t.Fatal(err)
	}
	want := "summary: owner-waiting 1; next [drain=1, design seat=1, operator=1]"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("summary absent: %s", out.String())
	}
	for _, row := range rows {
		if row.Mover == "owner" || row.Mover == "the owner" {
			t.Fatalf("owner without a document/question: %+v", row)
		}
	}
	if rows[0].Mover != "owner; answer docs/feature.md#OQ-1" {
		t.Fatalf("answer target lost: %+v", rows[0])
	}
}
