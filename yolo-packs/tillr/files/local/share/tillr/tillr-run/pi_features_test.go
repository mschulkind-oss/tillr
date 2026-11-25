package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPiWithStub runs the adapter against a stand-in `pi` and answers its exit code and
// combined output. The stand-in's body decides what `pi --features` prints.
func runPiWithStub(t *testing.T, body string, env ...string) (int, string, string) {
	t.Helper()
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	base := scratch(t, "pi-features")
	bin := filepath.Join(base, "bin")
	ran := filepath.Join(base, "ran")
	writeExecutable(t, filepath.Join(bin, "pi"), "#!/bin/sh\n"+body+"\ncat > /dev/null\n: > \"$STUB_RAN\"\n")
	worktree := filepath.Join(base, "lane", "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "lane", "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
	cmd.Dir = worktree
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "TILLR_RESUME_PROMPT_FILE=") && !strings.HasPrefix(entry, "TILLR_SESSION_MODE=") &&
			!strings.HasPrefix(entry, "TILLR_PI_FEATURES=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_RAN="+ran)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, string(out), ran
}

// The land and obstacle tools park a tool call and the loop resumes it later. A pi that
// cannot park a call would run the worker and lose its ending, so the adapter asks
// `pi --features` first and refuses to start a run on a pi that does not list the feature.
func TestThePiAdapterRefusesAPiWithoutParkedToolCalls(t *testing.T) {
	for name, probe := range map[string]string{
		"an empty list":     `if [ "${1:-}" = --features ]; then echo '[]'; exit 0; fi`,
		"another feature":   `if [ "${1:-}" = --features ]; then echo '["something-else"]'; exit 0; fi`,
		"output not a list": `if [ "${1:-}" = --features ]; then echo 'pi 1.6.0'; exit 0; fi`,
	} {
		t.Run(name, func(t *testing.T) {
			code, out, ran := runPiWithStub(t, probe, "TILLR_PI_PROBE_SECONDS=1")
			if code != 2 {
				t.Fatalf("exit %d, want the refusal's 2: %s", code, out)
			}
			for _, want := range []string{"run-pi: refused:", "parked-tool-calls", "pi --features", "mschulkind-oss/tillr-pi"} {
				if !strings.Contains(out, want) {
					t.Fatalf("the refusal does not say %q: %s", want, out)
				}
			}
			if _, err := os.Stat(ran); err == nil {
				t.Fatalf("the run started on a pi without the feature")
			}
		})
	}
}

func TestThePiAdapterRefusesWhenTheFeatureProbeFails(t *testing.T) {
	for name, tc := range map[string]struct {
		probe string
		wants []string
	}{
		"a probe that times out": {
			probe: `if [ "${1:-}" = --features ]; then echo x >> "$PROBE_COUNT"; sleep 600; fi`,
			wants: []string{"run-pi: refused:", "probe", "failed", "timed out after 1 seconds", "unknown", "parked-tool-calls"},
		},
		"an unknown flag": {
			probe: `if [ "${1:-}" = --features ]; then echo x >> "$PROBE_COUNT"; printf 'unknown option --features\nmore details\n' >&2; exit 1; fi`,
			wants: []string{"run-pi: refused:", "probe", "failed", "1", "unknown option --features", "unknown", "parked-tool-calls"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			countFile := filepath.Join(scratch(t, "probe-count"), "count")
			code, out, ran := runPiWithStub(t, tc.probe, "TILLR_PI_PROBE_SECONDS=1", "PROBE_COUNT="+countFile)
			if code != 2 {
				t.Fatalf("exit %d, want the refusal's 2: %s", code, out)
			}
			for _, want := range tc.wants {
				if !strings.Contains(out, want) {
					t.Fatalf("the refusal does not say %q: %s", want, out)
				}
			}
			for _, unwanted := range []string{"lacks the feature", "mschulkind-oss/tillr-pi"} {
				if strings.Contains(out, unwanted) {
					t.Fatalf("a failed probe must not say %q: %s", unwanted, out)
				}
			}
			if _, err := os.Stat(ran); err == nil {
				t.Fatalf("the run started when the probe failed")
			}
			countBytes, err := os.ReadFile(countFile)
			if err != nil {
				t.Fatalf("reading probe count: %v", err)
			}
			if got := strings.Count(string(countBytes), "\n"); got != 2 {
				t.Fatalf("probed %d times, want 2 (initial attempt plus one retry)", got)
			}
		})
	}
}

func TestThePiAdapterRetriesAFailedFeatureProbeOnceThenRuns(t *testing.T) {
	countFile := filepath.Join(scratch(t, "probe-retry"), "count")
	probe := `if [ "${1:-}" = --features ]; then
		if [ ! -f "$PROBE_FLAG" ]; then
			: > "$PROBE_FLAG"
			echo "transient failure" >&2
			exit 1
		fi
		echo '["parked-tool-calls"]'
		exit 0
	fi`
	code, out, ran := runPiWithStub(t, probe, "PROBE_FLAG="+countFile)
	if code != 0 {
		t.Fatalf("exit %d, want 0 after retry: %s", code, out)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatalf("the run did not start after the retry succeeded: %s", out)
	}
}

func TestThePiAdapterRunsAPiThatListsParkedToolCalls(t *testing.T) {
	probe := `if [ "${1:-}" = --features ]; then echo '["other","parked-tool-calls"]'; exit 0; fi`
	code, out, ran := runPiWithStub(t, probe)
	if code != 0 {
		t.Fatalf("exit %d, want 0: %s", code, out)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatalf("the run did not start: %s", out)
	}
}

// A caller that has already established what its pi supports states it, and the adapter
// does not run the probe: the stand-in below would fail the probe if it were asked.
func TestThePiAdapterTakesStatedFeaturesWithoutProbing(t *testing.T) {
	probe := `if [ "${1:-}" = --features ]; then echo probed >&2; exit 9; fi`
	code, out, ran := runPiWithStub(t, probe, "TILLR_PI_FEATURES=parked-tool-calls")
	if code != 0 || strings.Contains(out, "probed") {
		t.Fatalf("exit %d, want 0 and no probe: %s", code, out)
	}
	if _, err := os.Stat(ran); err != nil {
		t.Fatalf("the run did not start: %s", out)
	}
	code, out, _ = runPiWithStub(t, probe, "TILLR_PI_FEATURES=something-else")
	if code != 2 || !strings.Contains(out, "parked-tool-calls") {
		t.Fatalf("exit %d, want the refusal for a stated list without the feature: %s", code, out)
	}
}
