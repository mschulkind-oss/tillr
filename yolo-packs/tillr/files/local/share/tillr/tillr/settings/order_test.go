package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOrderSettings(t *testing.T) {
	root := t.TempDir()
	s, err := Read(root)
	if err != nil || s.Order.Record != "" || s.Order.SettleMinutes != 10 {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{"[order]\nrecord='docs/order.txt'\nsettle_minutes=0", true},
		{"[order]\nrecord='../escape'", false},
		{"[order]\nrecord='/absolute'", false},
		{"[order]\nrecord='docs/../order.txt'", false},
		{"[order]\nrecord=42", false},
		{"[order]\nsettle_minutes=-1", false},
		{"[order]\nsettle_minutes='10'", false},
		{"[order]\nunknown=true", false},
	} {
		if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tc.raw), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := Read(root)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if tc.valid && (s.Order.Record != "docs/order.txt" || s.Order.SettleMinutes != 0) {
			t.Fatal(s.Order)
		}
	}
}
