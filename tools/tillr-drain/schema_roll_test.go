package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// Build real replacement binaries with a higher schema version. The Python
// fixture supplies those builds to the real roller; it does not fake --check,
// installation, the restart request, migration, or worker completion.
func schemaRollFixture(t *testing.T, f loopFixture, live string) string {
	t.Helper()
	dir := filepath.Dir(live)
	schema, err := filepath.Abs("../tillr/store/schema.go")
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(schema)
	if err != nil {
		t.Fatal(err)
	}
	old := fmt.Sprintf("const SchemaVersion = %d", store.SchemaVersion)
	next := fmt.Sprintf("const SchemaVersion = %d", store.SchemaVersion+1)
	if !strings.Contains(string(original), old) {
		t.Fatalf("missing schema declaration %q", old)
	}
	raised := filepath.Join(dir, "schema.go.new")
	source := strings.Replace(string(original), old, next, 1)
	source = strings.Replace(source, "CREATE TABLE IF NOT EXISTS schema_migrations", `CREATE TABLE IF NOT EXISTS fixture_roll_upgrade (id INTEGER);
CREATE TABLE IF NOT EXISTS schema_migrations`, 1)
	writeFile(t, raised, source)
	overlay := filepath.Join(dir, "overlay.json")
	data, err := json.Marshal(map[string]any{"Replace": map[string]string{schema: raised}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, overlay, string(data))
	for _, module := range []string{"drain", "run", "item"} {
		cmd := exec.Command("go", "build", "-overlay", overlay, "-o", filepath.Join(dir, "next-"+module), ".")
		cmd.Dir = "../tillr-" + module
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building version-raising %s: %v: %s", module, err, out)
		}
	}
	roller, err := filepath.Abs("../tillr-roll/tillr-roll.py")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCHEMA_ROLL_SCRIPT", roller)
	t.Setenv("SCHEMA_ROLL_STORE", f.store)
	t.Setenv("SCHEMA_ROLL_POLICY", string(policy))
	fixture := filepath.Join(dir, "roll-fixture.py")
	writeFile(t, fixture, `import importlib.util
import os
from pathlib import Path
import shutil
import sqlite3
from contextlib import closing

spec = importlib.util.spec_from_file_location('roll', os.environ['SCHEMA_ROLL_SCRIPT'])
roll = importlib.util.module_from_spec(spec)
spec.loader.exec_module(roll)
root = Path.cwd()
live = Path(__file__).parent
store = Path(os.environ['SCHEMA_ROLL_STORE'])
real_run = roll.run
with closing(sqlite3.connect(store.as_uri() + '?mode=ro', uri=True)) as db:
    before = db.execute('SELECT max(version) FROM schema_migrations').fetchone()[0]
old_item = (live / 'tillr-item').stat().st_ino

def checkout(root, tip, dest, shared):
    for module in roll.MODULES:
        (dest / 'tools' / ('tillr-' + module)).mkdir(parents=True)
    adapter = dest / 'tools/tillr-loop/tillr-loop'
    adapter.parent.mkdir()
    adapter.write_text('# fixture step program\n')
    policy = dest / 'config/tillr/backend-policy.json'
    policy.parent.mkdir(parents=True)
    policy.write_text(os.environ['SCHEMA_ROLL_POLICY'])

def run(*args, **kwargs):
    if args[:2] == ('go', 'build'):
        target = args[args.index('-o') + 1]
        built = live / ('next-' + target.name.removeprefix('tillr-'))
        if built.is_file():
            shutil.copy2(built, target)
        else:
            target.write_text('#!/bin/sh\nexit 0\n')
            target.chmod(0o755)
        return None
    return real_run(*args, **kwargs)

roll.clean_checkout = checkout
roll.test_commit = lambda *args: None
roll.run = run
try:
    rc = roll.roll(root, live, True)
except roll.RollFailed as exc:
    raise SystemExit(str(exc))
with closing(sqlite3.connect(store.as_uri() + '?mode=ro', uri=True)) as db:
    assert db.execute('SELECT max(version) FROM schema_migrations').fetchone()[0] == before
    assert db.execute("SELECT 1 FROM sqlite_master WHERE name='fixture_roll_upgrade'").fetchone() is None
assert (live / 'tillr-item').stat().st_ino == old_item
raise SystemExit(rc)
`)
	t.Setenv("SCHEMA_ROLL_FIXTURE", fixture)
	return `python3 -B "$SCHEMA_ROLL_FIXTURE"`
}
