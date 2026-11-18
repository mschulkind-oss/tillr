package main

import (
	"context"
	"strings"
	"testing"
)

func TestWaitingPostLandingFailures(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1000,'drain.post-landing-action','landing','roll-tip','{"action":"roll","detail":"exit status 7: build failed\nlast line"}'),
 (2000,'drain.post-landing-action','landing','push-tip','{"action":"push","detail":"rejected by hook"}')`)
	read := func() map[string]string {
		t.Helper()
		rows, err := readWaiting(context.Background(), h.store, nil)
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]string{}
		for _, row := range rows {
			if strings.HasPrefix(row.Observation, "post-landing:") {
				result[row.Observation] = row.Why
			}
		}
		return result
	}
	rows := read()
	if len(rows) != 2 || rows["post-landing:roll"] != "running build is behind the trunk at roll-tip: exit status 7: build failed last line" || rows["post-landing:push"] != "push failed at push-tip: rejected by hook" {
		t.Fatalf("unexpected warnings: %v", rows)
	}
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (3000,'drain.post-landing-action','landing','retry-tip','{"action":"push","detail":""}')`)
	rows = read()
	if len(rows) != 1 || rows["post-landing:roll"] == "" {
		t.Fatalf("successful push should clear only its warning: %v", rows)
	}
}
