package export

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/mschulkind/tillr/internal/models"
)

// statusEmoji returns a status indicator for Mermaid node labels.
func statusEmoji(status string) string {
	switch status {
	case "done":
		return "✅"
	case "implementing":
		return "🔨"
	case "planning":
		return "📐"
	case "agent-qa", "human-qa":
		return "🧪"
	case "blocked":
		return "🚫"
	default:
		return "📋"
	}
}

// statusStyle returns a Mermaid style definition for a feature status.
func statusStyle(status string) string {
	switch status {
	case "done":
		return "fill:#2ecc71,stroke:#27ae60,color:#000"
	case "implementing":
		return "fill:#f1c40f,stroke:#f39c12,color:#000"
	case "agent-qa", "human-qa":
		return "fill:#e67e22,stroke:#d35400,color:#000"
	case "blocked":
		return "fill:#e74c3c,stroke:#c0392b,color:#fff"
	case "planning":
		return "fill:#9b59b6,stroke:#8e44ad,color:#fff"
	default:
		return "fill:#3498db,stroke:#2980b9,color:#fff"
	}
}

// dotStatusColor returns a Graphviz DOT fill color for a feature status.
func dotStatusColor(status string) string {
	switch status {
	case "done":
		return "#2ecc71"
	case "implementing":
		return "#f1c40f"
	case "agent-qa", "human-qa":
		return "#e67e22"
	case "blocked":
		return "#e74c3c"
	case "planning":
		return "#9b59b6"
	default:
		return "#3498db"
	}
}

// dotFontColor returns a contrasting font color for DOT nodes.
func dotFontColor(status string) string {
	switch status {
	case "blocked", "planning":
		return "white"
	default:
		return "black"
	}
}

// sanitizeID creates a safe Mermaid/DOT node identifier from a feature ID.
var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func sanitizeID(id string) string {
	return nonAlphaNum.ReplaceAllString(id, "_")
}

// Diagram exports a feature dependency diagram.
// Supported formats: "mermaid" (default), "dot" (Graphviz DOT).
func Diagram(features []models.Feature, milestones []models.Milestone, w io.Writer, format string) error {
	switch format {
	case "dot", "graphviz":
		return DiagramDOT(features, milestones, w)
	default:
		return DiagramMermaid(features, milestones, w)
	}
}

// DiagramMermaid writes a Mermaid flowchart of features grouped by milestone.
func DiagramMermaid(features []models.Feature, milestones []models.Milestone, w io.Writer) error {
	pr := newPrinter(w)

	if len(features) == 0 {
		pr.println("graph TD")
		pr.println("    empty[No features]")
		return pr.err
	}

	pr.println("graph TD")

	// Build milestone lookup
	msMap := make(map[string]string, len(milestones))
	for _, m := range milestones {
		msMap[m.ID] = m.Name
	}

	// Group features by milestone
	type group struct {
		name     string
		features []models.Feature
	}
	grouped := map[string]*group{}
	var order []string
	var ungrouped []models.Feature

	for _, f := range features {
		if f.MilestoneID != "" {
			g, ok := grouped[f.MilestoneID]
			if !ok {
				name := msMap[f.MilestoneID]
				if name == "" {
					name = f.MilestoneName
				}
				if name == "" {
					name = f.MilestoneID
				}
				g = &group{name: name}
				grouped[f.MilestoneID] = g
				order = append(order, f.MilestoneID)
			}
			g.features = append(g.features, f)
		} else {
			ungrouped = append(ungrouped, f)
		}
	}

	// Collect all styled node IDs
	var styledNodes []models.Feature

	// Emit milestones as subgraphs
	for _, msID := range order {
		g := grouped[msID]
		pr.printf("    subgraph %q\n", g.name)
		for _, f := range g.features {
			nodeID := sanitizeID(f.ID)
			label := fmt.Sprintf("%s %s", f.Name, statusEmoji(f.Status))
			pr.printf("        %s[%s]\n", nodeID, escapeMermaidLabel(label))
			styledNodes = append(styledNodes, f)
		}
		pr.println("    end")
	}

	// Emit ungrouped features
	for _, f := range ungrouped {
		nodeID := sanitizeID(f.ID)
		label := fmt.Sprintf("%s %s", f.Name, statusEmoji(f.Status))
		pr.printf("    %s[%s]\n", nodeID, escapeMermaidLabel(label))
		styledNodes = append(styledNodes, f)
	}

	// Build a set of known feature IDs for edge validation
	known := make(map[string]bool, len(features))
	for _, f := range features {
		known[f.ID] = true
	}

	// Emit edges (feature --> dependency)
	for _, f := range features {
		for _, dep := range f.DependsOn {
			if known[dep] {
				pr.printf("    %s --> %s\n", sanitizeID(f.ID), sanitizeID(dep))
			}
		}
	}

	// Emit styles
	for _, f := range styledNodes {
		pr.printf("    style %s %s\n", sanitizeID(f.ID), statusStyle(f.Status))
	}

	return pr.err
}

// DiagramDOT writes a Graphviz DOT representation of features grouped by milestone.
func DiagramDOT(features []models.Feature, milestones []models.Milestone, w io.Writer) error {
	pr := newPrinter(w)

	pr.println("digraph tillr {")
	pr.println("    rankdir=TD;")
	pr.println("    node [shape=box, style=filled, fontname=\"Helvetica\"];")

	if len(features) == 0 {
		pr.println("    empty [label=\"No features\"];")
		pr.println("}")
		return pr.err
	}

	// Build milestone lookup
	msMap := make(map[string]string, len(milestones))
	for _, m := range milestones {
		msMap[m.ID] = m.Name
	}

	// Group features by milestone
	type group struct {
		name     string
		features []models.Feature
	}
	grouped := map[string]*group{}
	var order []string
	var ungrouped []models.Feature

	for _, f := range features {
		if f.MilestoneID != "" {
			g, ok := grouped[f.MilestoneID]
			if !ok {
				name := msMap[f.MilestoneID]
				if name == "" {
					name = f.MilestoneName
				}
				if name == "" {
					name = f.MilestoneID
				}
				g = &group{name: name}
				grouped[f.MilestoneID] = g
				order = append(order, f.MilestoneID)
			}
			g.features = append(g.features, f)
		} else {
			ungrouped = append(ungrouped, f)
		}
	}

	// Emit milestones as subgraphs (DOT uses cluster_ prefix)
	for i, msID := range order {
		g := grouped[msID]
		pr.printf("    subgraph cluster_%d {\n", i)
		pr.printf("        label=%q;\n", g.name)
		pr.println("        style=dashed;")
		for _, f := range g.features {
			nodeID := sanitizeID(f.ID)
			label := fmt.Sprintf("%s %s", f.Name, statusEmoji(f.Status))
			pr.printf("        %s [label=%q, fillcolor=%q, fontcolor=%q];\n",
				nodeID, label, dotStatusColor(f.Status), dotFontColor(f.Status))
		}
		pr.println("    }")
	}

	// Emit ungrouped features
	for _, f := range ungrouped {
		nodeID := sanitizeID(f.ID)
		label := fmt.Sprintf("%s %s", f.Name, statusEmoji(f.Status))
		pr.printf("    %s [label=%q, fillcolor=%q, fontcolor=%q];\n",
			nodeID, label, dotStatusColor(f.Status), dotFontColor(f.Status))
	}

	// Build a set of known feature IDs for edge validation
	known := make(map[string]bool, len(features))
	for _, f := range features {
		known[f.ID] = true
	}

	// Emit edges
	for _, f := range features {
		for _, dep := range f.DependsOn {
			if known[dep] {
				pr.printf("    %s -> %s;\n", sanitizeID(f.ID), sanitizeID(dep))
			}
		}
	}

	pr.println("}")
	return pr.err
}

// escapeMermaidLabel escapes characters that would break Mermaid syntax.
func escapeMermaidLabel(s string) string {
	s = strings.ReplaceAll(s, "[", "(")
	s = strings.ReplaceAll(s, "]", ")")
	s = strings.ReplaceAll(s, "\"", "'")
	return s
}
