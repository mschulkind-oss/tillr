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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

func whyFace(args []string) int {
	fs := newFlagSet("why")
	path := fs.String("store", "", "the store to read (required)")
	policyRef := fs.String("policy", "", "the loop's policy file; absent means no-policy, not a guessed backend")
	lanesArg := fs.String("lanes", "", "running-agent limit or lanes offered by the loop: a comma-separated list, or a count N as the loop's --lanes takes it (lane-1..lane-N); default: the running loop's lanes when a loop is live for the root, otherwise all store lanes")
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
	ctx := context.Background()
	if err := drain.CheckTrunkWorktree(ctx, *root); err != nil {
		return fail(err)
	}
	policy, err := drain.EffectivePolicy(*root, *policyRef)
	if err != nil {
		return fail(err)
	}
	repositorySettings, err := settings.Read(*root)
	if err != nil {
		return fail(drain.Usagef("%v", err))
	}
	tick := drain.Pass{Now: *now, Root: *root, StorePath: *path, Comments: repositorySettings.Comments, ItemWorktrees: true}
	if tick.Now == "" {
		tick.Now = time.Now().UTC().Format(time.RFC3339)
	}
	lanesSource := ""
	if *lanesArg != "" {
		tick.AvailableLanes = whyLanes(*lanesArg, repositorySettings.Lanes.Express)
		lanesSource = "flag"
	}
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
		if sha, ok := revParseTrunk(ctx, *root, repositorySettings.TrunkBranch()); ok {
			if preloaded, ok := drain.LoadSavedQuestionsIndex(*root, sha, nil, false); ok {
				questions = preloaded
			}
		}
	}
	var answer drain.WhyAnswer
	var costs bytes.Buffer
	rewriteNote := ""
	var livePriority *drain.ProcessPrioritySummary
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
		if lp, ok, err := drain.LiveLoopProcessPriority(r, *path, time.Now().UTC()); err != nil {
			return err
		} else if ok {
			livePriority = lp
		}
		answer, err = drain.WhyForItem(r, tick, policy, questions, itemID)
		if err == nil {
			answer.LanesSource = lanesSource
			answer.ProcessPriority = livePriority
		}
		if err == nil && !*asJSON && itemID == "" {
			err = landedCosts(r, &costs)
		}
		if err == nil {
			if absorbed, ok, rerr := heavy.LatestRewrite(r); rerr != nil {
				err = rerr
			} else if ok {
				rewriteNote = absorbed.Line()
			}
		}
		return err
	}); err != nil {
		return fail(err)
	}
	answer.Trunk, answer.TrunkLine = whyTrunk(ctx, *root, *path, answer.Definition, tick.Now)
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if !*asJSON {
		if err := alarms.Print(os.Stdout, answer.Alarms); err != nil {
			return fail(drain.Externalf("writing why: %v", err))
		}
		fmt.Fprintf(os.Stdout, "definition: %s\n", answer.Definition)
	}
	if itemID != "" {
		for _, row := range answer.Rows {
			if row.ItemID == itemID {
				var report any = row
				if *asJSON {
					report = struct {
						Alarms          []alarms.Alarm                `json:"alarms,omitempty"`
						LoopRestartLine string                        `json:"loop_restart_line,omitempty"`
						ProcessPriority *drain.ProcessPrioritySummary `json:"process_priority,omitempty"`
						drain.WhyRow
					}{answer.Alarms, answer.LoopRestartLine, answer.ProcessPriority, row}
				}
				if err := enc.Encode(report); err != nil {
					return fail(drain.Externalf("writing why: %v", err))
				}
				if !*asJSON {
					fmt.Println(formatWhyLanes(answer.Lanes, answer.LanesSource))
					if line := formatWhyProcessPriority(answer.ProcessPriority); line != "" {
						fmt.Println(line)
					}
					if line := formatWhyDraining(answer.Draining); line != "" {
						fmt.Println(line)
					}
					fmt.Println(answer.Summary.String())
					if rewriteNote != "" {
						fmt.Println(rewriteNote)
					}
					if answer.TrunkLine != "" {
						fmt.Println(answer.TrunkLine)
					}
					if answer.LoopRestartLine != "" {
						fmt.Println(answer.LoopRestartLine)
					}
				}
				return record.ExitOK
			}
		}
		return fail(drain.Usagef("item %q is not in the drain's report", itemID))
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
			if _, err = fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\n", whyPlace(row.Place), whyRowLabel(row), row.Position, row.TableReason(), row.Mover); err != nil {
				break
			}
		}
		if err == nil {
			_, err = fmt.Fprintln(os.Stdout, formatWhyLanes(answer.Lanes, answer.LanesSource))
		}
		if line := formatWhyProcessPriority(answer.ProcessPriority); err == nil && line != "" {
			_, err = fmt.Fprintln(os.Stdout, line)
		}
		if line := formatWhyDraining(answer.Draining); err == nil && line != "" {
			_, err = fmt.Fprintln(os.Stdout, line)
		}
		if err == nil {
			_, err = fmt.Fprintln(os.Stdout, answer.Summary.String())
		}
		if err == nil && rewriteNote != "" {
			_, err = fmt.Fprintln(os.Stdout, rewriteNote)
		}
		if err == nil && answer.TrunkLine != "" {
			_, err = fmt.Fprintln(os.Stdout, answer.TrunkLine)
		}
		if err == nil && answer.LoopRestartLine != "" {
			_, err = fmt.Fprintln(os.Stdout, answer.LoopRestartLine)
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

// whyTrunk reads the trunk's proof and answers it only when the trunk is unproved. A root
// with no repository, a store that is not the landing definition, and a repository
// with no proof surface yet all say nothing: record-only fixtures have no trunk to prove.
func whyTrunk(ctx context.Context, root, storePath, def, now string) (*heavy.HeavyStatus, string) {
	if def != definition.GitLandingName {
		return nil, ""
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil, ""
	}
	status, err := heavy.ReadStatus(ctx, root, storePath)
	if err != nil || status.GreenCommit == "" && status.Result == "unjudged" {
		return nil, ""
	}
	at, err := time.Parse(time.RFC3339, now)
	if err != nil {
		at = time.Now().UTC()
	}
	line := heavy.UnprovedLine(status, at)
	if line == "" {
		return nil, ""
	}
	return &status, line
}

// whyLanes answers the lanes `why --lanes` offers. A bare count is the loop's own --lanes
// spelling and means the loop's ordinary running-agent slots: lane-1..lane-N. Express work
// takes its own item directory outside the limit, so [lanes] express reserves no pool slot.
func whyLanes(arg string, _ int64) *[]string {
	if n, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && n > 0 {
		lanes := laneIDs(n)
		return &lanes
	}
	lanes := strings.Split(arg, ",")
	return &lanes
}

// formatWhyDraining is the line that names each lane above the count with the item it still
// holds, or "" when no lane is draining.
func formatWhyDraining(draining []drain.DrainingLane) string {
	if len(draining) == 0 {
		return ""
	}
	parts := make([]string, len(draining))
	for i, d := range draining {
		parts[i] = d.LaneID + " (" + d.ItemID + ")"
	}
	return "draining: " + strings.Join(parts, ", ") + " (above the lane count: each finishes what it holds and takes nothing new)"
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

func formatWhyProcessPriority(p *drain.ProcessPrioritySummary) string {
	if p == nil {
		return ""
	}
	if p.Mechanism == "cgroup" {
		return fmt.Sprintf(
			"priority: cgroup v2 (%s; cpu.weight repair=%d proof=%d gate=%d worker=%d)",
			p.CgroupRoot,
			p.Classes[string(drain.ProcessClassRepair)],
			p.Classes[string(drain.ProcessClassProof)],
			p.Classes[string(drain.ProcessClassGate)],
			p.Classes[string(drain.ProcessClassWorker)],
		)
	}
	return fmt.Sprintf(
		"priority: nice (repair=%d proof=%d gate=%d worker=%d; cgroup unavailable: %s)",
		p.Classes[string(drain.ProcessClassRepair)],
		p.Classes[string(drain.ProcessClassProof)],
		p.Classes[string(drain.ProcessClassGate)],
		p.Classes[string(drain.ProcessClassWorker)],
		p.UnavailableReason,
	)
}

func whyRowLabel(row drain.WhyRow) string {
	if row.SplitPart == nil {
		return row.ItemID
	}
	if row.SplitPart.ParentTitle != "" {
		return fmt.Sprintf("%s (%s · part %s)", row.ItemID, row.SplitPart.ParentTitle, row.SplitPart.PartLabel)
	}
	return fmt.Sprintf("%s (part %s)", row.ItemID, row.SplitPart.PartLabel)
}

func revParseTrunk(ctx context.Context, root, trunkRef string) (string, bool) {
	if trunkRef == "" {
		trunkRef = drain.TrunkRef
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", trunkRef)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(string(out))
	return sha, sha != ""
}
