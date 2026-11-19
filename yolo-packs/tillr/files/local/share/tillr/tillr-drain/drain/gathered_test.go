package drain

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func gatheredStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A gathered write's facts are fetched with no store write open, each once, and the write
// that commits is the one that had them all: its row is written exactly once.
func TestAGatheredWriteFetchesEachFactWithNoWriteOpen(t *testing.T) {
	s := gatheredStore(t)
	attempts := 0
	fetches := map[string]int{}
	held := false
	fact := func(key, value string) func(context.Context) (string, error) {
		return func(context.Context) (string, error) {
			fetches[key]++
			held = held || store.OpenWriteTransactions() != 0
			return value, nil
		}
	}
	err := writeGathered(context.Background(), s, func(ctx context.Context, tx *store.Tx) error {
		attempts++
		a, err := gather(ctx, "fact a", fact("fact a", "A"))
		if err != nil {
			return err
		}
		b, err := gather(ctx, "fact b", fact("fact b", "B"))
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO meta (key, value) VALUES ('gathered', ?)`, a+b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("a fact was fetched while a store write was open")
	}
	if attempts != 3 || fetches["fact a"] != 1 || fetches["fact b"] != 1 {
		t.Fatalf("attempts=%d fetches=%v, want 3 attempts and each fact once", attempts, fetches)
	}
	var value string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT value FROM meta WHERE key = 'gathered'`).Scan(&value)
	}); err != nil || value != "AB" {
		t.Fatalf("committed %q, %v", value, err)
	}
}

// A read phase gathers what the write then asks again, so the write needs no fetch; a
// fetched error is the answer too, given back exactly as it was fetched.
func TestAGatheredWriteReusesTheReadPhasesAnswers(t *testing.T) {
	s := gatheredStore(t)
	boom := errors.New("git failed")
	fetched := 0
	run := func(context.Context) (string, error) { fetched++; return "", boom }
	ctx, facts := gatheringFor(context.Background())
	if _, err := gather(ctx, "failing fact", run); !errors.Is(err, boom) {
		t.Fatalf("read phase: %v", err)
	}
	err := writeGatheredFrom(context.Background(), s, facts, func(ctx context.Context, tx *store.Tx) error {
		_, err := gather(ctx, "failing fact", run)
		return err
	})
	if !errors.Is(err, boom) || fetched != 1 {
		t.Fatalf("write: %v, fetched %d times", err, fetched)
	}
}

// A recording read phase run inside a store write is the fault itself, and is refused
// before the I/O starts.
func TestGatheringInsideAWriteIsRefused(t *testing.T) {
	s := gatheredStore(t)
	ran := false
	err := s.WriteTx(context.Background(), func(*store.Tx) error {
		ctx, _ := gatheringFor(context.Background())
		_, err := gather(ctx, "git rev-parse", func(context.Context) (string, error) { ran = true; return "", nil })
		return err
	})
	if !errors.Is(err, store.ErrIOUnderWrite) || ran {
		t.Fatalf("gathering under a write: %v, ran=%v", err, ran)
	}
}

// Two wrappers that run the same git command but answer it differently are gathered
// apart: the loop's git answers stdout with its newline, heavyGitOutput answers it
// trimmed, and a trimmed rev-parse handed the other's answer named a commit that does not
// exist (the lane-base ancestry check failed with "Not a valid object name").
func TestTheSameGitCommandThroughTwoWrappersIsGatheredApart(t *testing.T) {
	s := gatheredStore(t)
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "commit", "--allow-empty", "-qm", "one")
	l := &Loop{}
	var raw, trimmed string
	err := writeGathered(context.Background(), s, func(ctx context.Context, tx *store.Tx) error {
		var err error
		if raw, err = l.git(ctx, root, "rev-parse", "HEAD"); err != nil {
			return err
		}
		trimmed, err = heavyGitOutput(ctx, root, "rev-parse", "HEAD")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw != trimmed+"\n" || len(trimmed) != 40 {
		t.Fatalf("raw=%q trimmed=%q", raw, trimmed)
	}
}

// A write that swallows a miss, reading it as a negative answer, commits nothing from that
// attempt: the attempt rolls back, the fact is fetched, and the write that commits is the
// one that saw the real answer.
func TestASwallowedGatherMissStillRollsTheAttemptBack(t *testing.T) {
	s := gatheredStore(t)
	attempts := 0
	err := writeGathered(context.Background(), s, func(ctx context.Context, tx *store.Tx) error {
		attempts++
		answer, err := gather(ctx, "fact", func(context.Context) (string, error) { return "yes", nil })
		if err != nil {
			answer = "no" // the swallowing a caller must not get away with
		}
		_, err = tx.Exec(`INSERT INTO meta (key, value) VALUES ('swallowed', ?)`, answer)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var value string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT value FROM meta WHERE key = 'swallowed'`).Scan(&value)
	}); err != nil || value != "yes" || attempts != 2 {
		t.Fatalf("committed %q after %d attempts, %v", value, attempts, err)
	}
}
