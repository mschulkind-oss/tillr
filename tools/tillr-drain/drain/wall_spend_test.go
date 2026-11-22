package drain_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWallContinuationRefreshesPassCumulativeSpend(t *testing.T) {
	adapter := stub(t, "wall-spend", counting+`if [ "$TILLR_RUN_RESUME" != true ]; then
 printf 'tokens: 1500\ncost usd: 0.030000\n'
 exit 124
fi
printf 'tokens: 4500\ncost usd: 0.090000\n'
`+committingAdapterBody+`printf 'RESULT: done\n'`)
	dir := filepath.Dir(adapter)
	spendBackends(t, adapter, "")
	writeFile(t, filepath.Join(dir, "extract-report"), `#!/bin/sh
for a in "$@"; do [ "$prev" != --out ] || out=$a; prev=$a; log=$a; done
awk '/^tokens:/ { tokens=$2 }; /^cost usd:/ { cost=$3 }; END { printf "tokens: %s\ncost usd: %s\n---\n",tokens,cost }' "$log" > "$out"
`)
	writeFile(t, filepath.Join(dir, "pause-kind"), "stops-where-it-stands\n")
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	steps.BackendsDir = dir
	stager := &drain.LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.store, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: func() time.Time { return time.Now().UTC() }}
	result, err := f.newLoopWith(t, 1, steps, stager, &strings.Builder{}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Landed != 1 || result.StartedSpend.CostUSD != .09 || result.StartedSpend.Runs != 1 {
		t.Fatalf("continuation total: %+v", result)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var cost float64
		var tokens int64
		err := r.QueryRow(`SELECT usage_cost_usd,usage_tokens FROM drain_pass_spend WHERE run_id=?`, "run-item_a-a1").Scan(&cost, &tokens)
		if err == nil && (cost != .09 || tokens != 4500) {
			t.Fatalf("pass kept pre-continuation spend: $%f %d tokens", cost, tokens)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
