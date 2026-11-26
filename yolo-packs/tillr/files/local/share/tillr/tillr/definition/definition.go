// Package definition reads the vocabulary and rules for one use of tillr.
// It also reads a store's declaration. It does not execute hooks or check prerequisites.
package definition

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

// GitLandingName is a placeholder name, pending the owner's naming decision.
const GitLandingName = "git-landing"

// These are the closed prerequisite vocabulary. A definition selects checks;
// this package validates their names but does not perform them.
const (
	PrerequisiteFence           = "fence"
	PrerequisiteDesignDocument  = "design-document"
	PrerequisiteDesignQuestions = "design-questions"
	PrerequisiteOwnerClean      = "owner-clean"
)

// Definition is the version 1 JSON contract. Optional fields retain their Go
// zero values, except retention, which Read defaults to keep.
type Definition struct {
	Name            string             `json:"definition"`
	Version         int                `json:"version"`
	States          []string           `json:"states"`
	Ready           string             `json:"ready"`
	Blocked         string             `json:"blocked"`
	Closed          string             `json:"closed"`
	Finished        []string           `json:"finished"`
	Transitions     []Transition       `json:"transitions"`
	Prerequisites   []string           `json:"prerequisites"`
	Completes       string             `json:"completes"`
	Retention       string             `json:"retention"`
	BriefTemplate   string             `json:"brief_template"`
	DefaultCriteria []DefaultCriterion `json:"default_criteria,omitempty"`
	Hooks           *Hooks             `json:"hooks"`
	Observability   *Observability     `json:"observability,omitempty"`
}

// DefaultCriterion is a criterion declared by a charter to apply to items
// applied without their own criteria.
type DefaultCriterion struct {
	CriterionID string `json:"criterion_id"`
	Text        string `json:"text"`
	ClaimClass  string `json:"claim_class,omitempty"`
}

// HasState reports whether the definition declares the state.
func (d Definition) HasState(state string) bool { return slices.Contains(d.States, state) }

// IsFinished reports whether the state finishes the work under this definition.
func (d Definition) IsFinished(state string) bool { return slices.Contains(d.Finished, state) }

// Allows reports whether the definition lists the move. Same-state writes are not moves.
func (d Definition) Allows(from, to string) bool {
	return slices.Contains(d.Transitions, Transition{From: from, To: to})
}

// Transition is one allowed state move. Finished states may have outgoing moves.
type Transition struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Hooks names one program's argument vector and the hook names it answers.
// Argv is passed directly to a process, never interpreted as shell syntax.
type Hooks struct {
	Argv  []string `json:"argv"`
	Names []string `json:"names"`
}

// Observability names optional charter-level watch bindings.
type Observability struct {
	ProgressEvent string `json:"progress_event,omitempty"`
	RulePack      string `json:"rule_pack,omitempty"`
}

//go:embed landing.json
var gitLandingJSON []byte

// GitLanding reads the embedded file anew, so callers cannot mutate later reads.
func GitLanding() (Definition, error) {
	return Read(bytes.NewReader(gitLandingJSON))
}

// Load reads and validates a definition file. It does not consult a store.
func Load(path string) (Definition, error) {
	file, err := os.Open(path)
	if err != nil {
		return Definition{}, fmt.Errorf("definition file %q: %w", path, err)
	}
	defer file.Close()
	return Read(file)
}

// Read accepts exactly one JSON object, rejects unknown keys at every level,
// applies defaults, and validates its vocabulary. No partial value escapes on error.
func Read(r io.Reader) (Definition, error) {
	d := Definition{Retention: "keep"}
	decoder := json.NewDecoder(r)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return Definition{}, fmt.Errorf("definition: %w", err)
	}
	// encoding/json replaces invalid UTF-8 in strings. Refuse those bytes so a
	// validated declaration can be stored and printed without changing its hash.
	if !utf8.Valid(raw) {
		return Definition{}, fmt.Errorf("definition: content must be valid UTF-8")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Definition{}, fmt.Errorf("definition: expected one JSON object")
		}
		return Definition{}, fmt.Errorf("definition: trailing data: %w", err)
	}
	if err := checkJSONKeys(raw, reflect.TypeFor[Definition](), "definition"); err != nil {
		return Definition{}, err
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return Definition{}, fmt.Errorf("definition: %w", err)
	}
	var rawObs struct {
		Observability *struct {
			ProgressEvent *string `json:"progress_event"`
			RulePack      *string `json:"rule_pack"`
		} `json:"observability"`
	}
	if err := json.Unmarshal(raw, &rawObs); err != nil {
		return Definition{}, fmt.Errorf("definition: %w", err)
	}
	if rawObs.Observability != nil {
		if rawObs.Observability.ProgressEvent != nil && strings.TrimSpace(*rawObs.Observability.ProgressEvent) == "" {
			return Definition{}, fmt.Errorf("observability.progress_event: must be non-blank")
		}
		if rawObs.Observability.RulePack != nil && strings.TrimSpace(*rawObs.Observability.RulePack) == "" {
			return Definition{}, fmt.Errorf("observability.rule_pack: must be a clean relative .toml path")
		}
	}
	if err := d.Validate(); err != nil {
		return Definition{}, err
	}
	return d, nil
}

// checkJSONKeys uses the struct tags as the key vocabulary. The standard JSON
// decoder alone is insufficient: it accepts case-insensitive field spellings.
func checkJSONKeys(raw json.RawMessage, shape reflect.Type, key string) error {
	if shape.Kind() == reflect.Pointer {
		shape = shape.Elem()
	}
	switch shape.Kind() {
	case reflect.String, reflect.Int:
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s: null is not a %s", key, shape.Kind())
		}
	case reflect.Struct:
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		opening, err := decoder.Token()
		if err != nil || opening != json.Delim('{') {
			return fmt.Errorf("%s: expected an object", key)
		}
		fields := make(map[string]reflect.Type, shape.NumField())
		for i := 0; i < shape.NumField(); i++ {
			field := shape.Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields[name] = field.Type
		}
		seen := make(map[string]bool, shape.NumField())
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			name, ok := token.(string)
			if !ok {
				return fmt.Errorf("%s: expected a key", key)
			}
			if seen[name] {
				return fmt.Errorf("%s: duplicate key %q", key, name)
			}
			seen[name] = true
			field, ok := fields[name]
			if !ok {
				return fmt.Errorf("%s: unknown key %q", key, name)
			}
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil {
				return fmt.Errorf("%s.%s: %w", key, name, err)
			}
			if err := checkJSONKeys(value, field, key+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var entries []json.RawMessage
		if err := json.Unmarshal(raw, &entries); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		for i, entry := range entries {
			if err := checkJSONKeys(entry, shape.Elem(), fmt.Sprintf("%s[%d]", key, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate checks values and references. It permits outgoing moves from finished
// states: being finished satisfies dependencies, not necessarily permanent closure.
func (d Definition) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("definition: must be a non-empty name")
	}
	if d.Version != 1 {
		return fmt.Errorf("version: unsupported %d; expected 1", d.Version)
	}
	if len(d.States) == 0 {
		return fmt.Errorf("states: must declare at least one state")
	}
	states := make(map[string]bool, len(d.States))
	for _, state := range d.States {
		if strings.TrimSpace(state) == "" {
			return fmt.Errorf("states: empty state name")
		}
		if states[state] {
			return fmt.Errorf("states: state %q is named twice", state)
		}
		states[state] = true
	}
	for _, role := range []struct{ key, state string }{{"ready", d.Ready}, {"blocked", d.Blocked}, {"closed", d.Closed}} {
		if !states[role.state] {
			return fmt.Errorf("%s: undeclared state %q", role.key, role.state)
		}
	}
	for _, state := range d.Finished {
		if !states[state] {
			return fmt.Errorf("finished: undeclared state %q", state)
		}
	}
	for i, move := range d.Transitions {
		if !states[move.From] {
			return fmt.Errorf("transitions[%d].from: undeclared state %q", i, move.From)
		}
		if !states[move.To] {
			return fmt.Errorf("transitions[%d].to: undeclared state %q", i, move.To)
		}
	}
	for _, prerequisite := range d.Prerequisites {
		switch prerequisite {
		case PrerequisiteFence, PrerequisiteDesignDocument, PrerequisiteDesignQuestions, PrerequisiteOwnerClean:
		default:
			return fmt.Errorf("prerequisites: unknown name %q", prerequisite)
		}
	}
	if d.Completes != "landing" && d.Completes != "hook" {
		return fmt.Errorf("completes: must be landing or hook, got %q", d.Completes)
	}
	if d.Retention != "keep" {
		return fmt.Errorf("retention: only keep is supported, got %q", d.Retention)
	}
	seenCriteria := make(map[string]bool, len(d.DefaultCriteria))
	for i, c := range d.DefaultCriteria {
		if strings.TrimSpace(c.CriterionID) == "" {
			return fmt.Errorf("default_criteria[%d].criterion_id: must be non-blank", i)
		}
		if strings.TrimSpace(c.Text) == "" {
			return fmt.Errorf("default_criteria[%d].text: must be non-blank", i)
		}
		if seenCriteria[c.CriterionID] {
			return fmt.Errorf("default_criteria: duplicate criterion_id %q", c.CriterionID)
		}
		seenCriteria[c.CriterionID] = true
		switch c.ClaimClass {
		case "", "verification-only", "red-gate-triage", "direction", "owner-only":
		default:
			return fmt.Errorf("default_criteria[%d].claim_class: unknown claim class %q", i, c.ClaimClass)
		}
	}
	if d.Observability != nil {
		if d.Observability.ProgressEvent != "" {
			if strings.TrimSpace(d.Observability.ProgressEvent) == "" || strings.TrimSpace(d.Observability.ProgressEvent) != d.Observability.ProgressEvent {
				return fmt.Errorf("observability.progress_event: must be non-blank and trimmed")
			}
		}
		if d.Observability.RulePack != "" {
			if !validRulePackPath(d.Observability.RulePack) {
				return fmt.Errorf("observability.rule_pack: must be a clean relative .toml path, got %q", d.Observability.RulePack)
			}
		}
	}
	if d.Hooks == nil {
		if d.Completes == "hook" {
			return fmt.Errorf("hooks: hook completion requires a program")
		}
		return nil
	}
	if len(d.Hooks.Argv) == 0 || strings.TrimSpace(d.Hooks.Argv[0]) == "" {
		return fmt.Errorf("hooks.argv: must name a program")
	}
	if len(d.Hooks.Names) == 0 {
		return fmt.Errorf("hooks.names: must name at least one hook")
	}
	for _, name := range d.Hooks.Names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("hooks.names: empty hook name")
		}
	}
	return nil
}

func validRulePackPath(rp string) bool {
	if rp == "" || rp == ".toml" || strings.TrimSpace(rp) != rp {
		return false
	}
	if !strings.HasSuffix(rp, ".toml") || strings.HasPrefix(rp, "/") || strings.Contains(rp, "\\") {
		return false
	}
	if path.Clean(rp) != rp {
		return false
	}
	for _, seg := range strings.Split(rp, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
