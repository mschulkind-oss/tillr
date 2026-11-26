package drain

// Architecture and invariants: docs/reference/tillr-restart.md

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/restart"
)

// SchemaRollCheckLine is the staged loop's signal to the repository roller and
// the old loop that companions must wait for the schema handoff.
const SchemaRollCheckLine = "tillr-drain: schema migration required"

// The roller exposes the new drain first; its siblings stay here until this
// image owns the loop and has migrated the store. The running image's digest,
// not the current contents of the live executable, selects the staged build.
func schemaRollDir(executable, digest string) string {
	return filepath.Join(filepath.Dir(executable), ".tillr-schema-roll-"+digest)
}

type schemaRoll struct {
	Commit string `json:"commit"`
	Store  string `json:"store"`
}

func readSchemaRoll(dir string) (schemaRoll, error) {
	var roll schemaRoll
	raw, err := os.ReadFile(filepath.Join(dir, "roll.json"))
	if err != nil {
		return roll, err
	}
	err = json.Unmarshal(raw, &roll)
	return roll, err
}

// StagedSchemaPrograms returns the companion directory for this image's
// startup checks. Resolution for ordinary steps continues to use live PATH.
func (l *Loop) StagedSchemaPrograms() string {
	if l.cfg.Restart == nil || l.build.Digest == "" {
		return ""
	}
	return schemaRollDir(l.build.Executable, l.build.Digest)
}

func (l *Loop) activateSchemaRoll(ctx context.Context) (resultErr error) {
	dir := l.StagedSchemaPrograms()
	if dir == "" {
		return nil
	}
	roll, err := readSchemaRoll(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading staged schema roll: %w", err)
	}
	step := "store migration"
	defer func() {
		if resultErr == nil || ctx.Err() != nil {
			return
		}
		l.alarmStoreRoll(ctx, roll.Commit, step, resultErr)
		if id := os.Getenv(restart.RequestEnv); id != "" {
			if err := l.restarter().Record(ctx, restart.Answer{
				RequestID: id, Answer: restart.AnswerRefused, FromBuild: os.Getenv(restart.FromEnv),
				ToBuild: l.build.String(), Detail: resultErr.Error(),
			}); err != nil {
				l.reportf("loop: could not record the schema roll refusal: %v\n", err)
			}
		}
	}()
	path, err := filepath.Abs(l.cfg.Store)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil || path != roll.Store {
		return fmt.Errorf("staged schema roll for %s cannot activate on %s", roll.Store, l.cfg.Store)
	}
	if err := l.refreshSession(ctx, l.cfg.Now().Add(PassInterval)); err != nil {
		return err
	}
	step = "schema roll install"
	live := filepath.Dir(l.build.Executable)
	for _, program := range []string{"tillr", "tillr-gate", "tillr-item", "tillr-land", "tillr-run", "tillr-store", "tillr-view", "tillr-watch"} {
		// Each rename is atomic. A keeper restart can resume a partial
		// activation: a missing source has already been promoted.
		if err := os.Rename(filepath.Join(dir, program), filepath.Join(live, program)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("activating staged %s: %w", program, err)
		}
	}
	step = "view restart request"
	if _, err := restart.File(ctx, l.cfg.Store, restart.RoleView, l.cfg.Now()); err != nil {
		return err
	}
	step = "schema roll completion"
	if err := os.WriteFile(filepath.Join(filepath.Dir(live), "tillr-roll.commit"), []byte(roll.Commit+"\n"), 0o644); err != nil {
		return fmt.Errorf("recording installed schema roll: %w", err)
	}
	return os.RemoveAll(dir)
}

func (l *Loop) alarmStoreRoll(ctx context.Context, commit, step string, cause error) {
	if err := alarms.RecordStoreRollFailure(ctx, l.cfg.Store, commit, step, cause, l.cfg.Now()); err != nil {
		l.reportf("loop: ALARM store-roll-blocked at %s: %s: %v; mover: operator; could not record alarm: %v\n", shortSHA(commit), step, cause, err)
	}
}
