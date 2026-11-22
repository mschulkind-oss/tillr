package main

import (
	"encoding/json"
	"os"
)

const goBuildEvidenceEnv = "TILLR_GO_BUILD_EVIDENCE_OUT"

// GoBuildModule separates module reuse from the Go command's result cache.
// No package inventory is claimed when a remembered module bypasses Go.
type GoBuildModule struct {
	Module    string               `json:"module"`
	Execution string               `json:"execution"`
	Mode      string               `json:"mode,omitempty"`
	Discovery string               `json:"discovery,omitempty"`
	Packages  *[]GoPackageEvidence `json:"packages,omitempty"`
}

type GoPackageEvidence struct {
	ImportPath string `json:"import_path"`
	TestCache  string `json:"test_cache"`
}

func goBuildEvidencePath(dir string) string {
	file, err := os.CreateTemp(dir, "go-build-evidence-*.json")
	if err != nil {
		return ""
	}
	file.Close()
	return file.Name()
}

func readGoBuildEvidence(path string) []GoBuildModule {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var evidence []GoBuildModule
	if json.Unmarshal(body, &evidence) != nil {
		return nil
	}
	return evidence
}
