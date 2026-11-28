package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr/disabledtests"
	"github.com/mschulkind-oss/tillr/settings"
)

// disabledTestsFinding validates the tracked disabled-test list under root. An absent
// list is valid: no test is disabled.
func disabledTestsFinding(root string) (string, error) {
	cfg, err := settings.Read(root)
	if err != nil {
		return "", err
	}
	rel := cfg.DisabledTestsPath()
	if _, err := disabledtests.Load(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		return fmt.Sprintf("disabled-tests: %v", err), nil
	}
	return "", nil
}

func cmdDisabledTests(args []string) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "disabled-tests: could not judge: %v\n", err)
		return 4
	}
	finding, err := disabledTestsFinding(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "disabled-tests: could not judge: %v\n", err)
		return 4
	}
	if finding != "" {
		fmt.Println(finding)
		return 1
	}
	return 0
}
