package definition

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const sample = `{"definition":"findings","version":1,"states":["queued","waiting","closed","done"],"ready":"queued","blocked":"waiting","closed":"closed","finished":["done","closed"],"transitions":[{"from":"queued","to":"done"},{"from":"done","to":"queued"}],"prerequisites":[],"completes":"hook","hooks":{"argv":["checker","--verbose"],"names":["complete-item"]}}`

func TestRead(t *testing.T) {
	d, err := Read(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "findings" || d.Version != 1 || d.Retention != "keep" || d.BriefTemplate != "" || d.Transitions[1] != (Transition{From: "done", To: "queued"}) || !reflect.DeepEqual(d.Hooks, &Hooks{Argv: []string{"checker", "--verbose"}, Names: []string{"complete-item"}}) {
		t.Fatalf("decoded: %+v", d)
	}
	withEmptyArgument, err := Read(strings.NewReader(strings.Replace(sample, `"--verbose"`, `""`, 1)))
	if err != nil || withEmptyArgument.Hooks.Argv[1] != "" {
		t.Fatalf("valid empty argument: %+v, %v", withEmptyArgument, err)
	}
	path := filepath.Join(t.TempDir(), "definition.json")
	if err := os.WriteFile(path, []byte(sample), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || !reflect.DeepEqual(loaded, d) {
		t.Fatalf("Load: %+v, %v", loaded, err)
	}
	if _, err := Load(path + ".missing"); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestReadRefusals(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, key string }{
		{"unknown", `"version":1`, `"surprise":true,"version":1`, "surprise"},
		{"unknown role spelling", `"ready":"queued"`, `"READY":"queued"`, "READY"},
		{"unknown transition spelling", `"from":"queued"`, `"FROM":"queued"`, "FROM"},
		{"unknown hook spelling", `"argv":["checker","--verbose"]`, `"ARGV":["checker","--verbose"]`, "ARGV"},
		{"duplicate parent bypass", `"hooks":{"argv":["checker","--verbose"],"names":["complete-item"]}`, `"hooks":{"ARGV":["checker","--verbose"]},"hooks":{"names":["complete-item"]}`, "ARGV"},
		{"duplicate parent", `"hooks":{"argv":["checker","--verbose"],"names":["complete-item"]}`, `"hooks":{"argv":["checker"]},"hooks":{"names":["complete-item"]}`, "hooks"},
		{"duplicate nested key", `"from":"queued"`, `"from":"missing","from":"queued"`, "from"},
		{"null argument", `"argv":["checker","--verbose"]`, `"argv":["checker",null]`, "argv"},
		{"null prerequisite", `"prerequisites":[]`, `"prerequisites":[null]`, "prerequisites"},
		{"duplicate state", `"queued","waiting"`, `"queued","queued"`, "states"},
		{"empty state", `"queued","waiting"`, `"queued",""`, "states"},
		{"ready", `"ready":"queued"`, `"ready":"missing"`, "ready"},
		{"blocked", `"blocked":"waiting"`, `"blocked":"missing"`, "blocked"},
		{"closed", `"closed":"closed"`, `"closed":"missing"`, "closed"},
		{"finished", `"finished":["done","closed"]`, `"finished":["missing"]`, "finished"},
		{"from", `"from":"queued"`, `"from":"missing"`, "transitions"},
		{"to", `"to":"done"`, `"to":"missing"`, "transitions"},
		{"unknown transition key", `"from":"queued"`, `"from":"queued","unless":true`, "unless"},
		{"prerequisite", `"prerequisites":[]`, `"prerequisites":["magic"]`, "prerequisites"},
		{"name", `"definition":"findings"`, `"definition":""`, "definition"},
		{"version", `"version":1`, `"version":2`, "version"},
		{"version type", `"version":1`, `"version":"1"`, "version"},
		{"completion", `"completes":"hook"`, `"completes":"anything"`, "completes"},
		{"retention", `"version":1`, `"version":1,"retention":"delete"`, "retention"},
		{"empty retention", `"version":1`, `"version":1,"retention":""`, "retention"},
		{"null retention", `"version":1`, `"version":1,"retention":null`, "retention"},
		{"null template", `"version":1`, `"version":1,"brief_template":null`, "brief_template"},
		{"no hook program", `"argv":["checker","--verbose"]`, `"argv":[]`, "hooks"},
		{"blank executable", `"argv":["checker","--verbose"]`, `"argv":[" "]`, "hooks"},
		{"no hook names", `"names":["complete-item"]`, `"names":[]`, "hooks"},
		{"blank hook name", `"names":["complete-item"]`, `"names":[""]`, "hooks"},
		{"unknown hook key", `"names":["complete-item"]`, `"names":["complete-item"],"shell":true`, "shell"},
		{"observability unknown key", `"version":1`, `"version":1,"observability":{"extra":true}`, "extra"},
		{"observability duplicate key", `"version":1`, `"version":1,"observability":{"progress_event":"a","progress_event":"b"}`, "progress_event"},
		{"observability null progress_event", `"version":1`, `"version":1,"observability":{"progress_event":null}`, "progress_event"},
		{"observability null rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":null}`, "rule_pack"},
		{"observability empty progress_event", `"version":1`, `"version":1,"observability":{"progress_event":""}`, "progress_event"},
		{"observability blank progress_event", `"version":1`, `"version":1,"observability":{"progress_event":"  "}`, "progress_event"},
		{"observability untrimmed progress_event", `"version":1`, `"version":1,"observability":{"progress_event":" land.landing "}`, "progress_event"},
		{"observability empty rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":""}`, "rule_pack"},
		{"observability leading slash rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":"/charters/a.toml"}`, "rule_pack"},
		{"observability parent segment rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":"../a.toml"}`, "rule_pack"},
		{"observability dot segment rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":"./a.toml"}`, "rule_pack"},
		{"observability empty segment rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":"charters//a.toml"}`, "rule_pack"},
		{"observability backslash rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":"charters\\a.toml"}`, "rule_pack"},
		{"observability non-toml rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":"charters/a.json"}`, "rule_pack"},
		{"observability bare toml rule_pack", `"version":1`, `"version":1,"observability":{"rule_pack":".toml"}`, "rule_pack"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Read(strings.NewReader(strings.Replace(sample, tc.old, tc.replacement, 1)))
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("want error naming %s, got %v", tc.key, err)
			}
		})
	}
	for _, input := range []string{"", "null", "[]", sample + `{}`, sample + ` garbage`} {
		if _, err := Read(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(sample), &record); err != nil {
		t.Fatal(err)
	}
	delete(record, "hooks")
	body, _ := json.Marshal(record)
	if _, err := Read(strings.NewReader(string(body))); err == nil || !strings.Contains(err.Error(), "hooks") {
		t.Fatalf("hook completion without hooks: %v", err)
	}
}

func TestDefaultsAndIndependentReads(t *testing.T) {
	d, err := Read(strings.NewReader(`{"definition":"minimal","version":1,"states":["ready"],"ready":"ready","blocked":"ready","closed":"ready","completes":"landing"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Retention != "keep" || d.BriefTemplate != "" || d.Hooks != nil || len(d.Finished) != 0 || len(d.Transitions) != 0 || len(d.Prerequisites) != 0 {
		t.Fatalf("defaults: %+v", d)
	}
	d.Retention = ""
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("Validate must not apply defaults: %v", err)
	}
	first, err := GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	first.States[0] = "mutated"
	second, err := GitLanding()
	if err != nil || second.States[0] != "not-started" {
		t.Fatalf("shared embedded data: %+v, %v", second, err)
	}
}

func TestGitLandingAcceptsTillrLoopHooksWithoutChangingDefault(t *testing.T) {
	d, err := GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	if d.Hooks != nil {
		t.Fatal("embedded definition must leave hooks unset")
	}
	d.Hooks = &Hooks{Argv: []string{"tillr-loop"}, Names: []string{"gate", "land", "verdict", "fixer-gate", "replay", "rolling"}}
	body, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Read(strings.NewReader(string(body)))
	if err != nil || !reflect.DeepEqual(got, d) {
		t.Fatalf("Git landing with tillr-loop: %+v / %v", got, err)
	}
	fresh, err := GitLanding()
	if err != nil || fresh.Hooks != nil {
		t.Fatalf("hook opt-in changed embedded default: %+v / %v", fresh, err)
	}
}

func TestGitLandingLiteralValues(t *testing.T) {
	got, err := GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	want := Definition{
		Name: GitLandingName, Version: 1,
		States: []string{"not-started", "blocked", "in-queue", "landed", "resolved-elsewhere", "closed"},
		Ready:  "in-queue", Blocked: "blocked", Closed: "closed",
		Finished: []string{"landed", "resolved-elsewhere", "closed"},
		Transitions: []Transition{
			{"not-started", "blocked"}, {"not-started", "in-queue"}, {"not-started", "landed"}, {"not-started", "resolved-elsewhere"}, {"not-started", "closed"},
			{"blocked", "not-started"}, {"blocked", "in-queue"}, {"blocked", "landed"}, {"blocked", "resolved-elsewhere"}, {"blocked", "closed"},
			{"in-queue", "not-started"}, {"in-queue", "blocked"}, {"in-queue", "landed"}, {"in-queue", "resolved-elsewhere"}, {"in-queue", "closed"},
			{"landed", "in-queue"},
			{"closed", "not-started"}, {"closed", "blocked"}, {"closed", "in-queue"},
		},
		Prerequisites: []string{"design-document", "design-questions", "owner-clean"},
		Completes:     "landing", Retention: "keep", BriefTemplate: "", Hooks: nil,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded definition:\n got %+v\nwant %+v", got, want)
	}
}

func TestObservability(t *testing.T) {
	base := `{"definition":"minimal","version":1,"states":["ready"],"ready":"ready","blocked":"ready","closed":"ready","completes":"landing"`
	for _, tc := range []struct {
		name string
		raw  string
		want *Observability
	}{
		{"omitted", base + `}`, nil},
		{"null", base + `,"observability":null}`, nil},
		{"empty object", base + `,"observability":{}}`, &Observability{}},
		{"valid both", base + `,"observability":{"progress_event":"land.landing","rule_pack":"charters/default.toml"}}`, &Observability{ProgressEvent: "land.landing", RulePack: "charters/default.toml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := Read(strings.NewReader(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d.Observability, tc.want) {
				t.Fatalf("got %+v, want %+v", d.Observability, tc.want)
			}
		})
	}
	d, err := Read(strings.NewReader(base + `}`))
	if err != nil {
		t.Fatal(err)
	}
	d.Observability = &Observability{ProgressEvent: " "}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "observability.progress_event") {
		t.Fatalf("Validate blank progress_event: %v", err)
	}
	d.Observability = &Observability{RulePack: "../bad.toml"}
	if err := d.Validate(); err == nil || !strings.Contains(err.Error(), "observability.rule_pack") {
		t.Fatalf("Validate invalid rule_pack: %v", err)
	}
}

func TestNameOnlyInOneConstantAndOnlyLifecycleConsumers(t *testing.T) {
	defDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("locating source: %v", err)
	}
	modules, err := filepath.Glob(filepath.Join(defDir, "..", "..", "tillr*"))
	if err != nil || len(modules) == 0 {
		t.Fatalf("locating modules: %v", err)
	}
	count := 0
	for _, module := range modules {
		err := filepath.WalkDir(module, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			n := strings.Count(string(body), "git"+"-landing")
			count += n
			parsed, err := parser.ParseFile(token.NewFileSet(), path, body, 0)
			if err != nil {
				return err
			}
			for _, imp := range parsed.Imports {
				value, _ := strconv.Unquote(imp.Path.Value)
				if value == "github.com/mschulkind-oss/tillr/definition" &&
					filepath.Base(module) != "tillr-store" && filepath.Base(module) != "tillr-item" && filepath.Base(module) != "tillr-drain" && filepath.Base(module) != "tillr-view" && filepath.Base(module) != "tillr-watch" && filepath.Dir(path) != defDir && filepath.Clean(filepath.Dir(path)) != filepath.Join(defDir, "..", "watch") {
					t.Errorf("utility other than store, item, drain, view or watch imports definition: %s", path)
				}
			}
			if n == 0 {
				return nil
			}
			found := false
			for _, decl := range parsed.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					v := spec.(*ast.ValueSpec)
					if len(v.Names) != 1 || v.Names[0].Name != "GitLandingName" || len(v.Values) != 1 {
						continue
					}
					lit, ok := v.Values[0].(*ast.BasicLit)
					if !ok {
						continue
					}
					value, _ := strconv.Unquote(lit.Value)
					found = value == GitLandingName && filepath.Clean(path) == filepath.Join(defDir, "definition.go")
				}
			}
			if !found || n != 1 {
				t.Errorf("name outside its sole constant: %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("name occurrences = %d, want 1", count)
	}
}
