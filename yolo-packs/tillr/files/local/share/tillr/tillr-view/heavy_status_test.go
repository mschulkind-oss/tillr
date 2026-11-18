package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestStatusPrintsGreenRangeAttributionUnavailableAndOverruns(t *testing.T) {
	parent := os.Getenv("YOLO_DURABLE_DIR")
	if parent == "" {
		parent = filepath.Join("..", "..", ".yolo", "durable")
	}
	root, err := os.MkdirTemp(parent, "view-heavy-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	commit := func(value string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "value"), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
		git("add", "value")
		git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", value)
		return git("rev-parse", "HEAD")
	}
	git("init", "-q", "-b", "main")
	green := commit("good")
	red := commit("bad")
	tip := commit("worse")
	git("update-ref", "refs/tillr/green", green)
	dir := filepath.Join(root, ".git", "tillr-gate")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"base_sha": green, "tip_sha": tip, "result": "red", "checks": []map[string]any{{"check_id": "unit", "result": "fail", "first_red_landing": red}, {"check_id": "slow", "result": "pass", "duration_ms": 125, "budget_ms": 100, "over_budget_ms": 25}, {"check_id": "external", "result": "unavailable"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	proved := time.Now().UTC().Add(-2 * time.Hour)
	proofRaw, _ := json.Marshal(map[string]any{"commit": green, "proved_at": proved})
	if err := os.WriteFile(filepath.Join(dir, "green.json"), proofRaw, 0644); err != nil {
		t.Fatal(err)
	}
	path := fixture(t)
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS landings(trunk_after TEXT, result TEXT)`); err != nil {
			return err
		}
		for _, sha := range []string{red, tip} {
			if _, err := tx.Exec(`INSERT INTO landings(trunk_after,result) VALUES(?,'landed')`, sha); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	code, out := runView(t, "status", "--store", path, "--root", root)
	if code != 0 {
		t.Fatalf("status %d: %s", code, out)
	}
	var env struct {
		Payload struct {
			Heavy struct {
				GreenCommit   string    `json:"green_commit"`
				GreenProvedAt time.Time `json:"green_proved_at"`
				LandingsSince int       `json:"landings_since"`
				FailingChecks []struct {
					CheckID         string `json:"check_id"`
					FirstRedLanding string `json:"first_red_landing"`
				} `json:"failing_checks"`
				Unavailable []struct {
					CheckID string `json:"check_id"`
				} `json:"unavailable_checks"`
				Overruns []struct {
					DurationMS int64 `json:"duration_ms"`
					BudgetMS   int64 `json:"budget_ms"`
				} `json:"overruns"`
			} `json:"heavy"`
			Fault string `json:"heavy_fault"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	code, waiting := runView(t, "waiting", "--history", "--json", "--store", path, "--root", root)
	if code != 0 || !strings.Contains(string(waiting), green[:12]) || !strings.Contains(string(waiting), "ago") {
		t.Fatalf("waiting %d: %s", code, waiting)
	}
	heavy := env.Payload.Heavy
	if len(heavy.Overruns) != 1 || heavy.Overruns[0].DurationMS != 125 || heavy.Overruns[0].BudgetMS != 100 {
		t.Fatalf("overruns=%s", out)
	}
	if env.Payload.Fault != "" || heavy.GreenCommit != green || !heavy.GreenProvedAt.Equal(proved) || heavy.LandingsSince != 2 || len(heavy.FailingChecks) != 1 || heavy.FailingChecks[0].FirstRedLanding != red || len(heavy.Unavailable) != 1 || heavy.Unavailable[0].CheckID != "external" || len(heavy.Overruns) != 1 {
		t.Fatalf("status=%s", out)
	}
}
