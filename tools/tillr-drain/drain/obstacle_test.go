package drain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The worker's endings are judged by two readers: this package, and the pi adapter's
// keep-going extension, which answers a missing output inside the session. Both are tested
// against the one case file, so the two cannot disagree about what an obstacle is.
func TestAWorkerEndsWithItsCommitOrACompleteObstacle(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "tillr-run", "backends", "pi", "testdata", "obstacle-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string `json:"name"`
		Report string `json:"report"`
		Ending bool   `json:"ending"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 10 {
		t.Fatalf("the case file holds %d cases", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			problem := workerEndingProblem(c.Report)
			if (problem == "") != c.Ending {
				t.Fatalf("ending=%v, problem %q", problem == "", problem)
			}
			if c.Kind != "" {
				if got := parseObstacle(c.Report).Kind; got != c.Kind {
					t.Fatalf("kind %q, want %q", got, c.Kind)
				}
			}
			if !c.Ending && !strings.Contains(missingOutputDetail(problem), "OBSTACLE: <kind>") {
				t.Fatal("a missing output is returned without the contract")
			}
		})
	}
}

func TestAnObstacleFieldRunsToTheNextField(t *testing.T) {
	o := parseObstacle("OBSTACLE: tooling\nCOMMAND: go test ./...\nEVIDENCE:\npanic: timed out\nTestX (10m0s)\nPATHS: a.go, b.go\nRESULT: blocked")
	if o.Field("EVIDENCE") != "panic: timed out\nTestX (10m0s)" {
		t.Fatalf("evidence %q", o.Field("EVIDENCE"))
	}
	if got := strings.Join(o.List("PATHS"), "|"); got != "a.go|b.go" {
		t.Fatalf("paths %q", got)
	}
}

// Each kind the design seat takes is handed over with its own instruction, and the kinds
// the loop acts on itself need none.
func TestEachDesignKindCarriesItsOwnInstruction(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range []string{ObstacleDefinition, ObstacleOwner, ObstacleSeat} {
		text := obstacleDesignInstruction(Obstacle{Kind: kind, Fields: map[string]string{"QUESTION": "q?", "TRIED": "t", "MISSING": "m"}})
		if text == "" || seen[text] {
			t.Fatalf("%s: instruction %q", kind, text)
		}
		seen[text] = true
	}
	for _, kind := range []string{ObstacleTrunk, ObstacleTooling} {
		if obstacleDesignInstruction(Obstacle{Kind: kind}) != "" {
			t.Fatalf("%s is the loop's to act on, not the design seat's", kind)
		}
	}
}

func TestRequestObstacleDesignInstructionCarriesProvenanceRule(t *testing.T) {
	const wantSentence = "A decision the designer or operator made (including a filed request and any mechanism, format, field names, or rejected alternatives the design seat writes) is recorded as theirs — never presented as the owner's ruling, and marked in prose and Decision Ledger rows as the designer's (or from an operator request) that the owner may overturn — and only a quoted owner statement is recorded as an owner ruling (quoting the owner's dated words verbatim)."
	seen := map[string]bool{}
	for _, kind := range []string{ObstacleDefinition, ObstacleOwner, ObstacleSeat} {
		text := requestObstacleDesignInstruction(Obstacle{Kind: kind, Fields: map[string]string{"PATHS": "x.go", "QUESTION": "q?", "TRIED": "t", "MISSING": "m"}})
		if text == "" || seen[text] {
			t.Fatalf("%s: instruction %q", kind, text)
		}
		if !strings.Contains(text, wantSentence) {
			t.Fatalf("%s: request obstacle instruction omits provenance rule:\n%s", kind, text)
		}
		if !strings.Contains(text, "has no item block") {
			t.Fatalf("%s: request obstacle instruction omits no-item-block rule:\n%s", kind, text)
		}
		seen[text] = true
	}
	for _, kind := range []string{ObstacleTrunk, ObstacleTooling} {
		if requestObstacleDesignInstruction(Obstacle{Kind: kind}) != "" {
			t.Fatalf("%s is the loop's to act on, not the design seat's", kind)
		}
	}
}

// A generated repair has no block to amend, so its obstacles that need a ruling reach the
// owner with a question written from them, and its other reports go back to its worker.
func TestAGeneratedRepairsObstacleIsAnOwnerQuestion(t *testing.T) {
	def := parseObstacle("OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: the criterion contradicts the tree\nRESULT: blocked")
	q := generatedQuestion("fixer-main-abc", def)
	if !strings.Contains(q, "c1") || !strings.Contains(q, "contradicts the tree") {
		t.Fatalf("question %q", q)
	}
	// A report naming the retired fence obstacle is a missing output, not an ending.
	retiredFence := parseObstacle("OBSTACLE: fence\nPATHS: tools/a.go\nEVIDENCE: the test lives there\nRESULT: blocked")
	if retiredFence.Problem == "" {
		t.Fatalf("retired fence obstacle was accepted as valid: %+v", retiredFence)
	}
	if generatedQuestion("fixer-main-abc", parseObstacle("OBSTACLE: trunk\nCHECKS: x\nBASE: y\nRESULT: blocked")) != "" {
		t.Fatal("a trunk obstacle is not a question for the owner")
	}
	if !IsGeneratedRepair("fixer-main-abc") || !IsGeneratedRepair(DefectItemID("go test")) || IsGeneratedRepair("item_041") {
		t.Fatal("generated repair ids")
	}
}

// The defect item is named by its command alone, so two reports of the same defect from
// two items attach to one item.
func TestADefectItemIsNamedByItsCommand(t *testing.T) {
	a := DefectItemID("go test  ./tools/tillr-drain/...")
	b := DefectItemID("go test ./tools/tillr-drain/...\n")
	if a != b || !IsDefectItem(a) || len(a) != len(DefectItemPrefix)+12 {
		t.Fatalf("ids %q %q", a, b)
	}
	if DefectItemID("go test ./tools/tillr-run/...") == a {
		t.Fatal("two commands share a defect item")
	}
}

// A seat obstacle climbs the seat's list and never returns to a model the item already
// named a seat obstacle on; past the end the design seat is asked.
func TestASeatObstacleClimbsTheEscalationList(t *testing.T) {
	p := Policy{Escalate: map[string][]string{"build": {"pi-build", "pi"}}}
	for _, c := range []struct {
		name, from string
		tried      []string
		want       string
	}{
		{"terra to sol", "pi-build", nil, "pi"},
		{"sol is the last", "pi", []string{"pi-build"}, ""},
		{"a backend off the list starts at its head", "claude", nil, "pi-build"},
		{"a tried backend is skipped", "claude", []string{"pi-build"}, "pi"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tried := map[string]bool{c.from: true}
			for _, name := range c.tried {
				tried[name] = true
			}
			if got := p.EscalationNext("", c.from, tried); got != c.want {
				t.Fatalf("next %q, want %q", got, c.want)
			}
		})
	}
	bad := Policy{Seats: map[string]string{"build": "pi"}, Backends: map[string]Backend{"pi": {}}, Escalate: map[string][]string{"build": {"nope"}}}
	if bad.CheckEscalate() == nil {
		t.Fatal("an escalation list naming an unknown backend was accepted")
	}
	unknownSeat := Policy{Seats: map[string]string{"build": "pi"}, Backends: map[string]Backend{"pi": {}}, Escalate: map[string][]string{"desk": {"pi"}}}
	if unknownSeat.CheckEscalate() == nil {
		t.Fatal("an escalation list for an unmapped seat was accepted")
	}
}

// Every shipped policy that names an escalation list passes Check.
func TestTheShippedPoliciesEscalateThroughTheirOwnBackends(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "config", "tillr", "backend-policy*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("policies %v: %v", paths, err)
	}
	escalating := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var p Policy
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if err := p.Check(); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(p.Escalate["build"]) > 0 {
			escalating++
		}
	}
	if escalating == 0 {
		t.Fatal("no shipped policy escalates the build seat")
	}
}
