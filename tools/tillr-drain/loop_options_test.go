package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func setupLoopOptionTestRepo(t *testing.T) (root, storePath, tmpl, runBin, itemBin, passStub, configDir string, clearDrainEnv func()) {
	t.Helper()
	runBin, itemBin = faceSiblingBins(t)
	root, _, storePath, tmpl = newFaceLoopRepo(t, "", faceItemRecord("item_opt_test", "in-queue", ""))
	scripts := scratch(t, "scripts")
	adapter := writeExecScript(t, scripts, "adapter.sh", `
printf 'work\n' > change.txt
git add change.txt
git commit -q -m "work"
printf 'RESULT: done\n'
`)
	writeFile(t, filepath.Join(scripts, "pause-kind"), "stops-where-it-stands\n")
	passStub = writeExecScript(t, scripts, "pass.sh", `exit 0`)

	// Write default repo policy at config/tillr/backend-policy.json.
	defaultPolicy := writeFaceAdapterPolicy(t, adapter)
	rawPol, err := os.ReadFile(defaultPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config", "tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "tillr", "backend-policy.json"), rawPol, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a second policy file inside the repo for [drain] policy tests.
	if err := os.WriteFile(filepath.Join(root, "config", "tillr", "alt-policy.json"), rawPol, 0o644); err != nil {
		t.Fatal(err)
	}

	// Write two named profiles in TILLR_CONFIG_DIR.
	configDir = scratch(t, "tillr-config")
	t.Setenv("TILLR_CONFIG_DIR", configDir)
	for _, name := range []string{"envprof", "tomlprof", "cliprof"} {
		if err := os.WriteFile(filepath.Join(configDir, "backend-policy."+name+".json"), rawPol, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Clear any inherited TILLR_DRAIN_* variables for clean isolation.
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "TILLR_DRAIN_") {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
	clearDrainEnv = func() {
		for _, kv := range os.Environ() {
			if k, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "TILLR_DRAIN_") {
				os.Unsetenv(k)
			}
		}
	}
	return root, storePath, tmpl, runBin, itemBin, passStub, configDir, clearDrainEnv
}

func TestLoopStartOptionsPrecedenceAndReporting(t *testing.T) {
	// 1. Built-in default: no CLI, no .tillr.toml, no TILLR_DRAIN_*.
	t.Run("builtin_default", func(t *testing.T) {
		root, storePath, tmpl, runBin, itemBin, passStub, _, _ := setupLoopOptionTestRepo(t)
		code, out := invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
		)
		if code != 0 {
			t.Fatalf("loop exited %d:\n%s", code, out)
		}
		text := string(out)
		if !strings.Contains(text, "loop: ordinary lanes: 2 (from default)") {
			t.Fatalf("missing default lanes line:\n%s", text)
		}
		if !strings.Contains(text, "loop: start-options:") || !strings.Contains(text, "lanes=2 (built-in default)") {
			t.Fatalf("missing start-options default line:\n%s", text)
		}
		// Check tillr drain policy (--json and text).
		pCode, pJSON := invoke(t, "policy", "--root", root, "--json")
		if pCode != 0 {
			t.Fatalf("policy --json exited %d:\n%s", pCode, pJSON)
		}
		var state drain.PolicyState
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatalf("unmarshal policy --json: %v\n%s", err, pJSON)
		}
		if state.Source != "built-in default" || state.Profile != "" {
			t.Fatalf("unexpected PolicyState source/profile: %+v", state)
		}
		if len(state.StartOptions) == 0 {
			t.Fatalf("PolicyState.StartOptions is empty")
		}
		pCode, pText := invoke(t, "policy", "--root", root)
		if pCode != 0 {
			t.Fatalf("policy text exited %d:\n%s", pCode, pText)
		}
		if !strings.Contains(string(pText), "policy selection: source=built-in default") || !strings.Contains(string(pText), "start options:") {
			t.Fatalf("policy text output missing source or start options:\n%s", pText)
		}
	})

	// 2. Environment variable overrides built-in default.
	t.Run("environment_variable", func(t *testing.T) {
		root, storePath, tmpl, runBin, itemBin, passStub, configDir, _ := setupLoopOptionTestRepo(t)
		t.Setenv("TILLR_DRAIN_PROFILE", "envprof")
		t.Setenv("TILLR_DRAIN_LANES", "3")
		t.Setenv("TILLR_DRAIN_REPLAY", "")
		t.Setenv("TILLR_DRAIN_ROLLING", "")
		t.Setenv("TILLR_DRAIN_FIXER_GATE", "")
		t.Setenv("TILLR_DRAIN_NO_NOTIFY", "true")
		t.Setenv("TILLR_DRAIN_EXIT_WHEN_DRAINED", "true")

		code, out := invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--fallback-poll", "50ms",
		)
		if code != 0 {
			t.Fatalf("loop exited %d:\n%s", code, out)
		}
		text := string(out)
		if !strings.Contains(text, "loop: ordinary lanes: 3 (from environment variable TILLR_DRAIN_LANES)") {
			t.Fatalf("missing env lanes line:\n%s", text)
		}
		if !strings.Contains(text, "profile=envprof (environment variable TILLR_DRAIN_PROFILE)") {
			t.Fatalf("missing env profile in start-options:\n%s", text)
		}
		_, pJSON := invoke(t, "policy", "--root", root, "--json")
		var state drain.PolicyState
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatal(err)
		}
		wantRef := filepath.Join(configDir, "backend-policy.envprof.json")
		if state.Ref != wantRef || state.Source != "environment variable" || state.Profile != "envprof" {
			t.Fatalf("PolicyState = %+v, want ref=%q source=environment variable profile=envprof", state, wantRef)
		}
	})

	// 3. .tillr.toml overrides environment variable (even if env var has an invalid value!).
	t.Run("tillr_toml_overrides_env", func(t *testing.T) {
		root, storePath, tmpl, runBin, itemBin, passStub, configDir, _ := setupLoopOptionTestRepo(t)
		t.Setenv("TILLR_DRAIN_PROFILE", "nonexistent-env-profile")
		t.Setenv("TILLR_DRAIN_LANES", "1")
		tomlBody := `[drain]
profile = "tomlprof"
lanes = 4
`
		if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tomlBody), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out := invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
		)
		if code != 0 {
			t.Fatalf("loop exited %d:\n%s", code, out)
		}
		text := string(out)
		if !strings.Contains(text, "loop: ordinary lanes: 4 (from .tillr.toml)") {
			t.Fatalf("missing .tillr.toml lanes line:\n%s", text)
		}
		if !strings.Contains(text, "profile=tomlprof (.tillr.toml drain.profile)") {
			t.Fatalf("missing .tillr.toml profile in start-options:\n%s", text)
		}
		_, pJSON := invoke(t, "policy", "--root", root, "--json")
		var state drain.PolicyState
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatal(err)
		}
		wantRef := filepath.Join(configDir, "backend-policy.tomlprof.json")
		if state.Ref != wantRef || state.Source != ".tillr.toml" || state.Profile != "tomlprof" {
			t.Fatalf("PolicyState = %+v, want ref=%q source=.tillr.toml profile=tomlprof", state, wantRef)
		}
	})

	// 4. Saved scale target overrides .tillr.toml and environment variable; CLI option overrides saved scale target.
	t.Run("saved_scale_target_and_cli_override", func(t *testing.T) {
		root, storePath, tmpl, runBin, itemBin, passStub, configDir, _ := setupLoopOptionTestRepo(t)
		t.Setenv("TILLR_DRAIN_PROFILE", "envprof")
		t.Setenv("TILLR_DRAIN_LANES", "1")
		tomlBody := `[drain]
profile = "tomlprof"
lanes = 4
`
		if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tomlBody), 0o644); err != nil {
			t.Fatal(err)
		}
		if sCode, sOut := invoke(t, "scale", "--store", storePath, "--lanes", "5"); sCode != 0 {
			t.Fatalf("scale exited %d:\n%s", sCode, sOut)
		}

		// Without --lanes on CLI, saved scale target (5) beats .tillr.toml (4) and env (1).
		code, out := invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
		)
		if code != 0 {
			t.Fatalf("loop exited %d:\n%s", code, out)
		}
		if !strings.Contains(string(out), "loop: ordinary lanes: 5 (from saved target)") {
			t.Fatalf("expected saved target 5:\n%s", out)
		}
		if !strings.Contains(string(out), "lanes=5 (saved scale target drain_runtime_config)") {
			t.Fatalf("expected saved scale target in start-options:\n%s", out)
		}
		_, pJSON := invoke(t, "policy", "--root", root, "--json")
		var state drain.PolicyState
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatal(err)
		}
		foundSaved := false
		for _, opt := range state.StartOptions {
			if opt.Name == "lanes" && opt.Value == "5" && opt.Source == "saved scale target" {
				foundSaved = true
			}
		}
		if !foundSaved {
			t.Fatalf("PolicyState.StartOptions did not record saved scale target: %+v", state.StartOptions)
		}

		// With --lanes 6 and --profile cliprof on CLI, CLI beats saved scale target, .tillr.toml, and env.
		code, out = invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--profile", "cliprof",
			"--lanes", "6",
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
		)
		if code != 0 {
			t.Fatalf("loop exited %d:\n%s", code, out)
		}
		if !strings.Contains(string(out), "loop: ordinary lanes: 6 (from --lanes flag; replaced saved target 5)") {
			t.Fatalf("expected CLI override of saved target:\n%s", out)
		}
		if !strings.Contains(string(out), "lanes=6 (option --lanes)") {
			t.Fatalf("expected --lanes in start-options:\n%s", out)
		}
		_, pJSON = invoke(t, "policy", "--root", root, "--json")
		state = drain.PolicyState{}
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatal(err)
		}
		wantRef := filepath.Join(configDir, "backend-policy.cliprof.json")
		if state.Ref != wantRef || state.Source != "option" || state.Profile != "cliprof" {
			t.Fatalf("PolicyState = %+v, want ref=%q source=option profile=cliprof", state, wantRef)
		}
	})

	// 5. .tillr.toml [drain] policy resolves relative to <root>, and wins over [drain] profile.
	t.Run("toml_relative_policy_wins_over_profile", func(t *testing.T) {
		root, storePath, tmpl, runBin, itemBin, passStub, _, _ := setupLoopOptionTestRepo(t)
		tomlBody := `[drain]
policy = "config/tillr/alt-policy.json"
profile = "tomlprof"
`
		if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tomlBody), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out := invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
		)
		if code != 0 {
			t.Fatalf("loop exited %d:\n%s", code, out)
		}
		_, pJSON := invoke(t, "policy", "--root", root, "--json")
		var state drain.PolicyState
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatal(err)
		}
		wantRef := filepath.Join(root, "config", "tillr", "alt-policy.json")
		if state.Ref != wantRef || state.Source != ".tillr.toml" {
			t.Fatalf("PolicyState = %+v, want ref=%q source=.tillr.toml", state, wantRef)
		}
	})
}

func TestLoopInvalidEnvOrSettingsRefusesStartup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		envKey   string
		envVal   string
		extraEnv map[string]string
		toml     string
		wantSub  []string
	}{
		{
			name:    "non_numeric_lanes_env",
			envKey:  "TILLR_DRAIN_LANES",
			envVal:  "notanumber",
			wantSub: []string{"TILLR_DRAIN_LANES", "notanumber"},
		},
		{
			name:    "empty_lanes_env",
			envKey:  "TILLR_DRAIN_LANES",
			envVal:  "",
			wantSub: []string{"TILLR_DRAIN_LANES"},
		},
		{
			name:     "conflicting_max_agents_and_lanes_env",
			envKey:   "TILLR_DRAIN_MAX_AGENTS",
			envVal:   "3",
			extraEnv: map[string]string{"TILLR_DRAIN_LANES": "4"},
			wantSub:  []string{"TILLR_DRAIN_MAX_AGENTS", "TILLR_DRAIN_LANES"},
		},
		{
			name:    "missing_profile_env",
			envKey:  "TILLR_DRAIN_PROFILE",
			envVal:  "missing-prof",
			wantSub: []string{"TILLR_DRAIN_PROFILE", "missing-prof"},
		},
		{
			name:    "missing_policy_env",
			envKey:  "TILLR_DRAIN_POLICY",
			envVal:  "/no/such/policy.json",
			wantSub: []string{"TILLR_DRAIN_POLICY", "/no/such/policy.json"},
		},
		{
			name:    "invalid_bool_env",
			envKey:  "TILLR_DRAIN_NO_NOTIFY",
			envVal:  "maybe",
			wantSub: []string{"TILLR_DRAIN_NO_NOTIFY", "maybe"},
		},
		{
			name:    "invalid_duration_env",
			envKey:  "TILLR_DRAIN_BACKOFF_BASE",
			envVal:  "later",
			wantSub: []string{"TILLR_DRAIN_BACKOFF_BASE", "later"},
		},
		{
			name:    "invalid_toml_policy",
			toml:    "[drain]\npolicy = \"/abs/policy.json\"\n",
			wantSub: []string{".tillr.toml", "drain.policy", "/abs/policy.json"},
		},
		{
			name:    "invalid_toml_lanes",
			toml:    "[drain]\nlanes = -2\n",
			wantSub: []string{".tillr.toml", "drain.lanes", "-2"},
		},
		{
			name:    "missing_toml_profile",
			toml:    "[drain]\nprofile = \"missing-prof\"\n",
			wantSub: []string{".tillr.toml", "drain.profile", "missing-prof"},
		},
		{
			name:    "missing_toml_policy",
			toml:    "[drain]\npolicy = \"config/tillr/no-such-policy.json\"\n",
			wantSub: []string{".tillr.toml", "drain.policy", "no-such-policy.json"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, storePath, tmpl, runBin, itemBin, passStub, _, _ := setupLoopOptionTestRepo(t)
			if tc.envKey != "" {
				t.Setenv(tc.envKey, tc.envVal)
			}
			for k, v := range tc.extraEnv {
				t.Setenv(k, v)
			}
			if tc.toml != "" {
				if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tc.toml), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			code, out := invoke(t,
				"loop",
				"--root", root,
				"--store", storePath,
				"--run-bin", runBin,
				"--item-bin", itemBin,
				"--brief-template", tmpl,
				"--gate", passStub,
				"--land", passStub,
				"--verdict", passStub,
				"--check",
			)
			if code != 2 {
				t.Fatalf("expected exit 2, got %d:\n%s", code, out)
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(string(out), sub) {
					t.Fatalf("output missing %q:\n%s", sub, out)
				}
			}
		})
	}
}

func TestProfileListPrecedenceRefusalsAndValidation(t *testing.T) {
	t.Run("precedence_env_then_toml_then_option_and_policy_override", func(t *testing.T) {
		// 1. Environment variable TILLR_DRAIN_PROFILES.
		root, storePath, tmpl, runBin, itemBin, passStub, configDir, _ := setupLoopOptionTestRepo(t)
		t.Setenv("TILLR_DRAIN_PROFILES", "envprof,tomlprof")
		code, out := invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
		)
		if code != 0 {
			t.Fatalf("env profiles loop exited %d:\n%s", code, out)
		}
		pCode, pJSON := invoke(t, "policy", "--root", root, "--json")
		if pCode != 0 {
			t.Fatalf("policy --json exited %d:\n%s", pCode, pJSON)
		}
		var state drain.PolicyState
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatalf("unmarshal policy --json: %v\n%s", err, pJSON)
		}
		wantEnvRef := filepath.Join(configDir, "backend-policy.envprof.json")
		if state.Ref != wantEnvRef || state.Profile != "envprof" || state.ProfileIndex != 0 || state.Source != "environment variable" ||
			len(state.Profiles) != 2 || state.Profiles[0] != "envprof" || state.Profiles[1] != "tomlprof" {
			t.Fatalf("env PolicyState = %+v", state)
		}
		pCode, pText := invoke(t, "policy", "--root", root)
		if pCode != 0 {
			t.Fatalf("policy text exited %d:\n%s", pCode, pText)
		}
		if !strings.Contains(string(pText), "profiles=envprof,tomlprof (1/2)") {
			t.Fatalf("policy text missing profile list summary:\n%s", pText)
		}

		// 2. .tillr.toml [drain] profiles overrides TILLR_DRAIN_PROFILES.
		if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[drain]\nprofiles = [\"tomlprof\", \"envprof\"]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, out = invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
		)
		if code != 0 {
			t.Fatalf("toml profiles loop exited %d:\n%s", code, out)
		}
		pCode, pJSON = invoke(t, "policy", "--root", root, "--json")
		if pCode != 0 {
			t.Fatalf("policy --json exited %d:\n%s", pCode, pJSON)
		}
		state = drain.PolicyState{}
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatalf("unmarshal policy --json: %v\n%s", err, pJSON)
		}
		wantTomlRef := filepath.Join(configDir, "backend-policy.tomlprof.json")
		if state.Ref != wantTomlRef || state.Profile != "tomlprof" || state.ProfileIndex != 0 || state.Source != ".tillr.toml" ||
			len(state.Profiles) != 2 || state.Profiles[0] != "tomlprof" || state.Profiles[1] != "envprof" {
			t.Fatalf("toml PolicyState = %+v", state)
		}

		// 3. CLI --profiles overrides .tillr.toml and environment.
		code, out = invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--replay", "",
			"--rolling", "",
			"--fixer-gate", "",
			"--no-notify",
			"--fallback-poll", "50ms",
			"--exit-when-drained",
			"--profiles", "cliprof, tomlprof",
		)
		if code != 0 {
			t.Fatalf("cli profiles loop exited %d:\n%s", code, out)
		}
		pCode, pJSON = invoke(t, "policy", "--root", root, "--json")
		if pCode != 0 {
			t.Fatalf("policy --json exited %d:\n%s", pCode, pJSON)
		}
		state = drain.PolicyState{}
		if err := json.Unmarshal(pJSON, &state); err != nil {
			t.Fatalf("unmarshal policy --json: %v\n%s", err, pJSON)
		}
		wantCLIRef := filepath.Join(configDir, "backend-policy.cliprof.json")
		if state.Ref != wantCLIRef || state.Profile != "cliprof" || state.ProfileIndex != 0 || state.Source != "option" ||
			len(state.Profiles) != 2 || state.Profiles[0] != "cliprof" || state.Profiles[1] != "tomlprof" {
			t.Fatalf("cli PolicyState = %+v", state)
		}

		// 4. --policy wins over --profiles without resolving missing profile names.
		explicitPolicy := filepath.Join(root, "config", "tillr", "alt-policy.json")
		code, out = invoke(t,
			"loop",
			"--root", root,
			"--store", storePath,
			"--run-bin", runBin,
			"--item-bin", itemBin,
			"--brief-template", tmpl,
			"--gate", passStub,
			"--land", passStub,
			"--verdict", passStub,
			"--policy", explicitPolicy,
			"--profiles", "missing1,missing2",
			"--check",
		)
		if code != 0 {
			t.Fatalf("--policy overriding --profiles exited %d:\n%s", code, out)
		}
	})

	t.Run("refuse_single_profile_and_list_together", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			args    []string
			toml    string
			env     map[string]string
			wantSub []string
		}{
			{
				name:    "cli_profile_and_profiles",
				args:    []string{"--profile", "cliprof", "--profiles", "cliprof,tomlprof"},
				wantSub: []string{"--profile", "--profiles"},
			},
			{
				name:    "toml_profile_and_profiles",
				toml:    "[drain]\nprofile = \"tomlprof\"\nprofiles = [\"tomlprof\", \"envprof\"]\n",
				wantSub: []string{"drain.profile", "drain.profiles"},
			},
			{
				name: "env_profile_and_profiles",
				env: map[string]string{
					"TILLR_DRAIN_PROFILE":  "envprof",
					"TILLR_DRAIN_PROFILES": "envprof,tomlprof",
				},
				wantSub: []string{"TILLR_DRAIN_PROFILE", "TILLR_DRAIN_PROFILES"},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				root, storePath, tmpl, runBin, itemBin, passStub, _, _ := setupLoopOptionTestRepo(t)
				for k, v := range tc.env {
					t.Setenv(k, v)
				}
				if tc.toml != "" {
					if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tc.toml), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				baseArgs := []string{
					"loop",
					"--root", root,
					"--store", storePath,
					"--run-bin", runBin,
					"--item-bin", itemBin,
					"--brief-template", tmpl,
					"--gate", passStub,
					"--land", passStub,
					"--verdict", passStub,
					"--check",
				}
				code, out := invoke(t, append(baseArgs, tc.args...)...)
				if code != 2 {
					t.Fatalf("expected exit 2, got %d:\n%s", code, out)
				}
				for _, sub := range tc.wantSub {
					if !strings.Contains(string(out), sub) {
						t.Fatalf("output missing %q:\n%s", sub, out)
					}
				}
			})
		}
	})

	t.Run("second_profile_missing_or_invalid_stops_start_and_check_with_exit_2", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			secondName  string
			secondBody  string
			writeSecond bool
			wantSub     []string
		}{
			{
				name:        "missing_second_profile",
				secondName:  "no-such-prof",
				writeSecond: false,
				wantSub:     []string{"no-such-prof", "index 1 of 2"},
			},
			{
				name:        "malformed_json_second_profile",
				secondName:  "badjson",
				secondBody:  "{not-json",
				writeSecond: true,
				wantSub:     []string{"badjson", "index 1 of 2"},
			},
			{
				name:        "invalid_policy_second_profile",
				secondName:  "empty-policy",
				secondBody:  "{}\n",
				writeSecond: true,
				wantSub:     []string{"empty-policy", "index 1 of 2"},
			},
			{
				name:        "missing_adapter_second_profile",
				secondName:  "bad-adapter",
				secondBody:  `{"default_backend":"b","backends":{"b":{"adapter":"/no/such/adapter","model":"m","timeout":"1m"}}}` + "\n",
				writeSecond: true,
				wantSub:     []string{"bad-adapter", "index 1 of 2"},
			},
		} {
			for _, checkMode := range []bool{true, false} {
				modeName := "start"
				if checkMode {
					modeName = "check"
				}
				t.Run(tc.name+"_"+modeName, func(t *testing.T) {
					root, storePath, tmpl, runBin, itemBin, passStub, configDir, _ := setupLoopOptionTestRepo(t)
					if tc.writeSecond {
						if err := os.WriteFile(filepath.Join(configDir, "backend-policy."+tc.secondName+".json"), []byte(tc.secondBody), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					args := []string{
						"loop",
						"--root", root,
						"--store", storePath,
						"--run-bin", runBin,
						"--item-bin", itemBin,
						"--brief-template", tmpl,
						"--gate", passStub,
						"--land", passStub,
						"--verdict", passStub,
						"--replay", "",
						"--rolling", "",
						"--fixer-gate", "",
						"--no-notify",
						"--fallback-poll", "50ms",
						"--exit-when-drained",
						"--profiles", "cliprof," + tc.secondName,
					}
					if checkMode {
						args = append(args, "--check")
					}
					code, out := invoke(t, args...)
					if code != 2 {
						t.Fatalf("expected exit 2, got %d:\n%s", code, out)
					}
					for _, sub := range tc.wantSub {
						if !strings.Contains(string(out), sub) {
							t.Fatalf("output missing %q:\n%s", sub, out)
						}
					}
				})
			}
		}
	})
}

func TestLoopProfileStartLineAndWaitingStatusLine(t *testing.T) {
	prefLine := drain.FormatProfileStartLine(drain.PolicyState{
		Profile:      "claude-sub",
		Profiles:     []string{"claude-sub", "copilot-gemini"},
		ProfileIndex: 0,
		Source:       "toml",
		Why:          "preferred profile (1/2); all quota windows open",
	})
	wantPref := "loop: profile claude-sub (1/2 of [claude-sub, copilot-gemini], source toml): preferred profile (1/2); all quota windows open"
	if prefLine != wantPref {
		t.Fatalf("FormatProfileStartLine = %q, want %q", prefLine, wantPref)
	}

	// Verify --check emits the profile start line on stderr.
	root, storePath, tmpl, runBin, itemBin, passStub, configDir, clearEnv := setupLoopOptionTestRepo(t)
	defer clearEnv()
	t.Setenv("TILLR_CONFIG_DIR", configDir)
	tomlProf, err := os.ReadFile(filepath.Join(configDir, "backend-policy.tomlprof.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(configDir, "backend-policy.alpha.json"), string(tomlProf))
	writeFile(t, filepath.Join(configDir, "backend-policy.beta.json"), string(tomlProf))

	code, out := invoke(t,
		"loop",
		"--root", root,
		"--store", storePath,
		"--run-bin", runBin,
		"--item-bin", itemBin,
		"--brief-template", tmpl,
		"--gate", passStub,
		"--land", passStub,
		"--verdict", passStub,
		"--profiles", "alpha,beta",
		"--check",
	)
	if code != 0 {
		t.Fatalf("loop --check exited %d:\n%s", code, out)
	}
	wantCheck := "loop: profile alpha (1/2 of [alpha, beta], source option): preferred profile (1/2); all quota windows open"
	if !strings.Contains(string(out), wantCheck) {
		t.Fatalf("loop --check output missing %q:\n%s", wantCheck, out)
	}
}
