package drain

import (
	"context"
	"testing"
)

// A design landing can answer the conflict between document refresh and planning.
// The store's item/run/lease version can then be current while the pass's base is old.
func TestDispatchDefersWhenTrunkMovesAfterDocumentRefresh(t *testing.T) {
	for _, movement := range []string{"before-plan", "after-plan"} {
		t.Run(movement, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			ctx := context.Background()
			var result LoopResult
			base, err := l.refreshDocuments(ctx, &result)
			if err != nil {
				t.Fatal(err)
			}
			move := func() {
				runGit(t, f.root, "commit", "--allow-empty", "-qm", "design amendment landed")
			}
			if movement == "before-plan" {
				move()
			}
			plan := mustPlan(t, l)[0]
			if movement == "after-plan" {
				move()
			}
			before := eventCount(t, f.storePath)
			generation := ""
			ok, work, err := l.applyPlanGeneration(ctx, plan, base, l.cfg.Now(), &generation)
			if err != nil || ok || work != nil {
				t.Fatalf("dispatch used stale refreshed trunk %s: applied=%v work=%+v error=%v", base, ok, work, err)
			}
			if got := eventCount(t, f.storePath); got != before || generation != "" {
				t.Fatalf("stale dispatch wrote events or a generation: events=%d want=%d generation=%q", got, before, generation)
			}
			fresh, err := l.refreshDocuments(ctx, &result)
			if err != nil {
				t.Fatal(err)
			}
			if fresh == base {
				t.Fatal("refresh did not observe the landing")
			}
			ok, work, err = l.applyPlan(ctx, mustPlan(t, l)[0], fresh, l.cfg.Now())
			if err != nil || !ok || work == nil || work.BaseSHA != fresh {
				t.Fatalf("refreshed dispatch: applied=%v work=%+v error=%v", ok, work, err)
			}
		})
	}
}
