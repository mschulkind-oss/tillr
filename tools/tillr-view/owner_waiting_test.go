package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPublishedQuestionWaitingUsesConflictRecord(t *testing.T) {
	const ref = "docs/feature.md#OQ-1"
	c := waitingConflict{Status: "question", QuestionRef: ref}
	var row waitingRow
	decorateOwnerQuestionWaiting(&row, c, nil)
	if row.Class != "needs you" || row.Mover != "owner; answer "+ref || !strings.HasPrefix(row.Why, "question-open:") {
		t.Fatalf("open conflict with no index: %+v", row)
	}
	c.QuestionRemovedHead = "trunk-commit"
	decorateOwnerQuestionWaiting(&row, c, nil)
	if row.Class != "needs you" || row.Mover != "operator; restore "+ref+" under the item's block and land it" || row.Why != "question-removed: "+ref+": the question was removed without a ruling" {
		t.Fatalf("removed conflict with no index: %+v", row)
	}
}

func TestWaitingSummarySeparatesDocumentAnswersFromOtherNextActors(t *testing.T) {
	rows := []waitingRow{
		{ItemID: "item-question", Class: "needs you", Mover: "owner; answer docs/feature.md#OQ-1"},
		{ItemID: "item-design", Class: "waits on work", Mover: "design"},
		{ItemID: "item-drain", Class: "waits on work", Mover: "the next pass"},
		{ItemID: "item-operator", Class: "needs you", Mover: "owner"},
		{ItemID: "item-removed", Class: "needs you", Why: "question-removed", Mover: "operator; restore docs/feature.md#OQ-2 under the item's block and land it"},
	}
	for i := range rows {
		normalizeWaitingMover(&rows[i])
	}
	var out bytes.Buffer
	if err := printWaiting(&out, rows); err != nil {
		t.Fatal(err)
	}
	want := "summary: owner-waiting 1; next [drain=1, design seat=1, operator=2]"
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
