package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestCleanForkPreservationHoldNamesCauseAndClearCommand(t *testing.T) {
	f, l, c := heldForkFixture(t)
	ctx := context.Background()
	var log bytes.Buffer
	l.cfg.Report = &log
	dir := scratchUnderDurable(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	const cause = "object database is read-only"
	wrapper := fmt.Sprintf("#!/bin/sh\ncase \"$3\" in commit-tree) echo '%s' >&2; exit 99;; esac\nexec %q \"$@\"\n", cause, realGit)
	if err := writeForkSafe(filepath.Join(dir, "git"), []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if stop := l.abandonFromBase(ctx, c.LaneID, c.RunID, c.HeadSHA, c.BaseSHA, "blocked"); stop != stopLaneRetained {
		t.Fatalf("abandon = %q, want lane retained", stop)
	}
	if status := runGit(t, c.Worktree, "status", "--porcelain"); status != "" {
		t.Fatalf("fork worktree is not clean: %s", status)
	}
	if head := runGit(t, c.Worktree, "rev-parse", "HEAD"); head != c.HeadSHA {
		t.Fatalf("failed preservation changed HEAD to %s", head)
	}
	fault, held := faultOf(t, f, c.ItemID)
	if !held || fault.Reason != ReasonPreserveFailed || fault.Seq <= 0 {
		t.Fatalf("not held for preservation: %+v", fault)
	}
	command := fmt.Sprintf("tillr drain clear-fault --store '%s' --item '%s' --fault %d", f.storePath, c.ItemID, fault.Seq)
	checkDetail := func(surface, detail string) {
		t.Helper()
		for _, want := range []string{cause, preservedPrefix + c.RunID, "1 file"} {
			if !strings.Contains(detail, want) {
				t.Errorf("%s omits %q: %s", surface, want, detail)
			}
		}
		for _, unwanted := range []string{"candidate.txt", "uncommitted work", "index.lock"} {
			if strings.Contains(detail, unwanted) {
				t.Errorf("%s contains %q: %s", surface, unwanted, detail)
			}
		}
	}
	checkDetail("stored fault", fault.Detail)
	checkDetail("loop log", log.String())
	if !strings.Contains(log.String(), command) {
		t.Errorf("loop log omits %q: %s", command, log.String())
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{Now: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), StorePath: f.storePath, AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == c.ItemID {
				checkDetail("queue detail", row.Why)
				if !strings.Contains(row.Why, command) || !strings.Contains(row.Mover, command) || strings.Contains(row.Mover, "index.lock") {
					t.Errorf("queue does not give the correct recovery: detail %q, mover %q", row.Why, row.Mover)
				}
				return nil
			}
		}
		t.Fatal("held item has no queue row")
		return nil
	})
}

func TestPreservationKeepsThousandsOfFiles(t *testing.T) {
	for _, state := range []string{"committed fork", "dirty worktree"} {
		t.Run(state, func(t *testing.T) {
			const count = 3000
			ctx := context.Background()
			var f refusalLoopFixture
			var l *Loop
			var lane, runID, base, target string
			if state == "committed fork" {
				var c Candidate
				f, l, c = heldForkFixture(t)
				lane, runID, base, target = c.LaneID, c.RunID, c.HeadSHA, c.Worktree
			} else {
				var d Decision
				f, l, d, _ = savedFixture(t)
				lane, runID = d.LaneID, d.RunRequest.RunID
				target = l.laneTarget(lane)
				runGit(t, target, "reset", "--hard", "HEAD")
				base = runGit(t, target, "rev-parse", "HEAD")
			}
			var last string
			for i := range count {
				last = fmt.Sprintf("%s-%04d.txt", strings.Repeat("long-name-", 10), i)
				writeForkFile(t, filepath.Join(target, last), "saved contents\n")
			}
			if state == "committed fork" {
				runGit(t, target, "add", "--all")
				runGit(t, target, "commit", "-qm", "large committed change")
				if status := runGit(t, target, "status", "--porcelain"); status != "" {
					t.Fatalf("fork worktree is not clean: %s", status)
				}
			}
			// The record exceeds Linux's single-argument limit (128 KiB with 4 KiB
			// pages). Running preservation again also exercises dirty snapshot coverage.
			for range 2 {
				if err := l.preserveWorkFromBase(ctx, lane, runID, base, "blocked"); err != nil {
					t.Fatalf("preserving %d files: %.500s", count, err)
				}
			}
			ref := preservedPrefix + runID
			body := runGit(t, target, "show", "-s", "--format=%B", ref)
			var saved PreservedWork
			if err := json.Unmarshal([]byte(body), &saved); err != nil || len(saved.Files) != count || len(body) <= 128*1024 {
				t.Fatalf("preserved record: %d files, %d bytes, error %v", len(saved.Files), len(body), err)
			}
			if got := runGit(t, target, "show", ref+":"+last); got != "saved contents" {
				t.Fatalf("last preserved file = %q", got)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				rows, err := ReadPreservedWork(r)
				if err == nil && (len(rows) != 1 || rows[0].Ref != ref || len(rows[0].Files) != count) {
					t.Fatalf("wrong preservation event: %d records", len(rows))
				}
				return err
			})
		})
	}
}
