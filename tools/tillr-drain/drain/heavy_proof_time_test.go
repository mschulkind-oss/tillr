package drain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
)

func TestHeavyUnpublishedProofKeepsPreviousGreenTime(t *testing.T) {
	for _, mode := range []string{"cancelled", "refused"} {
		t.Run(mode, func(t *testing.T) {
			root, base := heavyRepo(t)
			heavyGit(t, root, "update-ref", GreenRef, base)
			dir, err := heavyDirectory(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			proved := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
			if err := publishHeavyJSON(dir, "green.json", heavy.GreenProof{Commit: base, ProvedAt: proved}); err != nil {
				t.Fatal(err)
			}
			tip := heavyCommit(t, root, "next")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
				if mode == "cancelled" {
					cancel()
				} else {
					// A Git lock refuses publication without moving the old proof pointer.
					common := heavyGit(t, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
					if err := os.WriteFile(filepath.Join(common, GreenRef+".lock"), nil, 0644); err != nil {
						t.Fatal(err)
					}
				}
				return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
			})
			runner.Now = func() time.Time { return proved.Add(time.Hour) }
			if _, err := runner.Run(ctx); err == nil {
				t.Fatal("unpublished proof passed")
			}
			status, err := ReadHeavyStatus(context.Background(), root, "")
			if err != nil || status.GreenCommit != base || !status.GreenProvedAt.Equal(proved) {
				t.Fatalf("previous proof lost: status=%+v err=%v", status, err)
			}
			// With publication now allowed, the same tip gets its own proof time.
			common := heavyGit(t, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
			if err := os.Remove(filepath.Join(common, GreenRef+".lock")); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			runner.Judge = func(context.Context, string, string) ([]HeavyCheck, error) {
				return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
			}
			if _, err := runner.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			status, err = ReadHeavyStatus(context.Background(), root, "")
			if err != nil || status.GreenCommit != tip || !status.GreenProvedAt.Equal(proved.Add(time.Hour)) {
				t.Fatalf("status=%+v err=%v", status, err)
			}
		})
	}
}
