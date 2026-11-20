package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// CommandEntry registers a fixed program and argv. Flag types are string, number,
// or path (a relative repository path); workflow data is appended as argv only.
type CommandEntry struct {
	Argv  []string          `json:"argv"`
	Flags map[string]string `json:"flags"`
}

func resolveCommand(root string, stage *WorkflowStage) error {
	raw, err := os.ReadFile(filepath.Join(root, "config/tillr/commands.json"))
	if err != nil {
		return fmt.Errorf("command-registry: %w", err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var entries map[string]CommandEntry
	if err = d.Decode(&entries); err != nil {
		return fmt.Errorf("command-registry: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("command-registry: trailing content")
	}
	entry, ok := entries[stage.CommandRef]
	if !ok {
		return fmt.Errorf("command-reference: %s", stage.CommandRef)
	}
	if len(entry.Argv) == 0 || entry.Argv[0] == "" {
		return fmt.Errorf("command-registry: %s needs argv", stage.CommandRef)
	}
	// A registered shell would turn otherwise inert flag values into shell input.
	switch filepath.Base(entry.Argv[0]) {
	case "sh", "bash", "dash", "zsh", "fish", "ksh", "csh", "tcsh":
		return fmt.Errorf("command-registry: shell programs are not command nodes")
	}
	for flag, kind := range entry.Flags {
		if !strings.HasPrefix(flag, "--") || strings.ContainsAny(flag, "= \t\n") || (kind != "string" && kind != "number" && kind != "path") {
			return fmt.Errorf("command-registry: invalid flag %q or type %q", flag, kind)
		}
	}
	argv := append([]string(nil), entry.Argv...)
	keys := make([]string, 0, len(stage.Flags))
	for key := range stage.Flags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, flag := range keys {
		kind, ok := entry.Flags[flag]
		if !ok {
			return fmt.Errorf("command-flag: %s does not accept %s", stage.CommandRef, flag)
		}
		value := stage.Flags[flag]
		var word string
		switch kind {
		case "string", "path":
			var ok bool
			word, ok = value.(string)
			if !ok {
				return fmt.Errorf("command-value: %s requires %s", flag, kind)
			}
		case "number":
			n, ok := value.(json.Number)
			if !ok {
				return fmt.Errorf("command-value: %s requires number", flag)
			}
			word = n.String()
		}
		if strings.ContainsRune(word, 0) {
			return fmt.Errorf("command-value: %s contains NUL", flag)
		}
		if kind == "path" {
			if err := commandPath(root, word); err != nil {
				return err
			}
		}
		// One argv element, even for strings beginning with '-' or containing shell syntax.
		argv = append(argv, flag+"="+word)
	}
	stage.Command = argv
	return nil
}

func commandPath(root, path string) error {
	// Cleaning before validation but transmitting the original spelling would
	// disagree with filesystem traversal through a symlink followed by '..'.
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." {
			return fmt.Errorf("command-path: parent traversal is not allowed in %q", path)
		}
	}
	clean := filepath.Clean(path)
	if path == "" || filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("command-path: %q is outside the repository", path)
	}
	// Inspect the nearest existing ancestor too: generated paths may not exist yet.
	candidate := filepath.Join(root, clean)
	for {
		_, err := os.Lstat(candidate)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("command-path: %w", err)
		}
		candidate = filepath.Dir(candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return fmt.Errorf("command-path: %w", err)
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("command-path: %w", err)
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("command-path: %q resolves outside the repository", path)
	}
	return nil
}

func (s WorkflowStage) commandRequest(itemID, laneID, runID string) *run.Request {
	return &run.Request{RunID: runID, ItemID: itemID, NodeID: s.ID, Node: s.ID, LaneID: laneID, Backend: "command", Command: append([]string(nil), s.Command...), WorkingDir: WorktreeDir, TimeoutMS: s.TimeoutMS, EnvRefs: []string{}}
}

func commandStage(d Decision) bool {
	return d.Workflow != nil && d.Workflow.Stages[d.NodeIndex].Kind == "command"
}

// replayCommandHold completes the cleanup of a recorded terminal hold. A pin
// means the earlier examination retained its head before resetting the lane;
// comparing the reset worktree to NodeBaseSHA would reclassify that hold and
// pin the trunk over the builder's commit. Never update the retained pin here.
func (l *Loop) replayCommandHold(ctx context.Context, d Decision) (bool, error) {
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		return false, err
	}
	var outcome string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, d.RunRequest.RunID).Scan(&outcome)
	})
	s.Close()
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if outcome != "failed" && outcome != "fault" && outcome != "refused" {
		return false, nil
	}
	ref := "refs/tillr/runs/" + d.RunRequest.RunID
	pins, err := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(refname)", ref)
	if err != nil {
		return false, err
	}
	pinned := false
	for _, name := range strings.Split(pins, "\n") {
		if name == ref {
			pinned = true
		}
	}
	if !pinned {
		// Classification persisted but cleanup did not reach the pin: the
		// original worktree remains available for the ordinary settlement.
		return false, nil
	}
	if err := l.preserveWork(ctx, d.LaneID, d.RunRequest.RunID, "replayed command node hold"); err != nil {
		return true, err
	}
	if err := l.resetLane(ctx, d.LaneID); err != nil {
		return true, err
	}
	return true, l.markExamined(ctx, d)
}
