package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var adapterBuildOnce sync.Once
var adapterBuildName string

func stepBuildEnv() string {
	adapterBuildOnce.Do(func() { adapterBuildName = ReadBuildStamp().String() })
	return "TILLR_LOOP_ADAPTER_BUILD=" + adapterBuildName
}

// builtAdapterProgram selects the adapter shipped inside the running drain binary.
// The keeper passes `tillr-loop` and older configurations may pass `builtin:tillr-loop`.
const builtAdapterProgram = "builtin:tillr-loop"

const loopAdapterProgram = "tillr-loop"

const execLoopAdapterEnv = "TILLR_DRAIN_EXEC_LOOP_ADAPTER=1"

// standardAdapterArg recognizes the built-in command (`tillr-loop`, `builtin:tillr-loop`)
// and, for older configurations, the standard script under this loop's root. Other
// programs run as configured.
func (s *ProcessSteps) standardAdapterArg(argv []string) int {
	if len(argv) == 0 {
		return -1
	}
	if argv[0] == builtAdapterProgram || argv[0] == loopAdapterProgram {
		return 0
	}
	if s.Root == "" {
		return -1
	}
	standard, err := filepath.Abs(filepath.Join(s.Root, "tools", "tillr-loop", "tillr-loop"))
	if err != nil {
		return -1
	}
	// Resolve symlinks when available, while retaining lexical matching after a landing
	// removes the source. Runtime execution must not depend on that file's continued existence.
	resolve := func(p string) string {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return resolved
		}
		return filepath.Clean(p)
	}
	standard = resolve(standard)
	matches := func(arg string) bool {
		path := arg
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.Root, path)
		}
		path, err := filepath.Abs(path)
		return err == nil && resolve(path) == standard
	}
	if matches(argv[0]) {
		return 0
	}
	if !strings.HasPrefix(filepath.Base(argv[0]), "python") {
		return -1
	}
	// Python options may precede the script. A module or inline command is not a
	// script invocation, even if one of its data arguments happens to name the adapter.
	for i := 1; i < len(argv); i++ {
		switch argv[i] {
		case "-c", "-m", "-":
			return -1
		case "-W", "-X":
			i++
		case "--":
			if i+1 < len(argv) && matches(argv[i+1]) {
				return i + 1
			}
			return -1
		default:
			if !strings.HasPrefix(argv[i], "-") {
				if matches(argv[i]) {
					return i
				}
				return -1
			}
		}
	}
	return -1
}

// stepCommand executes the compiled-in Go adapter via os.Executable() with
// TILLR_DRAIN_EXEC_LOOP_ADAPTER=1 rather than reading a working-tree script or
// invoking Python. All remaining flags stay as configured.
func (s *ProcessSteps) stepCommand(ctx context.Context, argv []string, env []string) *exec.Cmd {
	argv = append([]string(nil), argv...)
	index := s.standardAdapterArg(argv)
	var cmd *exec.Cmd
	if index >= 0 {
		exe, err := os.Executable()
		if err != nil || exe == "" {
			exe = os.Args[0]
		}
		cmd = exec.CommandContext(ctx, exe, argv[index+1:]...)
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
	}
	cmd.Env = append(withoutProcessEnv(os.Environ(), "TILLR_LOOP_ADAPTER_BUILD", "TILLR_DRAIN_EXEC_LOOP_ADAPTER"), env...)
	cmd.Env = append(cmd.Env, stepBuildEnv())
	if index >= 0 {
		cmd.Env = append(cmd.Env, execLoopAdapterEnv)
	}
	cmd.Env = withGitCeilingEnv(cmd.Env, s.LaneRoot)
	return cmd
}
