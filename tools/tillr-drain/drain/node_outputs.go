package drain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/tillr/store"
)

func outputNameValid(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
func outputTypeValid(typ string) bool { return typ == "markdown" || typ == "json" || typ == "patch" }
func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateOutputWiring(w *Workflow) error {
	stages := map[string]WorkflowStage{}
	fed := map[string]map[string]bool{}
	for _, s := range w.Stages {
		stages[s.ID] = s
		fed[s.ID] = map[string]bool{}
		for _, name := range sortedNames(s.Outputs) {
			if !outputNameValid(name) || !outputTypeValid(s.Outputs[name]) {
				return fmt.Errorf("workflow-output: %s: invalid name or type for %q", s.ID, name)
			}
		}
		for _, name := range sortedNames(s.Inputs) {
			if !outputNameValid(name) || !outputTypeValid(s.Inputs[name]) {
				return fmt.Errorf("workflow-input: %s: invalid name or type for %q", s.ID, name)
			}
		}
	}
	for _, e := range w.Edges {
		producer, present := stages[e.From]
		consumer, exists := stages[e.To]
		if !present || !exists {
			return fmt.Errorf("workflow-unknown-node: %s -> %s", e.From, e.To)
		}
		if e.FromOutput == "" && e.ToInput == "" {
			continue
		}
		if e.FromOutput == "" || e.ToInput == "" {
			return fmt.Errorf("workflow-input: edge %s -> %s must name both from_output and to_input", e.From, e.To)
		}
		typ, declared := producer.Outputs[e.FromOutput]
		if !declared {
			return fmt.Errorf("workflow-output: %s does not declare %s", e.From, e.FromOutput)
		}
		inputType, declared := consumer.Inputs[e.ToInput]
		if !declared || inputType != typ {
			return fmt.Errorf("workflow-input: %s: %s undeclared or type differs from %s.%s", e.To, e.ToInput, e.From, e.FromOutput)
		}
		if fed[e.To][e.ToInput] {
			return fmt.Errorf("workflow-input: %s.%s fed by two edges", e.To, e.ToInput)
		}
		fed[e.To][e.ToInput] = true
	}
	for _, s := range w.Stages {
		for _, name := range sortedNames(s.Inputs) {
			if !fed[s.ID][name] {
				return fmt.Errorf("workflow-input: %s.%s fed by no edge", s.ID, name)
			}
		}
	}
	return nil
}

func (w *Workflow) hasNamedFiles() bool {
	for _, s := range w.Stages {
		if len(s.Inputs) > 0 || len(s.Outputs) > 0 {
			return true
		}
	}
	return false
}

func nodeOutputsDir(root, runID string) string {
	return filepath.Join(RunDir(root, runID), "artifacts")
}

// Input paths come from the accepted producer row, not a guessed run id: a
// restarted producer may have a different run id from its original dispatch.
func (l *Loop) nodeInputPaths(ctx context.Context, d Decision) (map[string]string, error) {
	paths := map[string]string{}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		for _, e := range d.Workflow.Edges {
			if e.To != d.Workflow.Stages[d.NodeIndex].ID || e.ToInput == "" {
				continue
			}
			var path string
			if err := r.QueryRow(`SELECT a.location FROM node_artifacts a JOIN node_runs n ON n.item_id=a.item_id AND n.attempt=a.attempt AND n.node_id=a.node_id AND n.run_id=a.run_id WHERE a.item_id=? AND a.attempt=? AND a.node_id=? AND a.name=? AND n.outcome='passed'`, d.ItemID, d.Attempt, e.From, e.FromOutput).Scan(&path); err != nil {
				return fmt.Errorf("node-input-missing: %s.%s from %s.%s: %w", e.To, e.ToInput, e.From, e.FromOutput, err)
			}
			paths[e.ToInput] = path
		}
		return nil
	})
	return paths, err
}

type nodeArtifact struct{ Name, Type, SHA256, Location string }

func checkNodeOutputs(root string, d Decision) ([]nodeArtifact, error) {
	stage := d.Workflow.Stages[d.NodeIndex]
	artifacts := []nodeArtifact{}
	dir := nodeOutputsDir(root, d.RunRequest.RunID)
	for _, name := range sortedNames(stage.Outputs) {
		path := filepath.Join(dir, name)
		// No symlinks or special files: an output is the producer's regular file.
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("node-output-missing: %s.%s: output directory is missing or not a directory", stage.ID, name)
		}
		info, err = os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("node-output-missing: %s.%s: file missing or not a regular file", stage.ID, name)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("node-output-missing: %s.%s: %w", stage.ID, name, err)
		}
		if stage.Outputs[name] == "json" {
			if err := uniqueJSON(raw); err != nil {
				return nil, fmt.Errorf("node-output-missing: %s.%s: invalid json: %w", stage.ID, name, err)
			}
		}
		artifacts = append(artifacts, nodeArtifact{Name: name, Type: stage.Outputs[name], SHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Location: path})
	}
	return artifacts, nil
}

// Token traversal keeps object keys at every depth; unmarshaling into a map
// would silently replace the first occurrence of a duplicate key.
func uniqueJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("expected object key")
				}
				if keys[name] {
					return fmt.Errorf("duplicate key %q", name)
				}
				keys[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected delimiter %s", delim)
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

// Acceptance and the passed outcome share a transaction. A crash cannot leave
// a passed node whose declared artifacts were never recorded.
func (l *Loop) acceptNodeOutputs(ctx context.Context, d Decision, artifacts []nodeArtifact) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, a := range artifacts {
			if _, err := tx.Exec(`INSERT INTO node_artifacts(item_id,attempt,node_id,run_id,name,type,sha256,location) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(item_id,attempt,node_id,name) DO NOTHING`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID, d.RunRequest.RunID, a.Name, a.Type, a.SHA256, a.Location); err != nil {
				return err
			}
			var stored nodeArtifact
			var runID string
			if err := tx.QueryRow(`SELECT run_id,name,type,sha256,location FROM node_artifacts WHERE item_id=? AND attempt=? AND node_id=? AND name=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID, a.Name).Scan(&runID, &stored.Name, &stored.Type, &stored.SHA256, &stored.Location); err != nil {
				return err
			}
			if stored != a || runID != d.RunRequest.RunID {
				return fmt.Errorf("node artifact %s.%s already recorded with different contents", d.Workflow.Stages[d.NodeIndex].ID, a.Name)
			}
		}
		_, err := tx.Exec(`UPDATE node_runs SET outcome='passed' WHERE run_id=?`, d.RunRequest.RunID)
		return err
	})
}
