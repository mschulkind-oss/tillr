// Package loop implements the tillr-loop Git-landing definition hook adapter.
package loop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const Version = "0.1.0"

const (
	ExitPass     = 0
	ExitFail     = 1
	ExitUsage    = 2
	ExitExternal = 4
	ExitStalled  = 6
)

const (
	gateModuleHeader = "module github.com/mschulkind-oss/tillr-gate"
	registryDir      = "tools/gate/checks"
	registryEnv      = "TILLR_LOOP_REGISTRY"
	gateLogsDir      = "logs"
)

var hookNames = []string{"gate", "land", "verdict", "fixer-gate", "replay", "rolling"}

var programString = map[string]string{
	"gate":    "tools/tillr-loop/tillr-loop gate",
	"land":    "tools/tillr-loop/tillr-loop land",
	"verdict": "tools/tillr-loop/tillr-loop verdict",
}

const topHelpDoc = `tillr-loop: the Git-landing definition's hook program.

One executable answers the definition's hook program contract (see
../tillr/definition/README.md). Candidate adapters turn the loop's argv into real
` + "`tillr-gate`" + `, ` + "`tillr-land`" + ` and ` + "`tillr item`" + ` invocations and translate their exit codes.
Revision hooks run the drain's default repository checks. The embedded definition
leaves hooks unset; explicit step programs keep their existing behavior.

  tools/tillr-loop/tillr-loop gate    [--registry <check-directory>] <candidate flags>
  tools/tillr-loop/tillr-loop land    [--observed-trunk <sha>] [--base-ref <ref>] [--message-from <sha>] <candidate flags>
  tools/tillr-loop/tillr-loop verdict --verdict pass <candidate flags>
  tools/tillr-loop/tillr-loop fixer-gate --only <check> <candidate flags>
  tools/tillr-loop/tillr-loop replay --only <check>
  tools/tillr-loop/tillr-loop rolling (--since <sha> | --all)
  tools/tillr-loop/tillr-loop describe                       # capabilities, runs nothing
  tools/tillr-loop/tillr-loop self-check <candidate flags>   # prints, runs nothing

The loop appends the candidate to whatever program string it is given (one flag per
field: --item --lane --run --worktree --base --head --root, plus the optional flags each
step's branches add), so the loop's argv carries no judgment: every value that decides *what* is judged
comes from a flag of this adapter's own, and every value that can be derived from the
candidate is derived here rather than defaulted to something convenient.

Exit codes: the loop reads gate as 0 pass / 1 fail / 6 stalled / anything else could-not-judge, land
as 0 landed / 1 not-landed, and verdict as 0 recorded / non-zero refused. Each adapter
translates the underlying program's code onto that contract; see README.md for the table.`

type Refusal struct {
	Code    int
	Message string
}

func (r *Refusal) Error() string {
	return r.Message
}

type Step struct {
	Argv   []string
	Cwd    string
	Stdout string
	Env    map[string]string
}

type Ctx struct {
	Item          string
	Lane          string
	Run           string
	Worktree      string
	Base          string
	Head          string
	Root          string
	Store         string
	WorkDir       string
	Now           string
	Registry      string
	GateProgram   string
	BaseRef       string
	MessageFrom   string
	ObservedTrunk string
	Verdict       string
	GateBin       string
	LandBin       string
	ItemBin       string
}

func (c Ctx) runDir() string       { return filepath.Join(c.WorkDir, c.Run) }
func (c Ctx) changeRecord() string { return filepath.Join(c.runDir(), "gate.change.json") }
func (c Ctx) planRecord() string   { return filepath.Join(c.runDir(), "gate.plan.json") }
func (c Ctx) verdictRecord() string {
	return filepath.Join(c.runDir(), "gate.verdict.json")
}

// Main is the CLI entrypoint for tillr-loop, taking arguments without argv[0].
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, topHelpDoc)
		if len(args) == 0 {
			return ExitUsage
		}
		return ExitPass
	}
	kind, rest := args[0], args[1:]
	if kind == "describe" {
		_, helpShown, exitVal := parseArgs("describe", rest, stdout, stderr)
		if helpShown || exitVal != ExitPass {
			return exitVal
		}
		return describe(stdout)
	}
	if !slices.Contains(hookNames, kind) && kind != "self-check" {
		fmt.Fprintf(stderr, "tillr-loop: %q is not a step: want %s, describe or self-check\n",
			kind, strings.Join(hookNames, ", "))
		return ExitUsage
	}
	parsed, helpShown, exitVal := parseArgs(kind, rest, stdout, stderr)
	if helpShown || exitVal != ExitPass {
		return exitVal
	}
	if parsed.checkArgv {
		return ExitPass
	}
	var err error
	var status int
	if kind == "replay" || kind == "rolling" || kind == "fixer-gate" {
		status, err = revisionCheck(kind, parsed, stdin, stdout, stderr)
	} else {
		var ctx Ctx
		ctx, err = buildContext(kind, parsed)
		if err == nil {
			if kind == "self-check" {
				status = printSelfCheck(ctx, stdout, stderr)
			} else {
				var steps []Step
				steps, err = compose(kind, ctx, false, stdout)
				if err == nil {
					status = execute(kind, steps, stdin, stdout, stderr)
				}
			}
		}
	}
	if err != nil {
		var ref *Refusal
		if errors.As(err, &ref) {
			fmt.Fprintf(stderr, "tillr-loop %s: %s\n", kind, ref.Message)
			return ref.Code
		}
		fmt.Fprintf(stderr, "tillr-loop %s: %v\n", kind, err)
		return ExitExternal
	}
	return status
}

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		return realPath
	}
	return abs
}

func gitCmd(directory string, args ...string) (string, error) {
	cmdArgs := append([]string{"-C", directory}, args...)
	cmd := exec.Command("git", cmdArgs...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", &Refusal{
				Code: ExitExternal,
				Message: fmt.Sprintf("git -C %s %s answered exit %d: %s",
					directory, strings.Join(args, " "), exitErr.ExitCode(), strings.TrimSpace(errBuf.String())),
			}
		}
		return "", &Refusal{
			Code:    ExitExternal,
			Message: fmt.Sprintf("git is not runnable: %v", err),
		}
	}
	return strings.TrimSpace(outBuf.String()), nil
}

// nonLandingStoreCharter reports whether the named store (or ./.tillr/state.db
// when storePath is empty) declares a charter that does not land in Git.
func nonLandingStoreCharter(storePath string) (string, bool) {
	candidate := storePath
	if candidate == "" {
		candidate = filepath.Join(".", ".tillr", "state.db")
	}
	if _, err := os.Stat(candidate); err != nil {
		return "", false
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, candidate)
	if err != nil {
		return "", false
	}
	defer s.Close()
	completes, err := s.Completes(ctx)
	if err != nil || completes == "landing" {
		return "", false
	}
	return candidate, true
}

func resolveRoot(explicit, worktree, storePath string) (string, error) {
	if explicit != "" {
		return resolvePath(explicit), nil
	}
	if path, ok := nonLandingStoreCharter(storePath); ok {
		return "", &Refusal{
			Code:    ExitUsage,
			Message: fmt.Sprintf("no root: store %s uses a non-landing charter; name --root <directory> (refusing to resolve an enclosing git repository)", path),
		}
	}
	if os.Getenv("TILLR_NON_LANDING") == "1" {
		return "", &Refusal{
			Code:    ExitUsage,
			Message: "no root: non-landing store requires --root <directory> (refusing to resolve an enclosing git repository)",
		}
	}
	if worktree != "" {
		if common, err := gitCmd(worktree, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
			return filepath.Dir(resolvePath(common)), nil
		}
	}
	if top, err := gitCmd(".", "rev-parse", "--show-toplevel"); err == nil {
		return top, nil
	}
	return "", &Refusal{
		Code:    ExitUsage,
		Message: "no root: name --root <repository>, or run this adapter from the repository (the working directory is not a git checkout)",
	}
}

func resolveBaseRef(explicit, root string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	name, err := gitCmd(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if name == "HEAD" {
		return "", &Refusal{
			Code: ExitUsage,
			Message: fmt.Sprintf("cannot derive the branch to land onto: %s is on a detached HEAD. Name --base-ref <branch>: a landing onto no branch is a landing in the wrong place",
				root),
		}
	}
	return name, nil
}

type recordEnvelope struct {
	RecordVersion int             `json:"record_version"`
	Kind          string          `json:"kind"`
	ID            string          `json:"id"`
	Producer      record.Producer `json:"producer"`
	CreatedAt     string          `json:"created_at"`
	Payload       any             `json:"payload"`
}

func makeEnvelope(kind, ident string, payload any, createdAt string) recordEnvelope {
	return recordEnvelope{
		RecordVersion: record.SupportedVersion,
		Kind:          kind,
		ID:            ident,
		Producer: record.Producer{
			System:  record.System,
			Utility: "tillr-loop",
			Version: Version,
		},
		CreatedAt: createdAt,
		Payload:   payload,
	}
}

func writeRecord(path string, body any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(body); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func criterionHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func pathDigest(path string, data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("path %s digest sha256:%s", path, hex.EncodeToString(sum[:]))
}

func itemBuildName(program string) string {
	target := program
	if found, err := exec.LookPath(program); err == nil {
		target = found
	}
	resolved := resolvePath(target)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("path %s (cannot identify build: %v)", resolved, err)
	}
	if bytes.HasPrefix(data, []byte("#!")) {
		return "launcher " + pathDigest(resolved, data)
	}
	if goBin, err := exec.LookPath("go"); err == nil {
		out, err := exec.Command(goBin, "version", "-m", resolved).Output()
		if err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && fields[0] == "build" && strings.HasPrefix(fields[1], "vcs.revision=") {
					return "revision " + strings.SplitN(fields[1], "=", 2)[1]
				}
			}
		}
	}
	return pathDigest(resolved, data)
}

func adapterBuildName() string {
	if stamp := os.Getenv("TILLR_LOOP_ADAPTER_BUILD"); stamp != "" {
		return stamp
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Sprintf("path <unknown> (cannot identify build: %v)", err)
	}
	resolved := resolvePath(exe)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return fmt.Sprintf("path %s (cannot identify build: %v)", resolved, err)
	}
	if root, err := gitCmd(filepath.Dir(resolved), "rev-parse", "--show-toplevel"); err == nil {
		if rel, err := filepath.Rel(root, resolved); err == nil && !strings.HasPrefix(rel, "..") {
			cmd := exec.Command("git", "-C", root, "show", "HEAD:"+filepath.ToSlash(rel))
			if committed, err := cmd.Output(); err == nil && bytes.Equal(committed, data) {
				if rev, err := gitCmd(root, "rev-parse", "HEAD"); err == nil {
					return "revision " + rev
				}
			}
		}
	}
	return pathDigest(resolved, data)
}

func requireBinary(name, def string, dry bool) (string, error) {
	if name == "" {
		name = def
	}
	if strings.Contains(name, "/") {
		return name, nil
	}
	found, err := exec.LookPath(name)
	if err != nil {
		if dry {
			return name + " (MISSING)", nil
		}
		return "", &Refusal{
			Code:    ExitExternal,
			Message: fmt.Sprintf("%s is not on PATH: the step cannot judge, and a judge that could not run is not a candidate that failed", name),
		}
	}
	return found, nil
}

func gateProgramArgs(program string) ([]string, error) {
	if program == "" {
		return nil, nil
	}
	words, err := shellSplit(program)
	if err != nil {
		return nil, &Refusal{Code: ExitUsage, Message: fmt.Sprintf("invalid --gate-program: %v", err)}
	}
	if len(words) == 0 {
		return nil, nil
	}
	args := []string{"--gate", words[0]}
	for _, w := range words[1:] {
		args = append(args, "--gate-arg", w)
	}
	return args, nil
}

func compose(kind string, ctx Ctx, dry bool, stdout io.Writer) ([]Step, error) {
	switch kind {
	case "gate":
		return composeGate(ctx, dry)
	case "land":
		return composeLand(ctx, dry)
	case "verdict":
		return composeVerdict(ctx, dry, stdout)
	default:
		return nil, fmt.Errorf("unknown composer %q", kind)
	}
}

func composeGate(ctx Ctx, dry bool) ([]Step, error) {
	gateBin, err := requireBinary(ctx.GateBin, "tillr-gate", dry)
	if err != nil {
		return nil, err
	}
	registry := ctx.Registry
	if registry == "" {
		registry = filepath.Join(ctx.Worktree, registryDir)
	}
	gpArgs, err := gateProgramArgs(ctx.GateProgram)
	if err != nil {
		return nil, err
	}
	runArgv := []string{
		gateBin, "run",
		"--input", ctx.planRecord(),
		"--worktree", ctx.Worktree,
		"--output", ctx.verdictRecord(),
		"--logs", filepath.Join(ctx.runDir(), gateLogsDir),
	}
	runArgv = append(runArgv, gpArgs...)
	steps := []Step{
		{
			Argv: []string{
				gateBin, "change",
				"--item", ctx.Item,
				"--lane", ctx.Lane,
				"--run", ctx.Run,
				"--worktree", ctx.Worktree,
				"--base", ctx.Base,
				"--head", ctx.Head,
				"--now", ctx.Now,
				"--output", ctx.changeRecord(),
			},
			Cwd: ctx.Worktree,
		},
		{
			Argv: []string{
				gateBin, "plan",
				"--registry", registry,
				"--input", ctx.changeRecord(),
				"--output", ctx.planRecord(),
				"--root", ctx.Worktree,
			},
			Cwd: ctx.Worktree,
		},
		{
			Argv: runArgv,
			Cwd:  ctx.Worktree,
		},
	}
	if !dry {
		if err := os.MkdirAll(ctx.runDir(), 0o755); err != nil {
			return nil, &Refusal{Code: ExitExternal, Message: err.Error()}
		}
	}
	return steps, nil
}

func composeLand(ctx Ctx, dry bool) ([]Step, error) {
	if _, err := os.Stat(ctx.verdictRecord()); err != nil {
		return nil, &Refusal{
			Code:    ExitExternal,
			Message: fmt.Sprintf("the land step needs the gate's verdict at %s: a landing against no verdict would be a candidate nobody judged", ctx.verdictRecord()),
		}
	}
	baseRef, err := resolveBaseRef(ctx.BaseRef, ctx.Root)
	if err != nil {
		return nil, err
	}
	landBin, err := requireBinary(ctx.LandBin, "tillr-land", dry)
	if err != nil {
		return nil, err
	}
	argv := []string{
		landBin, "pass",
		"--store", ctx.Store,
		"--repo", ctx.Root,
		"--item", ctx.Item,
		"--run", ctx.Run,
		"--base", ctx.Base,
		"--head", ctx.Head,
		"--verdict", ctx.verdictRecord(),
		"--base-ref", baseRef,
		"--now", ctx.Now,
		"--run-dir", ctx.runDir(),
	}
	if ctx.MessageFrom != "" {
		argv = append(argv, "--message-from", ctx.MessageFrom)
	}
	if ctx.ObservedTrunk != "" {
		argv = append(argv, "--observed-trunk", ctx.ObservedTrunk)
	}
	return []Step{{Argv: argv, Cwd: ctx.Root}}, nil
}

type itemShowDoc struct {
	Payload struct {
		Criteria []map[string]json.RawMessage `json:"criteria"`
	} `json:"payload"`
}

func composeVerdict(ctx Ctx, dry bool, stdout io.Writer) ([]Step, error) {
	if ctx.Verdict != "pass" && ctx.Verdict != "pass-inherited" {
		return nil, &Refusal{
			Code: ExitUsage,
			Message: fmt.Sprintf("--verdict %q is refused: only pass or pass-inherited after a landing is allowed; a failed attempt must not freeze criteria. See docs/design/tillr-judge-and-lifecycle.md#prevent-a-failed-landing-from-freezing-the-items-criteria",
				ctx.Verdict),
		}
	}
	itemBin, err := requireBinary(ctx.ItemBin, "tillr-item", dry)
	if err != nil {
		return nil, err
	}
	show := Step{
		Argv: []string{itemBin, "show", "--store", ctx.Store, "--item", ctx.Item, "--root", ctx.Root},
		Cwd:  ctx.Root,
	}
	if dry {
		return []Step{
			show,
			{
				Argv: []string{itemBin, "verdict", "record", "--store", ctx.Store, "--input", "<criterion record>", "--now", ctx.Now},
				Cwd:  ctx.Root,
			},
		}, nil
	}
	cmd := exec.Command(show.Argv[0], show.Argv[1:]...)
	cmd.Dir = show.Cwd
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		code := ExitExternal
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return nil, &Refusal{
			Code:    ExitExternal,
			Message: fmt.Sprintf("cannot name the item's criteria: %s show answered exit %d: %s", show.Argv[0], code, strings.TrimSpace(errBuf.String())),
		}
	}
	var shown itemShowDoc
	if err := json.Unmarshal(outBuf.Bytes(), &shown); err != nil {
		return nil, &Refusal{
			Code:    ExitExternal,
			Message: fmt.Sprintf("reading the item.record %s show printed: %v", itemBin, err),
		}
	}
	if len(shown.Payload.Criteria) == 0 {
		return nil, &Refusal{
			Code:    ExitExternal,
			Message: fmt.Sprintf("item %s carries no criterion: a verdict retires criteria, and an item with none records no judgment", ctx.Item),
		}
	}
	var steps []Step
	for _, rawCrit := range shown.Payload.Criteria {
		var cid string
		if rawID, ok := rawCrit["criterion_id"]; ok {
			_ = json.Unmarshal(rawID, &cid)
		}
		var text string
		if rawText, ok := rawCrit["text"]; ok {
			_ = json.Unmarshal(rawText, &text)
		}
		rawClass, hasClass := rawCrit["claim_class"]
		if !hasClass {
			return nil, &Refusal{
				Code: ExitExternal,
				Message: fmt.Sprintf("criterion %s has no claim_class field: tillr-item build is older than the adapter; tillr-item build: %s; adapter build: %s. Rebuild the tillr programs together and restart the loop",
					cid, itemBuildName(itemBin), adapterBuildName()),
			}
		}
		var claimClass string
		if err := json.Unmarshal(rawClass, &claimClass); err != nil ||
			(claimClass != "verification-only" && claimClass != "red-gate-triage" && claimClass != "direction" && claimClass != "owner-only") {
			return nil, &Refusal{
				Code:    ExitExternal,
				Message: fmt.Sprintf("criterion %s has no valid stored claim_class", cid),
			}
		}
		if claimClass != "verification-only" {
			fmt.Fprintf(stdout, "left criterion %s: stored claim class %s requires a non-mechanical verdict\n", cid, claimClass)
			continue
		}
		rec := makeEnvelope(
			"item.verdict",
			fmt.Sprintf("verdict-%s-%s", ctx.Item, cid),
			map[string]any{
				"item_id":        ctx.Item,
				"criterion_id":   cid,
				"criterion_hash": criterionHash(text),
				"verdict":        "done",
				"claim_class":    "verification-only",
				"evidence":       []string{ctx.verdictRecord(), "run:" + ctx.Run, "head:" + ctx.Head},
				"evaluated_head": ctx.Head,
				"actor_kind":     "mechanical",
			},
			ctx.Now,
		)
		path := filepath.Join(ctx.runDir(), fmt.Sprintf("item.verdict.%s.json", cid))
		if err := writeRecord(path, rec); err != nil {
			return nil, &Refusal{Code: ExitExternal, Message: err.Error()}
		}
		steps = append(steps, Step{
			Argv: []string{itemBin, "verdict", "record", "--store", ctx.Store, "--input", path, "--now", ctx.Now},
			Cwd:  ctx.Root,
		})
	}
	return steps, nil
}

func invoke(step Step, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.Command(step.Argv[0], step.Argv[1:]...)
	cmd.Dir = step.Cwd
	cmd.Stdin = stdin
	cmd.Stderr = stderr
	if len(step.Env) > 0 {
		env := os.Environ()
		for k, v := range step.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	var outFile *os.File
	if step.Stdout != "" {
		if err := os.MkdirAll(filepath.Dir(step.Stdout), 0o755); err != nil {
			return ExitExternal, err
		}
		var err error
		outFile, err = os.Create(step.Stdout)
		if err != nil {
			return ExitExternal, err
		}
		defer outFile.Close()
		cmd.Stdout = outFile
	} else {
		cmd.Stdout = stdout
	}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return ExitExternal, err
	}
	return ExitPass, nil
}

func execute(kind string, steps []Step, stdin io.Reader, stdout, stderr io.Writer) int {
	judgedFrom := len(steps) - 1
	if kind == "verdict" {
		judgedFrom = 0
	}
	for _, step := range steps[:judgedFrom] {
		prepStatus, err := invoke(step, stdin, stdout, stderr)
		if err != nil || prepStatus != ExitPass {
			fmt.Fprintf(stderr, "tillr-loop %s: %s %s could not prepare the judgment: exit %d\n",
				kind, filepath.Base(step.Argv[0]), step.Argv[1], prepStatus)
			return ExitExternal
		}
	}
	stepStatus := ExitPass
	for _, step := range steps[judgedFrom:] {
		var err error
		stepStatus, err = invoke(step, stdin, stdout, stderr)
		if err != nil || stepStatus != ExitPass {
			break
		}
	}
	if kind == "gate" {
		obs := []checkresult.Observation{{
			Source:   checkresult.JudgingProgram,
			ExitCode: &stepStatus,
		}}
		payload, err := json.Marshal(obs)
		if err != nil {
			fmt.Fprintf(stderr, "tillr-loop gate: could not judge: tillr-gate classify: %v\n", err)
			return ExitExternal
		}
		cmd := exec.Command("tillr-gate", "classify")
		cmd.Stdin = bytes.NewReader(payload)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "tillr-loop gate: could not judge: tillr-gate classify: %v\n", err)
			return ExitExternal
		}
		var answers []checkresult.Result
		if err := json.Unmarshal(outBuf.Bytes(), &answers); err != nil ||
			len(answers) != 1 ||
			answers[0].Detail == "" ||
			!validOutcome(answers[0].Outcome) {
			fmt.Fprintf(stderr, "tillr-loop gate: could not judge: tillr-gate classify: invalid classification response\n")
			return ExitExternal
		}
		res := answers[0]
		switch res.Outcome {
		case checkresult.Pass:
			return ExitPass
		case checkresult.Fail:
			return ExitFail
		case checkresult.Unknown:
			if res.Detail == checkresult.DetailStalled {
				return ExitStalled
			}
		}
	} else if kind == "verdict" {
		return stepStatus
	} else if stepStatus == ExitPass || stepStatus == ExitFail {
		return stepStatus
	}
	last := steps[len(steps)-1]
	fmt.Fprintf(stderr, "tillr-loop %s: %s %s answered exit %d: no judgment was made\n",
		kind, filepath.Base(last.Argv[0]), last.Argv[1], stepStatus)
	return ExitExternal
}

func validOutcome(o checkresult.Outcome) bool {
	switch o {
	case checkresult.Pass, checkresult.Fail, checkresult.Unknown, checkresult.Unavailable, checkresult.Disabled:
		return true
	default:
		return false
	}
}

func isTillrGateDir(path string) bool {
	data, err := os.ReadFile(filepath.Join(path, "go.mod"))
	if err != nil {
		return false
	}
	firstLine, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimRight(firstLine, "\r") == gateModuleHeader
}

func resolveGateRunner(script string) (string, error) {
	var gateDir string
	if installRoot := os.Getenv("TILLR_INSTALL_ROOT"); installRoot != "" {
		if !filepath.IsAbs(installRoot) {
			return "", &Refusal{
				Code:    ExitExternal,
				Message: fmt.Sprintf("TILLR_INSTALL_ROOT must be absolute: %s", installRoot),
			}
		}
		gateDir = filepath.Join(installRoot, "tillr-gate")
	} else {
		if exe, err := os.Executable(); err == nil {
			sibling := filepath.Join(filepath.Dir(filepath.Dir(resolvePath(exe))), "tillr-gate")
			if isTillrGateDir(sibling) {
				gateDir = sibling
			}
		}
		if gateDir == "" {
			if cwd, err := os.Getwd(); err == nil {
				resolvedCwd := resolvePath(cwd)
				checkout := resolvedCwd
				if filepath.Base(filepath.Dir(resolvedCwd)) == "tools" {
					checkout = filepath.Dir(filepath.Dir(resolvedCwd))
				}
				candidate := filepath.Join(checkout, "tools", "tillr-gate")
				if isTillrGateDir(candidate) {
					gateDir = candidate
				}
			}
		}
		if gateDir == "" {
			return "", &Refusal{
				Code:    ExitExternal,
				Message: fmt.Sprintf("cannot locate tillr-gate installation for %s; set TILLR_INSTALL_ROOT", script),
			}
		}
	}
	runnerPath := filepath.Join(gateDir, script)
	info, err := os.Stat(runnerPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", &Refusal{
			Code:    ExitExternal,
			Message: fmt.Sprintf("installed tillr-gate runner %s is missing or not a regular file", runnerPath),
		}
	}
	return runnerPath, nil
}

func resolveGateProgram(args parsedArgs) (string, error) {
	if args.gateProgram != "" {
		return args.gateProgram, nil
	}
	script, err := resolveGateRunner("change-gate.py")
	if err != nil {
		return "", err
	}
	root, err := resolveRoot(args.root, args.worktree, args.store)
	if err != nil {
		return "", err
	}
	workDir := args.workDir
	if workDir == "" {
		workDir = filepath.Join(root, ".tillr", "loop")
	}
	logs := filepath.Join(workDir, args.run, "change-logs")
	return fmt.Sprintf("python3 %s --base %s --head %s --runner %s --logs %s",
		shellQuote(script), shellQuote(args.base), shellQuote(args.head), shellQuote(args.tillrGate), shellQuote(logs)), nil
}

func buildContext(kind string, args parsedArgs) (Ctx, error) {
	root, err := resolveRoot(args.root, args.worktree, args.store)
	if err != nil {
		return Ctx{}, err
	}
	workDir := args.workDir
	if workDir == "" {
		workDir = filepath.Join(root, ".tillr", "loop")
	}
	now := args.now
	if now == "" {
		now = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	store := args.store
	if store == "" {
		store = filepath.Join(root, ".tillr", "state.db")
	}
	registry := args.registry
	if registry == "" {
		registry = os.Getenv(registryEnv)
	}
	var gp string
	if kind == "gate" || kind == "self-check" {
		gp, err = resolveGateProgram(args)
		if err != nil {
			return Ctx{}, err
		}
	}
	return Ctx{
		Item:          args.item,
		Lane:          args.lane,
		Run:           args.run,
		Worktree:      resolvePath(args.worktree),
		Base:          args.base,
		Head:          args.head,
		Root:          root,
		Store:         store,
		WorkDir:       workDir,
		Now:           now,
		Registry:      registry,
		GateProgram:   gp,
		BaseRef:       args.baseRef,
		MessageFrom:   args.messageFrom,
		ObservedTrunk: args.observedTrunk,
		Verdict:       args.verdict,
		GateBin:       args.tillrGate,
		LandBin:       args.tillrLand,
		ItemBin:       args.tillrItem,
	}, nil
}

func printSelfCheck(ctx Ctx, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "tillr-loop self-check: one tick's three judgments, and the argv behind them")
	laneDisplay := ctx.Lane
	if laneDisplay == "" {
		laneDisplay = "-"
	}
	fmt.Fprintf(stdout, "tick    item=%s lane=%s run=%s base=%s head=%s worktree=%s\n",
		ctx.Item, laneDisplay, ctx.Run, ctx.Base, ctx.Head, ctx.Worktree)
	missing := false
	for _, kind := range []string{"gate", "land", "verdict"} {
		fmt.Fprintf(stdout, "\n%-7s %s\n", kind, programString[kind])
		steps, err := compose(kind, ctx, true, stdout)
		if err != nil {
			var ref *Refusal
			code := ExitExternal
			msg := err.Error()
			if errors.As(err, &ref) {
				code = ref.Code
				msg = ref.Message
			}
			fmt.Fprintf(stdout, "        REFUSED (exit %d): %s\n", code, msg)
			missing = true
			continue
		}
		for _, step := range steps {
			quoted := make([]string, len(step.Argv))
			for i, w := range step.Argv {
				quoted[i] = shellQuote(w)
			}
			shown := strings.Join(quoted, " ")
			if step.Stdout != "" {
				shown += " > " + shellQuote(step.Stdout)
			}
			if len(step.Env) > 0 {
				keys := make([]string, 0, len(step.Env))
				for k := range step.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				pairs := make([]string, len(keys))
				for i, k := range keys {
					pairs[i] = k + "=" + step.Env[k]
				}
				shown += "  (" + strings.Join(pairs, " ") + ")"
			}
			if strings.Contains(shown, "MISSING") {
				missing = true
			}
			fmt.Fprintf(stdout, "        %s    (in %s)\n", shown, step.Cwd)
		}
	}
	fmt.Fprintf(stdout, "\nloop    tillr drain loop --root %s --gate %s --land %s --verdict %s\n",
		shellQuote(ctx.Root),
		shellQuote(programString["gate"]),
		shellQuote(programString["land"]),
		shellQuote(programString["verdict"]))
	if missing {
		fmt.Fprintln(stderr, "\ntillr-loop self-check: a judge is missing or a step refused: the loop would stop rather than pass a candidate nobody judged")
		return ExitExternal
	}
	return ExitPass
}

func describe(stdout io.Writer) int {
	hooks := make(map[string][]string, len(hookNames))
	for _, name := range hookNames {
		hooks[name] = describeFlagNames(name)
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"hooks": hooks}); err != nil {
		return ExitExternal
	}
	return ExitPass
}

func revisionCheck(kind string, args parsedArgs, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	workDir := ""
	if kind == "fixer-gate" {
		actual, err := gitCmd(args.worktree, "rev-parse", "HEAD")
		if err != nil || actual != args.head {
			return 0, &Refusal{Code: ExitExternal, Message: "named check could not judge: --head is not checked out"}
		}
		workDir = args.worktree
	}
	scratch := os.Getenv("YOLO_DURABLE_DIR")
	if scratch == "" {
		dir := "."
		if workDir != "" {
			dir = workDir
		}
		if _, nonLanding := nonLandingStoreCharter(args.store); nonLanding || os.Getenv("TILLR_NON_LANDING") == "1" {
			if args.root != "" {
				scratch = resolvePath(args.root)
			} else {
				scratch = "."
			}
		} else if common, err := gitCmd(dir, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && common != "" {
			scratch = common
		} else {
			scratch = "."
		}
	}
	logs := filepath.Join(scratch, "tillr-loop-revision-logs", strconv.Itoa(os.Getpid()))
	runner, err := exec.LookPath("tillr-gate")
	if err != nil {
		return 0, &Refusal{Code: ExitExternal, Message: "cannot run revision check: tillr-gate is not on PATH"}
	}
	argv := []string{
		runner, "checks", "--heavy-tree", "--base", "HEAD", "--head", "HEAD",
		"--worktree", ".", "--checks", registryDir, "--logs", logs,
	}
	if kind != "rolling" {
		var scope, tier string
		globBase := registryDir
		if workDir != "" {
			globBase = filepath.Join(workDir, registryDir)
		}
		matches, _ := filepath.Glob(filepath.Join(globBase, "*.json"))
		sort.Strings(matches)
		for _, p := range matches {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var decl struct {
				ID    string `json:"id"`
				Scope string `json:"scope"`
				Tier  string `json:"tier"`
			}
			if err := json.Unmarshal(data, &decl); err != nil {
				continue
			}
			if decl.ID == args.only {
				scope, tier = decl.Scope, decl.Tier
			}
		}
		if scope != "tree" {
			return 0, &Refusal{
				Code:    ExitExternal,
				Message: fmt.Sprintf("named check could not judge: %s is not a whole-tree declaration in %s", args.only, registryDir),
			}
		}
		if tier == "heavy" {
			argv = append(argv, "--only", args.only)
		} else {
			treeCheck, err := resolveGateRunner("tree-check.py")
			if err != nil {
				return 0, err
			}
			argv = []string{
				"python3", treeCheck, "--only", args.only,
				"--base", "HEAD", "--head", "HEAD", "--runner", runner,
			}
		}
	}
	if stdin == os.Stdin && stdout == os.Stdout && stderr == os.Stderr {
		execPath := argv[0]
		if !filepath.IsAbs(execPath) {
			resolved, lookErr := exec.LookPath(execPath)
			if lookErr != nil {
				return 0, &Refusal{Code: ExitExternal, Message: fmt.Sprintf("cannot run revision check: %v", lookErr)}
			}
			execPath = resolved
		}
		if workDir != "" {
			if chdirErr := os.Chdir(workDir); chdirErr != nil {
				return 0, &Refusal{Code: ExitExternal, Message: fmt.Sprintf("cannot run revision check: %v", chdirErr)}
			}
		}
		if execErr := syscall.Exec(execPath, argv, os.Environ()); execErr != nil {
			return 0, &Refusal{Code: ExitExternal, Message: fmt.Sprintf("cannot run revision check: %v", execErr)}
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, &Refusal{Code: ExitExternal, Message: fmt.Sprintf("cannot run revision check: %v", err)}
	}
	return ExitPass, nil
}

func isShellSafe(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		strings.ContainsRune("_@%+=:,./-", r)
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !isShellSafe(r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func shellSplit(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inToken := false
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range s {
		if escaped {
			if inDouble && r != '"' && r != '\\' && r != '$' && r != '`' && r != '\n' {
				cur.WriteByte('\\')
			}
			cur.WriteRune(r)
			escaped = false
			inToken = true
			continue
		}
		if inSingle {
			if r == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(r)
			}
			inToken = true
			continue
		}
		if inDouble {
			if r == '\\' {
				escaped = true
			} else if r == '"' {
				inDouble = false
			} else {
				cur.WriteRune(r)
			}
			inToken = true
			continue
		}
		if unicode.IsSpace(r) {
			if inToken {
				words = append(words, cur.String())
				cur.Reset()
				inToken = false
			}
			continue
		}
		switch r {
		case '\\':
			escaped = true
			inToken = true
		case '\'':
			inSingle = true
			inToken = true
		case '"':
			inDouble = true
			inToken = true
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if escaped || inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote or escape in %q", s)
	}
	if inToken {
		words = append(words, cur.String())
	}
	return words, nil
}
