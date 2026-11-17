package main

import (
	"strings"
	"testing"
	"time"
)

// fakeGit answers rev-parse with head, and each `diff --quiet` with the code the case names:
// moved for the committed range, dirty for the working tree.
func fakeGit(head string, revParse, moved, dirty int, calls *int) gitRunner {
	return func(root string, args ...string) (string, int) {
		if calls != nil {
			*calls++
		}
		switch {
		case args[0] == "rev-parse":
			return head + "\n", revParse
		case args[0] == "diff" && args[2] == "HEAD":
			return "", dirty
		default:
			return "", moved
		}
	}
}

func TestCheckoutFreshness(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		build                  buildStamp
		revParse, moved, dirty int
		want, note             string
	}{
		{"built at HEAD", buildStamp{Revision: "h"}, 0, 0, 0, "current", "are the ones"},
		{"HEAD moved, tillr sources did not", buildStamp{Revision: "old"}, 0, 0, 0, "current", "are the ones"},
		{"tillr sources moved", buildStamp{Revision: "old"}, 0, 1, 0, "behind", "just observatory"},
		{"uncommitted edits since a clean build", buildStamp{Revision: "h"}, 0, 0, 1, "behind", "uncommitted"},
		{"revision not in this history", buildStamp{Revision: "gone"}, 0, 128, 0, "unknown", "not in the checkout's history"},
		{"no repository", buildStamp{Revision: "h"}, 128, 0, 0, "unknown", "git cannot read HEAD"},
		{"no revision in the binary", buildStamp{}, 0, 0, 0, "unknown", "carries no revision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := freshness(tc.build, "/r", fakeGit("h", tc.revParse, tc.moved, tc.dirty, nil))
			if got.State != tc.want || !strings.Contains(got.Note, tc.note) {
				t.Fatalf("%+v, want %s / %q", got, tc.want, tc.note)
			}
		})
	}
}

// The comparison is on every response, so it is cached: a second read inside the TTL asks
// git nothing, and a read after it asks again. The clock is the test's.
func TestCheckoutWatchIsCached(t *testing.T) {
	calls := 0
	now := time.Unix(1000, 0)
	w := &checkoutWatch{root: "/r", git: fakeGit("h", 0, 0, 0, &calls), now: func() time.Time { return now }}
	first := w.read(buildStamp{Revision: "h"})
	n := calls
	w.read(buildStamp{Revision: "h"})
	if first.State != "current" || calls != n {
		t.Fatalf("a read inside the TTL ran git: %d -> %d", n, calls)
	}
	now = now.Add(checkoutTTL)
	w.read(buildStamp{Revision: "h"})
	if calls == n {
		t.Fatal("a read after the TTL did not refresh")
	}
}
