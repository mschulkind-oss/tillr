package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/settings"
)

const (
	sourceOption         = "option"
	sourceSettings       = ".tillr.toml"
	sourceEnv            = "environment variable"
	sourceBuiltinDefault = "built-in default"
)

// optionEnvName maps a loop flag name (--<option>) to TILLR_DRAIN_<UPPER_SNAKE>.
func optionEnvName(flagName string) string {
	return "TILLR_DRAIN_" + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

func resolveBoolOption(flagName string, explicit map[string]bool, cliVal, tomlSet, tomlVal, defVal bool, tomlKey string) (bool, string, string, error) {
	if explicit[flagName] {
		return cliVal, sourceOption, "--" + flagName, nil
	}
	if tomlSet {
		return tomlVal, sourceSettings, tomlKey, nil
	}
	envName := optionEnvName(flagName)
	if raw, ok := os.LookupEnv(envName); ok {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return false, "", "", drain.Usagef("environment variable %s=%q: expected a boolean (true or false): %v", envName, raw, err)
		}
		return v, sourceEnv, envName, nil
	}
	return defVal, sourceBuiltinDefault, "", nil
}

func resolveNonnegIntOption(flagName string, explicit map[string]bool, cliVal int, tomlSet bool, tomlVal int64, defVal int, tomlKey string) (int, string, string, error) {
	if explicit[flagName] {
		if cliVal < 0 {
			return 0, "", "", drain.Usagef("loop needs --%s N (N >= 0)", flagName)
		}
		return cliVal, sourceOption, "--" + flagName, nil
	}
	if tomlSet {
		return int(tomlVal), sourceSettings, tomlKey, nil
	}
	envName := optionEnvName(flagName)
	if raw, ok := os.LookupEnv(envName); ok {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return 0, "", "", drain.Usagef("environment variable %s=%q: expected a nonnegative integer", envName, raw)
		}
		return v, sourceEnv, envName, nil
	}
	return defVal, sourceBuiltinDefault, "", nil
}

func resolveNonnegInt64Option(flagName string, explicit map[string]bool, cliVal int64, tomlSet bool, tomlVal int64, defVal int64, tomlKey string) (int64, string, string, error) {
	if explicit[flagName] {
		if cliVal < 0 {
			return 0, "", "", drain.Usagef("loop needs --%s N (N >= 0)", flagName)
		}
		return cliVal, sourceOption, "--" + flagName, nil
	}
	if tomlSet {
		return tomlVal, sourceSettings, tomlKey, nil
	}
	envName := optionEnvName(flagName)
	if raw, ok := os.LookupEnv(envName); ok {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return 0, "", "", drain.Usagef("environment variable %s=%q: expected a nonnegative integer", envName, raw)
		}
		return v, sourceEnv, envName, nil
	}
	return defVal, sourceBuiltinDefault, "", nil
}

func resolveDurationOption(flagName string, explicit map[string]bool, cliVal time.Duration, tomlSet bool, tomlVal string, defVal time.Duration, tomlKey string) (time.Duration, string, string, error) {
	if explicit[flagName] {
		if cliVal < 0 {
			return 0, "", "", drain.Usagef("loop needs --%s >= 0", flagName)
		}
		return cliVal, sourceOption, "--" + flagName, nil
	}
	if tomlSet {
		d, err := time.ParseDuration(tomlVal)
		if err != nil || d < 0 {
			return 0, "", "", drain.Usagef("%s=%q: expected a nonnegative duration", tomlKey, tomlVal)
		}
		return d, sourceSettings, tomlKey, nil
	}
	envName := optionEnvName(flagName)
	if raw, ok := os.LookupEnv(envName); ok {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			return 0, "", "", drain.Usagef("environment variable %s=%q: expected a nonnegative duration (for example \"1s\")", envName, raw)
		}
		return d, sourceEnv, envName, nil
	}
	return defVal, sourceBuiltinDefault, "", nil
}

func resolveStringOption(flagName string, explicit map[string]bool, cliVal string, tomlSet bool, tomlVal, defVal, tomlKey string, allowEmpty bool) (string, string, string, error) {
	if explicit[flagName] {
		if !allowEmpty && strings.TrimSpace(cliVal) == "" {
			return "", "", "", drain.Usagef("loop needs nonempty --%s", flagName)
		}
		return cliVal, sourceOption, "--" + flagName, nil
	}
	if tomlSet {
		if !allowEmpty && strings.TrimSpace(tomlVal) == "" {
			return "", "", "", drain.Usagef("%s must not be empty", tomlKey)
		}
		return tomlVal, sourceSettings, tomlKey, nil
	}
	envName := optionEnvName(flagName)
	if raw, ok := os.LookupEnv(envName); ok {
		if !allowEmpty && strings.TrimSpace(raw) == "" {
			return "", "", "", drain.Usagef("environment variable %s=%q: must not be empty", envName, raw)
		}
		return raw, sourceEnv, envName, nil
	}
	return defVal, sourceBuiltinDefault, "", nil
}

func resolveLaneLimitOptions(explicit map[string]bool, cliMaxAgents, cliLanes int, s settings.Settings) (limit int, explicitCLI bool, source, detail string, err error) {
	if explicit["max-agents"] || explicit["lanes"] {
		val, _, err := resolveAgentLimit(explicit, cliMaxAgents, cliLanes, 2)
		if err != nil {
			return 0, false, "", "", err
		}
		if val < 0 {
			return 0, false, "", "", drain.Usagef("loop needs --max-agents N (N >= 0)")
		}
		flagName := "--lanes"
		if explicit["max-agents"] && !explicit["lanes"] {
			flagName = "--max-agents"
		}
		return val, true, sourceOption, flagName, nil
	}
	if s.Drain.MaxAgentsSet || s.Drain.LanesSet {
		if s.Drain.LanesSet {
			return int(s.Drain.Lanes), false, sourceSettings, "drain.lanes", nil
		}
		return int(s.Drain.MaxAgents), false, sourceSettings, "drain.max_agents", nil
	}
	rawMax, hasMax := os.LookupEnv("TILLR_DRAIN_MAX_AGENTS")
	rawLanes, hasLanes := os.LookupEnv("TILLR_DRAIN_LANES")
	var maxVal, lanesVal int
	if hasMax {
		v, convErr := strconv.Atoi(rawMax)
		if convErr != nil || v < 0 {
			return 0, false, "", "", drain.Usagef("environment variable TILLR_DRAIN_MAX_AGENTS=%q: expected a nonnegative integer", rawMax)
		}
		maxVal = v
	}
	if hasLanes {
		v, convErr := strconv.Atoi(rawLanes)
		if convErr != nil || v < 0 {
			return 0, false, "", "", drain.Usagef("environment variable TILLR_DRAIN_LANES=%q: expected a nonnegative integer", rawLanes)
		}
		lanesVal = v
	}
	if hasMax && hasLanes {
		if maxVal != lanesVal {
			return 0, false, "", "", drain.Usagef("TILLR_DRAIN_MAX_AGENTS (%d) and TILLR_DRAIN_LANES (%d) disagree; set one", maxVal, lanesVal)
		}
		return lanesVal, false, sourceEnv, "TILLR_DRAIN_LANES", nil
	}
	if hasMax {
		return maxVal, false, sourceEnv, "TILLR_DRAIN_MAX_AGENTS", nil
	}
	if hasLanes {
		return lanesVal, false, sourceEnv, "TILLR_DRAIN_LANES", nil
	}
	return 2, false, sourceBuiltinDefault, "", nil
}

func resolvePolicyAndProfileOptions(root string, explicit map[string]bool, cliPolicy, cliProfile, cliProfiles string, s settings.Settings) (policyRef string, profileNames, profileRefs []string, source, detail string, err error) {
	// 1. Command-line option (--policy wins over --profile / --profiles; --profile and --profiles together are refused).
	if explicit["profile"] && explicit["profiles"] {
		return "", nil, nil, "", "", drain.Usagef("--profile and --profiles cannot both be set; set one")
	}
	if explicit["policy"] {
		if strings.TrimSpace(cliPolicy) == "" {
			return "", nil, nil, "", "", drain.Usagef("loop needs nonempty --policy")
		}
		return cliPolicy, nil, nil, sourceOption, "--policy", nil
	}
	if explicit["profiles"] {
		names, err := drain.ParseProfileList(cliProfiles)
		if err != nil {
			return "", nil, nil, "", "", drain.Usagef("--profiles %q: %v", cliProfiles, err)
		}
		refs, err := drain.ResolveProfilePaths(names)
		if err != nil {
			return "", nil, nil, "", "", drain.Usagef("--profiles %q: %v", cliProfiles, err)
		}
		return refs[0], names, refs, sourceOption, "--profiles", nil
	}
	if explicit["profile"] {
		if !settings.ValidProfileName(cliProfile) {
			return "", nil, nil, "", "", drain.Usagef("--profile %q: expected a valid profile name matching [a-z0-9][a-z0-9_-]*", cliProfile)
		}
		resolved, err := drain.ResolveProfilePath(cliProfile)
		if err != nil {
			return "", nil, nil, "", "", err
		}
		return resolved, []string{cliProfile}, []string{resolved}, sourceOption, "--profile", nil
	}
	// 2. Workspace .tillr.toml ([drain] policy wins over [drain] profile / [drain] profiles).
	if s.Drain.PolicySet {
		return filepath.Join(root, filepath.FromSlash(s.Drain.Policy)), nil, nil, sourceSettings, "drain.policy", nil
	}
	if s.Drain.ProfilesSet {
		refs, err := drain.ResolveProfilePaths(s.Drain.Profiles)
		if err != nil {
			return "", nil, nil, "", "", drain.Usagef(".tillr.toml drain.profiles=%v: %v", s.Drain.Profiles, err)
		}
		return refs[0], append([]string(nil), s.Drain.Profiles...), refs, sourceSettings, "drain.profiles", nil
	}
	if s.Drain.Profile != "" {
		resolved, err := drain.ResolveProfilePath(s.Drain.Profile)
		if err != nil {
			return "", nil, nil, "", "", drain.Usagef(".tillr.toml drain.profile=%q: %v", s.Drain.Profile, err)
		}
		return resolved, []string{s.Drain.Profile}, []string{resolved}, sourceSettings, "drain.profile", nil
	}
	// 3. Environment variable (TILLR_DRAIN_POLICY wins over TILLR_DRAIN_PROFILE / TILLR_DRAIN_PROFILES).
	if rawPolicy, ok := os.LookupEnv("TILLR_DRAIN_POLICY"); ok {
		if strings.TrimSpace(rawPolicy) == "" {
			return "", nil, nil, "", "", drain.Usagef("environment variable TILLR_DRAIN_POLICY=%q: must not be empty", rawPolicy)
		}
		return rawPolicy, nil, nil, sourceEnv, "TILLR_DRAIN_POLICY", nil
	}
	rawProfile, hasProfile := os.LookupEnv("TILLR_DRAIN_PROFILE")
	rawProfiles, hasProfiles := os.LookupEnv("TILLR_DRAIN_PROFILES")
	if hasProfile || hasProfiles {
		var singleName string
		if hasProfile {
			if !settings.ValidProfileName(rawProfile) {
				return "", nil, nil, "", "", drain.Usagef("environment variable TILLR_DRAIN_PROFILE=%q: expected a valid profile name matching [a-z0-9][a-z0-9_-]*", rawProfile)
			}
			singleName = rawProfile
		}
		var listNames []string
		if hasProfiles {
			parsed, err := drain.ParseProfileList(rawProfiles)
			if err != nil {
				return "", nil, nil, "", "", drain.Usagef("environment variable TILLR_DRAIN_PROFILES=%q: %v", rawProfiles, err)
			}
			listNames = parsed
		}
		if hasProfile && hasProfiles {
			if len(listNames) != 1 || listNames[0] != singleName {
				return "", nil, nil, "", "", drain.Usagef("environment variables TILLR_DRAIN_PROFILE=%q and TILLR_DRAIN_PROFILES=%q disagree; set one", rawProfile, rawProfiles)
			}
		}
		if hasProfiles {
			refs, err := drain.ResolveProfilePaths(listNames)
			if err != nil {
				return "", nil, nil, "", "", drain.Usagef("environment variable TILLR_DRAIN_PROFILES=%q: %v", rawProfiles, err)
			}
			return refs[0], listNames, refs, sourceEnv, "TILLR_DRAIN_PROFILES", nil
		}
		resolved, err := drain.ResolveProfilePath(singleName)
		if err != nil {
			return "", nil, nil, "", "", drain.Usagef("environment variable TILLR_DRAIN_PROFILE=%q: %v", rawProfile, err)
		}
		return resolved, []string{singleName}, []string{resolved}, sourceEnv, "TILLR_DRAIN_PROFILE", nil
	}
	// 4. Built-in default: <root>/config/tillr/backend-policy.json.
	defaultPolicy := filepath.Join(root, "config", "tillr", "backend-policy.json")
	if _, statErr := os.Stat(defaultPolicy); !os.IsNotExist(statErr) || !drain.CommandOnlyWorkflows(root) {
		return defaultPolicy, nil, nil, sourceBuiltinDefault, "", nil
	}
	return "", nil, nil, sourceBuiltinDefault, "", nil
}
