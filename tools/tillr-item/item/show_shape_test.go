package item_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

func TestShowWithQuestionsPreservesStoredPlaceWithoutRank(t *testing.T) {
	s := syncStore(t)
	exec(t, s, `INSERT INTO items(item_id,title,state,rank,design_doc,updated_at) VALUES ('item-show-shape','Shape','in-queue','k','docs/shape.md',0)`)
	root := scratch(t)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs/shape.md"), []byte("# Shape\n"), 0644); err != nil {
		t.Fatal(err)
	}
	source := vantage.Preloaded(root, vantage.ParseIndex([]byte(`{"index":{"documents":[{"path":"docs/shape.md","questions":[{"id":"OQ-S1","state":"open","title":"Still open"}]}]}}`), "fixture"))
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		stored, err := item.ReadStored(r, "item-show-shape", root)
		if err != nil {
			return err
		}
		shown, err := item.ShowWithQuestions(r, "item-show-shape", root, source)
		if err != nil {
			return err
		}
		var before, after map[string]json.RawMessage
		if err := json.Unmarshal(stored.Payload, &before); err != nil {
			return err
		}
		if err := json.Unmarshal(shown.Payload, &after); err != nil {
			return err
		}
		if _, ok := after["rank"]; ok {
			t.Error("question annotation reintroduced the private rank")
		}
		if string(before["place"]) != "1" || string(after["place"]) != string(before["place"]) {
			t.Errorf("place changed: before %s, after %s", before["place"], after["place"])
		}
		if string(after["questions_status"]) != `"known"` || string(after["questions_source"]) != `"docs/shape.md"` {
			t.Errorf("question metadata missing: %s", shown.Payload)
		}
		var questions []item.Question
		if err := json.Unmarshal(after["questions"], &questions); err != nil {
			return err
		}
		if len(questions) != 1 || questions[0].QuestionID != "OQ-S1" {
			t.Errorf("questions %v", questions)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
