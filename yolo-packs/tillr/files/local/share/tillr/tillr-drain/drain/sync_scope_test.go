package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSyncForwardsRoadmapsInOrderEveryTime(t *testing.T) {
	for _, roadmaps := range [][]string{nil, {}, {"docs/b/roadmap.md", "docs/a/roadmap.md"}} {
		t.Run(strings.Join(roadmaps, ","), func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "args")
			t.Setenv("SYNC_ARGS", log)
			child := filepath.Join(dir, "item")
			body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SYNC_ARGS\"\nprintf '{\"kind\":\"item.sync\",\"payload\":{}}\\n'\n"
			if err := os.WriteFile(child, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			steps := &ProcessSteps{Root: dir, Store: filepath.Join(dir, "fixture.db"), ItemBin: child, SyncRoadmaps: roadmaps}
			for call := 0; call < 2; call++ {
				if _, err := steps.Sync(context.Background()); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
				var got []string
				for i := 0; i < len(args); i++ {
					if args[i] == "--roadmap" {
						i++
						if i == len(args) {
							t.Fatal("roadmap flag has no value")
						}
						got = append(got, args[i])
					}
				}
				if len(roadmaps) == 0 {
					if len(got) != 0 {
						t.Fatalf("empty scope forwarded flags: %v", args)
					}
				} else if !reflect.DeepEqual(got, roadmaps) {
					t.Fatalf("call %d roadmaps = %v, want %v", call, got, roadmaps)
				}
			}
		})
	}
}
