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
			fmt.Printf("policy selection: source=%s", state.Source)
			if state.Profile != "" {
				fmt.Printf(" profile=%s", state.Profile)
			}
			if len(state.Profiles) > 1 {
				fmt.Printf(" profiles=%s (%d/%d)", strings.Join(state.Profiles, ","), state.ProfileIndex+1, len(state.Profiles))
			}
			fmt.Println()
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
