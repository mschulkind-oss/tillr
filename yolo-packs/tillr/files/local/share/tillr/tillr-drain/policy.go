package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
)

func policyFace(args []string) int {
	fs := newFlagSet("policy")
	root := fs.String("root", ".", "the repository whose loop accepted the policy")
	asJSON := fs.Bool("json", false, "print the recorded policy in force as JSON")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	state, err := drain.ReadPolicyState(*root)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		raw, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return fail(err)
		}
		fmt.Println(string(raw))
	} else {
		var policy drain.Policy
		if err := json.Unmarshal([]byte(state.Content), &policy); err != nil {
			return fail(err)
		}
		fmt.Printf("policy in force: %s (%s)\n", state.Hash, state.Ref)
		if state.Source != "" || state.Profile != "" || len(state.Profiles) > 0 {
			var parts []string
			if state.Source != "" {
				parts = append(parts, "source="+state.Source)
			}
			if state.Profile != "" {
				parts = append(parts, "profile="+state.Profile)
			}
			if len(state.Profiles) > 0 {
				parts = append(parts, fmt.Sprintf("profiles=%s (%d/%d)", strings.Join(state.Profiles, ","), state.ProfileIndex+1, len(state.Profiles)))
			}
			fmt.Printf("policy selection: %s\n", strings.Join(parts, " "))
		}
		if state.Why != "" {
			fmt.Printf("policy reason: %s\n", state.Why)
		}
		if state.SwitchedFrom != "" {
			if state.SwitchedAt != nil {
				fmt.Printf("switched from: %s at %s\n", state.SwitchedFrom, state.SwitchedAt.UTC().Format("2006-01-02T15:04:05Z"))
			} else {
				fmt.Printf("switched from: %s\n", state.SwitchedFrom)
			}
		}
		if len(state.SkippedProfiles) > 0 {
			fmt.Println("skipped profiles:")
			for _, sp := range state.SkippedProfiles {
				var winParts []string
				for _, w := range sp.Windows {
					winParts = append(winParts, fmt.Sprintf("%s=%g", w.Kind, w.Fraction))
				}
				winText := "none"
				if len(winParts) > 0 {
					winText = strings.Join(winParts, ",")
				}
				line := fmt.Sprintf("  %s (backend=%s windows=[%s] reserve=%g", sp.Profile, sp.Backend, winText, sp.Reserve)
				if sp.ReopenThreshold > 0 {
					line += fmt.Sprintf(" reopen<=%g", sp.ReopenThreshold)
				}
				if sp.ResetAt != nil {
					line += fmt.Sprintf(" resets=%s", sp.ResetAt.UTC().Format("2006-01-02T15:04:05Z"))
				}
				line += ")"
				fmt.Println(line)
			}
		}
		fmt.Print(policy.Description())
		if len(state.StartOptions) > 0 {
			fmt.Println("start options:")
			for _, opt := range state.StartOptions {
				if opt.Detail != "" {
					fmt.Printf("  %s=%q (%s: %s)\n", opt.Name, opt.Value, opt.Source, opt.Detail)
				} else {
					fmt.Printf("  %s=%q (%s)\n", opt.Name, opt.Value, opt.Source)
				}
			}
		}
		if state.Refusal != "" {
			fmt.Printf("policy on disk is not the policy in force: %s\n", state.Refusal)
		}
	}
	return record.ExitOK
}
