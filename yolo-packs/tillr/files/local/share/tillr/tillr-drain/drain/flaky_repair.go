package drain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// FlakyRepairPrefix prefixes every repair item the marker sweep files for a disabled check.
	FlakyRepairPrefix = "flaky-"
	// FlakyRepairCriterion is the criterion id on a filed flaky-check repair item.
	FlakyRepairCriterion = "re-enable-check"
)

// EnableStep is the optional capability a Steps implementation may carry: clear a disabled
// check's episode after its repair item has landed (`tillr-gate enable`).
type EnableStep interface {
	EnableCheck(ctx context.Context, checkID, episode, storeDir string) error
}

// EnableCheck runs `tillr-gate enable` against the configured result store so a landed
// repair clears its episode and writes the enabled/resolved markers atomically.
func (s *ProcessSteps) EnableCheck(ctx context.Context, checkID, episode, storeDir string) error {
	bin := s.HeavyGateBin
	if bin == "" {
		bin = "tillr-gate"
	}
	args := []string{"enable", "--check", checkID, "--episode", episode}
	if storeDir != "" {
		args = append(args, "--store", storeDir)
	}
	if s.Root != "" {
		args = append(args, "--worktree", s.Root)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if s.Root != "" {
		cmd.Dir = s.Root
	}
	cmd.Env = withGitCeilingEnv(os.Environ(), s.LaneRoot)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w (%s)", bin, strings.Join(args, " "), err, strings.TrimSpace(string(raw)))
	}
	return nil
}

// IsFlakyRepairItem answers whether itemID is a generated repair item for a disabled check.
func IsFlakyRepairItem(itemID string) bool {
	return strings.HasPrefix(itemID, FlakyRepairPrefix)
}

// FlakyRepairBaseID answers the primary item id (`flaky-<check-id>`) for checkID.
func FlakyRepairBaseID(checkID string) string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, strings.TrimSpace(checkID))
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "check"
	}
	return FlakyRepairPrefix + slug
}

// FlakyRepairTitle answers the title of the repair item filed for checkID: re-enabling
// that named check correctly rather than reporting a generic flake.
func FlakyRepairTitle(checkID string) string {
	return "re-enable check `" + checkID + "` correctly"
}

func flakyLogLocation(ref string) string {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "output location not recorded"
	}
	trimmed = strings.ReplaceAll(trimmed, "{{", "{ {")
	return strings.ReplaceAll(trimmed, "}}", "} }")
}

// FlakyRepairCriterionText states the work and completion condition of a disabled-check
// repair item, carrying the disabling episode, fingerprint, both timestamps, and both
// retained output locations.
func FlakyRepairCriterionText(marker checkresult.DisabledMarker) string {
	episode := marker.Episode
	if episode == "" && marker.Fingerprint != "" && marker.PassAt != "" && marker.FailAt != "" {
		episode = checkresult.FormatEpisode(marker.Fingerprint, marker.PassAt, marker.FailAt)
	}
	return fmt.Sprintf(
		"Fix check `%s` at its cause and re-enable `%s` correctly (do not add retries, sleeps, or quarantine skips); its work is done when `%s` is back in the gate program and no longer both passes and fails on the same inputs. Disabling episode `%s` (fingerprint `%s`): passed at %s (%s) and failed at %s (%s).",
		marker.CheckID,
		marker.CheckID,
		marker.CheckID,
		episode,
		marker.Fingerprint,
		marker.PassAt,
		flakyLogLocation(marker.PassLogRef),
		marker.FailAt,
		flakyLogLocation(marker.FailLogRef),
	)
}

func (l *Loop) gateResultsStoreDir(ctx context.Context) (string, error) {
	if l.cfg.GateResultsStore != "" {
		return l.cfg.GateResultsStore, nil
	}
	if l.cfg.Root == "" {
		return "", nil
	}
	dotGit := filepath.Join(l.cfg.Root, ".git")
	if info, err := os.Stat(dotGit); err == nil && info.IsDir() {
		return filepath.Join(dotGit, "tillr-gate", "results"), nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", l.cfg.Root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	raw, err := cmd.Output()
	if err != nil {
		return "", err
	}
	commonDir := strings.TrimSpace(string(raw))
	if commonDir == "" {
		return "", nil
	}
	return filepath.Join(commonDir, "tillr-gate", "results"), nil
}

// sweepDisabledMarkers scans `<results-store>/disabled/*.json` and ensures every active
// episode has its repair item filed (or cleared via `tillr-gate enable` once that repair
// item has landed). It detaches from caller cancellation so a step that errored or was
// cancelled still files its repair item before attribution, pending reverts, or dispatch.
func (l *Loop) sweepDisabledMarkers(ctx context.Context) {
	if l == nil || l.nonLanding || l.cfg.Store == "" || l.isRestartReplaced() {
		return
	}
	sweepCtx := context.WithoutCancel(ctx)
	l.flakySweepMu.Lock()
	defer l.flakySweepMu.Unlock()

	storeDir, err := l.gateResultsStoreDir(sweepCtx)
	if err != nil || storeDir == "" {
		return
	}
	disabledDir := filepath.Join(storeDir, "disabled")
	entries, err := os.ReadDir(disabledDir)
	if err != nil || len(entries) == 0 {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(disabledDir, name))
		if err != nil {
			continue
		}
		var marker checkresult.DisabledMarker
		if err := json.Unmarshal(raw, &marker); err != nil {
			l.reportf("loop: ignoring unreadable disabled marker %s: %v\n", name, err)
			continue
		}
		if err := l.reconcileDisabledMarker(sweepCtx, storeDir, marker); err != nil {
			l.reportf("loop: reconciling disabled marker for %s: %v\n", marker.CheckID, err)
		}
	}
}

type existingFlakyRow struct {
	itemID    string
	state     string
	criterion string
}

func isNumberedFlakyID(baseID, candidate string) bool {
	if candidate == baseID {
		return true
	}
	if !strings.HasPrefix(candidate, baseID+"-") {
		return false
	}
	suffix := strings.TrimPrefix(candidate, baseID+"-")
	n, err := strconv.Atoi(suffix)
	return err == nil && n >= 2
}

func (l *Loop) reconcileDisabledMarker(ctx context.Context, storeDir string, marker checkresult.DisabledMarker) error {
	marker.CheckID = strings.TrimSpace(marker.CheckID)
	if marker.CheckID == "" {
		return nil
	}
	if marker.Episode == "" && marker.Fingerprint != "" && marker.PassAt != "" && marker.FailAt != "" {
		marker.Episode = checkresult.FormatEpisode(marker.Fingerprint, marker.PassAt, marker.FailAt)
	}
	fp, passAt, failAt, err := checkresult.ParseEpisode(marker.Episode)
	if err != nil {
		return fmt.Errorf("invalid episode %q: %w", marker.Episode, err)
	}
	if marker.Fingerprint == "" {
		marker.Fingerprint = fp
	}
	if marker.PassAt == "" {
		marker.PassAt = passAt
	}
	if marker.FailAt == "" {
		marker.FailAt = failAt
	}

	baseID := FlakyRepairBaseID(marker.CheckID)
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()

	action := ""
	repairItem := ""
	now := l.cfg.Now().UTC()
	episodeNeedle := "Disabling episode `" + marker.Episode + "`"
	checkNeedle := "Fix check `" + marker.CheckID + "`"

	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		declared, err := definition.ForReader(tx.Reader())
		if err != nil {
			return err
		}
		rows, err := tx.Reader().Query(
			`SELECT i.item_id, i.state, COALESCE((SELECT c.text FROM item_criteria c WHERE c.item_id = i.item_id AND c.criterion_id = ?), '')
			 FROM items i
			 WHERE i.item_id = ? OR i.item_id GLOB ?
			 ORDER BY i.item_id`,
			FlakyRepairCriterion, baseID, baseID+"-[0-9]*",
		)
		if err != nil {
			return err
		}
		var existing []existingFlakyRow
		usedIDs := map[string]bool{}
		for rows.Next() {
			var row existingFlakyRow
			if err := rows.Scan(&row.itemID, &row.state, &row.criterion); err != nil {
				rows.Close()
				return err
			}
			if isNumberedFlakyID(baseID, row.itemID) {
				usedIDs[row.itemID] = true
				existing = append(existing, row)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if marker.RepairItem != "" && !usedIDs[marker.RepairItem] {
			var row existingFlakyRow
			qErr := tx.Reader().QueryRow(
				`SELECT i.item_id, i.state, COALESCE((SELECT c.text FROM item_criteria c WHERE c.item_id = i.item_id AND c.criterion_id = ?), '')
				 FROM items i WHERE i.item_id = ?`,
				FlakyRepairCriterion, marker.RepairItem,
			).Scan(&row.itemID, &row.state, &row.criterion)
			if qErr == nil {
				existing = append(existing, row)
			}
		}

		var checkRows []existingFlakyRow
		for _, row := range existing {
			if row.itemID == marker.RepairItem || strings.Contains(row.criterion, checkNeedle) || row.criterion == "" {
				checkRows = append(checkRows, row)
			}
		}

		for _, row := range checkRows {
			if !finishedState(declared.Parsed, row.state) {
				repairItem = row.itemID
				action = "stamp"
				return nil
			}
		}
		for _, row := range checkRows {
			if row.state == StateLanded && (strings.Contains(row.criterion, episodeNeedle) || row.itemID == marker.RepairItem) {
				repairItem = row.itemID
				action = "enable"
				return nil
			}
		}

		nextID := baseID
		for n := 2; usedIDs[nextID]; n++ {
			nextID = fmt.Sprintf("%s-%d", baseID, n)
		}
		payload, err := json.Marshal(map[string]any{
			"item_id":       nextID,
			"title":         FlakyRepairTitle(marker.CheckID),
			"state":         StateInQueue,
			"seat":          TrunkFixerSeat,
			"blocked_by":    []string{},
			"allowed_paths": []string{},
			"rank":          "",
			"criteria": []map[string]string{{
				"criterion_id": FlakyRepairCriterion,
				"text":         FlakyRepairCriterionText(marker),
				"state":        "not-done",
			}},
		})
		if err != nil {
			return err
		}
		epHash := checkMarkerHash(marker.Episode)
		env := record.Envelope{
			RecordVersion:  record.SupportedVersion,
			Kind:           item.RecordKind,
			ID:             "flaky-repair-" + nextID + "-" + epHash[:12],
			Producer:       record.Producer{System: record.System, Utility: Utility, Version: Version},
			CreatedAt:      now,
			Subject:        &record.Subject{Kind: "item", ID: nextID},
			IdempotencyKey: "drain:flaky-repair:" + nextID + ":" + epHash[:16],
			Payload:        payload,
		}
		if _, err := item.Apply(ctx, tx, env); err != nil {
			return err
		}
		if _, err := item.Place(ctx, tx, nextID, item.Destination{To: "last"}, now, Utility, "disabled-marker"); err != nil {
			return err
		}
		repairItem = nextID
		action = "filed"
		return nil
	})
	if err != nil {
		return err
	}

	switch action {
	case "filed":
		_ = stampDisabledMarkerRepairItem(storeDir, marker.CheckID, marker.Episode, repairItem)
		l.reportf("loop: filed %s to re-enable disabled check %s (episode %s)\n", repairItem, marker.CheckID, marker.Episode)
	case "stamp":
		if marker.RepairItem != repairItem {
			_ = stampDisabledMarkerRepairItem(storeDir, marker.CheckID, marker.Episode, repairItem)
		}
	case "enable":
		if err := l.enableDisabledEpisode(ctx, marker.CheckID, marker.Episode, storeDir); err != nil {
			return err
		}
		l.reportf("loop: re-enabled check %s after landed repair %s (episode %s)\n", marker.CheckID, repairItem, marker.Episode)
	}
	return nil
}

func (l *Loop) enableDisabledEpisode(ctx context.Context, checkID, episode, storeDir string) error {
	if step, ok := l.cfg.Steps.(EnableStep); ok {
		return step.EnableCheck(ctx, checkID, episode, storeDir)
	}
	steps := &ProcessSteps{Root: l.cfg.Root}
	return steps.EnableCheck(ctx, checkID, episode, storeDir)
}

func checkMarkerHash(checkID string) string {
	sum := sha256.Sum256([]byte(checkID))
	return hex.EncodeToString(sum[:])
}

func stampDisabledMarkerRepairItem(storeDir, checkID, episode, repairItem string) error {
	hash := checkMarkerHash(checkID)
	lockDir := filepath.Join(storeDir, "locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(filepath.Join(lockDir, hash), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) }()

	markerPath := filepath.Join(storeDir, "disabled", hash+".json")
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		return err
	}
	var current checkresult.DisabledMarker
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	curEpisode := current.Episode
	if curEpisode == "" && current.Fingerprint != "" && current.PassAt != "" && current.FailAt != "" {
		curEpisode = checkresult.FormatEpisode(current.Fingerprint, current.PassAt, current.FailAt)
	}
	if curEpisode != episode || current.RepairItem == repairItem {
		return nil
	}
	current.RepairItem = repairItem
	encoded, err := json.Marshal(current)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(markerPath), ".tmp-repair-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(encoded); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, markerPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
