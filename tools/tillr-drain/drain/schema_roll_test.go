package drain_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr-roll/roll"
	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

func TestSchemaRollHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SCHEMA_ROLL_HELPER") != "1" {
		return
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	live := os.Getenv("SCHEMA_ROLL_LIVE")
	storePath := os.Getenv("SCHEMA_ROLL_STORE")
	policyJSON := os.Getenv("SCHEMA_ROLL_POLICY")

	db, err := sql.Open("sqlite", "file:"+storePath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var before int
	if err := db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&before); err != nil {
		t.Fatal(err)
	}
	oldItemInfo, err := os.Stat(filepath.Join(live, "tillr-item"))
	if err != nil {
		t.Fatal(err)
	}
	oldItemIno := oldItemInfo.Sys().(*syscall.Stat_t).Ino

	deps := &roll.Deps{
		CleanCheckout: func(rootDir, tip, dest, shared string) error {
			for _, module := range roll.Modules {
				if err := os.MkdirAll(filepath.Join(dest, "tools", "tillr-"+module), 0o755); err != nil {
					return err
				}
			}
			policy := filepath.Join(dest, "config", "tillr", "backend-policy.json")
			if err := os.MkdirAll(filepath.Dir(policy), 0o755); err != nil {
				return err
			}
			return os.WriteFile(policy, []byte(policyJSON), 0o644)
		},
		TestCommit: func(rootDir, source, work, tip, storeDir string, old map[string]roll.BuildFact, buildEnv []string) error {
			return nil
		},
		Run: func(args []string, opts roll.RunOpts) (roll.CmdResult, error) {
			if len(args) >= 2 && args[0] == "go" && args[1] == "build" {
				target := ""
				for i := 0; i+1 < len(args); i++ {
					if args[i] == "-o" {
						target = args[i+1]
						break
					}
				}
				if target == "" {
					return roll.CmdResult{}, fmt.Errorf("missing -o in go build args: %v", args)
				}
				built := filepath.Join(live, "next-"+strings.TrimPrefix(filepath.Base(target), "tillr-"))
				if data, err := os.ReadFile(built); err == nil {
					if err := os.WriteFile(target, data, 0o755); err != nil {
						return roll.CmdResult{}, err
					}
				} else {
					if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
						return roll.CmdResult{}, err
					}
				}
				return roll.CmdResult{}, nil
			}
			return roll.Run(args, opts, nil)
		},
	}

	rc, err := roll.Roll(root, live, true, false, "", deps)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var after int
	if err := db.QueryRow("SELECT max(version) FROM schema_migrations").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("schema_migrations max(version) = %d, want %d", after, before)
	}
	var one int
	if err := db.QueryRow("SELECT 1 FROM sqlite_master WHERE name='fixture_roll_upgrade'").Scan(&one); err != sql.ErrNoRows {
		t.Fatalf("expected no fixture_roll_upgrade table, got err=%v", err)
	}
	newItemInfo, err := os.Stat(filepath.Join(live, "tillr-item"))
	if err != nil {
		t.Fatal(err)
	}
	if newItemInfo.Sys().(*syscall.Stat_t).Ino != oldItemIno {
		t.Fatalf("tillr-item inode changed during staged schema roll")
	}
	os.Exit(rc)
}

// Build real replacement binaries with a higher schema version. The Go test
// helper supplies those builds to the real roller; it does not fake --check,
// installation, the restart request, migration, or worker completion.
func schemaRollFixture(t *testing.T, f loopFixture, live string) string {
	t.Helper()
	dir := filepath.Dir(live)
	schema, err := filepath.Abs("../../tillr/store/schema.go")
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
		cmd.Dir = "../../tillr-" + module
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("building version-raising %s: %v: %s", module, err, out)
		}
	}
	policy, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCHEMA_ROLL_STORE", f.store)
	t.Setenv("SCHEMA_ROLL_POLICY", string(policy))
	installRoot := filepath.Join(dir, "install")
	installedTemplate := filepath.Join(installRoot, "tillr-run", "backends", "brief-template.md")
	if err := os.MkdirAll(filepath.Dir(installedTemplate), 0755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "../../tillr-run/backends/brief-template.md", installedTemplate, nil)
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	t.Setenv("SCHEMA_ROLL_LIVE", dir)
	return fmt.Sprintf(`GO_WANT_SCHEMA_ROLL_HELPER=1 "%s" -test.run=^TestSchemaRollHelperProcess$`, exe)
}
