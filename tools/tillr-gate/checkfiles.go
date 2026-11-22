package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// checkFile is one JSON declaration beside its program and tests. Execution policy
// fields are required; the input and scheduling defaults are those in gate-rework
// Section 6.4 (docs/design/gate-rework.md#64-a-check-is-one-file).
// Pointers distinguish an omitted field from an explicitly invalid zero or null.
type checkFile struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Command   []string `json:"command"`
	Scope     string   `json:"scope"`
	Tier      string   `json:"tier"`
	BudgetMS  *int     `json:"budget_ms"`
	TimeoutMS *int     `json:"timeout_ms"`
	Reads     []string `json:"reads"`
	Tools     []string `json:"tools"`
	Needs     []string `json:"needs"`
	Remember  *bool    `json:"remember"`
	Writes    []string `json:"writes"`
	Exclusive bool     `json:"exclusive"`
}

// loadCheckDirectory loads every direct *.json child, in filename order. Other
// files are programs or tests, not declarations. Loading is read-only and completes
// validation of the entire directory before a caller can plan or run any check.
func loadCheckDirectory(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, externalf("reading check directory %s: %v", dir, err)
	}
	reg := &Registry{Declarations: []Declaration{}, InProcess: []InProcessCheck{}}
	hash := sha256.New()
	seen := map[string]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil, externalf("check declaration %s: expected a regular file", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, externalf("reading check declaration %s: %v", path, err)
		}
		decl, err := parseCheckDeclaration(path, raw)
		if err != nil {
			return nil, err
		}
		if previous, ok := seen[decl.ID]; ok {
			return nil, externalf("check declaration %s: id %q is already declared in %s", path, decl.ID, previous)
		}
		seen[decl.ID] = path
		reg.Declarations = append(reg.Declarations, decl)
		// The digest binds the filenames and bytes without depending on the absolute
		// directory, so copies of the same declarations plan identically.
		fmt.Fprintf(hash, "%d:%s%d:", len(entry.Name()), entry.Name(), len(raw))
		hash.Write(raw)
	}
	if len(reg.Declarations) == 0 {
		return nil, externalf("check directory %s: no *.json declarations", dir)
	}
	reg.Digest = hex.EncodeToString(hash.Sum(nil))
	return reg, nil
}

func parseCheckDeclaration(path string, raw []byte) (Declaration, error) {
	var file checkFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return Declaration{}, externalf("check declaration %s: %v", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Declaration{}, externalf("check declaration %s: expected exactly one JSON object", path)
	}
	if err := file.validate(); err != nil {
		return Declaration{}, externalf("check declaration %s: %v", path, err)
	}
	declarationHash := sha256.Sum256(raw)
	decl := Declaration{
		DeclarationDigest: hex.EncodeToString(declarationHash[:]),

		ID: file.ID, Kind: file.ID, Label: file.Label, Command: file.Command,
		Scope: file.Scope, Tier: file.Tier, Tools: file.Tools, Needs: file.Needs,
		Remember: file.Remember, Writes: file.Writes, Exclusive: file.Exclusive,
	}
	if decl.Remember == nil {
		remember := true
		decl.Remember = &remember
	}
	if file.BudgetMS != nil {
		decl.BudgetMS = *file.BudgetMS
	}
	if file.TimeoutMS != nil {
		decl.TimeoutMS = *file.TimeoutMS
	}
	if len(file.Reads) == 0 {
		decl.undeclaredReads = true
		file.Reads = []string{"."}
	}
	for _, readPath := range file.Reads {
		decl.Watches = append(decl.Watches, Pattern{Path: readPath})
		decl.Selects = append(decl.Selects, Pattern{Path: readPath})
		if readPath == "." {
			decl.Forced = "whole-tree"
		}
	}
	if file.Scope == "change" {
		decl.Forced = "change"
	}
	return decl, nil
}

func (file checkFile) validate() error {
	for _, field := range []struct{ name, value string }{{"id", file.ID}, {"label", file.Label}} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if len(file.Command) == 0 || strings.TrimSpace(file.Command[0]) == "" {
		return fmt.Errorf("command must name a program")
	}
	if file.Scope != "change" && file.Scope != "tree" {
		return fmt.Errorf("scope must be change or tree")
	}
	if file.Tier != "fast" && file.Tier != "heavy" {
		return fmt.Errorf("tier must be fast or heavy")
	}
	if file.Tier == "fast" && file.TimeoutMS != nil {
		return fmt.Errorf("timeout_ms is not allowed for tier fast; use budget_ms as the target")
	}
	if file.Tier == "fast" && file.BudgetMS == nil {
		return fmt.Errorf("budget_ms is required for tier fast")
	}
	if file.Tier == "heavy" && file.TimeoutMS == nil {
		return fmt.Errorf("timeout_ms is required for tier heavy")
	}
	if file.BudgetMS != nil && *file.BudgetMS <= 0 {
		return fmt.Errorf("budget_ms must be positive")
	}
	if file.TimeoutMS != nil && *file.TimeoutMS <= 0 {
		return fmt.Errorf("timeout_ms must be positive")
	}
	for _, field := range []struct {
		name   string
		values []string
	}{{"reads", file.Reads}, {"tools", file.Tools}, {"needs", file.Needs}, {"writes", file.Writes}} {
		for _, value := range field.values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s contains an empty value", field.name)
			}
		}
	}
	return nil
}
