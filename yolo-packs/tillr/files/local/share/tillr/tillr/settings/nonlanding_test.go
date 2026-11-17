package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func TestReadWithoutLanding(t *testing.T) {
	dir := root(t)
	if _, err := settings.ReadWithoutLanding(dir, "findings"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".tillr.toml")
	body := "roll.command = ''\nroll.paths = []\nroll.pause_timeout_seconds = 120\npush.after_landing = false\npush.remote = ''\npush.ref = ''\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := settings.ReadWithoutLanding(dir, "findings")
	if err == nil {
		t.Fatal("explicit landing settings accepted")
	}
	for _, want := range []string{path, "findings", "roll.command", "roll.paths", "roll.pause_timeout_seconds", "push.after_landing", "push.remote", "push.ref"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q: %v", want, err)
		}
	}
	// The very same explicit zero values remain legal under Git landing.
	if _, err := settings.Read(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("prose.rewrite = false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.ReadWithoutLanding(dir, "findings"); err != nil {
		t.Fatal(err)
	}
}
