package drain

import (
	"context"
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

func piBuildsDir(root string) string {
	if dir, ok := os.LookupEnv("TILLR_PI_BUILDS_DIR"); ok {
		return dir
	}
	if durable := os.Getenv("YOLO_DURABLE_DIR"); durable != "" {
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

func wantedPiKey(ctx context.Context, root string, fromPatchRef bool, tip string) string {
	if ref := optionalCommit(ctx, root, piForkPatchRef); len(ref) >= 12 {
		return ref[:12]
	}
	if fromPatchRef && len(tip) >= 12 {
		return tip[:12]
	}
	return ""
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
