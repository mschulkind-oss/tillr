package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDesignAmendmentLandsThenDispatchesOriginalSeat(t *testing.T) {
	testDesignAmendment(t, false)
}

func TestSecondFailureDesignAmendmentDispatchesOriginalSeat(t *testing.T) {
	testDesignAmendment(t, true)
}

func testDesignAmendment(t *testing.T, secondFailure bool) {
	failure := "Conflict: c1 contradicts the API\nRESULT: blocked\n"
	if secondFailure {
		failure = "c1 impossible contract failed\nRESULT: failed\n"
	}
	worker := counting + `if rg -q 'impossible contract' docs/work.md; then
 printf '` + failure + `'
else
 ` + committingAdapterBody + `printf 'RESULT: done\n'
fi
`
	designer := counting + `python3 - <<'PY'
from pathlib import Path
p=Path('docs/work.md')
p.write_text(p.read_text().replace('impossible contract','implementable contract'))
PY
git add docs/work.md
git -c user.name=stub -c user.email=stub@example.invalid commit -qm 'amend contract'
printf 'RESULT: done\n'
`
	f := blockedRouteFixture(t, worker, designer)
	land := stub(t, "land", counting+fmt.Sprintf(`unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
while [ "$#" -gt 0 ]; do
 if [ "$1" = --head ]; then shift; head=$1; fi
 shift
done
git -C %q cherry-pick "$head" >/dev/null
`, f.root))
	gate := stub(t, "gate", counting+`set -eu
if [ "$(git log -1 --format=%s)" = 'amend contract' ]; then
 git show HEAD:docs/work.md | rg -q 'implementable contract'
 git show HEAD:docs/work.md | rg -q 'id="item-route"'
else
 [ "$(git log -1 --format=%s)" = 'chore: retire landed document block' ]
 ! git show HEAD:docs/work.md | rg -q 'id="item-route"'
fi
`)
	steps := f.steps(t, gate, land, always(t, "verdict", 0))
	stage := &drain.LaneStager{Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Template: filepath.Join("..", "tillr-run", "backends", "brief-template.md"), Now: func() time.Time { return steps.Now() }}
	if secondFailure {
		first, err := routeLoop(t, f, steps, stage, os.Stderr).Run(context.Background())
		if err != nil || first.Dispatched != 1 {
			t.Fatalf("first failure: %+v / %v", first, err)
		}
		s, err := store.Open(context.Background(), f.store, true)
		if err != nil {
			t.Fatal(err)
		}
		err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := drain.AuthorizeRetry(context.Background(), tx, drain.RetryRequest{ItemID: "item-route", RunID: "run-item-route-a1", By: "owner", Why: "diagnosed", At: steps.Now()})
			return err
		})
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := routeLoop(t, f, steps, stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Dispatched != 3 || result.Landed != 2 {
		t.Fatalf("result: %+v", result)
	}
	if got := itemStateOf(t, f.store, "item-route"); got != "landed" {
		t.Fatalf("state %s", got)
	}
	if countMatching(lines(t, f.count), "verdict") != 1 {
		t.Fatalf("design run retired original criteria: %v", lines(t, f.count))
	}
	var routed, answered string
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "item.conflict" {
			var c map[string]any
			if err := json.Unmarshal(e.Payload, &c); err != nil {
				t.Fatal(err)
			}
			if c["status"] == "awaiting" {
				routed = c["design_run_id"].(string)
			}
		}
		if e.Kind == "drain.started" {
			var w struct {
				Decision drain.Decision `json:"decision"`
			}
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				t.Fatal(err)
			}
			if w.Decision.Reason == "conflict-answered" {
				answered = w.Decision.ConflictID
				if w.Decision.Seat != "build" || w.Decision.RunRequest.Backend != "fixture" {
					t.Fatalf("wrong own seat: %+v", w.Decision)
				}
			}
		}
	}
	if routed == "" || answered == "" {
		t.Fatalf("routing=%s answering=%s", routed, answered)
	}
	// Staging retains each run's actual brief and fence for later inspection.
	fence, err := os.ReadFile(filepath.Join(f.root, ".tillr", "loop", routed, "fence.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(fence) != "docs/work.md\n" {
		t.Fatalf("design fence %q", fence)
	}
	brief, err := os.ReadFile(filepath.Join(f.root, ".tillr", "loop", routed, "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "conflict.json") || !strings.Contains(string(brief), answered) {
		t.Fatalf("brief lost conflict: %s", brief)
	}
	record, err := os.ReadFile(filepath.Join(f.root, ".tillr", "loop", routed, "conflict.json"))
	wantEvidence := "c1 contradicts the API"
	if secondFailure {
		wantEvidence = "Second failed attempt: run-item-route-a1 and run-item-route-a2"
	}
	if err != nil || !strings.Contains(string(record), wantEvidence) {
		t.Fatalf("conflict record %s / %v", record, err)
	}
}

func TestWorkerMayEditAnotherSectionOfItsDefinitionDocument(t *testing.T) {
	f := blockedRouteFixture(t, counting+`printf '\n### Other\n\nUnrelated prose.\n' >> docs/work.md
git add docs/work.md
git -c user.name=stub -c user.email=stub@example.invalid commit -qm other
printf 'RESULT: done\n'
`, "exit 2")
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"docs/work.md"}}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	_, err := routeLoop(t, f, steps, stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if countMatching(lines(t, f.count), "gate") != 1 {
		t.Fatal("unrelated section was refused")
	}
}
