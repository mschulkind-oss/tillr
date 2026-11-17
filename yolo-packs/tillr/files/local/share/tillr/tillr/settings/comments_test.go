package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentsSettings(t *testing.T) {
	for _, tc := range []struct{ body, refusal string }{
		{"[comments]\n", "comments.vantage_url"},
		{"[comments]\nvantage_url='http://localhost:8001'\n", "comments.paths"},
		{"[comments]\nvantage_url='http://localhost:8001'\npaths=['docs/design/']\nquiet_seconds=-1\n", "comments.quiet_seconds"},
		{"[comments]\nvantage_url='http://localhost:8001'\npaths=['../']\n", "comments.paths"},
		{"[comments]\nvantage_url='http://localhost:8001'\npaths=['docs/design/']\n", ""},
	} {
		t.Run(tc.body, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			s, err := Read(root)
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("got %v, want %s", err, tc.refusal)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c := s.Comments
			if c == nil || c.QuietSeconds != 60 || c.PollSeconds != 15 || c.MaxRunning != 3 || c.CompactOverTokens != 40000 || c.RefreshHours != 0 {
				t.Fatalf("defaults: %+v", c)
			}
		})
	}
	root := t.TempDir()
	body := "[comments]\nvantage_url='http://localhost:8001'\nrepo='fixture'\npaths=['docs/design/']\nquiet_seconds=90\npoll_seconds=10\nmax_running=4\ncompact_over_tokens=50000\nrefresh_hours=2\n"
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Comments
	if c.Repo != "fixture" || c.QuietSeconds != 90 || c.PollSeconds != 10 || c.MaxRunning != 4 || c.CompactOverTokens != 50000 || c.RefreshHours != 2 {
		t.Fatalf("explicit settings: %+v", c)
	}
	root = t.TempDir()
	s, err = Read(root)
	if err != nil || s.Comments != nil {
		t.Fatalf("absent table: %+v %v", s, err)
	}
}
