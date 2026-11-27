package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func recoveryFace(face string, args []string) int {
	fs := newFlagSet(face)
	root := fs.String("root", ".", "the repository holding the candidate")
	path := fs.String("store", "", "the existing store (default <root>/.tillr/state.db)")
	var runID, itemID, why, laneRoot, policyPath, gate, land, verdict, itemBin *string
	var runBin, template, questionsCmd, replay, fixerGate *string
	if face == "set-aside" {
		itemID = fs.String("item", "", "the item whose committed candidate needs rework")
	} else {
		runID = fs.String("run", "", "the run whose conviction or candidate is inspected")
	}
	if face != "examine" {
		why = fs.String("why", "", "the operator's explanation, retained in the record")
	}
	if face != "acquit" {
		laneRoot = fs.String("lane-root", "", "the lanes directory (default <root>/.tillr/lanes)")
	}
	if face == "examine" {
		policyPath = fs.String("policy", "", "the backend policy (default <root>/config/tillr/backend-policy.json)")
		gate = fs.String("gate", "", "the loop's gate program")
		land = fs.String("land", "", "the loop's land program")
		verdict = fs.String("verdict", "", "the loop's verdict program")
		itemBin = fs.String("item-bin", "tillr-item", "the item-state program used by the loop")
		runBin = fs.String("run-bin", "tillr-run", "the run program used for the loop's conflict repair")
		template = fs.String("brief-template", "", "the loop's brief template (default: the installed tillr-run template)")
		questionsCmd = fs.String("questions-cmd", "", questionsCmdHelp)
		replay = fs.String("replay", drain.DefaultReplayProgram, "the loop's failing-check replay program")
		fixerGate = fs.String("fixer-gate", drain.DefaultFixerGateProgram, "the loop's named-check judge for trunk fixers")
	}
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if runID != nil && strings.TrimSpace(*runID) == "" {
		return fail(drain.Usagef("%s needs --run <id>", face))
	}
	if itemID != nil && strings.TrimSpace(*itemID) == "" {
		return fail(drain.Usagef("set-aside needs --item <id>"))
	}
	if why != nil && strings.TrimSpace(*why) == "" {
		return fail(drain.Usagef("%s needs --why <text>", face))
	}
	if *root == "" {
		return fail(drain.Usagef("%s needs --root <repository>", face))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	ctx := context.Background()
	if face == "acquit" {
		s, err := store.Open(ctx, *path, true)
		if err != nil {
			return fail(openFailure(err))
		}
		defer s.Close()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error { return drain.AcquitRun(tx, *runID, *why, time.Now().UTC()) }); err != nil {
			return fail(err)
		}
		fmt.Printf("acquitted %s; conviction retained\n", *runID)
		return record.ExitOK
	}
	if *laneRoot == "" {
		*laneRoot = filepath.Join(*root, ".tillr", "lanes")
	}
	policy := &drain.Policy{}
	steps := &drain.ProcessSteps{Root: *root, Store: *path, LaneRoot: *laneRoot, Now: time.Now}
	stager := &drain.LaneStager{Root: *root, LaneRoot: *laneRoot, Store: *path, Now: time.Now}
	var questions []string
	if face == "examine" {
		if *gate == "" || *land == "" || *verdict == "" {
			return fail(drain.Usagef("examine needs --gate, --land and --verdict: use the same programs as the loop"))
		}
		if *policyPath == "" {
			*policyPath = filepath.Join(*root, "config", "tillr", "backend-policy.json")
		}
		var err error
		policy, err = readPolicy(*policyPath)
		if err != nil {
			return fail(err)
		}
		if policy == nil {
			return fail(drain.Usagef("examine needs a backend policy"))
		}
		steps.GateProgram, steps.LandProgram, steps.VerdictProgram = *gate, *land, *verdict
		steps.ItemBin, steps.RunBin = *itemBin, *runBin
		steps.ReplayProgram, steps.FixerGateProgram = *replay, *fixerGate
		if *template == "" {
			resolved, err := run.ResolveBriefTemplate()
			if err != nil {
				return fail(drain.Usagef("--brief-template: %v", err))
			}
			*template = resolved
		}
		stager.Template = *template
		questions = commandFields(*questionsCmd)
	}
	loop, err := drain.NewLoop(drain.LoopConfig{Root: *root, Store: *path, LaneRoot: *laneRoot, Lanes: []string{"recovery"}, ItemWorktrees: true, Policy: policy, Steps: steps, Stage: stager, QuestionsCmd: questions, Now: time.Now, Report: os.Stderr})
	if err != nil {
		return fail(err)
	}
	if face == "set-aside" {
		ref, err := loop.SetAside(ctx, *itemID, *why)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("set aside %s at %s; lane released, item held for rework\n", *itemID, ref)
		return record.ExitOK
	}
	result, err := loop.Examine(ctx, *runID)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("examined %s: landed %d, rejected %d\n", *runID, result.Landed, result.Rejected)
	if result.Stop != "" {
		return drain.StopExitCode(result.Stop)
	}
	return record.ExitOK
}
