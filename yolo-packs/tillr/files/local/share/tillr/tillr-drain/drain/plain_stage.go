package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// PlainDir is the default working directory for definitions that finish without landing.
const PlainDir = "work"

// readDefinition closes the read transaction before staging does any file or process work.
func (s *LaneStager) readDefinition(ctx context.Context) (definition.Definition, error) {
	r, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return definition.Definition{}, err
	}
	defer r.Close()
	var d definition.Definition
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		declared, err := definition.ForReader(reader)
		if err == nil {
			d = declared.Parsed
		}
		return err
	})
	return d, err
}

// stagePlain renders the definition's template, then creates an ordinary directory.
// It neither reads a Git base nor derives a fence from the item's paths.
func (s *LaneStager) stagePlain(ctx context.Context, spec StageSpec, def definition.Definition) error {
	if err := checkRunID(spec.RunID); err != nil {
		return err
	}
	workingDir := filepath.Clean(spec.WorkingDir)
	if spec.WorkingDir == "" || filepath.IsAbs(workingDir) || workingDir == ".." || strings.HasPrefix(workingDir, ".."+string(filepath.Separator)) {
		return Usagef("staging %s needs a working_dir inside the lane directory, got %q", spec.RunID, spec.WorkingDir)
	}
	if def.BriefTemplate == "" {
		return Usagef("definition %s needs a brief_template to stage %s", def.Name, spec.RunID)
	}
	template := def.BriefTemplate
	if !filepath.IsAbs(template) {
		template = filepath.Join(s.Root, template)
	}
	recorded, err := s.readItem(ctx, spec.ItemID)
	if err != nil {
		return err
	}
	if recorded.ItemID == "" {
		return Usagef("staging %s: the store holds no item %q", spec.RunID, spec.ItemID)
	}
	if recorded.Title == "" {
		return ItemContentf(recorded.ItemID, "title", "item %s carries no title: add a title before staging", recorded.ItemID)
	}
	if len(recorded.Criteria) == 0 {
		return ItemContentf(recorded.ItemID, "criteria", "item %s carries no criteria: add criteria before staging", recorded.ItemID)
	}
	criteria := recorded.Criteria[0].Text
	for i, c := range recorded.Criteria[1:] {
		criteria += fmt.Sprintf("\n%d. %s", i+2, c.Text)
	}
	laneDir := filepath.Join(s.LaneRoot, spec.LaneID)
	target, err := filepath.Abs(filepath.Join(laneDir, workingDir))
	if err != nil {
		return err
	}
	durableDir, err := filepath.Abs(run.DurableDir(laneDir, spec.RunID))
	if err != nil {
		return err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	settled := "None recorded: the item names no settled decision."
	seat := stagedSeat(recorded, spec)
	if carriesDesignDocumentRule(seat) {
		settled += "\n\n" + DesignDocumentRule
	}
	if carriesDesignProvenanceRule(seat) {
		settled += "\n\n" + DesignProvenanceRule
	}
	fixerFacts, fixerCopies, err := s.prepareTrunkFixerFacts(ctx, recorded, spec)
	if err != nil {
		return err
	}
	facts := "None carried: staging read no facts about the task."
	if fixerFacts != "" {
		facts = fixerFacts
	}
	values := briefValues{
		{placeholder: "ITEM_ID", text: recorded.ItemID, field: "item_id"},
		{placeholder: "TITLE", text: recorded.Title, field: "title"},
		{placeholder: "TASK", text: taskSection(recorded), field: "body_ref"},
		{placeholder: "CRITERION_1", text: criteria, field: "criteria"},
		{placeholder: "RUN_ID", text: spec.RunID},
		{placeholder: "BACKEND", text: spec.Backend},
		{placeholder: "SEAT_LINES", text: seatLines(spec.Policy)},
		{placeholder: "TARGET", text: target},
		{placeholder: "TARGET_KIND", text: "plain directory"},
		{placeholder: "DATE", text: now().UTC().Format("2006-01-02")},
		{placeholder: "TIMEOUT_SECONDS", text: wallWords(spec.TimeoutMS)},
		{placeholder: "DURABLE_DIR", text: durableDir},
		{placeholder: "SETTLED", text: settled},
		{placeholder: "FACTS", text: facts},
		{placeholder: "METHOD", text: "The item states no method; the order of work is yours."},
		{placeholder: "VERIFICATION", text: "None recorded: report each verification command and its output."},
	}
	brief, err := renderBrief(template, recorded.ItemID, values)
	if err != nil {
		return err
	}
	guidance, err := itemGuidanceBrief(ctx, s.Store, spec.ItemID, "")
	if err != nil {
		return err
	}
	brief += guidance
	brief, err = s.workflowNodeBrief(spec, brief, false)
	if err != nil {
		return err
	}
	sessionID, err := sessionIDFor(spec.SessionID)
	if err != nil {
		return err
	}
	// Refuse a reused Git lane rather than remove its worktree or hide its boundary.
	for _, name := range []string{WorktreeDir, BaseSHAFile, FenceFile, ItemBaseSHAFile, ItemFenceFile, TargetFile, PatchRefFile} {
		if _, err := os.Lstat(filepath.Join(laneDir, name)); err == nil {
			return Usagef("staging %s: plain lane still contains Git layout %s; use a clean lane directory", spec.RunID, name)
		} else if !os.IsNotExist(err) {
			return Externalf("checking plain lane %s: %v", laneDir, err)
		}
	}
	// Check every component inside the lane: a symlink in a nested working_dir
	// must not redirect directory creation or brief writes outside that lane.
	laneAbs, err := filepath.Abs(laneDir)
	if err != nil {
		return err
	}
	for dir := target; ; dir = filepath.Dir(dir) {
		if info, err := os.Lstat(dir); err == nil {
			if !info.IsDir() {
				return Usagef("plain working_dir %s has a non-directory component %s", target, dir)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if dir == laneAbs {
			break
		}
	}
	if _, err := os.Lstat(filepath.Join(target, ".git")); err == nil {
		return Usagef("plain working_dir %s contains Git metadata", target)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return Externalf("creating plain working_dir %s: %v", target, err)
	}
	metadataDir := laneDir
	if spec.Isolated {
		metadataDir = run.NodeDir(laneDir, spec.RunID)
	}
	for _, copyFile := range fixerCopies {
		if err := os.MkdirAll(filepath.Dir(copyFile.path), 0o755); err != nil {
			return Externalf("creating fixer log directory %s: %v", filepath.Dir(copyFile.path), err)
		}
		if err := os.WriteFile(copyFile.path, copyFile.body, 0o644); err != nil {
			return Externalf("writing fixer log %s: %v", copyFile.path, err)
		}
	}
	for _, dir := range []string{metadataDir, RunDir(s.Root, spec.RunID)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		for name, body := range map[string]string{BriefFile: brief, SessionIDFile: sessionID + "\n", endingCapabilityFile: run.EndingFooter + "\n", braceLiteralsFile: strconv.Itoa(braceLiteralCount(brief)) + "\n"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
				return Externalf("writing %s: %v", name, err)
			}
		}
	}
	return nil
}
