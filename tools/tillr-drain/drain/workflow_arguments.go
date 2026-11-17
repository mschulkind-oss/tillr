package drain

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
)

// WorkflowArgument declares a typed parameter. Most bounds only string lists.
type WorkflowArgument struct {
	Type string `json:"type"`
	Most int    `json:"most,omitempty"`
}

// WorkflowPort identifies the executable node that produces an exported file.
type WorkflowPort struct {
	Node   string `json:"node"`
	Output string `json:"output"`
}

// WorkflowScope is either inherit (nil Paths) or a nonempty list of path prefixes.
type WorkflowScope struct {
	Paths    []string
	explicit bool // Distinguishes an omitted scope from an explicit inherit on read-only nodes.
	symbolic bool // Only the file checker creates unresolved parameter scopes.
}

func (s *WorkflowScope) UnmarshalJSON(raw []byte) error {
	s.explicit = true
	if string(raw) == `"inherit"` {
		s.Paths = nil
		return nil
	}
	if err := json.Unmarshal(raw, &s.Paths); err != nil || s.Paths == nil {
		return fmt.Errorf("workflow-write-scope: expected inherit or a path list")
	}
	if len(s.Paths) == 0 {
		return fmt.Errorf("workflow-write-scope-empty: expected a nonempty path list")
	}
	_, err := (WorkflowStage{WriteScope: *s}).scopePaths(nil)
	return err
}
func (s WorkflowScope) MarshalJSON() ([]byte, error) {
	if s.Paths == nil {
		return []byte(`"inherit"`), nil
	}
	return json.Marshal(s.Paths)
}

func scopeWithin(inner, outer []string) bool {
	for _, candidate := range inner {
		found := false
		for _, prefix := range outer {
			if insideFence(path.Clean(candidate), []string{path.Clean(prefix)}) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func inheritWorkflowScope(w *Workflow, scope WorkflowScope) error {
	for i := range w.Stages {
		s := &w.Stages[i]
		if s.Access != "write" {
			continue
		}
		if s.WriteScope.Paths == nil || s.WriteScope.symbolic && scope.Paths != nil && !scope.symbolic {
			s.WriteScope = scope
		} else if scope.Paths != nil && !scope.symbolic && !s.WriteScope.symbolic && !scopeWithin(fencePaths(s.WriteScope.Paths), fencePaths(scope.Paths)) {
			return fmt.Errorf("workflow-write-scope: %s: inner scope leaves stage scope", s.ID)
		}
	}
	return nil
}

// This spelling is a symbolic path only in file-check expansion, never at runtime.
const workflowCheckPathPrefix = "__tillr_check_argument__/"

// scopeForLane uses the same lane remapping as the item's staged fork fence.
func scopeForLane(paths []string, lane string) []string {
	if paths == nil {
		return nil
	}
	result := append([]string(nil), paths...)
	for i, path := range result {
		if _, name, suffix, ok := item.ForkCheckoutPath(path); ok {
			result[i] = ".forks/lanes/" + lane + "/" + name + suffix
		}
	}
	return result
}

// The file check binds representative strings and commands and symbolic paths.
// Actual calls validate every supplied value before expansion.
func workflowCheckArguments(root, path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var header struct {
		Args map[string]WorkflowArgument `json:"args"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, nil
	}
	args := map[string]any{}
	for _, name := range sortedNames(header.Args) {
		switch header.Args[name].Type {
		case "paths":
			// An opaque value prevents a literal path in the file from masquerading
			// as one of this check's unresolved argument values.
			var symbol [16]byte
			if _, err := rand.Read(symbol[:]); err != nil {
				return nil, fmt.Errorf("workflow-file: generating parameter symbol: %w", err)
			}
			args[name] = []any{fmt.Sprintf("%s%x/%s/", workflowCheckPathPrefix, symbol, name)}
		case "strings":
			args[name] = []any{"check"}
		case "command":
			raw, err := os.ReadFile(filepath.Join(root, "config/tillr/commands.json"))
			if err != nil {
				return nil, fmt.Errorf("command-registry: %w", err)
			}
			var entries map[string]CommandEntry
			if err := json.Unmarshal(raw, &entries); err != nil {
				return nil, fmt.Errorf("command-registry: %w", err)
			}
			names := sortedNames(entries)
			if len(names) == 0 {
				return nil, fmt.Errorf("command-registry: no registered commands")
			}
			args[name] = names[0]
		}
	}
	return args, nil
}

func bindWorkflow(root string, raw []byte, supplied map[string]any) ([]byte, error) {
	var obj map[string]any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if err := d.Decode(&obj); err != nil {
		return raw, nil
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("workflow-json: trailing content")
	}
	defsRaw, _ := json.Marshal(obj["args"])
	var defs map[string]WorkflowArgument
	dd := json.NewDecoder(strings.NewReader(string(defsRaw)))
	dd.DisallowUnknownFields()
	if err := dd.Decode(&defs); err != nil {
		return nil, fmt.Errorf("workflow-argument-type: declarations: %w", err)
	}
	for _, name := range sortedNames(defs) {
		def := defs[name]
		if !outputNameValid(name) || (def.Type != "paths" && def.Type != "strings" && def.Type != "command") || def.Type == "strings" && def.Most <= 0 || def.Type != "strings" && def.Most != 0 {
			return nil, fmt.Errorf("workflow-argument-type: %s: invalid declaration", name)
		}
		value, ok := supplied[name]
		if !ok {
			return nil, fmt.Errorf("workflow-argument-missing: %s", name)
		}
		if def.Type == "command" {
			ref, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("workflow-argument-type: %s: expected command name", name)
			}
			if err := resolveCommand(root, &WorkflowStage{CommandRef: ref}); err != nil {
				return nil, err
			}
			continue
		}
		values, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("workflow-argument-type: %s: expected list", name)
		}
		if def.Type == "strings" && len(values) > def.Most {
			return nil, fmt.Errorf("workflow-argument-most: %s: %d exceeds %d", name, len(values), def.Most)
		}
		for _, value := range values {
			s, ok := value.(string)
			if !ok || strings.ContainsRune(s, 0) {
				return nil, fmt.Errorf("workflow-argument-type: %s: expected strings", name)
			}
			if def.Type == "paths" {
				if err := commandPath(root, s); err != nil {
					return nil, fmt.Errorf("workflow-argument-type: %s: %w", name, err)
				}
			}
		}
	}
	for _, name := range sortedNames(supplied) {
		if _, ok := defs[name]; !ok {
			return nil, fmt.Errorf("workflow-argument-unknown: %s", name)
		}
	}
	var substitute func(any) (any, error)
	substitute = func(v any) (any, error) {
		switch v := v.(type) {
		case string:
			if strings.HasPrefix(v, "${args.") && strings.HasSuffix(v, "}") {
				name := strings.TrimSuffix(strings.TrimPrefix(v, "${args."), "}")
				value, ok := supplied[name]
				if !ok {
					return nil, fmt.Errorf("workflow-argument-missing: %s", name)
				}
				return value, nil
			}
			return v, nil
		case []any:
			for i := range v {
				next, err := substitute(v[i])
				if err != nil {
					return nil, err
				}
				v[i] = next
			}
			return v, nil
		case map[string]any:
			for _, k := range sortedNames(v) {
				next, err := substitute(v[k])
				if err != nil {
					return nil, err
				}
				v[k] = next
			}
			return v, nil
		}
		return v, nil
	}
	for _, key := range []string{"stages", "edges", "exit"} {
		v, err := substitute(obj[key])
		if err != nil {
			return nil, err
		}
		if v != nil {
			obj[key] = v
		}
	}
	// Replication is expanded by the same entry/exit mapping as workflow stages.
	stages, _ := obj["stages"].([]any)
	for _, v := range stages {
		s, ok := v.(map[string]any)
		if !ok {
			continue
		}
		name, _ := s["repeat"].(string)
		if name == "" {
			continue
		}
		if defs[name].Type != "strings" || s["access"] != "read_only" || s["kind"] == "workflow" {
			return nil, fmt.Errorf("workflow-repeat: %v: repeat needs a strings argument and a read-only run", s["id"])
		}
	}
	return json.Marshal(obj)
}

// Empty repetitions are identity steps for ordering, not executable nodes.
func removeEmptyRepeats(w *Workflow, args map[string]any) error {
	kept := []WorkflowStage{}
	for _, s := range w.Stages {
		values, _ := args[s.Repeat].([]any)
		if s.Repeat == "" || len(values) > 0 {
			kept = append(kept, s)
			continue
		}
		incoming, outgoing, edges := []WorkflowEdge{}, []WorkflowEdge{}, []WorkflowEdge{}
		for _, e := range w.Edges {
			if e.From == s.ID {
				outgoing = append(outgoing, e)
			} else if e.To == s.ID {
				incoming = append(incoming, e)
			} else {
				edges = append(edges, e)
			}
		}
		for _, a := range incoming {
			for _, b := range outgoing {
				if a.FromOutput != "" || b.FromOutput != "" || b.WhenOutcome == "fail" {
					return fmt.Errorf("workflow-repeat: %s: empty copies cannot route files or failure", s.ID)
				}
				edges = append(edges, WorkflowEdge{From: a.From, To: b.To, WhenOutcome: a.WhenOutcome})
			}
		}
		w.Edges = edges
		if w.Exit != nil {
			required := []string{}
			for _, id := range w.Exit.RequiresNodes {
				if id != s.ID {
					required = append(required, id)
				}
			}
			w.Exit.RequiresNodes = required
		}
	}
	w.Stages = kept
	if len(kept) == 0 {
		return fmt.Errorf("workflow-empty-stages: expansion has no executable nodes")
	}
	return nil
}

// A template's unconnected inputs are its public input ports. The final graph
// uses strict validation after the caller has supplied those edges.
func validateTemplateWiring(w *Workflow) error {
	for name, typ := range w.Inputs {
		if !outputNameValid(name) || !outputTypeValid(typ) {
			return fmt.Errorf("workflow-input: graph: invalid input %s", name)
		}
	}
	if w.Exit != nil {
		for name := range w.Exit.Outputs {
			if !outputNameValid(name) {
				return fmt.Errorf("workflow-output-promise: exit: invalid output %s", name)
			}
		}
	}
	copy := *w
	copy.Stages = append([]WorkflowStage(nil), w.Stages...)
	for i, s := range copy.Stages {
		inputs := map[string]string{}
		for name, typ := range s.Inputs {
			fed := false
			for _, e := range w.Edges {
				if e.To == s.ID && e.ToInput == name {
					fed = true
					break
				}
			}
			if !fed && w.Inputs[name] == typ && outputNameValid(name) && outputTypeValid(typ) {
				continue
			}
			inputs[name] = typ
		}
		copy.Stages[i].Inputs = inputs
	}
	return validateOutputWiring(&copy)
}
