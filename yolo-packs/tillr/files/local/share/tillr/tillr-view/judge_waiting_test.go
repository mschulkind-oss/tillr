package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWaitingClassifiesJudgeDispositions(t *testing.T) {
	for _, reason := range []string{"judge-blocked", "judge-defective"} {
		rule := waitingRuleFor(reason)
		if rule.class == "unclassified" || rule.where != "judge" || rule.mover == "" {
			t.Errorf("judge outcome %s: %+v", reason, rule)
		}
	}
	absent := waitingRuleFor("judge-grant-absent")
	if absent.class != "needs you" || !strings.Contains(absent.mover, "tillr item grant issue") {
		t.Errorf("judge-grant-absent rule = %+v", absent)
	}
}

func TestWaitingShowsJudgeReason(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`DELETE FROM dispatches`,
		`DELETE FROM landing_attempts`,
		`DELETE FROM candidates`,
		`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES('review-run','item-seed-02','lane-a','pi','exited',8000,8500,'[]',100)`,
		`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES('judge-hold','item-seed-02','hold','eval-failed',9000)`,
		`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(8500,'drain.refusal','run','review-run','{"item_id":"item-seed-02","step":"eval-failed","detail":"criterion c1 still lacks evidence"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("status %d %s", status, raw)
	}
	rows := body["data"].([]any)
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["item_id"] == "item-seed-02" {
			if row["why"] != "eval-failed: criterion c1 still lacks evidence" || row["where"] != "judge" {
				t.Errorf("judge hold %v", row)
			}
			return
		}
	}
	t.Fatal("judge hold missing")
}

func TestViewWarnsBeforeGrantExpiresAndShowsExpiredGrantHold(t *testing.T) {
	dir := plannerFixtureDir(t)
	gitRoot(t, dir)
	viewBin, drainBin := buildPairInto(t, dir)

	policyPath := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"version":1,"backends":{"pi":{"adapter":"pi","model":"m"}},"seats":{"build":["pi"],"review":["pi"]},"default_backend":"pi"}`), 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "pass.json")
	if err := os.WriteFile(input, []byte(`{"record_version":1,"kind":"drain.pass","id":"fixture","producer":{"system":"tillr","utility":"drain","version":"0.1.0"},"created_at":"2026-11-18T23:08:00Z","payload":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(dir, "state.db")
	if out, err := exec.Command(drainBin, "once", "--store", storePath, "--input", input, "--runner", "record-sink", "--runner-output", filepath.Join(dir, "sink.json")).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}

	issued := time.Date(2026, 11, 18, 23, 8, 0, 0, time.UTC)
	expires := issued.Add(24 * time.Hour) // 2026-11-19T23:08:00Z
	expText := expires.Format(time.RFC3339)
	renewCmd := "tillr item grant issue --store " + storePath + " --input <record> --now <RFC3339>"

	db, err := sql.Open("sqlite", "file:"+storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS item_grants (grant_id TEXT PRIMARY KEY, claim_classes TEXT NOT NULL, grantor TEXT NOT NULL, issued_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS item_criteria (item_id TEXT NOT NULL, criterion_id TEXT NOT NULL, text TEXT NOT NULL, state TEXT NOT NULL, claim_class TEXT NOT NULL)`,
		`INSERT INTO items(item_id,title,state,rank,judge_mode,allowed_paths,updated_at) VALUES ('item-verif','needs agent review','in-queue','a','agent','["tools/a/"]',1000)`,
		`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES ('item-verif','c1','verify behavior','','verification-only')`,
		`INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES ('grant-24h','verification-only','owner',?,?)`, issued.UnixMilli(), expires.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// 1. Two hours before expiry: expiry time is shown in waiting, status, and running, with no warning.
	twoHoursBefore := expires.Add(-2 * time.Hour).Format(time.RFC3339)
	for _, cmd := range [][]string{
		{viewBin, "waiting", "--store", storePath, "--root", dir, "--policy", policyPath, "--tillr-drain", drainBin, "--now", twoHoursBefore},
		{viewBin, "status", "--store", storePath, "--root", dir, "--text", "--now", twoHoursBefore},
		{viewBin, "running", "--store", storePath, "--now", twoHoursBefore},
	} {
		out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", cmd[1:], err, out)
		}
		text := string(out)
		if !strings.Contains(text, "grant: grant-24h (verification-only) expires at "+expText) {
			t.Errorf("%s at -2h missing expiry line:\n%s", cmd[1], text)
		}
		if strings.Contains(text, "warning: grant") {
			t.Errorf("%s at -2h warned early:\n%s", cmd[1], text)
		}
	}

	// 2. From one hour before expiry (-60m and -30m): warning appears across waiting, status, and running.
	for _, d := range []time.Duration{time.Hour, 30 * time.Minute} {
		at := expires.Add(-d).Format(time.RFC3339)
		for _, cmd := range [][]string{
			{viewBin, "waiting", "--store", storePath, "--root", dir, "--policy", policyPath, "--tillr-drain", drainBin, "--now", at},
			{viewBin, "status", "--store", storePath, "--root", dir, "--text", "--now", at},
			{viewBin, "running", "--store", storePath, "--now", at},
		} {
			out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %v\n%s", cmd[1:], err, out)
			}
			text := string(out)
			for _, want := range []string{"warning: grant grant-24h", expText, renewCmd} {
				if !strings.Contains(text, want) {
					t.Errorf("%s at -%s missing %q:\n%s", cmd[1], d, want, text)
				}
			}
		}
	}

	// 3. After expiry (+5m): item waiting on the expired grant is listed as held (not running),
	// and waiting names the grant, when it expired, and the renewal command.
	after := expires.Add(5 * time.Minute).Format(time.RFC3339)
	whyOut, err := exec.Command(drainBin, "why", "--store", storePath, "--root", dir, "--policy", policyPath, "--now", after, "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("drain why: %v\n%s", err, whyOut)
	}
	var whyDoc struct {
		Summary struct {
			Running int            `json:"running"`
			Held    map[string]int `json:"held"`
		} `json:"summary"`
		Rows []struct {
			ItemID   string `json:"item_id"`
			Position string `json:"position"`
			Class    string `json:"class"`
			Why      string `json:"why"`
			Mover    string `json:"mover"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(whyOut, &whyDoc); err != nil {
		t.Fatal(err)
	}
	if whyDoc.Summary.Running != 0 || whyDoc.Summary.Held["judge-grant-absent"] != 1 {
		t.Fatalf("why summary = %+v", whyDoc.Summary)
	}
	if len(whyDoc.Rows) != 1 || whyDoc.Rows[0].Position != "held" || whyDoc.Rows[0].Class != "needs you" {
		t.Fatalf("why rows = %+v", whyDoc.Rows)
	}

	waitOut, err := exec.Command(viewBin, "waiting", "--store", storePath, "--root", dir, "--policy", policyPath, "--tillr-drain", drainBin, "--now", after).CombinedOutput()
	if err != nil {
		t.Fatalf("waiting after expiry: %v\n%s", err, waitOut)
	}
	waitText := string(waitOut)
	for _, want := range []string{"item-verif", "needs you", "grant-24h", "expired at " + expText, renewCmd} {
		if !strings.Contains(waitText, want) {
			t.Errorf("waiting output after expiry missing %q:\n%s", want, waitText)
		}
	}
}

func TestServeSnapshotAndWaitingGrantLifecycle(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	issued := time.Date(2026, 11, 18, 23, 8, 0, 0, time.UTC)
	expires := issued.Add(24 * time.Hour)
	expText := expires.Format(time.RFC3339)

	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS item_grants (grant_id TEXT PRIMARY KEY, claim_classes TEXT NOT NULL, grantor TEXT NOT NULL, issued_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`,
		`ALTER TABLE item_criteria ADD COLUMN claim_class TEXT NOT NULL DEFAULT ''`,
		`DELETE FROM dispatches WHERE item_id='item-seed-02'`,
		`DELETE FROM item_criteria WHERE item_id='item-seed-02'`,
		`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES ('item-seed-02','c1','verify seed','','verification-only')`,
		`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('grant-hold','item-seed-02','hold','judge-grant-absent',9000)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES ('grant-seed','verification-only','owner',?,?)`, issued.UnixMilli(), expires.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// Within 1h before expiry: snapshot marks warning=true and waiting includes grant:grant-seed observation.
	warnNow := expires.Add(-45 * time.Minute).Format(time.RFC3339)
	status, snapBody, raw := h.get("/api/v1/snapshot?now=" + warnNow)
	if status != 200 {
		t.Fatalf("snapshot %d: %s", status, raw)
	}
	grants := snapBody["data"].(map[string]any)["grants"].([]any)
	if len(grants) != 1 || grants[0].(map[string]any)["state"] != "expiring" || grants[0].(map[string]any)["warning"] != true {
		t.Fatalf("snapshot grants at -45m = %+v", grants)
	}
	status, waitBody, raw := h.get("/api/v1/waiting?now=" + warnNow)
	if status != 200 {
		t.Fatalf("waiting %d: %s", status, raw)
	}
	sawWarn := false
	for _, r := range waitBody["data"].([]any) {
		row := r.(map[string]any)
		if row["observation"] == "grant:grant-seed" {
			sawWarn = true
			if row["class"] != "needs you" || !strings.Contains(row["why"].(string), expText) {
				t.Errorf("grant warning row = %+v", row)
			}
		}
	}
	if !sawWarn {
		t.Fatalf("missing grant:grant-seed warning row at -45m: %s", raw)
	}

	// After expiry: item-seed-02 waiting row names grant-seed, when it expired, and tillr item grant issue.
	afterNow := expires.Add(5 * time.Minute).Format(time.RFC3339)
	status, waitAfter, raw := h.get("/api/v1/waiting?now=" + afterNow)
	if status != 200 {
		t.Fatalf("waiting after %d: %s", status, raw)
	}
	foundItem := false
	for _, r := range waitAfter["data"].([]any) {
		row := r.(map[string]any)
		if row["item_id"] == "item-seed-02" {
			foundItem = true
			for _, field := range []string{"why", "mover"} {
				val, _ := row[field].(string)
				for _, want := range []string{"grant-seed", expText, "tillr item grant issue --store " + h.store} {
					if !strings.Contains(val, want) {
						t.Errorf("after expiry %s = %q, missing %q", field, val, want)
					}
				}
			}
		}
	}
	if !foundItem {
		t.Fatalf("missing item-seed-02 after expiry: %s", raw)
	}
}
