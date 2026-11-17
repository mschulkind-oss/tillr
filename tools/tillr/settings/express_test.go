package settings_test

import (
	"github.com/mschulkind-oss/tillr/settings"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpressLaneSettings(t *testing.T) {
	for _, value := range []string{"0", "2", "-1", "true", "1.5", `"2"`} {
		t.Run(value, func(t *testing.T) {
			dir := root(t)
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[lanes]\nexpress = "+value+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			s, err := settings.Read(dir)
			valid := value == "0" || value == "2"
			if valid && err != nil {
				t.Fatal(err)
			}
			if !valid && err == nil {
				t.Fatal("invalid lane count accepted")
			}
			if valid && !strings.Contains(s.StartupLine(), "lanes.express="+value) {
				t.Fatal(s.StartupLine())
			}
		})
	}
}
