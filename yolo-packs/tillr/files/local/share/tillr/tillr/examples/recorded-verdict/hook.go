//go:build ignore

// Record criterion judgments through the consumer CLI, then request completion.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "describe" {
		out, _ := json.Marshal(map[string]any{
			"hooks": map[string]any{
				"complete-item": []string{"--item", "--run", "--store", "--root", "--artifact-dir"},
			},
		})
		fmt.Println(string(out))
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "complete-item" {
		fmt.Fprintf(os.Stderr, "hook: expected describe or complete-item\n")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("complete-item", flag.ExitOnError)
	item := fs.String("item", "", "item id")
	run := fs.String("run", "", "run id")
	store := fs.String("store", "", "store path")
	root := fs.String("root", "", "root path")
	_ = fs.String("artifact-dir", "", "artifact directory")
	_ = fs.Parse(os.Args[2:])
	if *item == "" || *run == "" || *store == "" || *root == "" {
		fmt.Fprintln(os.Stderr, "hook: --item, --run, --store, and --root are required")
		os.Exit(2)
	}

	answerBytes, err := os.ReadFile(filepath.Join(*root, *item+"-assessment.json"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "hook: read assessment: %v\n", err)
		os.Exit(1)
	}
	var answer struct {
		Complete *bool  `json:"complete"`
		State    string `json:"state"`
		Verdict  string `json:"verdict"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(answerBytes, &answer); err != nil {
		fmt.Fprintf(os.Stderr, "hook: parse assessment: %v\n", err)
		os.Exit(1)
	}
	if answer.Complete != nil && !*answer.Complete {
		resp, _ := json.Marshal(map[string]any{
			"complete": false,
			"reason":   answer.Reason,
		})
		fmt.Println(string(resp))
		return
	}

	showOut, err := exec.Command("tillr", "item", "show", "--store", *store, "--item", *item).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "hook: tillr item show: %v\n", err)
		os.Exit(1)
	}
	var shown struct {
		Payload struct {
			Criteria []struct {
				CriterionID string `json:"criterion_id"`
				Text        string `json:"text"`
				ClaimClass  string `json:"claim_class"`
			} `json:"criteria"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(showOut, &shown); err != nil {
		fmt.Fprintf(os.Stderr, "hook: parse show: %v\n", err)
		os.Exit(1)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, criterion := range shown.Payload.Criteria {
		sum := sha256.Sum256([]byte(criterion.Text))
		verdict := map[string]any{
			"record_version": 1,
			"kind":           "item.verdict",
			"id":             *run + "-" + criterion.CriterionID,
			"producer": map[string]any{
				"system":  "tillr",
				"utility": "recorded-verdict-example",
				"version": "0.1.0",
			},
			"created_at": now,
			"payload": map[string]any{
				"item_id":        *item,
				"criterion_id":   criterion.CriterionID,
				"criterion_hash": hex.EncodeToString(sum[:]),
				"verdict":        answer.Verdict,
				"claim_class":    criterion.ClaimClass,
				"reason":         answer.Reason,
				"evidence":       []string{"doc:" + *item + "-assessment.json"},
				"actor_kind":     "mechanical",
				"actor":          "recorded-verdict-stub",
			},
		}
		vBytes, _ := json.Marshal(verdict)
		vPath := filepath.Join(*root, *item+"-"+criterion.CriterionID+"-verdict.json")
		if err := os.WriteFile(vPath, append(vBytes, '\n'), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "hook: write verdict: %v\n", err)
			os.Exit(1)
		}
		rec := exec.Command("tillr", "item", "verdict", "record", "--store", *store, "--input", vPath, "--now", now)
		rec.Stderr = os.Stderr
		if err := rec.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "hook: record verdict: %v\n", err)
			os.Exit(1)
		}
	}

	resp, _ := json.Marshal(map[string]any{
		"complete":     true,
		"state":        answer.State,
		"reason":       answer.Reason,
		"resolves_via": "doc:" + *item + "-assessment.json",
	})
	fmt.Println(string(resp))
}
