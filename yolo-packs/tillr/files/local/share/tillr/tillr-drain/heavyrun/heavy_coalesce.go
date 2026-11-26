package heavyrun

import (
	"bufio"
	"os"
	"strings"
)

type failureKey struct {
	testName    string
	failureLine string
}

// coalesceChecksByFailure merges failing checks that report the same single
// failing test and the same failure line into one group under the first
// landing that saw that failure, even when attribution named different
// first-red commits for each check.
func coalesceChecksByFailure(order []string, byLanding map[string][]HeavyCheck) []string {
	if len(order) <= 1 {
		return order
	}
	owner := map[failureKey]string{}
	outOrder := make([]string, 0, len(order))
	for _, sha := range order {
		checks := byLanding[sha]
		kept := make([]HeavyCheck, 0, len(checks))
		for _, check := range checks {
			testName, failureLine := checkTestFailureSignature(check)
			if testName == "" || failureLine == "" {
				kept = append(kept, check)
				continue
			}
			key := failureKey{testName: testName, failureLine: failureLine}
			if target, ok := owner[key]; ok && target != sha {
				byLanding[target] = append(byLanding[target], check)
				continue
			}
			if _, ok := owner[key]; !ok {
				owner[key] = sha
			}
			kept = append(kept, check)
		}
		byLanding[sha] = kept
		if len(kept) > 0 {
			outOrder = append(outOrder, sha)
		}
	}
	return outOrder
}

func checkTestFailureSignature(check HeavyCheck) (string, string) {
	logRef := check.LogPath
	if logRef == "" {
		logRef = check.LogRef
	}
	if logRef == "" {
		return "", ""
	}
	f, err := openHeavyCheckLog(logRef)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	roots := map[string]bool{}
	var singleRoot string
	if strings.HasPrefix(check.CheckID, "go-test:") {
		parts := strings.SplitN(strings.TrimPrefix(check.CheckID, "go-test:"), ":", 3)
		if len(parts) == 3 && parts[2] != "" {
			root := heavyTestRootName(parts[2])
			roots[root] = true
			singleRoot = root
		}
	}

	var (
		inTriage        bool
		triageCases     = map[string]bool{}
		singleTriage    string
		triageAssertion string
		lastFailureLine string
	)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !inTriage && strings.HasPrefix(line, "fork-check: triage -- ") {
			inTriage = true
			continue
		}
		if inTriage {
			if line == "" || (line[0] != ' ' && line[0] != '\t') {
				inTriage = false
			} else {
				switch {
				case strings.HasPrefix(trimmed, "case: "):
					name := strings.TrimPrefix(trimmed, "case: ")
					if idx := strings.LastIndex(name, " -- "); idx >= 0 {
						name = name[:idx]
					}
					name = strings.TrimSpace(name)
					if name != "" {
						root := heavyTestRootName(name)
						triageCases[root] = true
						singleTriage = root
					}
				case strings.HasPrefix(trimmed, "assertion: "):
					triageAssertion = strings.TrimSpace(strings.TrimPrefix(trimmed, "assertion: "))
				}
				continue
			}
		}

		if strings.HasPrefix(trimmed, "--- FAIL:") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "--- FAIL:"))
			if fields := strings.Fields(rest); len(fields) > 0 {
				root := heavyTestRootName(fields[0])
				roots[root] = true
				singleRoot = root
			}
			continue
		}
		if isRunnerBoilerplateLine(trimmed) {
			continue
		}
		lastFailureLine = trimmed
	}
	if scanner.Err() != nil {
		return "", ""
	}
	if len(triageCases) == 1 && triageAssertion != "" {
		return singleTriage, triageAssertion
	}
	if len(roots) == 1 && lastFailureLine != "" {
		return singleRoot, lastFailureLine
	}
	return "", ""
}

func openHeavyCheckLog(logRef string) (*os.File, error) {
	whole := strings.TrimSuffix(logRef, ".triage.log")
	if whole != logRef {
		whole += ".log"
	}
	f, err := os.Open(whole)
	if err != nil && whole != logRef {
		f, err = os.Open(logRef)
	}
	return f, err
}

func heavyTestRootName(name string) string {
	if idx := strings.IndexByte(name, '/'); idx >= 0 {
		return name[:idx]
	}
	return name
}

var runnerBoilerplateExact = map[string]bool{
	"":     true,
	"FAIL": true,
	"PASS": true,
}

func isRunnerBoilerplateLine(trimmed string) bool {
	if runnerBoilerplateExact[trimmed] {
		return true
	}
	for _, prefix := range []string{
		"=== RUN",
		"=== PAUSE",
		"=== CONT",
		"=== NAME",
		"--- PASS:",
		"--- SKIP:",
		"FAIL\t",
		"ok\t",
		"ok ",
		"exit status ",
		"go-test-shards: ",
		"tillr-gate: triage of ",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}
