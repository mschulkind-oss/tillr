package main

import (
	"encoding/json"
	"fmt"

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
		fmt.Printf("policy in force: %s (%s)\n%s", state.Hash, state.Ref, policy.Description())
		if state.Refusal != "" {
			fmt.Printf("policy on disk is not the policy in force: %s\n", state.Refusal)
		}
	}
	return record.ExitOK
}
