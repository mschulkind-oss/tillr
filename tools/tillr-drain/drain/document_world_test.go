package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// An unavailable rolling gate must not turn the daemon's idle polling into a rapid
// subprocess loop. It retries on the normal cadence or when a different tip arrives.
type unknownDocumentRollingSteps struct {
	documentCountingSteps
	rollingCalls int
}

func (s *unknownDocumentRollingSteps) RollingConfigured() bool { return true }
func (s *unknownDocumentRollingSteps) Rolling(context.Context, string, string) (RollingOutcome, error) {
	s.rollingCalls++
	return RollingOutcome{Known: false, ExitCode: 127, Detail: "fixture gate is unavailable"}, nil
}

func TestWaitingDoesNotRepeatUnknownRollingChecksOnIdlePolls(t *testing.T) {
	f, l := passFixture(t)
	steps := &unknownDocumentRollingSteps{}
	l.cfg.Steps = steps
	l.cfg.ExitWhenDrained = false
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }
	var result LoopResult
	pass := func(want int) {
		t.Helper()
		if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
			t.Fatalf("idle stay pass: %s / %v", stop, err)
		}
		if steps.rollingCalls != want {
			t.Fatalf("rolling checks = %d, want %d", steps.rollingCalls, want)
		}
	}
	pass(1)
	pass(1)
	pass(1)
	at = at.Add(RollingEvery)
	pass(2)
	pass(2)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "new tip while idle")
	pass(3)
	pass(3)
}

type documentCountingSteps struct {
	recordingSteps
	syncs int
}

func (s *documentCountingSteps) Sync(context.Context) (SyncOutcome, error) {
	s.syncs++
	return SyncOutcome{}, nil
}

func TestDocumentSyncAndQuestionsAreReusedUntilTrunkMoves(t *testing.T) {
	f, l := passFixture(t, "item-a")
	doc := filepath.Join(f.root, "docs", "plan.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, doc, "# Plan\n")
	runGit(t, f.root, "add", "docs/plan.md")
	runGit(t, f.root, "commit", "-qm", "plan")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET design_doc = ?`, "docs/plan.md")
		return err
	})
	count := filepath.Join(scratchUnderDurable(t), "questions-count")
	command := filepath.Join(scratchUnderDurable(t), "questions")
	body := fmt.Sprintf("#!/bin/sh\necho index >> '%s'\nprintf '%%s\\n' '{\"documents\":[{\"path\":\"docs/plan.md\",\"questions\":[{\"id\":\"OQ-1\",\"state\":\"open\"}]}]}'\n", count)
	if err := os.WriteFile(command, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	l.cfg.QuestionsCmd = []string{command}
	steps := &documentCountingSteps{}
	l.cfg.Steps = steps
	var result LoopResult
	pass := func(want int) {
		t.Helper()
		if stop, err := l.pass(context.Background(), &result); err != nil || stop != StopQueueDrained {
			t.Fatalf("pass: %s / %v", stop, err)
		}
		raw, err := os.ReadFile(count)
		if err != nil {
			t.Fatal(err)
		}
		if result.SyncedSHA != runGit(t, f.root, "rev-parse", TrunkRef) {
			t.Fatalf("synced commit = %q, want current trunk", result.SyncedSHA)
		}
		if steps.syncs != want || strings.Count(string(raw), "index\n") != want {
			t.Fatalf("syncs = %d, question invocations = %d; want %d of each", steps.syncs, strings.Count(string(raw), "index\n"), want)
		}
	}
	pass(1)
	pass(1)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "new trunk commit")
	pass(2)
	pass(2)
}
