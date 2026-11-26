package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
)

func TestBriefPolicySeatsAndPiIsolation(t *testing.T) {
	template, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	lane := filepath.Join(base, "lane")
	target := filepath.Join(lane, "worktree")
	for _, d := range []string{bin, target} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\nenv -0 | sort -z > \"$CAPTURE_ENV\"\n"
	if err := writeForkSafe(filepath.Join(bin, "pi"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	old := &Policy{Seats: map[string]string{"build": "pi", "design": "pi", "review": "pi", "scout": "pi"}, ReadOnlySeats: []string{"scout"}, SeatPurposes: map[string]string{"build": "Build the change.", "scout": "Find evidence."}}
	expanded := &Policy{Seats: map[string]string{}, ReadOnlySeats: []string{"scout", "test-run"}, SeatPurposes: map[string]string{}}
	for k, v := range old.Seats {
		expanded.Seats[k] = v
	}
	for k, v := range old.SeatPurposes {
		expanded.SeatPurposes[k] = v
	}
	for _, seat := range []string{"plan", "verify", "edit", "test-write", "test-run", "docs"} {
		expanded.Seats[seat] = "pi"
		expanded.SeatPurposes[seat] = "Purpose for " + seat + "."
	}
	var priorArgs, priorEnv []byte
	for i, policy := range []*Policy{old, expanded} {
		recorded := refusalStagedItem("item-a")
		spec := refusalSpec(recorded.ItemID)
		spec.Policy = policy
		values, err := briefValuesFor(recorded, spec, target, "lane worktree", strings.Repeat("0", 40), base, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		brief, err := renderBrief(template, recorded.ItemID, values)
		if err != nil {
			t.Fatal(err)
		}
		_, section, ok := strings.Cut(brief, "## Seats\n")
		if !ok {
			t.Fatal("missing Seats section")
		}
		section = strings.SplitN(section, "\n## ", 2)[0]
		if !strings.Contains(section, "tillr runs as nodes") || !strings.Contains(section, "worker cannot start") {
			t.Fatalf("seat boundary absent: %s", section)
		}
		if !strings.Contains(section, "Give mechanical searching, editing and checking to the cheaper sub-agents your own harness offers.") || !strings.Contains(section, "Your harness's configuration chooses those sub-agents' models; tillr's policy does not.") {
			t.Fatalf("delegation paragraph absent: %s", section)
		}
		got := map[string]string{}
		for _, line := range strings.Split(section, "\n") {
			if strings.HasPrefix(line, "- `") {
				name, rest, ok := strings.Cut(strings.TrimPrefix(line, "- `"), "`")
				if !ok {
					t.Fatalf("bad line %q", line)
				}
				if _, exists := got[name]; exists {
					t.Fatalf("duplicate seat %s", name)
				}
				got[name] = rest
			}
		}
		want := map[string]string{}
		for seat := range policy.Seats {
			line := ""
			for _, ro := range policy.ReadOnlySeats {
				if seat == ro {
					line += " (read-only)"
				}
			}
			if purpose := policy.SeatPurposes[seat]; purpose != "" {
				line += " — " + purpose
			}
			want[seat] = line
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seats got %#v want %#v", got, want)
		}
		if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(brief), 0644); err != nil {
			t.Fatal(err)
		}
		argsPath, envPath := filepath.Join(base, "args"), filepath.Join(base, "env")
		cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
		cmd.Dir = target
		cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + base, "CAPTURE_ARGS=" + argsPath, "CAPTURE_ENV=" + envPath, "TILLR_CHILD_SESSIONS_ROOT=" + filepath.Join(base, "children"), "TILLR_CHILD_SESSIONS_MARKER=" + filepath.Join(base, "marker"), "TILLR_PI_FEATURES=parked-tool-calls"}
		if out, err := cmd.CombinedOutput(); err != nil {
			stderr, _ := os.ReadFile(filepath.Join(lane, "stream.err"))
			t.Fatalf("adapter: %v %s %s", err, out, stderr)
		}
		args, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatal(err)
		}
		env, err := os.ReadFile(envPath)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			priorArgs, priorEnv = args, env
		} else if !reflect.DeepEqual(args, priorArgs) || !reflect.DeepEqual(env, priorEnv) {
			t.Fatalf("policy seats changed pi argv or environment: %q vs %q; %q vs %q", priorArgs, args, priorEnv, env)
		}
	}
}

func TestBriefSeatLinesListOnlyAndMultilinePurpose(t *testing.T) {
	p := &Policy{SeatLists: map[string][]string{"list-only": {"backend"}}, SeatPurposes: map[string]string{"list-only": "Find\n  evidence."}, ReadOnlySeats: []string{"list-only"}}
	if got, want := seatLines(p), "- `list-only` (read-only) — Find evidence."; got != want {
		t.Fatalf("seat line = %q; want %q", got, want)
	}
}

func TestBriefSeatsFollowLoopPolicy(t *testing.T) {
	f, l := designSeatFixture(t)
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	l.cfg.Policy.Seats["fixture-seat"] = p.decision.RunRequest.Backend
	l.cfg.Policy.ReadOnlySeats = append(l.cfg.Policy.ReadOnlySeats, "fixture-seat")
	if l.cfg.Policy.SeatPurposes == nil {
		l.cfg.Policy.SeatPurposes = map[string]string{}
	}
	l.cfg.Policy.SeatPurposes["fixture-seat"] = "A dynamically supplied purpose."
	staged, err := l.stageDispatches(context.Background(), []Decision{p.decision}, base, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage: %v %+v", err, staged)
	}
	brief, err := os.ReadFile(filepath.Join(f.laneRoot, p.decision.LaneID, BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), "- `fixture-seat` (read-only) — A dynamically supplied purpose.") {
		t.Fatalf("staged brief did not use current policy: %s", brief)
	}
}

func TestBriefDesignDocumentRuleBySeat(t *testing.T) {
	template, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	renderFor := func(recorded stagedItem, spec StageSpec, capability string) string {
		t.Helper()
		values, err := briefValuesFor(recorded, spec, "/tmp/target", "lane worktree", strings.Repeat("0", 40), "/tmp/durable", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		brief, err := renderBriefWithEnding(template, recorded.ItemID, values, capability)
		if err != nil {
			t.Fatal(err)
		}
		return brief
	}

	// 1. Single-worker item seats under both ending capabilities.
	carrySeats := []string{"", "build", "edit", "docs", "test-write"}
	omitSeats := []string{"design", "plan", "verify", "scout", "test-run", "review", item.SeatTriage}
	for _, capability := range []string{run.EndingFooter, run.EndingCallSuspends} {
		for _, seat := range carrySeats {
			recorded := refusalStagedItem("item-a")
			recorded.Seat = seat
			spec := refusalSpec(recorded.ItemID)
			brief := renderFor(recorded, spec, capability)
			if !strings.Contains(brief, DesignDocumentRule) {
				t.Fatalf("capability=%s seat=%q: expected DesignDocumentRule in brief", capability, seat)
			}
		}
		for _, seat := range omitSeats {
			recorded := refusalStagedItem("item-a")
			recorded.Seat = seat
			spec := refusalSpec(recorded.ItemID)
			brief := renderFor(recorded, spec, capability)
			if strings.Contains(brief, DesignDocumentRule) {
				t.Fatalf("capability=%s seat=%q: unexpected DesignDocumentRule in brief", capability, seat)
			}
		}
	}

	// 2. Workflow node seats follow spec.Node.Seat rather than recorded.Seat.
	for _, nodeSeat := range []string{"plan", "verify", "scout", "test-run", "design", "review", ""} {
		recorded := refusalStagedItem("item-a")
		recorded.Seat = "build"
		spec := refusalSpec(recorded.ItemID)
		spec.Node = &WorkflowStage{ID: "node-1", Kind: "agent", Seat: nodeSeat, Access: "read_only"}
		brief := renderFor(recorded, spec, run.EndingFooter)
		if strings.Contains(brief, DesignDocumentRule) {
			t.Fatalf("build item with node seat %q: unexpected DesignDocumentRule in brief", nodeSeat)
		}
	}
	for _, nodeSeat := range []string{"build", "edit", "docs", "test-write"} {
		recorded := refusalStagedItem("item-a")
		recorded.Seat = "design"
		spec := refusalSpec(recorded.ItemID)
		spec.Node = &WorkflowStage{ID: "node-1", Kind: "agent", Seat: nodeSeat, Access: "write"}
		brief := renderFor(recorded, spec, run.EndingFooter)
		if !strings.Contains(brief, DesignDocumentRule) {
			t.Fatalf("design item with node seat %q: expected DesignDocumentRule in brief", nodeSeat)
		}
	}

	// 3. Conflict/triage dispatches on a build item act on the design seat and omit DesignDocumentRule.
	recorded := refusalStagedItem("item-a")
	recorded.Seat = "build"
	spec := refusalSpec(recorded.ItemID)
	spec.BlockConflict = &item.Conflict{
		ConflictID: "conflict-1",
		RunID:      "run-1",
		BodyRef:    "docs/design/example.md#item-a",
	}
	if brief := renderFor(recorded, spec, run.EndingFooter); strings.Contains(brief, DesignDocumentRule) {
		t.Fatalf("block conflict dispatch on build item: unexpected DesignDocumentRule in brief")
	}

	// 4. End-to-end LaneStager.Stage check writing brief.md on disk.
	ctx := context.Background()
	f := newRefusalLoopFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}
	stageCases := []struct {
		seat string
		want bool
	}{
		{seat: "build", want: true},
		{seat: "edit", want: true},
		{seat: "docs", want: true},
		{seat: "test-write", want: true},
		{seat: "design", want: false},
	}
	for i, tc := range stageCases {
		spec := StageSpec{
			LaneID:     "lane-1",
			ItemID:     "item-a",
			RunID:      "run-stage-" + tc.seat,
			Backend:    "fixture",
			BaseSHA:    base,
			WorkingDir: WorktreeDir,
			Node:       &WorkflowStage{ID: "step-" + tc.seat, Kind: "agent", Seat: tc.seat, Access: "write"},
		}
		if i > 0 {
			spec.Continue = true
		}
		if err := stager.Stage(ctx, spec); err != nil {
			t.Fatalf("Stage(%s): %v", tc.seat, err)
		}
		raw, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", BriefFile))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", tc.seat, err)
		}
		got := strings.Contains(string(raw), DesignDocumentRule)
		if got != tc.want {
			t.Fatalf("Stage(%s): contains DesignDocumentRule = %v, want %v", tc.seat, got, tc.want)
		}
	}
}

func TestBriefDesignProvenanceRuleBySeat(t *testing.T) {
	const wantSentence = "A decision the designer or operator made (including a filed request and any mechanism, format, field names, or rejected alternatives the design seat writes) is recorded as theirs — never presented as the owner's ruling, and marked in prose and Decision Ledger rows as the designer's (or from an operator request) that the owner may overturn — and only a quoted owner statement is recorded as an owner ruling (quoting the owner's dated words verbatim)."
	if DesignProvenanceRule != wantSentence {
		t.Fatalf("DesignProvenanceRule = %q, want %q", DesignProvenanceRule, wantSentence)
	}
	template, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	renderFor := func(recorded stagedItem, spec StageSpec, capability string) string {
		t.Helper()
		values, err := briefValuesFor(recorded, spec, "/tmp/target", "lane worktree", strings.Repeat("0", 40), "/tmp/durable", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		brief, err := renderBriefWithEnding(template, recorded.ItemID, values, capability)
		if err != nil {
			t.Fatal(err)
		}
		return brief
	}

	// 1. Single-worker item seats under both ending capabilities.
	omitSeats := []string{"", "build", "edit", "docs", "test-write", "plan", "verify", "scout", "test-run", "review", item.SeatTriage}
	for _, capability := range []string{run.EndingFooter, run.EndingCallSuspends} {
		recorded := refusalStagedItem("item-a")
		recorded.Seat = "design"
		spec := refusalSpec(recorded.ItemID)
		if brief := renderFor(recorded, spec, capability); !strings.Contains(brief, wantSentence) {
			t.Fatalf("capability=%s seat=design: expected provenance rule in brief:\n%s", capability, brief)
		}
		for _, seat := range omitSeats {
			recorded := refusalStagedItem("item-a")
			recorded.Seat = seat
			spec := refusalSpec(recorded.ItemID)
			if brief := renderFor(recorded, spec, capability); strings.Contains(brief, wantSentence) {
				t.Fatalf("capability=%s seat=%q: unexpected provenance rule in brief", capability, seat)
			}
		}
	}

	// 2. Workflow node seats follow spec.Node.Seat rather than recorded.Seat.
	{
		recorded := refusalStagedItem("item-a")
		recorded.Seat = "build"
		spec := refusalSpec(recorded.ItemID)
		spec.Node = &WorkflowStage{ID: "node-design", Kind: "agent", Seat: "design", Access: "write"}
		if brief := renderFor(recorded, spec, run.EndingFooter); !strings.Contains(brief, wantSentence) {
			t.Fatalf("build item with design node: expected provenance rule in brief:\n%s", brief)
		}
	}
	for _, nodeSeat := range omitSeats {
		recorded := refusalStagedItem("item-a")
		recorded.Seat = "design"
		spec := refusalSpec(recorded.ItemID)
		spec.Node = &WorkflowStage{ID: "node-1", Kind: "agent", Seat: nodeSeat, Access: "read_only"}
		if brief := renderFor(recorded, spec, run.EndingFooter); strings.Contains(brief, wantSentence) {
			t.Fatalf("design item with node seat %q: unexpected provenance rule in brief", nodeSeat)
		}
	}

	// 3. Conflict and triage dispatches on a build item act on the design seat and carry the rule.
	for _, c := range []item.Conflict{
		{ConflictID: "conflict-doc", RunID: "run-1", BodyRef: "docs/design/example.md#item-a"},
		{ConflictID: "conflict-req", RunID: "run-1"},
		{ConflictID: "triage-item-a-v1", RunID: "run-1", BodyRef: "docs/design/example.md#item-a", TriageVersion: "v1"},
	} {
		recorded := refusalStagedItem("item-a")
		recorded.Seat = "build"
		spec := refusalSpec(recorded.ItemID)
		conflict := c
		spec.BlockConflict = &conflict
		if brief := renderFor(recorded, spec, run.EndingFooter); !strings.Contains(brief, wantSentence) {
			t.Fatalf("block conflict %s: expected provenance rule in brief:\n%s", c.ConflictID, brief)
		}
	}

	// 4. End-to-end LaneStager.Stage (Git worktree) and stagePlain (plain directory).
	ctx := context.Background()
	f := newRefusalLoopFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: template,
	}
	for i, tc := range []struct {
		seat string
		want bool
	}{
		{seat: "design", want: true},
		{seat: "build", want: false},
	} {
		spec := StageSpec{
			LaneID:     "lane-1",
			ItemID:     "item-a",
			RunID:      "run-prov-" + tc.seat,
			Backend:    "fixture",
			BaseSHA:    base,
			WorkingDir: WorktreeDir,
			Node:       &WorkflowStage{ID: "step-" + tc.seat, Kind: "agent", Seat: tc.seat, Access: "write"},
		}
		if i > 0 {
			spec.Continue = true
		}
		if err := stager.Stage(ctx, spec); err != nil {
			t.Fatalf("Stage(%s): %v", tc.seat, err)
		}
		raw, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", BriefFile))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", tc.seat, err)
		}
		if got := strings.Contains(string(raw), wantSentence); got != tc.want {
			t.Fatalf("Stage(%s): contains provenance rule = %v, want %v", tc.seat, got, tc.want)
		}
	}

	plainTemplate := filepath.Join(t.TempDir(), "plain-brief.md")
	if err := os.WriteFile(plainTemplate, []byte("# {{ITEM_ID}}: {{TITLE}}\n{{SETTLED}}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	plainStager := &LaneStager{
		Root:     f.root,
		LaneRoot: t.TempDir(),
		Store:    f.storePath,
	}
	for _, tc := range []struct {
		lane string
		seat string
		want bool
	}{
		{lane: "plain-design", seat: "design", want: true},
		{lane: "plain-build", seat: "build", want: false},
	} {
		spec := StageSpec{
			LaneID:     tc.lane,
			ItemID:     "item-a",
			RunID:      "run-" + tc.lane,
			Backend:    "fixture",
			WorkingDir: PlainDir,
			Node:       &WorkflowStage{ID: "step-" + tc.seat, Kind: "agent", Seat: tc.seat, Access: "write"},
		}
		if err := plainStager.stagePlain(ctx, spec, definition.Definition{Name: "plain-fixture", BriefTemplate: plainTemplate}); err != nil {
			t.Fatalf("stagePlain(%s): %v", tc.seat, err)
		}
		raw, err := os.ReadFile(filepath.Join(plainStager.LaneRoot, tc.lane, BriefFile))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", tc.lane, err)
		}
		if got := strings.Contains(string(raw), wantSentence); got != tc.want {
			t.Fatalf("stagePlain(%s): contains provenance rule = %v, want %v", tc.seat, got, tc.want)
		}
		// Whole words only: the brief names its own directory, and a scratch path such as
		// "go-shards-1" holds "sha" without the prose using the word.
		for _, word := range []string{"sha", "fence", "trunk"} {
			if regexp.MustCompile(`(?i)\b` + word + `\b`).Match(raw) {
				t.Fatalf("stagePlain(%s): plain prose carries Git word %q: %s", tc.seat, word, raw)
			}
		}
	}
}
