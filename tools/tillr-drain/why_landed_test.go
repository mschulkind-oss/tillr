package main_test

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestWhyNamesALandedItemAsDone(t *testing.T) {
	path := fixture(t, itemRecord("landed-item", "landed"))
	code, out := invoke(t, "why", "landed-item", "--store", path, "--json")
	if code != 0 {
		t.Fatalf("why: %d %s", code, out)
	}
	var row drain.WhyRow
	if err := json.Unmarshal(out, &row); err != nil {
		t.Fatal(err)
	}
	if row.ItemID != "landed-item" || row.State != "landed" || row.Class != "done" || row.Mover != "no further work: landed" {
		t.Fatalf("why did not report the item as done: %+v", row)
	}
}
