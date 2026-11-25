package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-roll/roll"
)

func main() {
	os.Exit(runMain(os.Args[1:], nil))
}

func runMain(args []string, deps *roll.Deps) int {
	fs := flag.NewFlagSet("tillr-roll", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Test and install the committed tillr tools.\n\nUsage: tillr-roll [flags]\n\n")
		fs.PrintDefaults()
	}
	noRequest := fs.Bool("no-request", false, "install without requesting restarts; the drain will request them")
	commit := fs.String("commit", "", "roll this commit instead of the newest tip")
	buildPi := fs.Bool("pi", false, "build the keyed pi fork binary and update current.json")
	piExtensions := fs.Bool("pi-extensions", false, "rebuild the drain pi extensions agent directory without restarting drain/view")
	_ = fs.Parse(args)

	status, err := execute(*noRequest, *commit, *buildPi, *piExtensions, deps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-roll: %v\n", err)
		return 1
	}
	return status
}

func execute(noRequest bool, commit string, buildPi, piExtensions bool, deps *roll.Deps) (int, error) {
	var root string
	var err error
	if deps != nil && deps.Git != nil {
		root, err = deps.Git("", "rev-parse", "--show-toplevel")
	} else {
		root, err = roll.Git("", deps, "rev-parse", "--show-toplevel")
	}
	if err != nil {
		return 1, err
	}
	if piExtensions {
		return roll.RollPiExtensions(root, deps)
	}
	durableDir, ok := os.LookupEnv("YOLO_DURABLE_DIR")
	if !ok || durableDir == "" {
		return 1, errors.New("YOLO_DURABLE_DIR")
	}
	absDurable, err := filepath.Abs(durableDir)
	if err != nil {
		return 1, err
	}
	live := filepath.Join(absDurable, "bin")
	return roll.Roll(root, live, noRequest, buildPi, commit, deps)
}
