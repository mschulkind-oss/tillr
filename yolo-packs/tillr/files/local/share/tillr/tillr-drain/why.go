package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

func whyFace(args []string) int {
	fs := newFlagSet("why")
	path := fs.String("store", "", "the store to read (required)")
	policyRef := fs.String("policy", "", "the loop's policy file; absent means no-policy, not a guessed backend")
	lanesArg := fs.String("lanes", "", "lanes offered by the loop: a comma-separated list, or a count N as the loop's --lanes takes it (lane-1..lane-N plus the configured express lanes); default: the running loop's lanes when a loop is live for the root, otherwise all store lanes")
	root := fs.String("root", ".", questionsRootHelp)
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	questionsIndex := fs.String("questions-index", "", "an index document `vantage-check index --format json` already produced, read instead of running the index command; `-` reads it from stdin (the observatory hands one cached walk to every planner it asks)")
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
	repositorySettings, err := settings.Read(*root)
	if err != nil {
		return fail(drain.Usagef("%v", err))
	}
	tick := drain.Pass{Now: *now, Comments: repositorySettings.Comments}
	if tick.Now == "" {
		tick.Now = time.Now().UTC().Format(time.RFC3339)
	}
	lanesSource := ""
	if *lanesArg != "" {
		tick.AvailableLanes = whyLanes(*lanesArg, repositorySettings.Lanes.Express)
		lanesSource = "flag"
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	questions := &drain.VantageQuestions{Root: *root, Command: commandFields(*questionsCmd), Context: ctx}
	if *questionsIndex != "" {
		if *questionsCmd != "" {
			return fail(drain.Usagef("why takes --questions-index or --questions-cmd, not both: the index document is the command's answer"))
		}
		var raw []byte
		if *questionsIndex == "-" {
			raw, err = io.ReadAll(os.Stdin)
		} else {
			raw, err = os.ReadFile(*questionsIndex)
		}
		if err != nil {
			return fail(drain.Usagef("reading --questions-index %s: %v", *questionsIndex, err))
		}
		questions = vantage.Preloaded(*root, vantage.ParseIndex(raw, "--questions-index "+*questionsIndex))
	} else if *questionsCmd == "" {
		if sha, ok := revParseTrunk(ctx, *root); ok {
			if preloaded, ok := drain.LoadSavedQuestionsIndex(*root, sha, nil, false); ok {
				questions = preloaded
			}
		}
	}
	var answer drain.WhyAnswer
	var costs bytes.Buffer
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		if tick.AvailableLanes == nil {
			liveLanes, ok, err := drain.LiveLoopLanes(r, *path, time.Now().UTC())
			if err != nil {
				return err
			}
			if ok {
				tick.AvailableLanes = &liveLanes
				lanesSource = "live loop"
			} else {
				lanesSource = "store"
			}
		}
		answer, err = drain.Why(r, tick, policy, questions)
		if err == nil {
			answer.LanesSource = lanesSource
		}
		if err == nil && !*asJSON && itemID == "" {
			err = landedCosts(r, &costs)
		}
		return err
	}); err != nil {
		return fail(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if !*asJSON {
		fmt.Fprintf(os.Stdout, "definition: %s\n", answer.Definition)
	}
	if itemID != "" {
		for _, row := range answer.Rows {
			if row.ItemID == itemID {
				if err := enc.Encode(row); err != nil {
					return fail(drain.Externalf("writing why: %v", err))
				}
				if !*asJSON {
					fmt.Println(formatWhyLanes(answer.Lanes, answer.LanesSource))
					fmt.Println(answer.Summary.String())
				}
				return record.ExitOK
			}
		}
		return fail(drain.Usagef("item %q is absent or already landed; why reports unlanded work", itemID))
	}
	if *asJSON {
		// The build rides beside the answer, not inside drain.WhyAnswer: it describes the
		// program that planned, not the plan, and tillr-view refuses an answer from a planner
		// that is not the build it was made with (tools/tillr-view/siblings.go).
		err = enc.Encode(struct {
			drain.WhyAnswer
			Build drain.BuildStamp `json:"build"`
		}{answer, drain.ReadBuildStamp()})
	} else {
		for _, row := range answer.Rows {
			if _, err = fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\n", whyPlace(row.Place), row.ItemID, row.Position, row.Reason, row.Mover); err != nil {
				break
			}
		}
		if err == nil {
			_, err = fmt.Fprintln(os.Stdout, formatWhyLanes(answer.Lanes, answer.LanesSource))
		}
		if err == nil {
			_, err = fmt.Fprintln(os.Stdout, answer.Summary.String())
		}
		if err == nil {
			_, err = io.Copy(os.Stdout, &costs)
		}
	}
	if err != nil {
		return fail(drain.Externalf("writing why: %v", err))
	}
	return record.ExitOK
}

// whyLanes answers the lanes `why --lanes` offers. A bare count is the loop's own --lanes
// spelling and means the loop's lanes: lane-1..lane-N and the configured express lanes. It
// was once read as one lane named "16", and every schedulable item past the first was then
// reported no-lane behind that lane, which no loop offers.
func whyLanes(arg string, express int64) *[]string {
	if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && n > 0 {
		lanes := append(laneIDs(n), expressLaneIDs(express)...)
		return &lanes
	}
	lanes := strings.Split(arg, ",")
	return &lanes
}

func formatWhyLanes(lanes []string, source string) string {
	label := "lanes"
	if source != "" {
		label = fmt.Sprintf("lanes (%s)", source)
	}
	if len(lanes) == 0 {
		return label + ": none"
	}
	return label + ": " + strings.Join(lanes, ", ")
}

func revParseTrunk(ctx context.Context, root string) (string, bool) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", drain.TrunkRef)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(out))
	return sha, sha != ""
}
