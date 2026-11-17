package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProseRewriteSettings(t *testing.T) {
	for _, tc := range []struct {
		body    string
		want    bool
		refusal string
	}{
		{"", false, ""},
		{"[prose]\nrewrite = false\n", false, ""},
		{"[prose]\nrewrite = true\n", true, ""},
		{"[prose]\nrewrite = 'true'\n", false, "prose.rewrite"},
		{"[prose]\nother = true\n", false, "prose.other"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			root := t.TempDir()
			if tc.body != "" {
				if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tc.body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Read(root)
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("expected refusal %s: %v", tc.refusal, err)
				}
				return
			}
			if err != nil || got.Prose.Rewrite != tc.want {
				t.Fatalf("settings=%+v err=%v", got, err)
			}
			flag := "false"
			if tc.want {
				flag = "true"
			}
			if !strings.Contains(got.StartupLine(), "prose.rewrite="+flag) {
				t.Fatalf("rewrite setting invisible: %s", got.StartupLine())
			}
		})
	}
}
