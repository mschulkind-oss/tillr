package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
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
			f := newRefusalLoopFixture(t, "item-a", "item-other")
			policy, err := os.ReadFile("../../../config/tillr/deny-list.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(f.root, filepath.Dir(denyListPath)), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, denyListPath), string(policy))
			writeForkFile(t, filepath.Join(f.root, "first.md"), original)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "before")
			base := runGit(t, f.root, "rev-parse", "HEAD")
			writeForkFile(t, filepath.Join(f.root, "first.md"), tc.first)
			writeForkFile(t, filepath.Join(f.root, "second.md"), tc.second)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "candidate")
			candidate := Candidate{ItemID: "item-a", Worktree: f.root, BaseSHA: base, HeadSHA: runGit(t, f.root, "rev-parse", "HEAD")}
			if tc.triage {
				candidate.BlockConflict = &item.Conflict{TriageVersion: "fixture"}
			}
			if tc.idea {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO idea_intake(item_id,words_path,words_hash,words_size,intake_path) VALUES ('item-a','words.md','fixture',0,'fixture')`)
					return err
				})
			}
			l := Loop{cfg: LoopConfig{Root: f.root, Store: f.storePath}}
			detail, err := l.denyListViolation(context.Background(), candidate)
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

func commitDenyListFixture(t *testing.T, root string) {
	t.Helper()
	policy, err := os.ReadFile("../../../config/tillr/deny-list.json")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, denyListPath)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, target, string(policy))
	runGit(t, root, "add", denyListPath)
	runGit(t, root, "commit", "-qm", "common deny-list")
}
