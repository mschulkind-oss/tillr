package drain

import (
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

// A node's run id carries one counter however many times it fails over or restarts, so the
// id of the 30th turn is no longer than the id of the 2nd. Repeating a suffix per turn grew
// an id until `git update-ref` could not store it.
func TestARunIDDoesNotGrowPerFailoverOrRestart(t *testing.T) {
	for _, kinds := range [][]string{{"failover"}, {"restart"}, {"failover", "restart"}, {"restart", "failover", "fresh", "interrupted"}} {
		id := "run-item-a-a1-n2"
		var second string
		for turn := 1; turn <= 30; turn++ {
			id = markRunID(id, kinds[turn%len(kinds)])
			if turn == 2 {
				second = id
			}
		}
		if len(id) > len(second) {
			t.Fatalf("%v: the 30th id %q is longer than the 2nd %q", kinds, id, second)
		}
	}
	if got := markRunID("run-x-n1", "restart"); got != "run-x-n1-restart" {
		t.Fatalf("the first restart is %q, want the unnumbered form", got)
	}
	if got := markRunID("run-x-n1-restart", "restart"); got != "run-x-n1-002-restart" {
		t.Fatalf("the second restart is %q", got)
	}
}

func TestARunIDKeepsItsKindSuffixAndWalksBackToTheNodesFirstRun(t *testing.T) {
	id := markRunID(markRunID(markRunID("run-x-n1", "restart"), "restart"), "restart")
	if id != "run-x-n1-003-restart" {
		t.Fatalf("the third restart is %q", id)
	}
	want := []string{"run-x-n1-002-restart", "run-x-n1-restart", "run-x-n1"}
	got := runIDAncestors(id)
	if len(got) != len(want) {
		t.Fatalf("ancestors = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ancestors = %v, want %v", got, want)
		}
	}
	if runIDAncestors("run-x-n1") != nil {
		t.Fatal("a node's own id has no ancestors")
	}
}

func TestAdapterRefusalWordsClassifyAnExit2AsRefusedNotInfrastructure(t *testing.T) {
	code := 2
	rec := run.Record{State: run.StateExited, ExitCode: &code}
	log := "noise\nrun-pi: refused: the brief 'b' has no '## Fence' section\n"
	if got := ClassifyAttempt(rec, log); got != AttemptRefused {
		t.Fatalf("an adapter refusal classified %q, want %q", got, AttemptRefused)
	}
	if words, _ := AdapterRefusal(log); words != "the brief 'b' has no '## Fence' section" {
		t.Fatalf("words = %q", words)
	}
	if got := ClassifyAttempt(rec, "usage: x\n"); got != AttemptInfrastructure {
		t.Fatalf("an exit 2 with no refusal words classified %q, want infrastructure", got)
	}
	if got := ClassifyAttempt(rec, "see: refused: in prose\n"); got != AttemptInfrastructure {
		t.Fatalf("prose containing the marker classified %q", got)
	}
}
