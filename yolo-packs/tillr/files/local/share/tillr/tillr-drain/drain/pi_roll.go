package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func belongsToEnclosingYoloDurable(root, durable string) bool {
	if root == "" || durable == "" {
		return false
	}
	cleanDurable := filepath.Clean(durable)
	if filepath.Base(cleanDurable) != "durable" || filepath.Base(filepath.Dir(cleanDurable)) != ".yolo" {
		return false
	}
	rel, err := filepath.Rel(cleanDurable, filepath.Clean(root))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func piBuildsDir(root string) string {
	if dir, ok := os.LookupEnv("TILLR_PI_BUILDS_DIR"); ok {
		return dir
	}
	if durable := os.Getenv("YOLO_DURABLE_DIR"); durable != "" && !belongsToEnclosingYoloDurable(root, durable) {
		return filepath.Join(durable, "pi-builds")
	}
	return filepath.Join(root, ".yolo", "durable", "pi-builds")
}

func piBuildStateObservable(buildsDir string) bool {
	if buildsDir == "" {
		return false
	}
	_, err := os.Stat(buildsDir)
	return err == nil
}

func readCurrentPiBuildKey(buildsDir string) string {
	if buildsDir == "" {
		return "none"
	}
	raw, err := os.ReadFile(filepath.Join(buildsDir, "current.json"))
	if err != nil {
		return "none"
	}
	var current struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &current); err != nil || strings.TrimSpace(current.Key) == "" {
		return "none"
	}
	return strings.TrimSpace(current.Key)
}

func hasVerifiedPiBuild(buildsDir, key string) bool {
	if buildsDir == "" || key == "" {
		return false
	}
	if info, err := os.Stat(filepath.Join(buildsDir, key, ".verified")); err != nil || info.IsDir() {
		return false
	}
	binInfo, err := os.Stat(filepath.Join(buildsDir, key, "home", ".local", "bin", "pi"))
	if err != nil || binInfo.IsDir() {
		return false
	}
	return binInfo.Mode()&0o111 != 0
}

func wantedPiTip(ctx context.Context, root string) string {
	if root == "" {
		return ""
	}
	if ref := optionalCommit(ctx, root, piForkPatchRef); len(ref) >= 12 {
		return ref
	}
	forkCache := filepath.Join(root, ".forks", "pi")
	if info, err := os.Stat(forkCache); err == nil && info.IsDir() {
		if ref := optionalCommit(ctx, forkCache, piForkPatchRef); len(ref) >= 12 {
			return ref
		}
	}
	return ""
}

func wantedPiKey(ctx context.Context, root string, fromPatchRef bool, tip string) string {
	if resolved := wantedPiTip(ctx, root); len(resolved) >= 12 {
		return resolved[:12]
	}
	if fromPatchRef && len(tip) >= 12 {
		return tip[:12]
	}
	return ""
}

func readStandingPiBuildStale(ctx context.Context, storePath string) (staleKey, wantedKey string, standing bool, err error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", "", false, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		err := r.QueryRow(`SELECT
 coalesce(json_extract(payload,'$.stale_key'), ''),
 coalesce(json_extract(payload,'$.wanted_key'), '')
FROM events
WHERE kind = ?
 AND seq > COALESCE((SELECT seq FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1), 0)
ORDER BY seq DESC LIMIT 1`, alarms.PiBuildStaleKind, alarms.PiBuildCurrentKind).Scan(&staleKey, &wantedKey)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		standing = true
		return nil
	})
	if err != nil {
		return "", "", false, err
	}
	if standing && staleKey == "" {
		staleKey = "none"
	}
	return staleKey, wantedKey, standing, nil
}

// piRollInFlight reports whether a patch/pi landing that will start a roll is
// in progress or the loop's own pi roll worker is running.
func (l *Loop) piRollInFlight() bool {
	return l.piRollChecking.Load() > 0 || l.piRollAction.busy()
}

func (l *Loop) checkPiBuild(ctx context.Context) {
	if ctx.Err() != nil || l.nonLanding || l.cfg.Store == "" || l.piRollInFlight() {
		return
	}
	buildsDir := piBuildsDir(l.cfg.Root)
	if !piBuildStateObservable(buildsDir) {
		return
	}
	tip := wantedPiTip(ctx, l.cfg.Root)
	if len(tip) < 12 {
		return
	}
	wantedKey := tip[:12]
	verified := hasVerifiedPiBuild(buildsDir, wantedKey)
	recordedStale, recordedWanted, standing, err := readStandingPiBuildStale(ctx, l.cfg.Store)
	if err != nil {
		l.reportf("loop: could not read pi build alarm state: %v\n", err)
		return
	}
	if verified {
		if standing {
			l.reportf("loop: verified pi build found for key %s\n", wantedKey)
			l.recordPiBuildCurrent(ctx, tip, wantedKey)
		}
		return
	}
	staleKey := readCurrentPiBuildKey(buildsDir)
	if standing && recordedWanted == wantedKey && recordedStale == staleKey {
		return
	}
	if l.piRollInFlight() || hasVerifiedPiBuild(buildsDir, wantedKey) {
		return
	}
	detail := fmt.Sprintf("no verified build for %s", piForkPatchRef)
	l.reportf("loop: pi build is stale (active key %s, wanted key %s): %s\n", staleKey, wantedKey, detail)
	l.recordPiBuildStale(ctx, tip, staleKey, wantedKey, "unbuilt", detail)
}

func (l *Loop) recordSkippedPiRoll(ctx context.Context, tip, detail string) {
	wantedKey := wantedPiKey(ctx, l.cfg.Root, true, tip)
	if wantedKey == "" {
		return
	}
	buildsDir := piBuildsDir(l.cfg.Root)
	if hasVerifiedPiBuild(buildsDir, wantedKey) {
		return
	}
	staleKey := readCurrentPiBuildKey(buildsDir)
	l.reportf("loop: post-landing pi roll at %s skipped (stale key %s, wanted key %s): %s\n", shortSHA(tip), staleKey, wantedKey, detail)
	l.recordPiBuildStale(ctx, tip, staleKey, wantedKey, "skipped", detail)
	l.recordActionWhat(ctx, "roll", "pi", tip, fmt.Errorf("skipped: %s", detail))
}

func (l *Loop) submitPiRoll(ctx context.Context, command, tip string, fromPatchRef bool) {
	l.piRollAction.submit(tip, func(tip string) {
		_, err := actionCommand(ctx, l.cfg.Root, "sh", "-c", command+" --pi")
		wantedKey := wantedPiKey(ctx, l.cfg.Root, fromPatchRef, tip)
		buildsDir := piBuildsDir(l.cfg.Root)
		staleKey := readCurrentPiBuildKey(buildsDir)
		if err != nil {
			detail := strings.Join(strings.Fields(err.Error()), " ")
			if wantedKey != "" {
				l.reportf("loop: post-landing pi roll at %s failed (stale key %s, wanted key %s): %s\n", shortSHA(tip), staleKey, wantedKey, detail)
				l.recordPiBuildStale(ctx, tip, staleKey, wantedKey, "failed", detail)
			} else {
				l.reportRollFailure(tip, "pi", err)
			}
			l.recordActionWhat(ctx, "roll", "pi", tip, err)
			return
		}
		if wantedKey != "" && piBuildStateObservable(buildsDir) && !hasVerifiedPiBuild(buildsDir, wantedKey) {
			detail := fmt.Sprintf("roll command exited 0 without producing verified build for %s", wantedKey)
			l.reportf("loop: post-landing pi roll at %s skipped (stale key %s, wanted key %s): %s\n", shortSHA(tip), staleKey, wantedKey, detail)
			l.recordPiBuildStale(ctx, tip, staleKey, wantedKey, "skipped", detail)
			l.recordActionWhat(ctx, "roll", "pi", tip, errors.New(detail))
			return
		}
		if wantedKey != "" {
			l.reportf("loop: post-landing pi roll at %s built key %s\n", shortSHA(tip), wantedKey)
			l.recordPiBuildCurrent(ctx, tip, wantedKey)
		}
		l.recordActionWhat(ctx, "roll", "pi", tip, nil)
	})
}

func (l *Loop) recordPiBuildStale(ctx context.Context, tip, staleKey, wantedKey, reason, detail string) {
	l.appendPiBuildEvent(ctx, alarms.PiBuildStaleKind, tip, wantedKey, map[string]any{
		"stale_key":  staleKey,
		"wanted_key": wantedKey,
		"tip":        tip,
		"reason":     reason,
		"detail":     detail,
	})
}

func (l *Loop) recordPiBuildCurrent(ctx context.Context, tip, key string) {
	l.appendPiBuildEvent(ctx, alarms.PiBuildCurrentKind, tip, key, map[string]any{
		"key": key,
		"tip": tip,
	})
}

func (l *Loop) appendPiBuildEvent(ctx context.Context, kind, tip, fallbackID string, payload map[string]any) {
	if l.cfg.Store == "" {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		l.reportf("loop: could not encode %s event: %v\n", kind, err)
		return
	}
	now := time.Now().UTC()
	if l.cfg.Now != nil {
		now = l.cfg.Now().UTC()
	}
	id := tip
	if id == "" {
		id = fallbackID
	}
	var objectID *string
	if id != "" {
		objectID = &id
	}
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		l.reportf("loop: could not open store to record %s: %v\n", kind, err)
		return
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       kind,
			ObjectType: "commit",
			ObjectID:   objectID,
			Payload:    body,
			At:         now,
		})
		return err
	}); err != nil {
		l.reportf("loop: could not record %s: %v\n", kind, err)
	}
}
