package drain_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// `why` says in one line that a trunk rewrite was absorbed, when, and how many commits it
// mapped, and says nothing before one is recorded.
func TestWhyNamesAnAbsorbedRewrite(t *testing.T) {
	path := fixture(t, itemRecordRanked("ready", "in-queue", "a"))
	ref := policy(t, "true")
	args := []string{"why", "--store", path, "--policy", ref, "--lanes", "lane-1", "--now", "2026-11-16T00:00:00Z"}
	if code, out := invoke(t, args...); code != 0 || strings.Contains(string(out), "absorbed a rewrite") {
		t.Fatalf("before any rewrite: %d %s", code, out)
	}
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, pair := range [][2]string{{"aaa", "bbb"}, {"ccc", "ddd"}, {"eee", "fff"}} {
			if _, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at,evidence) VALUES(?,?,?,'test')`, pair[0], pair[1], "2026-11-20T00:07:00-05:00"); err != nil {
				return err
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, args...)
	want := "trunk: absorbed a rewrite at 2026-11-20T00:07:00-05:00; 3 commits mapped to their new shas"
	if code != 0 || strings.Count(string(out), want) != 1 {
		t.Fatalf("after a rewrite: %d %s", code, out)
	}
}
