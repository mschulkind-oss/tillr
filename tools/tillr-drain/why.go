package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func whyFace(args []string) int {
	fs := newFlagSet("why")
	path := fs.String("store", "", "the store to read (required)")
	policyRef := fs.String("policy", "", "the loop's policy file; absent means no-policy, not a guessed backend")
	lanesArg := fs.String("lanes", "", "comma-separated lanes offered by the loop (default: all store lanes)")
	root := fs.String("root", ".", questionsRootHelp)
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	now := fs.String("now", "", "observation time in RFC 3339 (default: now)")
	asJSON := fs.Bool("json", false, "print rows and summary as JSON")
	// Go flags stop at the first positional. Accept the named item before or after flags.
	itemID := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		itemID, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(fs)
			return record.ExitOK
		}
		return fail(drain.Usagef("%v", err))
	}
	if fs.NArg() == 1 && itemID == "" {
		itemID = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return fail(drain.Usagef("why takes at most one item"))
	}
	if *path == "" {
		return fail(drain.Usagef("why needs --store <path>"))
	}
	policy, err := drain.EffectivePolicy(*root, *policyRef)
	if err != nil {
		return fail(err)
	}
	tick := drain.Pass{Now: *now}
	if tick.Now == "" {
		tick.Now = time.Now().UTC().Format(time.RFC3339)
	}
	if *lanesArg != "" {
		lanes := strings.Split(*lanesArg, ",")
		tick.AvailableLanes = &lanes
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	var answer drain.WhyAnswer
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		answer, err = drain.Why(r, tick, policy, &drain.VantageQuestions{Root: *root, Command: commandFields(*questionsCmd), Context: ctx})
		return err
	}); err != nil {
		return fail(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if itemID != "" {
		for _, row := range answer.Rows {
			if row.ItemID == itemID {
				if err := enc.Encode(row); err != nil {
					return fail(drain.Externalf("writing why: %v", err))
				}
				if !*asJSON {
					fmt.Println(answer.Summary.String())
				}
				return record.ExitOK
			}
		}
		return fail(drain.Usagef("item %q is absent or already landed; why reports unlanded work", itemID))
	}
	if *asJSON {
		err = enc.Encode(answer)
	} else {
		for _, row := range answer.Rows {
			if _, err = fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\n", row.Rank, row.ItemID, row.Position, row.Reason, row.Mover); err != nil {
				break
			}
		}
		if err == nil {
			_, err = fmt.Fprintln(os.Stdout, answer.Summary.String())
		}
	}
	if err != nil {
		return fail(drain.Externalf("writing why: %v", err))
	}
	return record.ExitOK
}
