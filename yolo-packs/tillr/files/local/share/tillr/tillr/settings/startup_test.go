package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func TestPartialSettingsKeepDefaultsAndStartupIsOneLine(t *testing.T) {
	dir := root(t)
	body := "[roll]\npaths = [\"one\\ntwo\"]\ncommand = \"echo \\\"ok\\\"\\nnext\"\n[push]\nremote = \"ori\\ngin\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Roll.PauseTimeoutSeconds != 120 || got.Push.What != "none" || got.Push.Ref != "" {
		t.Fatalf("partial file lost defaults: %+v", got)
	}
	line := got.StartupLine()
	if strings.ContainsAny(line, "\n\r") {
		t.Fatalf("startup is not one line: %q", line)
	}
	for _, expected := range []string{`roll.command="echo \"ok\"\nnext"`, `roll.paths=["one\ntwo"]`, "roll.pause_timeout_seconds=120", "push.what=none", `push.remote="ori\ngin"`, "push.ref=upstream"} {
		if !strings.Contains(line, expected) {
			t.Errorf("line %q lacks %q", line, expected)
		}
	}
}
