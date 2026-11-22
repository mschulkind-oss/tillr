package drain_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// A design commit that deletes or renames the blocked item's block is no answer: it never
// reaches the gate, and it goes back to the design seat rather than to the owner (the
// owner's ruling of 2026-11-17). The second design run here objects naming the owner,
// which is the only way the owner is reached.
func TestDesignAnswerCannotDeleteOrRenameTheBlockedItem(t *testing.T) {
	for _, change := range []string{"delete", "rename"} {
		t.Run(change, func(t *testing.T) {
			replacement := "### Work\n\nNo directive.\n"
			if change == "rename" {
				replacement = strings.ReplaceAll(blockedDefinition, "item-route", "item-other")
			}
			designer := counting + `if [ -e "$STUB_COUNT.erased" ]; then printf 'Only the owner can rule.\nNEXT-ACTOR: owner\nRESULT: blocked\n'; exit 0; fi
touch "$STUB_COUNT.erased"
` + fmt.Sprintf("printf '%%s' '%s' > docs/work.md\n", replacement) + `git add docs/work.md
git -c user.name=stub -c user.email=stub@example.invalid commit -qm erase
printf 'RESULT: done\n'
`
			f := blockedRouteFixture(t, counting+"printf 'Conflict: c1 is impossible\nRESULT: blocked\n'", designer)
			stage := passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt", "docs/work.md"}}
			steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
			result, err := routeLoop(t, f, steps, stage, os.Stderr).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if countMatching(lines(t, f.count), "gate") != 0 {
				t.Fatal("identity removal reached gate")
			}
			if got := countMatching(lines(t, f.count), "designer"); got != 2 {
				t.Fatalf("the removed block did not go back to the design seat: %d design runs", got)
			}
			if !strings.Contains(result.Detail, "conflict-owner") {
				t.Fatalf("not held on owner: %+v", result)
			}
		})
	}
}
