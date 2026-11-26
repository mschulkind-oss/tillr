package pihelper

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// RunCompact implements the pi-compact face: it compacts an existing Pi session
// using its JSONL RPC protocol, without a prompt.
func RunCompact(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tillr-run pi-compact", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	backend := fs.String("backend", "", "backend name (pi or pi-*)")
	model := fs.String("model", "", "model identifier")
	effort := fs.String("effort", "", "thinking effort level")
	timeout := fs.Int("timeout", 0, "compaction timeout in seconds (0 for none)")
	sessionID := fs.String("session-id", "", "pi session id")
	piDirFlag := fs.String("pi-dir", "", "directory holding pi-harness.sh")
	fs.Usage = func() {
		fmt.Fprintln(stdout, "usage: tillr-run pi-compact --backend <pi|pi-*> --model <model> --session-id <id> [--effort <level>] [--timeout <sec>] [--pi-dir <dir>]")
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.Usage()
			return 0
		}
		fmt.Fprintf(stderr, "compact: %v\n", err)
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "compact: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *backend == "" || *model == "" || *sessionID == "" {
		fmt.Fprintln(stderr, "compact: --backend, --model, and --session-id are required")
		return 2
	}
	if *backend != "pi" && !strings.HasPrefix(*backend, "pi-") {
		fmt.Fprintln(stderr, "compact: --backend must name pi or a pi seat alias")
		return 2
	}
	if *timeout < 0 {
		fmt.Fprintln(stderr, "compact: --timeout must be zero or positive")
		return 2
	}

	piDir := resolvePiDir(*piDirFlag)
	if err := runCompactExec(piDir, *model, *effort, *timeout, *sessionID, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "compact: %v\n", err)
		return 1
	}
	return 0
}

func resolvePiDir(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if envDir := strings.TrimSpace(os.Getenv("TILLR_PI_ADAPTER_DIR")); envDir != "" {
		return envDir
	}
	if os.Getenv("TILLR_NON_LANDING") != "1" {
		if out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output(); err == nil {
			common := strings.TrimSpace(string(out))
			if common != "" {
				candidate := filepath.Join(filepath.Dir(common), "tools", "tillr-run", "backends", "pi")
				if _, err := os.Stat(filepath.Join(candidate, "pi-harness.sh")); err == nil {
					return candidate
				}
				if root := strings.TrimSpace(os.Getenv("TILLR_INSTALL_ROOT")); root == "" {
					return candidate
				}
			}
		}
	}
	if root := strings.TrimSpace(os.Getenv("TILLR_INSTALL_ROOT")); root != "" {
		return filepath.Join(root, "tillr-run", "backends", "pi")
	}
	return ""
}

func runCompactExec(piDir, model, effort string, timeoutSec int, sessionID string, stdout, stderr io.Writer) error {
	harness := filepath.Join(piDir, "pi-harness.sh")
	argv := []string{"--exec", "--", "--mode", "rpc", "--session", sessionID, "--model", model}
	if effort != "" {
		argv = append(argv, "--thinking", effort)
	}
	cmd := exec.Command(harness, argv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	childOut, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	var timedOut atomic.Bool
	done := make(chan struct{})
	defer close(done)
	if timeoutSec > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				timedOut.Store(true)
				if cmd.Process != nil {
					_ = cmd.Process.Signal(syscall.SIGTERM)
				}
			case <-done:
			}
		}()
	}

	exited := false
	defer func() {
		if !exited && cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
		}
	}()

	reader := bufio.NewReader(childOut)
	request := func(command string) (map[string]any, error) {
		reqBytes, err := json.Marshal(map[string]string{"id": command, "type": command})
		if err != nil {
			return nil, err
		}
		if _, err := stdin.Write(append(reqBytes, '\n')); err != nil {
			if timedOut.Load() {
				return nil, errors.New("compaction exceeded the explicit timeout")
			}
			return nil, err
		}
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				var event map[string]any
				if jsonErr := json.Unmarshal(line, &event); jsonErr != nil {
					return nil, jsonErr
				}
				if typ, _ := event["type"].(string); typ == "response" {
					if id, _ := event["id"].(string); id == command {
						if ok, _ := event["success"].(bool); !ok {
							msg, _ := event["error"].(string)
							if msg == "" {
								msg = "Pi refused " + command
							}
							return nil, errors.New(msg)
						}
						data, _ := event["data"].(map[string]any)
						if data == nil {
							data = map[string]any{}
						}
						return data, nil
					}
				}
			}
			if err != nil {
				if timedOut.Load() {
					return nil, errors.New("compaction exceeded the explicit timeout")
				}
				if errors.Is(err, io.EOF) {
					return nil, errors.New("Pi exited before answering " + command)
				}
				return nil, err
			}
		}
	}

	state, err := request("get_state")
	if err != nil {
		return err
	}
	if gotID, _ := state["sessionId"].(string); gotID != sessionID {
		return errors.New("Pi opened a different session; refusing to compact")
	}

	if journalPath := os.Getenv("TILLR_COMPACTION_JOURNAL"); journalPath != "" {
		raw, err := os.ReadFile(journalPath)
		if err != nil {
			return err
		}
		var journal map[string]any
		if err := json.Unmarshal(raw, &journal); err != nil {
			return err
		}
		transcript, _ := state["sessionFile"].(string)
		if transcript == "" {
			return errors.New("Pi did not report the session transcript")
		}
		st, err := os.Stat(transcript)
		if err != nil {
			return err
		}
		journal["transcript"] = transcript
		journal["offset"] = st.Size()
		encoded, err := json.Marshal(journal)
		if err != nil {
			return err
		}
		nextPath := journalPath + ".next"
		f, err := os.OpenFile(nextPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(encoded); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Rename(nextPath, journalPath); err != nil {
			return err
		}
	}

	result, err := request("compact")
	if err != nil {
		return err
	}
	before, hasBefore := result["tokensBefore"]
	after, hasAfter := result["estimatedTokensAfter"]
	if !hasBefore || !hasAfter || before == nil || after == nil {
		return errors.New("Pi did not report compaction token measurements")
	}
	usage, _ := result["usage"].(map[string]any)
	var totalTokens any
	var costTotal any
	if usage != nil {
		totalTokens = usage["totalTokens"]
		if costMap, _ := usage["cost"].(map[string]any); costMap != nil {
			costTotal = costMap["total"]
		}
	}
	receipt := struct {
		TokensBefore any `json:"tokens_before"`
		TokensAfter  any `json:"tokens_after"`
		Tokens       any `json:"tokens"`
		CostUSD      any `json:"cost_usd"`
	}{
		TokensBefore: before,
		TokensAfter:  after,
		Tokens:       totalTokens,
		CostUSD:      costTotal,
	}

	_ = stdin.Close()
	waitErr := cmd.Wait()
	exited = true
	if timedOut.Load() {
		return errors.New("compaction exceeded the explicit timeout")
	}
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			return fmt.Errorf("Pi exited with status %d", exitErr.ExitCode())
		}
		return waitErr
	}

	outBytes, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(outBytes))
	return err
}
