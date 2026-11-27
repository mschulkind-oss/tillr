package workflow

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

// WorkflowArgument declares a typed parameter. Most bounds string and piece lists.
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
	_, err := (WorkflowStage{WriteScope: *s}).ScopePaths(nil)
	return err
}

func (s WorkflowScope) MarshalJSON() ([]byte, error) {
	if s.Paths == nil {
		return []byte(`"inherit"`), nil
	}
	return json.Marshal(s.Paths)
}

// ScopeWithin reports whether every path in inner is covered by some prefix in outer.
func ScopeWithin(inner, outer []string) bool {
	for _, candidate := range inner {
		found := false
		for _, prefix := range outer {
			if InsideFence(path.Clean(candidate), []string{path.Clean(prefix)}) {
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

// InheritWorkflowScope propagates an enclosing stage's write scope into w's write stages.
func InheritWorkflowScope(w *Workflow, scope WorkflowScope) error {
	for i := range w.Stages {
		s := &w.Stages[i]
		if s.Access != "write" {
			continue
		}
		if s.WriteScope.Paths == nil || s.WriteScope.symbolic && scope.Paths != nil && !scope.symbolic {
			s.WriteScope = scope
		} else if scope.Paths != nil && !scope.symbolic && !s.WriteScope.symbolic && !ScopeWithin(FencePaths(s.WriteScope.Paths), FencePaths(scope.Paths)) {
			return fmt.Errorf("workflow-write-scope: %s: inner scope leaves stage scope", s.ID)
		}
	}
	return nil
}

// WorkflowCheckPathPrefix is a symbolic path prefix used only in file-check expansion, never at runtime.
const WorkflowCheckPathPrefix = "__tillr_check_argument__/"

// ScopeForLane uses the same lane remapping as the item's staged fork fence.
func ScopeForLane(paths []string, lane string) []string {
	if paths == nil {
		return nil
	}
	result := append([]string(nil), paths...)
	for i, p := range result {
		if _, name, suffix, ok := item.ForkCheckoutPath(p); ok {
			result[i] = ".forks/lanes/" + lane + "/" + name + suffix
		}
	}
	return result
}

// WorkflowPiece is a single piece element in a "pieces" workflow argument.
type WorkflowPiece struct {
	ID         string            `json:"id"`
	Seat       string            `json:"seat"`
	Access     string            `json:"access"`
	After      *[]string         `json:"after,omitempty"`
	WriteScope []string          `json:"write_scope,omitempty"`
	Prompt     string            `json:"prompt"`
	Outputs    map[string]string `json:"outputs,omitempty"`
}

// ParseWorkflowPiece validates and decodes a single piece entry from a "pieces" argument.
func ParseWorkflowPiece(root string, value any) (WorkflowPiece, error) {
	var p WorkflowPiece
	raw, err := json.Marshal(value)
	if err != nil {
		return p, err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return p, fmt.Errorf("expected piece object")
	}
	for _, key := range []string{"id", "seat", "access", "prompt"} {
		if _, ok := fields[key].(string); !ok {
			return p, fmt.Errorf("piece requires string %s", key)
		}
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, err
	}
	if _, hasAfter := fields["after"]; hasAfter && p.After == nil {
		return p, fmt.Errorf("piece after must be a string list")
	}
	if p.After != nil {
		seenAfter := make(map[string]bool, len(*p.After))
		for _, dep := range *p.After {
			if !OutputNameValid(dep) {
				return p, fmt.Errorf("invalid piece after id %q", dep)
			}
			if dep == p.ID {
				return p, fmt.Errorf("piece %s cannot depend on itself", p.ID)
			}
			if seenAfter[dep] {
				return p, fmt.Errorf("duplicate piece after id %q", dep)
			}
			seenAfter[dep] = true
		}
	}
	if !OutputNameValid(p.ID) || !item.ValidSeatName(p.Seat) || strings.ContainsRune(p.Prompt, 0) {
		return p, fmt.Errorf("invalid piece id, seat or prompt")
	}
	if p.Access != "write" && p.Access != "read_only" {
		return p, fmt.Errorf("invalid piece access")
	}
	if p.Access == "write" && (p.Seat == "verify" || p.Seat == "review" || p.Seat == "plan") {
		return p, fmt.Errorf("workflow-read-only-seat-writes: seat %s may not have write access in a planned piece; use build or edit instead", p.Seat)
	}
	_, hasScope := fields["write_scope"]
	if p.Access == "read_only" && hasScope || p.Access == "write" && (!hasScope || len(p.WriteScope) == 0) {
		return p, fmt.Errorf("invalid piece write_scope for %s", p.Access)
	}
	if p.Access == "write" {
		if _, err := (WorkflowStage{ID: p.ID, WriteScope: WorkflowScope{Paths: p.WriteScope}}).ScopePaths(nil); err != nil {
			return p, err
		}
	}
	for _, scope := range p.WriteScope {
		if err := CommandPath(root, scope); err != nil {
			return p, err
		}
	}
	if err := ValidateNodeTask(root, p.ID, p.Access, p.WriteScope, p.Prompt); err != nil {
		return p, err
	}
	if outputs, ok := fields["outputs"]; ok {
		if _, ok := outputs.(map[string]any); !ok {
			return p, fmt.Errorf("piece outputs must be a map")
		}
	}
	for name, typ := range p.Outputs {
		if !OutputNameValid(name) || !OutputTypeValid(typ) {
			return p, fmt.Errorf("invalid piece output %s", name)
		}
	}
	return p, nil
}

// WorkflowCheckArguments synthesizes representative argument sets for static workflow checking.
// Actual calls validate every supplied value before expansion.
func WorkflowCheckArguments(root, p string) ([]map[string]any, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var header struct {
		Args map[string]WorkflowArgument `json:"args"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return []map[string]any{nil}, nil
	}
	args := map[string]any{}
	for _, name := range SortedNames(header.Args) {
		switch header.Args[name].Type {
		case "paths":
			// An opaque value prevents a literal path in the file from masquerading
			// as one of this check's unresolved argument values.
			var symbol [16]byte
			if _, err := rand.Read(symbol[:]); err != nil {
				return nil, fmt.Errorf("workflow-file: generating parameter symbol: %w", err)
			}
			args[name] = []any{fmt.Sprintf("%s%x/%s/", WorkflowCheckPathPrefix, symbol, name)}
		case "strings":
			args[name] = []any{"check"}
		case "pieces":
			args[name] = []any{map[string]any{"id": "check", "seat": "build", "access": "read_only", "prompt": "check"}}
		case "command":
			raw, err := os.ReadFile(filepath.Join(root, "config/tillr/commands.json"))
			if err != nil {
				return nil, fmt.Errorf("command-registry: %w", err)
			}
			var entries map[string]CommandEntry
			if err := json.Unmarshal(raw, &entries); err != nil {
				return nil, fmt.Errorf("command-registry: %w", err)
			}
			names := SortedNames(entries)
			if len(names) == 0 {
				return nil, fmt.Errorf("command-registry: no registered commands")
			}
			args[name] = names[0]
		}
	}
	// One copy can hide collisions and invalid output wiring. Exercise each
	// string list separately at two values when its declared bound permits it.
	cases := []map[string]any{args}
	for _, name := range SortedNames(header.Args) {
		def := header.Args[name]
		if def.Type != "strings" || def.Most < 2 {
			continue
		}
		variant := map[string]any{}
		for key, value := range args {
			variant[key] = value
		}
		variant[name] = []any{"check", "check-two"}
		cases = append(cases, variant)
	}
	return cases, nil
}

// BindWorkflow validates and substitutes supplied workflow arguments into raw JSON.
func BindWorkflow(root string, raw []byte, supplied map[string]any) ([]byte, error) {
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
	for _, name := range SortedNames(defs) {
		def := defs[name]
		if !OutputNameValid(name) || (def.Type != "paths" && def.Type != "strings" && def.Type != "command" && def.Type != "pieces") || (def.Type == "strings" || def.Type == "pieces") && def.Most <= 0 || def.Type != "strings" && def.Type != "pieces" && def.Most != 0 {
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
			if err := ResolveCommand(root, &WorkflowStage{CommandRef: ref}); err != nil {
				return nil, err
			}
			continue
		}
		values, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("workflow-argument-type: %s: expected list", name)
		}
		if (def.Type == "strings" || def.Type == "pieces") && len(values) > def.Most {
			return nil, fmt.Errorf("workflow-argument-most: %s: %d exceeds %d", name, len(values), def.Most)
		}
		seenPieces := map[string]bool{}
		for _, value := range values {
			if def.Type == "pieces" {
				piece, err := ParseWorkflowPiece(root, value)
				if err != nil {
					return nil, fmt.Errorf("workflow-argument-type: %s: %w", name, err)
				}
				if seenPieces[piece.ID] {
					return nil, fmt.Errorf("workflow-argument-type: %s: duplicate piece id %s", name, piece.ID)
				}
				seenPieces[piece.ID] = true
				continue
			}
			s, ok := value.(string)
			if !ok || strings.ContainsRune(s, 0) {
				return nil, fmt.Errorf("workflow-argument-type: %s: expected strings", name)
			}
			if def.Type == "paths" {
				if err := CommandPath(root, s); err != nil {
					return nil, fmt.Errorf("workflow-argument-type: %s: %w", name, err)
				}
			}
		}
	}
	for _, name := range SortedNames(supplied) {
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
			for _, k := range SortedNames(v) {
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
		if s["kind"] == "workflow" || (defs[name].Type == "pieces" && s["kind"] != "agent") || (defs[name].Type != "pieces" && (defs[name].Type != "strings" || s["access"] != "read_only")) {
			return nil, fmt.Errorf("workflow-repeat: %v: repeat needs pieces on an agent or strings on a read-only run", s["id"])
		}
	}
	return json.Marshal(obj)
}

// RemoveEmptyRepeats collapses empty repeated stages into direct edges.
// Empty repetitions are identity steps for ordering, not executable nodes.
func RemoveEmptyRepeats(w *Workflow, args map[string]any) error {
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

// ValidateTemplateWiring checks output wiring for a template before outer edges bind its public inputs.
// A template's unconnected inputs are its public input ports. The final graph
// uses strict validation after the caller has supplied those edges.
func ValidateTemplateWiring(w *Workflow) error {
	for name, typ := range w.Inputs {
		if !OutputNameValid(name) || !OutputTypeValid(typ) {
			return fmt.Errorf("workflow-input: graph: invalid input %s", name)
		}
	}
	if w.Exit != nil {
		for name := range w.Exit.Outputs {
			if !OutputNameValid(name) {
				return fmt.Errorf("workflow-output-promise: exit: invalid output %s", name)
			}
		}
	}
	copied := *w
	copied.Stages = append([]WorkflowStage(nil), w.Stages...)
	for i, s := range copied.Stages {
		inputs := map[string]string{}
		for name, typ := range s.Inputs {
			fed := false
			for _, e := range w.Edges {
				if e.To == s.ID && e.ToInput == name {
					fed = true
					break
				}
			}
			if !fed && w.Inputs[name] == typ && OutputNameValid(name) && OutputTypeValid(typ) {
				continue
			}
			inputs[name] = typ
		}
		copied.Stages[i].Inputs = inputs
	}
	return ValidateOutputWiring(&copied)
}
