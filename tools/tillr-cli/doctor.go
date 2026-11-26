package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	// Match tillr-watch's producer registry so watch.LoadRules recognizes
	// shipped drain/item/run/land/gate event kinds.
	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/setupcheck"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

type doctorCheckResult struct {
	name   string
	ok     bool
	detail string
	fix    string
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tillr doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("root", ".", "repository root directory")
	storeFlag := fs.String("store", "", "path to the tillr state database (default <root>/.tillr/state.db)")
	policyFlag := fs.String("policy", "", "path to the seat policy JSON (default <root>/config/tillr/backend-policy.json)")
	briefFlag := fs.String("brief-template", "", "path to the brief template (default resolved from installation)")
	rulesFlag := fs.String("rules", "", "path to the watch rules directory (default <root>/config/tillr/watch)")
	checksFlag := fs.String("checks", "", "path to the gate checks directory (default <root>/tools/gate/checks)")

	if parseErr := fs.Parse(args); parseErr != nil {
		if errors.Is(parseErr, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: tillr doctor [--root <dir>] [--store <path>] [--policy <path>] [--brief-template <path>] [--rules <dir>] [--checks <dir>]")
			fs.SetOutput(stdout)
			fs.PrintDefaults()
			return record.ExitOK
		}
		fmt.Fprintf(stderr, "tillr doctor: %v\n", parseErr)
		return record.ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "tillr doctor: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return record.ExitUsage
	}

	root := strings.TrimSpace(*rootFlag)
	if root == "" {
		fmt.Fprintln(stderr, "tillr doctor: --root must not be empty")
		return record.ExitUsage
	}

	storePath := strings.TrimSpace(*storeFlag)
	if storePath == "" {
		storePath = filepath.Join(root, ".tillr", "state.db")
	}
	policyPath := strings.TrimSpace(*policyFlag)
	if policyPath == "" {
		policyPath = filepath.Join(root, "config", "tillr", "backend-policy.json")
	}
	briefPath := strings.TrimSpace(*briefFlag)
	rulesDir := strings.TrimSpace(*rulesFlag)
	if rulesDir == "" {
		rulesDir = filepath.Join(root, "config", "tillr", "watch")
	}
	checksDir := strings.TrimSpace(*checksFlag)
	if checksDir == "" {
		checksDir = filepath.Join(root, "tools", "gate", "checks")
	}

	cfg, settingsRes := checkDoctorSettings(root)
	results := []doctorCheckResult{
		checkDoctorStore(storePath),
		settingsRes,
		checkDoctorPolicy(root, policyPath),
		checkDoctorBriefTemplate(briefPath),
		checkDoctorDefaultWorkflow(root, cfg, settingsRes.ok),
		checkDoctorWatchRules(rulesDir),
		checkDoctorGateChecks(checksDir),
	}

	allOK := true
	for _, r := range results {
		detail := singleLine(r.detail)
		if r.ok {
			fmt.Fprintf(stdout, "ok   %s: %s\n", r.name, detail)
			continue
		}
		allOK = false
		fix := singleLine(r.fix)
		fmt.Fprintf(stdout, "FAIL %s: %s — fix: %s\n", r.name, detail, fix)
	}
	if !allOK {
		return record.ExitNegative
	}
	return record.ExitOK
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func checkDoctorStore(storePath string) doctorCheckResult {
	info, err := os.Stat(storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorCheckResult{
				name:   "store",
				detail: fmt.Sprintf("%s does not exist", storePath),
				fix:    fmt.Sprintf("tillr store init --store %s", storePath),
			}
		}
		return doctorCheckResult{
			name:   "store",
			detail: err.Error(),
			fix:    storePath,
		}
	}
	if info.IsDir() {
		return doctorCheckResult{
			name:   "store",
			detail: fmt.Sprintf("%s is a directory, not a store file", storePath),
			fix:    fmt.Sprintf("tillr store init --store %s", storePath),
		}
	}

	var version int
	var integrity string
	err = store.ReadWithoutCreating(context.Background(), storePath, func(s *store.Store) error {
		var err error
		version, err = s.SchemaVersion()
		if err != nil {
			return err
		}
		if version <= 0 || version > store.SchemaVersion {
			return fmt.Errorf("schema version %d is outside supported range 1..%d", version, store.SchemaVersion)
		}
		integrity, err = s.Integrity()
		if err != nil {
			return err
		}
		if integrity != "ok" {
			return fmt.Errorf("integrity check returned %q", integrity)
		}
		return nil
	})
	if err != nil {
		fix := fmt.Sprintf("tillr store check --store %s", storePath)
		if store.NeedsRecovery(err) {
			fix = fmt.Sprintf("tillr store recover --store %s", storePath)
		}
		return doctorCheckResult{
			name:   "store",
			detail: fmt.Sprintf("%s: %v", storePath, err),
			fix:    fix,
		}
	}

	return doctorCheckResult{
		name:   "store",
		ok:     true,
		detail: fmt.Sprintf("%s (schema v%d, integrity %s)", storePath, version, integrity),
	}
}

func checkDoctorSettings(root string) (settings.Settings, doctorCheckResult) {
	settingsPath := filepath.Join(root, ".tillr.toml")
	cfg, err := settings.Read(root)
	if err != nil {
		return settings.Settings{}, doctorCheckResult{
			name:   "settings",
			detail: err.Error(),
			fix:    settingsPath,
		}
	}
	if _, statErr := os.Stat(settingsPath); errors.Is(statErr, os.ErrNotExist) {
		return cfg, doctorCheckResult{
			name:   "settings",
			ok:     true,
			detail: fmt.Sprintf("%s (not present; using defaults)", settingsPath),
		}
	}
	return cfg, doctorCheckResult{
		name:   "settings",
		ok:     true,
		detail: settingsPath,
	}
}

type doctorPolicyDoc struct {
	DefaultBackend string                     `json:"default_backend"`
	Seats          map[string]json.RawMessage `json:"seats"`
	Backends       map[string]struct {
		Adapter string   `json:"adapter"`
		Model   string   `json:"model"`
		Models  []string `json:"models"`
	} `json:"backends"`
}

func checkDoctorPolicy(root, policyPath string) doctorCheckResult {
	info, err := os.Stat(policyPath)
	if err != nil {
		fix := policyPath
		if errors.Is(err, os.ErrNotExist) {
			fix = fmt.Sprintf("--policy <path> or create %s", policyPath)
		}
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("%s: %v", policyPath, err),
			fix:    fix,
		}
	}
	if info.IsDir() {
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("%s is a directory, not a policy file", policyPath),
			fix:    fmt.Sprintf("--policy <path> or replace %s", policyPath),
		}
	}

	raw, err := os.ReadFile(policyPath)
	if err != nil {
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("reading %s: %v", policyPath, err),
			fix:    policyPath,
		}
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	var doc doctorPolicyDoc
	if err := dec.Decode(&doc); err != nil {
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("%s: invalid JSON: %v", policyPath, err),
			fix:    policyPath,
		}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("%s: must be a single JSON object", policyPath),
			fix:    policyPath,
		}
	}

	if len(doc.Backends) == 0 {
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("%s: backends must not be empty", policyPath),
			fix:    policyPath,
		}
	}
	if strings.TrimSpace(doc.DefaultBackend) == "" {
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("%s: default_backend must not be empty", policyPath),
			fix:    policyPath,
		}
	}
	if _, ok := doc.Backends[doc.DefaultBackend]; !ok {
		return doctorCheckResult{
			name:   "policy",
			detail: fmt.Sprintf("%s: default_backend %q is not declared in backends", policyPath, doc.DefaultBackend),
			fix:    policyPath,
		}
	}

	seatNames := make([]string, 0, len(doc.Seats))
	for seat := range doc.Seats {
		seatNames = append(seatNames, seat)
	}
	sort.Strings(seatNames)
	for _, seat := range seatNames {
		if strings.TrimSpace(seat) == "" {
			return doctorCheckResult{
				name:   "policy",
				detail: fmt.Sprintf("%s: seat name must not be empty", policyPath),
				fix:    policyPath,
			}
		}
		backends, err := parseDoctorSeatBackends(doc.Seats[seat])
		if err != nil {
			return doctorCheckResult{
				name:   "policy",
				detail: fmt.Sprintf("%s: seat %q: %v", policyPath, seat, err),
				fix:    policyPath,
			}
		}
		if len(backends) == 0 {
			return doctorCheckResult{
				name:   "policy",
				detail: fmt.Sprintf("%s: seat %q has no backends", policyPath, seat),
				fix:    policyPath,
			}
		}
		for _, bname := range backends {
			if _, ok := doc.Backends[bname]; !ok {
				return doctorCheckResult{
					name:   "policy",
					detail: fmt.Sprintf("%s: seat %q references unknown backend %q", policyPath, seat, bname),
					fix:    policyPath,
				}
			}
		}
	}

	backendNames := make([]string, 0, len(doc.Backends))
	for name := range doc.Backends {
		backendNames = append(backendNames, name)
	}
	sort.Strings(backendNames)
	for _, name := range backendNames {
		if strings.TrimSpace(name) == "" {
			return doctorCheckResult{
				name:   "policy",
				detail: fmt.Sprintf("%s: backend name must not be empty", policyPath),
				fix:    policyPath,
			}
		}
		b := doc.Backends[name]
		if strings.TrimSpace(b.Adapter) == "" {
			return doctorCheckResult{
				name:   "policy",
				detail: fmt.Sprintf("%s: backend %q names no adapter", policyPath, name),
				fix:    policyPath,
			}
		}
		hasModel := strings.TrimSpace(b.Model) != ""
		if !hasModel {
			for _, m := range b.Models {
				if strings.TrimSpace(m) != "" {
					hasModel = true
					break
				}
			}
		}
		if !hasModel {
			return doctorCheckResult{
				name:   "policy",
				detail: fmt.Sprintf("%s: backend %q must set model or models", policyPath, name),
				fix:    policyPath,
			}
		}
		if _, err := setupcheck.CheckAdapter(root, b.Adapter); err != nil {
			fix := b.Adapter
			if strings.Contains(err.Error(), "is not executable") {
				fix = fmt.Sprintf("chmod +x %s", b.Adapter)
			}
			return doctorCheckResult{
				name:   "policy",
				detail: fmt.Sprintf("%s: backend %q: %v", policyPath, name, err),
				fix:    fix,
			}
		}
	}

	return doctorCheckResult{
		name:   "policy",
		ok:     true,
		detail: fmt.Sprintf("%s (%d backend(s), all adapters executable)", policyPath, len(doc.Backends)),
	}
}

func parseDoctorSeatBackends(raw json.RawMessage) ([]string, error) {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if strings.TrimSpace(single) == "" {
			return nil, fmt.Errorf("backend name must not be empty")
		}
		return []string{single}, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var cfg struct {
			Backends json.RawMessage `json:"backends"`
			Pick     string          `json:"pick"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		if len(cfg.Backends) == 0 {
			return nil, fmt.Errorf("seat object requires backends")
		}
		return parseDoctorSeatBackends(cfg.Backends)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("expected backend string, list, or {backends, pick}")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("backend list must not be empty")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		var name string
		if json.Unmarshal(entry, &name) == nil {
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("backend name must not be empty")
			}
			names = append(names, name)
			continue
		}
		var weighted struct {
			Backend string `json:"backend"`
		}
		if err := json.Unmarshal(entry, &weighted); err != nil || strings.TrimSpace(weighted.Backend) == "" {
			return nil, fmt.Errorf("expected backend string or {backend, share} object")
		}
		names = append(names, weighted.Backend)
	}
	return names, nil
}

func checkDoctorBriefTemplate(briefPath string) doctorCheckResult {
	if briefPath != "" {
		info, err := os.Stat(briefPath)
		if err != nil {
			return doctorCheckResult{
				name:   "brief-template",
				detail: fmt.Sprintf("%s: %v", briefPath, err),
				fix:    fmt.Sprintf("--brief-template <path> (checked %s)", briefPath),
			}
		}
		if !info.Mode().IsRegular() {
			return doctorCheckResult{
				name:   "brief-template",
				detail: fmt.Sprintf("%s is not a regular file", briefPath),
				fix:    fmt.Sprintf("--brief-template <path> (checked %s)", briefPath),
			}
		}
		return doctorCheckResult{
			name:   "brief-template",
			ok:     true,
			detail: briefPath,
		}
	}

	resolved, err := setupcheck.ResolveBriefTemplate()
	if err != nil {
		return doctorCheckResult{
			name:   "brief-template",
			detail: err.Error(),
			fix:    "set TILLR_INSTALL_ROOT or pass --brief-template <path>",
		}
	}
	return doctorCheckResult{
		name:   "brief-template",
		ok:     true,
		detail: resolved,
	}
}

func checkDoctorDefaultWorkflow(root string, cfg settings.Settings, settingsOK bool) doctorCheckResult {
	if !settingsOK {
		return doctorCheckResult{
			name:   "default-workflow",
			detail: "cannot inspect [workflows.default] because .tillr.toml failed to parse",
			fix:    filepath.Join(root, ".tillr.toml"),
		}
	}

	if len(cfg.WorkflowDefaults) == 0 {
		ref := "planned-build@v1"
		wfPath, _, err := setupcheck.ResolveWorkflowFile(root, ref)
		if err != nil {
			return doctorCheckResult{
				name:   "default-workflow",
				detail: fmt.Sprintf("%s: %v", ref, err),
				fix:    fmt.Sprintf("config/tillr/workflows/%s.json or set TILLR_INSTALL_ROOT", ref),
			}
		}
		if err := validateJSONObjectFile(wfPath); err != nil {
			return doctorCheckResult{
				name:   "default-workflow",
				detail: fmt.Sprintf("%s (%s): %v", ref, wfPath, err),
				fix:    wfPath,
			}
		}
		return doctorCheckResult{
			name:   "default-workflow",
			ok:     true,
			detail: fmt.Sprintf("%s (%s)", ref, wfPath),
		}
	}

	seats := make([]string, 0, len(cfg.WorkflowDefaults))
	for seat := range cfg.WorkflowDefaults {
		seats = append(seats, seat)
	}
	sort.Strings(seats)

	for _, seat := range seats {
		ref := cfg.WorkflowDefaults[seat]
		if ref == "single-worker@v1" {
			continue
		}
		wfPath, _, err := setupcheck.ResolveWorkflowFile(root, ref)
		if err != nil {
			return doctorCheckResult{
				name:   "default-workflow",
				detail: fmt.Sprintf("seat %s workflow %q: %v", seat, ref, err),
				fix:    fmt.Sprintf("config/tillr/workflows/%s.json or %s ([workflows.default])", ref, filepath.Join(root, ".tillr.toml")),
			}
		}
		if err := validateJSONObjectFile(wfPath); err != nil {
			return doctorCheckResult{
				name:   "default-workflow",
				detail: fmt.Sprintf("seat %s workflow %q (%s): %v", seat, ref, wfPath, err),
				fix:    wfPath,
			}
		}
	}

	return doctorCheckResult{
		name:   "default-workflow",
		ok:     true,
		detail: fmt.Sprintf("%d configured seat default(s) valid", len(seats)),
	}
}

func validateJSONObjectFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return err
	}
	if obj == nil {
		return fmt.Errorf("expected a JSON object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("must contain a single JSON object")
	}
	return nil
}

func checkDoctorWatchRules(rulesDir string) doctorCheckResult {
	info, err := os.Stat(rulesDir)
	if err != nil {
		return doctorCheckResult{
			name:   "watch-rules",
			detail: fmt.Sprintf("%s: %v", rulesDir, err),
			fix:    rulesDir,
		}
	}
	if !info.IsDir() {
		return doctorCheckResult{
			name:   "watch-rules",
			detail: fmt.Sprintf("%s is not a directory", rulesDir),
			fix:    rulesDir,
		}
	}
	rules, err := watch.LoadRules(rulesDir)
	if err != nil {
		return doctorCheckResult{
			name:   "watch-rules",
			detail: err.Error(),
			fix:    rulesDir,
		}
	}
	return doctorCheckResult{
		name:   "watch-rules",
		ok:     true,
		detail: fmt.Sprintf("%s (%d rule(s))", rulesDir, len(rules)),
	}
}

func checkDoctorGateChecks(checksDir string) doctorCheckResult {
	info, err := os.Stat(checksDir)
	if err != nil {
		return doctorCheckResult{
			name:   "gate-checks",
			detail: fmt.Sprintf("%s: %v", checksDir, err),
			fix:    checksDir,
		}
	}
	if !info.IsDir() {
		return doctorCheckResult{
			name:   "gate-checks",
			detail: fmt.Sprintf("%s is not a directory", checksDir),
			fix:    checksDir,
		}
	}

	entries, err := os.ReadDir(checksDir)
	if err != nil {
		return doctorCheckResult{
			name:   "gate-checks",
			detail: fmt.Sprintf("reading %s: %v", checksDir, err),
			fix:    checksDir,
		}
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, filepath.Join(checksDir, entry.Name()))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return doctorCheckResult{
			name:   "gate-checks",
			detail: fmt.Sprintf("%s contains no *.json check declarations", checksDir),
			fix:    checksDir,
		}
	}

	seenIDs := make(map[string]string, len(files))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return doctorCheckResult{
				name:   "gate-checks",
				detail: fmt.Sprintf("reading %s: %v", file, err),
				fix:    file,
			}
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		var decl struct {
			ID      string   `json:"id"`
			Label   string   `json:"label"`
			Command []string `json:"command"`
			Scope   string   `json:"scope"`
			Tier    string   `json:"tier"`
		}
		if err := dec.Decode(&decl); err != nil {
			return doctorCheckResult{
				name:   "gate-checks",
				detail: fmt.Sprintf("%s: invalid JSON: %v", file, err),
				fix:    file,
			}
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return doctorCheckResult{
				name:   "gate-checks",
				detail: fmt.Sprintf("%s: must be a single JSON object", file),
				fix:    file,
			}
		}
		if strings.TrimSpace(decl.ID) == "" ||
			strings.TrimSpace(decl.Label) == "" ||
			len(decl.Command) == 0 ||
			strings.TrimSpace(decl.Scope) == "" ||
			strings.TrimSpace(decl.Tier) == "" {
			return doctorCheckResult{
				name:   "gate-checks",
				detail: fmt.Sprintf("%s: requires non-empty id, label, command, scope, and tier", file),
				fix:    file,
			}
		}
		for _, arg := range decl.Command {
			if strings.TrimSpace(arg) == "" {
				return doctorCheckResult{
					name:   "gate-checks",
					detail: fmt.Sprintf("%s: command entries must not be empty", file),
					fix:    file,
				}
			}
		}
		if prev, dup := seenIDs[decl.ID]; dup {
			return doctorCheckResult{
				name:   "gate-checks",
				detail: fmt.Sprintf("%s: duplicate check id %q (first in %s)", file, decl.ID, prev),
				fix:    file,
			}
		}
		seenIDs[decl.ID] = file
	}

	return doctorCheckResult{
		name:   "gate-checks",
		ok:     true,
		detail: fmt.Sprintf("%s (%d check(s))", checksDir, len(files)),
	}
}
