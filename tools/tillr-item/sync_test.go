package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// syncRepo is a git repository with the given tracked files, plus one untracked document
// whose block must never be read.
func syncRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := scratch(t, "sync-repo")
	gitIn := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q", "-b", "main")
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn("add", "-A")
	gitIn("commit", "-q", "-m", "fixture")
	untracked := "### Draft\n<!-- tillr: item id=\"item-untracked\" paths=\"x/\" -->\n- **c1:** never read\n"
	if err := os.WriteFile(filepath.Join(dir, "docs", "draft.md"), []byte(untracked), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const syncGood = "### Good\n<!-- tillr: item id=\"item-good\" paths=\"docs/\" -->\n- **c1:** it holds\n"

type syncRecord struct {
	Kind    string `json:"kind"`
	Payload struct {
		DryRun   bool     `json:"dry_run"`
		Created  []string `json:"created"`
		Findings []struct {
			File string `json:"file"`
			Rule string `json:"rule"`
		} `json:"findings"`
		UnsharedOpen   int `json:"unshared_open"`
		StaleDocBlocks int `json:"stale_doc_blocks"`
	} `json:"payload"`
}

func TestSyncFaceDesignWithoutItemIsWarning(t *testing.T) {
	repo := syncRepo(t, map[string]string{
		"docs/roadmap.md":        "# Roadmap\n[Design](design/example.md)\n",
		"docs/design/example.md": "---\nstage: DESIGN\n---\n# Design\n",
	})
	db := filepath.Join(scratch(t, "sync-design-warning"), "state.db")
	for _, extra := range [][]string{{"--dry-run"}, {}} {
		args := append([]string{"sync", "--store", db, "--root", repo, "--json"}, extra...)
		code, out := run(t, args...)
		if code != 0 {
			t.Fatalf("warning exited %d: %s", code, out)
		}
		var record struct {
			Payload struct {
				Warnings []struct {
					File, Roadmap, Rule string
					Line                int
				} `json:"warnings"`
				Findings []any    `json:"findings"`
				Created  []string `json:"created"`
				Updated  []string `json:"updated"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(out, &record); err != nil {
			t.Fatal(err)
		}
		p := record.Payload
		if len(p.Warnings) != 1 || p.Warnings[0].Rule != "design-without-item" || p.Warnings[0].File != "docs/design/example.md" || p.Warnings[0].Roadmap != "docs/roadmap.md" || p.Warnings[0].Line != 2 || len(p.Findings)+len(p.Created)+len(p.Updated) != 0 {
			t.Fatalf("warning record: %s", out)
		}
	}
	code, out := run(t, "sync", "--store", db, "--root", repo)
	if code != 0 || !strings.Contains(string(out), "warning:") || !strings.Contains(string(out), "design-without-item") || !strings.Contains(string(out), "docs/roadmap.md:2") {
		t.Fatalf("text warning: exit %d: %s", code, out)
	}
}

func TestSyncFaceRefusesCloneOfHeldItem(t *testing.T) {
	repo := syncRepo(t, map[string]string{"docs/original.md": syncGood})
	db := filepath.Join(scratch(t, "sync-clone-store"), "state.db")
	if code, out := run(t, "sync", "--store", db, "--root", repo, "--json"); code != 0 {
		t.Fatalf("initial sync exited %d: %s", code, out)
	}
	cloneRepo := syncRepo(t, map[string]string{
		"docs/clone.md": strings.ReplaceAll(syncGood, "item-good", "item-good-v2") +
			strings.ReplaceAll(syncGood, "item-good", "item-sibling"),
	})
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		args := append([]string{"sync", "--store", db, "--root", cloneRepo, "--json"}, extra...)
		code, out := run(t, args...)
		rec := decodeSync(t, out)
		if code != 1 || len(rec.Payload.Findings) != 1 || rec.Payload.Findings[0].Rule != "clone-of-held-item" || len(rec.Payload.Created) != 0 {
			t.Fatalf("clone sync exited %d: %s", code, out)
		}
		for _, want := range []string{"item-good-v2", "item-good", "tillr drain retry --item item-good"} {
			if !strings.Contains(string(out), want) {
				t.Fatalf("finding omits %q: %s", want, out)
			}
		}
	}
}

func decodeSync(t *testing.T, out []byte) syncRecord {
	t.Helper()
	var rec syncRecord
	if err := json.Unmarshal(out, &rec); err != nil {
		t.Fatalf("the sync record is not JSON: %v\n%s", err, out)
	}
	if rec.Kind != "item.sync" {
		t.Fatalf("kind %q, want item.sync", rec.Kind)
	}
	return rec
}

// A store initialized by the store face has every item table, empty (b0c26afac). Preview
// must leave them empty, append no events, and not change the store's content.
func TestSyncDryRunFreshStore(t *testing.T) {
	repo := syncRepo(t, map[string]string{"docs/good.md": syncGood})
	dir := scratch(t, "sync-fresh-store")
	db := filepath.Join(dir, "state.db")
	storeBin := filepath.Join(dir, "tillr-store")
	build := exec.Command("go", "build", "-o", storeBin, ".")
	build.Dir = "../tillr-store"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building tillr-store: %v\n%s", err, out)
	}
	if out, err := exec.Command(storeBin, "init", "--store", db).CombinedOutput(); err != nil {
		t.Fatalf("initializing the store: %v\n%s", err, out)
	}
	assertEmpty := func() {
		t.Helper()
		s, err := store.OpenRead(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			var events int
			if err := r.QueryRow(`SELECT count(*) FROM events`).Scan(&events); err != nil {
				return err
			}
			rows, err := r.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE 'item%'`)
			if err != nil {
				return err
			}
			var names []string
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					rows.Close()
					return err
				}
				names = append(names, n)
			}
			if err := rows.Close(); err != nil {
				return err
			}
			if len(names) == 0 {
				t.Fatal("initialized store holds no item tables, want them created empty")
			}
			for _, n := range names {
				var count int
				if err := r.QueryRow(`SELECT count(*) FROM "` + n + `"`).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					t.Fatalf("fresh store table %s holds %d rows, want none", n, count)
				}
			}
			if events != 0 {
				t.Fatalf("fresh store holds %d events, want none", events)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertEmpty()
	// The store's content, not its file: a commit stays in the write-ahead log, so the
	// database file's bytes do not move when a face writes (store.Fingerprint).
	before, err := store.Fingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	code, out := run(t, "sync", "--store", db, "--root", repo, "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("fresh-store preview exited %d: %s", code, out)
	}
	if rec := decodeSync(t, out); !rec.Payload.DryRun || strings.Join(rec.Payload.Created, ",") != "item-good" {
		t.Fatalf("fresh-store preview record %+v", rec.Payload)
	}
	assertEmpty()
	after, err := store.Fingerprint(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("preview changed the fresh store's content")
	}
}

// The face's exit contract: 0 when every block was read, 1 when a finding was recorded
// after the good documents were written, 2 for usage, 4 when git cannot list the documents.
// --dry-run creates no store, and an untracked document is never read.
func TestSyncFaceExitCodesAndOutput(t *testing.T) {
	repo := syncRepo(t, map[string]string{"docs/good.md": syncGood})
	db := filepath.Join(scratch(t, "sync-store"), "state.db")

	code, out := run(t, "sync", "--store", db, "--root", repo, "--now", "2026-11-15T12:00:00Z", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("dry run exited %d: %s", code, out)
	}
	if rec := decodeSync(t, out); !rec.Payload.DryRun || strings.Join(rec.Payload.Created, ",") != "item-good" {
		t.Fatalf("dry run record %+v", rec.Payload)
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("a dry run created the store")
	}

	code, out = run(t, "sync", "--store", db, "--root", repo, "--now", "2026-11-15T12:00:00Z")
	if code != 0 || !strings.Contains(string(out), "created 1") || !strings.Contains(string(out), "unshared_open: 0") || !strings.Contains(string(out), "stale_doc_blocks: 0") {
		t.Fatalf("sync exited %d:\n%s", code, out)
	}

	bad := syncRepo(t, map[string]string{
		"docs/good.md": syncGood,
		"docs/bad.md":  "### Bad\n<!-- tillr: item id=\"item-bad\" paths=\"docs/\" state=\"landed\" -->\n- **c1:** x\n",
	})
	db2 := filepath.Join(scratch(t, "sync-store"), "state.db")
	code, out = run(t, "sync", "--store", db2, "--root", bad, "--json")
	if code != 1 {
		t.Fatalf("a finding exited %d, want 1: %s", code, out)
	}
	rec := decodeSync(t, out)
	if strings.Join(rec.Payload.Created, ",") != "item-good" || len(rec.Payload.Findings) != 1 || rec.Payload.Findings[0].Rule != "unknown-attribute" {
		t.Fatalf("record %+v", rec.Payload)
	}

	if code, _ := run(t, "sync", "--root", repo); code != 2 {
		t.Fatalf("sync with no --store exited %d, want 2", code)
	}
	if code, _ := run(t, "sync", "--store", db, "--now", "yesterday"); code != 2 {
		t.Fatalf("a bad --now exited %d, want 2", code)
	}
	// The scratch root lives inside some repository, so a fresh directory there is a
	// subdirectory of it: usage, not a repository root.
	if code, _ := run(t, "sync", "--store", db, "--root", filepath.Join(repo, "docs")); code != 2 {
		t.Fatalf("a subdirectory root exited %d, want 2", code)
	}
	// A git that cannot answer at all is exit 4: GIT_DIR names nothing.
	cmd := exec.Command(bin, "sync", "--store", db, "--root", repo)
	cmd.Env = append(os.Environ(), "GIT_DIR="+filepath.Join(scratch(t, "no-git"), "missing"))
	if err := cmd.Run(); err == nil {
		t.Fatalf("sync answered with no git repository")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 4 {
		t.Fatalf("a git that cannot answer exited %v, want 4", err)
	}
	if code, out := run(t, "sync", "--help"); code != 0 || !strings.Contains(string(out), "sync") {
		t.Fatalf("sync --help exited %d: %s", code, out)
	}
}
