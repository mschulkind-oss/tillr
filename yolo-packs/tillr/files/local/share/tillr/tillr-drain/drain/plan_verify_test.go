package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanGuidanceBrief(t *testing.T) {
	root := t.TempDir()
	s := &LaneStager{Root: root}
	spec := StageSpec{RunID: "run-a", Node: &WorkflowStage{ID: "plan", Seat: "plan", Access: "read_only"}}
	brief, err := s.workflowNodeBrief(spec, "brief", true)
	if err != nil || strings.Contains(brief, "Model-use guidance") {
		t.Fatalf("missing guidance: %q %v", brief, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config/tillr"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "config/tillr/plan-guidance.md"), "cheap models for mechanical work")
	brief, err = s.workflowNodeBrief(spec, "brief", true)
	if err != nil || !strings.Contains(brief, "cheap models for mechanical work") {
		t.Fatalf("guidance: %q %v", brief, err)
	}
	spec.Node.Seat = "build"
	brief, err = s.workflowNodeBrief(spec, "brief", true)
	if err != nil || strings.Contains(brief, "cheap models for mechanical work") {
		t.Fatalf("non-plan guidance: %q %v", brief, err)
	}
	_ = os.Remove(filepath.Join(root, "config/tillr/plan-guidance.md"))
}
