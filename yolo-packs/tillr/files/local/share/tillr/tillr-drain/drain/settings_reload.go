package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) postLandingSettings() settings.Settings {
	l.settingsMu.RLock()
	defer l.settingsMu.RUnlock()
	return l.cfg.PostLanding
}

func (l *Loop) commentsSettings() *settings.Comments {
	l.settingsMu.RLock()
	defer l.settingsMu.RUnlock()
	return l.cfg.Comments
}

func (l *Loop) vantageSettings() (string, string) {
	l.settingsMu.RLock()
	defer l.settingsMu.RUnlock()
	return l.cfg.VantageURL, l.cfg.VantageRepo
}

func (l *Loop) workflowDefaultsSettings() map[string]string {
	l.settingsMu.RLock()
	defer l.settingsMu.RUnlock()
	return l.cfg.WorkflowDefaults
}

func (l *Loop) candidateSizeLimitsSettings() *settings.CandidateSizeLimits {
	l.settingsMu.RLock()
	defer l.settingsMu.RUnlock()
	return l.cfg.CandidateSizeLimits
}

func expressLaneCount(lanes []string) int64 {
	var n int64
	for _, lane := range lanes {
		if strings.HasPrefix(lane, "express-") {
			n++
		}
	}
	return n
}

func landingChangedSettingsFile(ctx context.Context, root, tip string) (bool, error) {
	out, err := actionCommand(ctx, root, "git", "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", "--root", tip, "--", ".tillr.toml")
	if err != nil {
		return false, err
	}
	for _, entry := range strings.Split(out, "\x00") {
		if entry == ".tillr.toml" {
			return true, nil
		}
	}
	return false, nil
}

// reloadSettingsOnLanding re-reads .tillr.toml when a trunk landing changed it.
// Settings consulted per pass, run or landing (roll, push, comments, workflows.default,
// sync.roadmaps, candidate_size, prose, intake) take effect immediately. Startup-only
// settings (trunk.branch, lanes.express) stay at their running values and raise a
// settings-drift alarm until the loop restarts or a later landing matches them.
func (l *Loop) reloadSettingsOnLanding(ctx context.Context, tip string) {
	if l.cfg.Root == "" || tip == "" {
		return
	}
	changed, err := landingChangedSettingsFile(ctx, l.cfg.Root, tip)
	if err != nil || !changed {
		return
	}
	next, err := settings.Read(l.cfg.Root)
	if err != nil {
		l.reportf("loop: landed .tillr.toml at %.12s could not be read; keeping running settings: %v\n", tip, err)
		l.recordSettingsEvent(ctx, alarms.SettingsDriftKind, tip, map[string]any{
			"detail":   err.Error(),
			"settings": []string{".tillr.toml"},
		})
		return
	}

	var diffNames, diffDetails []string
	runningTrunk := l.trunkRef()
	if next.TrunkBranch() != runningTrunk {
		diffNames = append(diffNames, "trunk.branch")
		diffDetails = append(diffDetails, fmt.Sprintf("trunk.branch (running %q, file %q)", runningTrunk, next.TrunkBranch()))
	}
	runningExpress := expressLaneCount(l.cfg.Lanes)
	if next.Lanes.Express != runningExpress {
		diffNames = append(diffNames, "lanes.express")
		diffDetails = append(diffDetails, fmt.Sprintf("lanes.express (running %d, file %d)", runningExpress, next.Lanes.Express))
	}

	var vantageURL, vantageRepo string
	if c := next.Comments; c != nil {
		vantageURL, vantageRepo = c.VantageURL, c.Repo
		if vantageRepo == "" {
			if absRoot, absErr := filepath.Abs(l.cfg.Root); absErr == nil {
				vantageRepo = filepath.Base(absRoot)
			}
		}
	}

	limits := next.CandidateSize
	l.settingsMu.Lock()
	l.cfg.PostLanding.Brief = next.Brief
	l.cfg.PostLanding.Roll = next.Roll
	l.cfg.PostLanding.Push = next.Push
	l.cfg.PostLanding.Comments = next.Comments
	l.cfg.PostLanding.WorkflowDefaults = next.WorkflowDefaults
	l.cfg.PostLanding.Sync = next.Sync
	l.cfg.PostLanding.CandidateSize = next.CandidateSize
	l.cfg.PostLanding.Prose = next.Prose
	l.cfg.PostLanding.Intake = next.Intake
	l.cfg.Comments = next.Comments
	l.cfg.VantageURL = vantageURL
	l.cfg.VantageRepo = vantageRepo
	l.cfg.WorkflowDefaults = next.WorkflowDefaults
	l.cfg.CandidateSizeLimits = &limits
	l.settingsMu.Unlock()

	if ps, ok := l.cfg.Steps.(*ProcessSteps); ok {
		ps.SetSyncRoadmaps(next.Sync.Roadmaps)
	}
	if policy := l.currentPolicy(); policy != nil {
		for _, diagnostic := range WorkflowDefaultDiagnostics(l.cfg.Root, next.WorkflowDefaults, policy) {
			l.reportf("workflow-invalid: %s\n", diagnostic)
		}
	}
	l.reportf("loop: reloaded .tillr.toml at %.12s: %s\n", tip, next.StartupLine())

	if len(diffDetails) > 0 {
		detail := strings.Join(diffDetails, "; ")
		l.reportf("loop: .tillr.toml at %.12s changed restart-only settings (%s); restart required\n", tip, detail)
		l.recordSettingsEvent(ctx, alarms.SettingsDriftKind, tip, map[string]any{
			"detail":   detail,
			"settings": diffNames,
		})
		return
	}
	l.recordSettingsEvent(ctx, alarms.SettingsCurrentKind, tip, map[string]any{})
}

func (l *Loop) recordSettingsEvent(ctx context.Context, kind, tip string, payload map[string]any) {
	if l.cfg.Store == "" {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	if l.cfg.Now != nil {
		now = l.cfg.Now().UTC()
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.WriteTx(ctx, func(tx *store.Tx) error {
		var objID *string
		if tip != "" {
			objID = &tip
		}
		_, err := tx.Append(event.Event{Kind: kind, ObjectType: "commit", ObjectID: objID, Payload: body, At: now})
		return err
	})
}
