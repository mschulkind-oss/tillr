//go:build ignore

// Deterministic command and agent stand-ins, not a real product reproduction.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "stage: missing workflow node")
		os.Exit(1)
	}
	node := os.Args[1]
	if (node != "reproduce" && node != "investigate") || os.Getenv("TILLR_NODE_ID") != node {
		fmt.Fprintln(os.Stderr, "stage: unexpected workflow node")
		os.Exit(1)
	}
	exeDir := filepath.Dir(os.Args[0])
	if _, err := os.Stat(filepath.Join(exeDir, "outcomes.json")); err != nil {
		if wd, werr := os.Getwd(); werr == nil {
			if _, serr := os.Stat(filepath.Join(wd, "outcomes.json")); serr == nil {
				exeDir = wd
			}
		}
	}
	if rootEnv := os.Getenv("TILLR_EXAMPLE_DIR"); rootEnv != "" {
		exeDir = rootEnv
	}
	item := os.Getenv("TILLR_ITEM_ID")
	rawOutcomes, err := os.ReadFile(filepath.Join(exeDir, "outcomes.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stage: read outcomes: %v\n", err)
		os.Exit(1)
	}
	var outcomes map[string]json.RawMessage
	if err := json.Unmarshal(rawOutcomes, &outcomes); err != nil {
		fmt.Fprintf(os.Stderr, "stage: parse outcomes: %v\n", err)
		os.Exit(1)
	}
	outcomeRaw, ok := outcomes[item]
	if !ok {
		fmt.Fprintf(os.Stderr, "stage: unknown item %q\n", item)
		os.Exit(1)
	}
	var outcome struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(outcomeRaw, &outcome); err != nil {
		fmt.Fprintf(os.Stderr, "stage: parse outcome: %v\n", err)
		os.Exit(1)
	}

	reproduction := filepath.Join(exeDir, item+"-reproduction.json")
	assessment := filepath.Join(exeDir, item+"-assessment.json")
	if node == "reproduce" {
		if err := os.WriteFile(reproduction, append(bytes.TrimSpace(outcomeRaw), '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "stage: write reproduction: %v\n", err)
			os.Exit(1)
		}
	} else {
		observed, err := os.ReadFile(reproduction)
		if err != nil {
			fmt.Fprintf(os.Stderr, "stage: read reproduction: %v\n", err)
			os.Exit(1)
		}
		var obsNorm, expNorm any
		if err := json.Unmarshal(observed, &obsNorm); err != nil {
			fmt.Fprintf(os.Stderr, "stage: parse reproduction: %v\n", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(outcomeRaw, &expNorm); err != nil {
			fmt.Fprintf(os.Stderr, "stage: parse expected: %v\n", err)
			os.Exit(1)
		}
		obsEnc, _ := json.Marshal(obsNorm)
		expEnc, _ := json.Marshal(expNorm)
		if !bytes.Equal(obsEnc, expEnc) {
			fmt.Fprintln(os.Stderr, "stage: reproduction disagrees with expected demonstration outcome")
			os.Exit(1)
		}
		if err := os.WriteFile(assessment, append(bytes.TrimSpace(observed), '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "stage: write assessment: %v\n", err)
			os.Exit(1)
		}
	}

	cwd, _ := os.Getwd()
	callLine, _ := json.Marshal(map[string]string{
		"item": item,
		"node": node,
		"cwd":  cwd,
	})
	f, err := os.OpenFile(filepath.Join(exeDir, "stage-calls.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stage: open stage-calls.jsonl: %v\n", err)
		os.Exit(1)
	}
	_, _ = f.Write(append(callLine, '\n'))
	_ = f.Close()

	fmt.Printf("%s: %s: %s\n", node, item, outcome.Reason)
	fmt.Println("RESULT: done")
}
