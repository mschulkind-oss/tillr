package drain

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// build-pieces expands its repeated "piece" stage into the planned work nodes.
func plannedWritePiece(node WorkflowStage) bool {
	return node.Kind == "agent" && node.Access == "write" && path.Base(node.CopiesOf) == "piece"
}

// workflowNodeBrief supplies node instructions and wired files for both lane layouts.
// The fence is the effective write scope computed by staging, not the workflow's declaration.
func (s *LaneStager) workflowNodeBrief(spec StageSpec, brief string, landing bool, fence string) (string, error) {
	if spec.Node == nil {
		return brief, nil
	}
	brief += "\n## Workflow node\n\nRun only node " + spec.Node.ID + " on the " + spec.Node.Seat + " seat; access: " + spec.Node.Access + ".\n"
	if landing && plannedWritePiece(*spec.Node) {
		// The runner recognizes this generated line immediately after the node header.
		brief += "An unchanged piece may finish without a commit.\n"
		brief += "If verification shows this piece's criteria already hold at the staged lane HEAD and the worktree is clean, leave both unchanged. Finish with RESULT: done. This is an exception to the general commit requirement, not an obstacle; do not make an empty commit or call land. If a change is needed, follow the ordinary commit and finishing instructions.\n"
	}
	if spec.Node.Prompt != "" {
		brief += "\nNode task: " + spec.Node.Prompt + "\n"
	}
	if spec.Node.Seat == "plan" {
		guidance, err := os.ReadFile(filepath.Join(s.Root, "config/tillr/plan-guidance.md"))
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if len(guidance) > 0 {
			brief += "\n## Model-use guidance\n\n" + string(guidance) + "\n"
		}
	}
	if landing && spec.Node.Access == "write" && fence != "" {
		brief += "\nThis node may write only these paths:\n" + fence
	}
	dir := nodeOutputsDir(s.Root, spec.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if spec.Node.Access == "read_only" {
		if planAllowsQuickWay(*spec.Node) {
			quickPath := filepath.Join(dir, quickWayFile)
			brief += "Read only unless you take the quick way: when the request is small and clear (one coherent change, no split into pieces, no question for the owner), your first move may be to decide 'do it now' by writing " + quickPath + ", then make the change (changing what the work needs, and nothing else), test it"
			if landing {
				brief += ", and commit once on this lane"
			}
			brief += " before finishing with RESULT: done (write no proposal). Otherwise, do not change files in the target"
			if landing {
				brief += " or commit"
			}
			brief += "; write your findings in the report and finish with RESULT: done, or report a named obstacle when blocked. The item criteria describe the whole workflow, not this node.\n"
		} else {
			brief += "Read only: do not change files in the target"
			if landing {
				brief += " or commit"
			}
			brief += ". Write your findings in the report and finish with RESULT: done, or report a named obstacle when blocked. The item criteria describe the whole workflow, not this node.\n"
		}
	}
	brief += "Write declared outputs only in your output directory: " + dir + ". These files are outside the target"
	if landing {
		brief += " and are not part of the candidate"
	}
	brief += ".\n"
	for _, name := range sortedNames(spec.Node.Outputs) {
		brief += "Output " + name + " (" + spec.Node.Outputs[name] + "): " + filepath.Join(dir, name) + "\n"
		if spec.Node.Outputs[name] == "graph.proposal" {
			allowed, err := json.Marshal(spec.Node.MayReplace)
			if err != nil {
				return "", err
			}
			brief += "This output is optional. To request replacement, write one JSON object with target, workflow, and args. Allowed target/workflow pairs: " + string(allowed) + ". No file means no graph change. A refused proposal leaves the graph unchanged.\n"
		}
	}
	for _, name := range sortedNames(spec.InputPaths) {
		for _, input := range spec.InputPaths[name] {
			brief += "Input " + name + " from node " + input.Producer + ": " + input.Path + "\n"
		}
	}
	// Preserve report-only workflows; explicit inputs receive only their wired files.
	if len(spec.Node.Inputs) == 0 && spec.PreviousReport != "" {
		brief += "Read the previous node report: " + spec.PreviousReport + "\n"
	}
	if len(spec.Node.Inputs) == 0 && strings.TrimSpace(spec.PreviousReportText) != "" {
		brief += "\nPrevious node report:\n" + quoteBriefLines(spec.PreviousReportText) + "\n"
	}
	return brief, nil
}
