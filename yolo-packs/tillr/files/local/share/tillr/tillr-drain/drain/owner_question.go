package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
)

// prepareOwnerQuestion authors only the document part of a design settlement. The
// ordinary candidate path still fences, gates, lands, and syncs this commit. No
// conflict is answered until that landing succeeds.
func (l *Loop) prepareOwnerQuestion(ctx context.Context, d *Decision, report string) (bool, error) {
	if d.BlockConflict == nil || designNextActor(report) != DesignNextOwner {
		return false, nil
	}
	if _, ok := item.OwnerDecision(report); !ok {
		return false, nil
	}
	c := *d.BlockConflict
	file, _, _ := strings.Cut(c.BodyRef, "#")
	if file == "" {
		l.reportf("loop: %s has no document for its owner question\n", c.ItemID)
		return false, nil
	}
	target := l.laneTarget(d.LaneID)
	paths, err := l.uncommittedPaths(ctx, target)
	if err != nil {
		return false, err
	}
	for _, dirty := range paths {
		if dirty != file {
			return false, fmt.Errorf("owner question settlement for %s has uncommitted work outside its document: %s", c.ItemID, strings.Join(paths, ", "))
		}
	}
	// Triage may have just created the named document and its item block. That
	// declaration and the question are committed together, then validated by
	// the existing triage admission and design gate.
	if err := commandPath(target, file); err != nil {
		return false, err
	}
	path := filepath.Join(target, file)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		l.reportf("loop: %s has no document for its owner question: %s\n", c.ItemID, file)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	blockID := c.ItemID
	if original := item.ProseRewriteOriginal(blockID); original != "" {
		blockID = original
	}
	if !item.DefinitionBlockExists(file, blockID, raw) {
		l.reportf("loop: %s has no document block for its owner question in %s\n", c.ItemID, file)
		return false, nil
	}
	body, id, err := item.OwnerQuestion(string(raw), blockID, d.RunRequest.RunID, report, l.cfg.Now())
	if err != nil {
		return false, err
	}
	if body != string(raw) || len(paths) > 0 {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return false, err
		}
		if _, err := l.git(ctx, target, "add", "--", file); err != nil {
			return false, err
		}
		if _, err := l.git(ctx, target, "commit", "-m", "docs: record an owner decision in its item document"); err != nil {
			return false, err
		}
	}
	decorateOwnerQuestion(d, report, file, id)
	l.reportf("loop: %s owner question %s written in %s; sending the document candidate through the design gate\n", c.ItemID, id, file)
	return true, nil
}

func decorateOwnerQuestion(d *Decision, report, file, id string) {
	c := *d.BlockConflict
	c.QuestionRef = file + "#" + id
	c.QuestionRunID = d.RunRequest.RunID
	if !strings.Contains(c.Text, "\n\nDesign run "+d.RunRequest.RunID+":\n") || !strings.Contains(c.Text, "\n\nOwner question: "+c.QuestionRef) {
		c.Text += "\n\nDesign run " + d.RunRequest.RunID + ":\n" + strings.TrimSpace(report) + "\n\nOwner question: " + c.QuestionRef
	}
	c.DesignRunID = d.RunRequest.RunID
	d.BlockConflict = &c
}

// A process can die after git commit and before candidate-ready. The committed
// origin marker recovers exactly that artifact and its report provenance, including
// for command runs whose changed HEAD no longer matches the no-commit branch.
func (l *Loop) recoverOwnerQuestion(ctx context.Context, d *Decision, report string) (bool, error) {
	if d.BlockConflict == nil || d.RunRequest == nil {
		return false, nil
	}
	file, _, _ := strings.Cut(d.BlockConflict.BodyRef, "#")
	if file == "" {
		return false, nil
	}
	target := l.laneTarget(d.LaneID)
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return false, err
	}
	raw, err := l.fileAt(ctx, target, head, file)
	if err != nil {
		return false, err
	}
	blockID := d.ItemID
	if original := item.ProseRewriteOriginal(blockID); original != "" {
		blockID = original
	}
	id := item.OwnerQuestionID(raw, blockID, d.RunRequest.RunID)
	if id == "" {
		return false, nil
	}
	decorateOwnerQuestion(d, report, file, id)
	return true, nil
}
