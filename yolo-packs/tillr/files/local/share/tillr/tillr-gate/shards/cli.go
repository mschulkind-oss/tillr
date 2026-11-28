package shards

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type cliConfig struct {
	module           string
	hasModule        bool
	shards           int
	cache            bool
	edgeDigest       string
	hasEdgeDigest    bool
	durations        string
	hasDurations     bool
	record           string
	events           string
	scratch          string
	hasScratch       bool
	timeout          string
	shardTimeout     float64
	hasShardTimeout  bool
	progressFile     string
	hasProgressFile  bool
	progressInterval float64
	hasProgInterval  bool
	proveDisabled    []string
	help             bool
}

func defaultDurationsPath() string {
	if cwd, err := os.Getwd(); err == nil {
		if root := LocateRepoRoot(cwd, true, false); root != "" {
			return filepath.Join(root, "tools", "go-test-durations.json")
		}
	}
	return filepath.Join("tools", "go-test-durations.json")
}

func printUsage() {
	fmt.Print(`usage: tillr-gate shards [-h] [-k SHARDS] [--cache] [--edge-digest IMPORT_PATH]
                         [--durations DURATIONS] [--record RECORD] [--events EVENTS]
                         [--scratch SCRATCH] [--timeout TIMEOUT]
                         [--shard-timeout SECONDS] [--progress-file PROGRESS_FILE]
                         [--progress-interval SECONDS] [--prove-disabled SELECTOR]
                         [module]

Build each Go package once; balance independent test processes on recorded durations.

positional arguments:
  module

options:
  -h, --help            show this help message and exit
  -k, --shards SHARDS
  --cache               reuse Go test result cache with edge digests and skip sharding below 100 tests
  --edge-digest IMPORT_PATH
                        print the current TILLR_GATE_EDGE_INPUTS digest for IMPORT_PATH and exit 0
  --durations DURATIONS
  --record RECORD       write measured top-level durations after success
  --events EVENTS       write Go test JSON events, including failed shards
  --scratch SCRATCH     parent of private scratch; defaults to TMPDIR or YOLO_DURABLE_DIR
  --timeout TIMEOUT     explicit Go test timeout; default disables Go's own deadline
  --shard-timeout SECONDS
                        maximum seconds without top-level test progress per shard (default 480)
  --progress-file PROGRESS_FILE
                        write throttled completion progress snapshots as JSON (or $TILLR_GO_SHARDS_PROGRESS_OUT)
  --progress-interval SECONDS
                        minimum seconds between completion progress snapshots (default 60)
  --prove-disabled SELECTOR
                        run this disabled test anyway (repeatable); the list is the
                        [gate] disabled_tests file in .tillr.toml
`)
}

func parseArgs(args []string) (cliConfig, error) {
	cfg := cliConfig{
		shards:           6,
		timeout:          "0",
		progressInterval: DefaultProgressInterval,
	}
	var positionals []string
	endOfFlags := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !endOfFlags && arg == "--" {
			endOfFlags = true
			continue
		}
		if endOfFlags || !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		if arg == "-h" || arg == "--help" {
			cfg.help = true
			return cfg, nil
		}
		if arg == "--cache" {
			cfg.cache = true
			continue
		}

		name, inlineVal, hasInline := strings.Cut(arg, "=")
		nextVal := func(flagName string) (string, error) {
			if hasInline {
				return inlineVal, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("argument %s: expected one argument", flagName)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "-k", "--shards":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			n, err := strconv.Atoi(val)
			if err != nil {
				return cfg, fmt.Errorf("argument -k/--shards: invalid int value: %q", val)
			}
			cfg.shards = n
		case "--edge-digest":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.edgeDigest = val
			cfg.hasEdgeDigest = true
		case "--durations":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.durations = val
			cfg.hasDurations = true
		case "--record":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.record = val
		case "--events":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.events = val
		case "--scratch":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.scratch = val
			cfg.hasScratch = true
		case "--timeout":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.timeout = val
		case "--shard-timeout":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return cfg, fmt.Errorf("argument --shard-timeout: invalid float value: %q", val)
			}
			cfg.shardTimeout = f
			cfg.hasShardTimeout = true
		case "--prove-disabled":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.proveDisabled = append(cfg.proveDisabled, val)
		case "--progress-file":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			cfg.progressFile = val
			cfg.hasProgressFile = true
		case "--progress-interval":
			val, err := nextVal(name)
			if err != nil {
				return cfg, err
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return cfg, fmt.Errorf("argument --progress-interval: invalid float value: %q", val)
			}
			cfg.progressInterval = f
			cfg.hasProgInterval = true
		default:
			if strings.HasPrefix(arg, "-k") && len(arg) > 2 && !hasInline {
				n, err := strconv.Atoi(arg[2:])
				if err != nil {
					return cfg, fmt.Errorf("argument -k/--shards: invalid int value: %q", arg[2:])
				}
				cfg.shards = n
				continue
			}
			return cfg, fmt.Errorf("unrecognized arguments: %s", arg)
		}
	}

	if len(positionals) > 1 {
		return cfg, fmt.Errorf("unrecognized arguments: %s", strings.Join(positionals[1:], " "))
	}
	if len(positionals) == 1 {
		cfg.module = positionals[0]
		cfg.hasModule = true
	}
	if !cfg.hasDurations {
		cfg.durations = defaultDurationsPath()
	}
	return cfg, nil
}

func usageError(msg string) int {
	fmt.Fprintf(os.Stderr, "go-test-shards: error: %s\n", msg)
	return 2
}

func writeEventsJSONL(path string, events []TestEvent) error {
	var buf bytes.Buffer
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// RunCLI executes the `tillr-gate shards` CLI entrypoint and returns its exit code.
func RunCLI(args []string) int {
	if len(args) >= 1 && args[0] == GuardFlag {
		if len(args) < 3 {
			return 2
		}
		fd, err := strconv.Atoi(args[1])
		if err != nil {
			return 2
		}
		cmdArgs := args[2:]
		if len(cmdArgs) > 0 && cmdArgs[0] == "--" {
			cmdArgs = cmdArgs[1:]
		}
		return GuardMain(fd, cmdArgs)
	}

	InstallSignalHandlers()

	cfg, err := parseArgs(args)
	if err != nil {
		return usageError(err.Error())
	}
	if cfg.help {
		printUsage()
		return 0
	}

	if cfg.hasEdgeDigest {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", err)
			return 1
		}
		repoRoot := LocateRepoRoot(cwd, true, false)
		edges, err := LoadEdges(repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", err)
			return 1
		}
		if edge, ok := edges[cfg.edgeDigest]; ok {
			var resolved [2][]string
			if len(edge.Builds) > 0 {
				graph, err := LoadTillrGraph(repoRoot, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", err)
					return 1
				}
				bf, mb := ResolveEdgeBuilds(repoRoot, edge, graph.PkgInfo)
				resolved = [2][]string{bf, mb}
			}
			fmt.Println(ComputeEdgeDigest(repoRoot, edge, nil, nil, &resolved))
		}
		return 0
	}

	if !cfg.hasModule {
		return usageError("the following arguments are required: module")
	}
	if cfg.shards < 1 {
		return usageError("shards must be positive")
	}
	if cfg.hasShardTimeout && (math.IsNaN(cfg.shardTimeout) || math.IsInf(cfg.shardTimeout, 0) || cfg.shardTimeout <= 0) {
		return usageError("shard timeout must be a positive finite number of seconds")
	}
	if math.IsNaN(cfg.progressInterval) || math.IsInf(cfg.progressInterval, 0) || cfg.progressInterval <= 0 {
		return usageError("progress interval must be a positive finite number of seconds")
	}

	parent := ""
	if cfg.hasScratch {
		parent = cfg.scratch
	} else if v := os.Getenv("TMPDIR"); v != "" {
		parent = v
	} else if v := os.Getenv("YOLO_DURABLE_DIR"); v != "" {
		parent = v
	}
	if parent == "" {
		return usageError("set TMPDIR or YOLO_DURABLE_DIR, or pass --scratch (never defaults to /tmp)")
	}

	durations, err := LoadDurations(cfg.durations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", err)
		return 1
	}

	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		parentAbs = parent
	}
	if real, err := filepath.EvalSymlinks(parentAbs); err == nil {
		parentAbs = real
	}
	if err := os.MkdirAll(parentAbs, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", err)
		return 1
	}
	temporary, err := os.MkdirTemp(parentAbs, "go-shards-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	var opts RunOptions
	if cfg.hasShardTimeout {
		opts.ShardTimeout = cfg.shardTimeout
	}
	if evOut := os.Getenv(EvidenceOutEnv); evOut != "" {
		_ = os.Unsetenv(EvidenceOutEnv)
		opts.EvidenceOut = evOut
	} else {
		_ = os.Unsetenv(EvidenceOutEnv)
	}
	envProgOut := os.Getenv(ProgressOutEnv)
	_ = os.Unsetenv(ProgressOutEnv)
	if cfg.hasProgressFile {
		if abs, aerr := filepath.Abs(cfg.progressFile); aerr == nil {
			opts.ProgressFile = abs
		} else {
			opts.ProgressFile = cfg.progressFile
		}
	} else if trimmed := strings.TrimSpace(envProgOut); trimmed != "" {
		if abs, aerr := filepath.Abs(trimmed); aerr == nil {
			opts.ProgressFile = abs
		} else {
			opts.ProgressFile = trimmed
		}
	}
	if opts.ProgressFile != "" || cfg.hasProgInterval {
		opts.ProgressInterval = cfg.progressInterval
	}

	var uncachedEvents []TestEvent
	if cfg.cache {
		opts.Cache = true
		opts.UncachedEvents = &uncachedEvents
		if cfg.hasScratch {
			opts.ExplicitScratch = true
			if abs, aerr := filepath.Abs(cfg.scratch); aerr == nil {
				opts.DurableDir = abs
			} else {
				opts.DurableDir = cfg.scratch
			}
		}
	}

	modPath := cfg.module
	if !filepath.IsAbs(modPath) {
		if _, statErr := os.Stat(modPath); os.IsNotExist(statErr) {
			if cwd, cwdErr := os.Getwd(); cwdErr == nil {
				if root := LocateRepoRoot(cwd, true, false); root != "" {
					candidate := filepath.Join(root, modPath)
					if _, candErr := os.Stat(candidate); candErr == nil {
						modPath = candidate
					}
				}
			}
		}
	}
	modAbs, err := filepath.Abs(modPath)
	if err != nil {
		modAbs = cfg.module
	}
	if real, rerr := filepath.EvalSymlinks(modAbs); rerr == nil {
		modAbs = real
	}

	disabled, derr := loadDisabledSet(modAbs, cfg.proveDisabled)
	if derr != nil {
		fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", derr)
		return 1
	}
	opts.Disabled = disabled

	events, runErr := Run(modAbs, cfg.shards, durations, temporary, cfg.timeout, opts)
	if runErr != nil {
		var se *ShardError
		if errors.As(runErr, &se) {
			if cfg.events != "" {
				if werr := writeEventsJSONL(cfg.events, se.Events); werr != nil {
					fmt.Fprintf(os.Stderr, "go-test-shards: cannot write events: %v\n", werr)
				}
			}
		}
		fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", runErr)
		return 1
	}

	if cfg.events != "" {
		if werr := writeEventsJSONL(cfg.events, events); werr != nil {
			fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", werr)
			return 1
		}
	}

	if cfg.record != "" {
		recordSource := events
		if cfg.cache {
			recordSource = uncachedEvents
		}
		for _, ev := range recordSource {
			if ev.Test != "" && !strings.Contains(ev.Test, "/") && (ev.Action == goTestActionPass || ev.Action == goTestActionSkip) {
				pkgDur := durations[ev.Package]
				if pkgDur == nil {
					pkgDur = make(map[string]float64)
					durations[ev.Package] = pkgDur
				}
				elapsed := 0.0
				if ev.Elapsed != nil {
					elapsed = *ev.Elapsed
				}
				pkgDur[ev.Test] = elapsed
			}
		}
		encoded, merr := json.MarshalIndent(durations, "", "  ")
		if merr != nil {
			fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", merr)
			return 1
		}
		if werr := os.WriteFile(cfg.record, append(encoded, '\n'), 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "go-test-shards: %v\n", werr)
			return 1
		}
	}

	passed := 0
	for _, ev := range events {
		if ev.Action == goTestActionPass && ev.Test != "" {
			passed++
		}
	}
	fmt.Printf("go-test-shards: %s: %d tests passed, %d shards per package, %d disabled\n", cfg.module, passed, cfg.shards, len(opts.Disabled.Applied()))
	return 0
}
