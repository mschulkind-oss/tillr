package boundary

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCommonDenyListDeclarations(t *testing.T) {
	const original = "<!-- tillr: item id=item-existing\n from=item-old -->\n"
	const ideaOutput = "<!-- tillr: item id=item-new from=item-a -->\n"
	const triageOutput = "<!-- tillr: item id=item-a -->\n"
	for _, tc := range []struct {
		name, first, second, want string
		idea, triage              bool
	}{
		{"move", "", original, "", false, false},
		{"fenced example", original, "```md\n" + triageOutput + "```\n", "", false, false},
		{"remove", "", "", "item-existing", false, false},
		{"edit", strings.ReplaceAll(original, "item-old", "item-new"), "", "item-existing", false, false},
		{"add", original, ideaOutput, "item-new", false, false},
		{"idea add", original, ideaOutput, "", true, false},
		{"idea wrong source", original, strings.ReplaceAll(ideaOutput, "from=item-a", "from=item-b"), "item-new", true, false},
		{"idea duplicate in base", original, strings.ReplaceAll(ideaOutput, "item-new", "item-existing"), "item-existing", true, false},
		{"idea duplicate in store", original, strings.ReplaceAll(ideaOutput, "item-new", "item-other"), "item-other", true, false},
		{"idea cannot remove", "", ideaOutput, "item-existing", true, false},
		{"triage add", original, triageOutput, "", false, true},
		{"triage cannot add twice", original, triageOutput + ideaOutput, "item-a", false, true},
		{"triage cannot remove", "", triageOutput, "item-existing", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := scratchUnderDurable(t)
			root := filepath.Join(dir, "repo")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, root, "init", "-q", "-b", "main")
			runGit(t, root, "config", "user.name", "Fixture")
			runGit(t, root, "config", "user.email", "fixture@example.invalid")

			storePath := filepath.Join(dir, "board.sqlite3")
			s, err := store.InitFixture(ctx, storePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				for _, id := range []string{"item-a", "item-other"} {
					if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES(?,?,'in-queue',0,'')`, id, id); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			s.Close()

			policy, err := os.ReadFile("../../../../config/tillr/deny-list.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, filepath.Dir(DenyListPath)), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(root, DenyListPath), string(policy))
			writeForkFile(t, filepath.Join(root, "first.md"), original)
			runGit(t, root, "add", ".")
			runGit(t, root, "commit", "-qm", "before")
			base := runGit(t, root, "rev-parse", "HEAD")
			writeForkFile(t, filepath.Join(root, "first.md"), tc.first)
			writeForkFile(t, filepath.Join(root, "second.md"), tc.second)
			runGit(t, root, "add", ".")
			runGit(t, root, "commit", "-qm", "candidate")
			head := runGit(t, root, "rev-parse", "HEAD")

			if tc.idea {
				s, err := store.Open(ctx, storePath, true)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.WriteTx(ctx, func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO idea_intake(item_id,words_path,words_hash,words_size,intake_path) VALUES ('item-a','words.md','fixture',0,'fixture')`)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				s.Close()
			}
			detail, err := DenyListViolation(ctx, DenyListRequest{
				Env:            drainmodel.Env{Root: root, StorePath: storePath},
				ItemID:         "item-a",
				Worktree:       root,
				BaseSHA:        base,
				HeadSHA:        head,
				TriageCaptured: tc.triage,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if detail != "" {
					t.Fatalf("unexpected refusal: %s", detail)
				}
			} else if !strings.Contains(detail, tc.want) || !strings.Contains(detail, "item-declaration") || !strings.Contains(detail, "cannot be edited or removed") {
				t.Fatalf("refusal %q, want declaration %q, entry and reason", detail, tc.want)
			}
		})
	}
}
