package drain

import "testing"

func TestNormalizeEvidenceKeepsWhatTellsFailuresApart(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		same       bool
	}{
		{"run id", "/w/.tillr/loop/run-x-a1/log: fail", "/w/.tillr/loop/run-x-a2/log: fail", true},
		{"duration", "check ran 3024 ms, over by 1024 ms", "check ran 2999 ms, over by 999 ms", true},
		{"timestamp", "at 2026-11-17T01:02:03.5Z failed", "at 2026-11-18T09:08:07Z failed", true},
		{"sha", "base 0123456789abcdef0123456789abcdef01234567", "base fedcba9876543210fedcba9876543210fedcba98", true},
		{"watchdog", "watchdog unavailable: x\nerror a", "error a", true},
		{"line number", "doc.md:199: dead anchor", "doc.md:244: dead anchor", false},
		{"message", "error: dead anchor", "error: missing file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ra, rb := "run-x-a1", "run-x-a2"
			if got := evidenceDigest(tc.a, ra) == evidenceDigest(tc.b, rb); got != tc.same {
				t.Fatalf("same=%v, want %v:\n%q\n%q", got, tc.same, normalizeEvidence(tc.a, ra), normalizeEvidence(tc.b, rb))
			}
		})
	}
}

func TestReturnedEndingReturnsRefusalForEveryDefinition(t *testing.T) {
	for _, landing := range []bool{true, false} {
		d := defaultDefinition()
		if !landing {
			d.Completes = "hook"
		}
		p := planner{
			definition: &d,
			latest:     map[string]string{"finding": "run-finding"},
			history:    runHistory{refusals: map[string]string{"run-finding": RefusalNoOp}},
		}
		ending := p.returnedEnding("finding")
		// A refused run goes back to its worker uncounted whatever the definition
		// completes by (the owner's ruling of 2026-11-17): a held refusal with no
		// mover would leave the item queued with nothing to move it.
		if ending == nil || ending.Cause != RefusalNoOp {
			t.Fatalf("refusal was not returned (landing=%v): %+v", landing, ending)
		}
	}
}

func TestEndingKey(t *testing.T) {
	a := []EndingCheck{{CheckID: "go", Digest: "1"}, {CheckID: "documents", Digest: "2"}}
	b := []EndingCheck{{CheckID: "documents", Digest: "2"}, {CheckID: "go", Digest: "1"}}
	if endingKey(RefusalGate, a, "", "r1") != endingKey(RefusalGate, b, "", "r2") {
		t.Fatal("the check order changed the key")
	}
	if endingKey(RefusalGate, a, "", "r1") == endingKey(RefusalGate, []EndingCheck{{CheckID: "go", Digest: "1"}}, "", "r1") {
		t.Fatal("a different failing set has the same key")
	}
	if endingKey(RefusalFence, nil, "escaped run-a-a1/x", "run-a-a1") != endingKey(RefusalFence, nil, "escaped run-a-a2/x", "run-a-a2") {
		t.Fatal("a detail differing only by its run id has another key")
	}
	if endingKey(RefusalFence, nil, "x", "r") == endingKey(RefusalLand, nil, "x", "r") {
		t.Fatal("two causes share a key")
	}
	if endingKey("", nil, "x", "r") != "" {
		t.Fatal("an ending with no cause has a key")
	}
}

func TestRepeatedEnding(t *testing.T) {
	e := func(run, producer, cause, key string) chainEnding { return chainEnding{run, producer, cause, key} }
	for _, tc := range []struct {
		name   string
		chain  []chainEnding
		latest chainEnding
		want   string
	}{
		{"first ending", nil, e("a1", "build", "gate", "k"), ""},
		{"same as the last", []chainEnding{e("a1", "build", "gate", "k")}, e("a2", "build", "gate", "k"), "a1"},
		{"different evidence", []chainEnding{e("a1", "build", "gate", "k1")}, e("a2", "build", "gate", "k2"), ""},
		{"different cause", []chainEnding{e("a1", "build", "gate", "k")}, e("a2", "build", "no-op", "k"), ""},
		{"different producer", []chainEnding{e("a1", "design", "gate", "k")}, e("a2", "build", "gate", "k"), ""},
		{"alternating is caught on the third", []chainEnding{e("a1", "build", "gate", "x"), e("a2", "build", "gate", "y")}, e("a3", "build", "gate", "x"), "a1"},
		// The latest matching ending is the one named, so a repeat after the owner moved an
		// earlier repeat on names a new pair (and raises a new conflict), never the old one.
		{"the latest match is named", []chainEnding{e("a1", "build", "no-op", "k"), e("a2", "build", "no-op", "k"), e("a3", "build", "no-op", "k")}, e("a4", "build", "no-op", "k"), "a3"},
		{"five different failures repeat nothing", []chainEnding{e("a1", "build", "gate", "1"), e("a2", "build", "gate", "2"), e("a3", "build", "no-op", "3"), e("a4", "build", "fence", "4")}, e("a5", "build", "gate", "5"), ""},
		{"an unkeyed ending repeats nothing", []chainEnding{e("a1", "build", "gate", "")}, e("a2", "build", "gate", ""), ""},
		{"the run itself is not an earlier ending", []chainEnding{e("a2", "build", "gate", "k")}, e("a2", "build", "gate", "k"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := repeatedEnding(tc.chain, tc.latest)
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("repeatedEnding = %q %v, want %q", got, ok, tc.want)
			}
		})
	}
}

// A landing conflict's key is its conflicting paths and the digest of its three-way hunks:
// the same conflict on another trunk sha or run is the same key, and a different hunk or
// path is another (the owner's ruling of 2026-11-17 replaced the one conflict-fix run with
// this comparison).
func TestConflictEndingKey(t *testing.T) {
	sha1, sha2 := "0123456789abcdef0123456789abcdef01234567", "fedcba9876543210fedcba9876543210fedcba98"
	hunk := func(sha, body string) string {
		return "### base.txt\n<<<<<<< " + sha + "\n" + body + "\n=======\ncandidate\n>>>>>>> " + sha + "\n"
	}
	a := conflictEndingKey([]string{"CONFLICT (content): Merge conflict in base.txt", "CONFLICT (content): Merge conflict in a.txt"}, hunk(sha1, "trunk"), "run-x-a1")
	b := conflictEndingKey([]string{"CONFLICT (content): Merge conflict in a.txt", "CONFLICT (content): Merge conflict in base.txt"}, hunk(sha2, "trunk"), "run-x-a2")
	if a == "" || a != b {
		t.Fatalf("the same conflict on another sha, run or path order has another key:\n%s\n%s", a, b)
	}
	if a == conflictEndingKey([]string{"CONFLICT (content): Merge conflict in base.txt", "CONFLICT (content): Merge conflict in a.txt"}, hunk(sha1, "trunk moved again"), "run-x-a1") {
		t.Fatal("a different hunk has the same key")
	}
	if a == conflictEndingKey([]string{"CONFLICT (content): Merge conflict in base.txt"}, hunk(sha1, "trunk"), "run-x-a1") {
		t.Fatal("a different path set has the same key")
	}
	if conflictEndingKey(nil, "", "run-x-a1") != "" {
		t.Fatal("a conflict that names nothing has a key")
	}
}
