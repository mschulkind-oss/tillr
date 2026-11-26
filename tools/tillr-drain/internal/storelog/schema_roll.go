package storelog

// Architecture and invariants: docs/reference/tillr-restart.md

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/restart"
)

// SchemaRollCheckLine is the staged loop's signal to the repository roller and
// the old loop that companions must wait for the schema handoff.
const SchemaRollCheckLine = "tillr-drain: schema migration required"

// SchemaRollDir returns the companion staging directory for the running image.
// The roller exposes the new drain first; its siblings stay here until this
// image owns the loop and has migrated the store. The running image's digest,
// not the current contents of the live executable, selects the staged build.
func SchemaRollDir(executable, digest string) string {
	return filepath.Join(filepath.Dir(executable), ".tillr-schema-roll-"+digest)
}

// SchemaRoll is the metadata written to roll.json in a staged schema roll directory.
type SchemaRoll struct {
	Commit string `json:"commit"`
	Store  string `json:"store"`
}

// ReadSchemaRoll reads roll.json from a staged schema roll directory.
func ReadSchemaRoll(dir string) (SchemaRoll, error) {
	var roll SchemaRoll
	raw, err := os.ReadFile(filepath.Join(dir, "roll.json"))
	if err != nil {
		return roll, err
	}
	err = json.Unmarshal(raw, &roll)
	return roll, err
}

// SchemaRollActivation supplies the narrow callbacks and paths needed to
// activate a staged schema roll.
type SchemaRollActivation struct {
	Dir            string
	StorePath      string
	Executable     string
	BuildString    string
	Now            func() time.Time
	PassInterval   time.Duration
	RefreshSession func(ctx context.Context, next time.Time) error
	Restarter      func() *restart.Restarter
	Reportf        func(format string, args ...any)
}

// ActivateSchemaRoll promotes staged companion binaries after the store migration
// succeeds, files a view restart request, records the installed commit, and removes
// the staging directory.
func ActivateSchemaRoll(ctx context.Context, p SchemaRollActivation) (resultErr error) {
	if p.Dir == "" {
		return nil
	}
	roll, err := ReadSchemaRoll(p.Dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading staged schema roll: %w", err)
	}
	now := p.Now
	if now == nil {
		now = time.Now
	}
	step := "store migration"
	defer func() {
		if resultErr == nil || ctx.Err() != nil {
			return
		}
		AlarmStoreRoll(ctx, p.StorePath, roll.Commit, step, resultErr, now(), p.Reportf)
		if id := os.Getenv(restart.RequestEnv); id != "" && p.Restarter != nil {
			if err := p.Restarter().Record(ctx, restart.Answer{
				RequestID: id, Answer: restart.AnswerRefused, FromBuild: os.Getenv(restart.FromEnv),
				ToBuild: p.BuildString, Detail: resultErr.Error(),
			}); err != nil && p.Reportf != nil {
				p.Reportf("loop: could not record the schema roll refusal: %v\n", err)
			}
		}
	}()
	path, err := filepath.Abs(p.StorePath)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil || path != roll.Store {
		return fmt.Errorf("staged schema roll for %s cannot activate on %s", roll.Store, p.StorePath)
	}
	if p.RefreshSession != nil {
		if err := p.RefreshSession(ctx, now().Add(p.PassInterval)); err != nil {
			return err
		}
	}
	step = "schema roll install"
	live := filepath.Dir(p.Executable)
	for _, program := range []string{"tillr", "tillr-gate", "tillr-item", "tillr-land", "tillr-run", "tillr-store", "tillr-view", "tillr-watch"} {
		// Each rename is atomic. A keeper restart can resume a partial
		// activation: a missing source has already been promoted.
		if err := os.Rename(filepath.Join(p.Dir, program), filepath.Join(live, program)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("activating staged %s: %w", program, err)
		}
	}
	step = "view restart request"
	if _, err := restart.File(ctx, p.StorePath, restart.RoleView, now()); err != nil {
		return err
	}
	step = "schema roll completion"
	if err := os.WriteFile(filepath.Join(filepath.Dir(live), "tillr-roll.commit"), []byte(roll.Commit+"\n"), 0o644); err != nil {
		return fmt.Errorf("recording installed schema roll: %w", err)
	}
	return os.RemoveAll(p.Dir)
}

// AlarmStoreRoll records a store-roll-blocked alarm or reports failure to record it.
func AlarmStoreRoll(ctx context.Context, storePath, commit, step string, cause error, now time.Time, reportf func(string, ...any)) {
	if err := alarms.RecordStoreRollFailure(ctx, storePath, commit, step, cause, now); err != nil && reportf != nil {
		short := commit
		if len(short) > 12 {
			short = short[:12]
		}
		reportf("loop: ALARM store-roll-blocked at %s: %s: %v; mover: operator; could not record alarm: %v\n", short, step, cause, err)
	}
}
