package main_test

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"
)

func TestApplyReplaysMalformedKnownFields(t *testing.T) {
	db := filepath.Join(scratch(t, "replay-validation"), "state.db")
	original := withKey(t, envelope(t, "item.record", "rec-original", base("existing", "original")), "same-key")
	code, first := apply(t, db, original)
	if code != 0 {
		t.Fatalf("initial apply: %d %s", code, first)
	}
	before := applyStoreSnapshot(t, db)
	malformed := envelope(t, "item.record", "rec-malformed", map[string]any{"item_id": "existing", "title": 123})
	code, replayed := apply(t, db, withKey(t, malformed, "same-key"))
	if code != 0 || !bytes.Equal(first, replayed) {
		t.Fatalf("known-field type error must still replay the original: %d %s", code, replayed)
	}
	if after := applyStoreSnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("replay changed store: before %v, after %v", before, after)
	}
	// A replay miss still validates the known fields, with the existing usage exit.
	if code, out := apply(t, db, withKey(t, malformed, "new-key")); code != 2 {
		t.Fatalf("malformed known field on replay miss: %d %s", code, out)
	}
	if after := applyStoreSnapshot(t, db); !reflect.DeepEqual(before, after) {
		t.Fatalf("malformed request changed store: before %v, after %v", before, after)
	}
}
