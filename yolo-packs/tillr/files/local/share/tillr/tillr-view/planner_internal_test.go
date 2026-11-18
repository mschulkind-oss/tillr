package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript writes an executable shell script.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// decoyPlanner is a `tillr-drain` that records that it ran and answers a well-formed why
// with the given build JSON (or none). A decoy that is run when it must not be leaves its
// marker; a decoy whose answer is accepted when it must not be is a test that passes for
// the wrong reason, so it answers rows a real planner would never produce.
func decoyPlanner(t *testing.T, dir, build string) (path, marker string) {
	t.Helper()
	path = filepath.Join(dir, "tillr-drain")
	marker = filepath.Join(dir, "decoy-ran")
	answer := `{"event_head_seq":1,"rows":[{"item_id":"decoy-row","reason":"decoy"}],"summary":{"held":{}}`
	if build != "" {
		answer += `,"build":` + build
	}
	answer += "}"
	// Builtins only: the decoy must work under an empty PATH.
	writeScript(t, path, "echo ran >> "+marker+"\nprintf '%s\\n' '"+answer+"'\n")
	return path, marker
}

func TestPlannerResolutionPrefersTheSiblingOverPATH(t *testing.T) {
	bin := t.TempDir()
	exe := filepath.Join(bin, "tillr-view")
	writeScript(t, exe, "exit 0\n")
	writeScript(t, filepath.Join(bin, "tillr-drain"), "exit 0\n")
	decoyDir := t.TempDir()
	decoy, _ := decoyPlanner(t, decoyDir, "")
	onPath := func(string) (string, error) { return decoy, nil }

	got, err := resolvePlannerFrom("", exe, onPath)
	if err != nil || got.Path != filepath.Join(bin, "tillr-drain") || got.How != "beside" {
		t.Fatalf("a sibling beside the server must win over PATH: %+v %v", got, err)
	}

	// The pack launcher's cache names both programs by the same source digest.
	digest := strings.Repeat("ab", 32)
	cache := t.TempDir()
	launched := filepath.Join(cache, "tillr-view-"+digest)
	writeScript(t, launched, "exit 0\n")
	writeScript(t, filepath.Join(cache, "tillr-drain-"+digest), "exit 0\n")
	got, err = resolvePlannerFrom("", launched, onPath)
	if err != nil || got.Path != filepath.Join(cache, "tillr-drain-"+digest) || got.How != "beside" {
		t.Fatalf("the launcher's same-digest sibling: %+v %v", got, err)
	}

	got, err = resolvePlannerFrom(decoy, exe, onPath)
	if err != nil || got.Path != decoy || got.How != "flag" {
		t.Fatalf("an explicit --tillr-drain wins: %+v %v", got, err)
	}

	lone := filepath.Join(t.TempDir(), "tillr-view")
	got, err = resolvePlannerFrom("", lone, onPath)
	if err != nil || got.How != "PATH" {
		t.Fatalf("PATH is the last resort: %+v %v", got, err)
	}

	noPath := func(string) (string, error) { return "", exec.ErrNotFound }
	_, err = resolvePlannerFrom("", lone, noPath)
	var planner *plannerError
	if !errors.As(err, &planner) || planner.Kind != plannerUnavailable || !strings.Contains(err.Error(), "just observatory") {
		t.Fatalf("no planner anywhere names the fix: %v", err)
	}
	_, err = resolvePlannerFrom(filepath.Join(bin, "absent"), exe, onPath)
	if !errors.As(err, &planner) || !strings.Contains(err.Error(), "--tillr-drain") {
		t.Fatalf("a named planner that is not there: %v", err)
	}
}

func TestCheckPlannerBuild(t *testing.T) {
	program := plannerProgram{Path: "/x/tillr-drain", How: "beside"}
	digest := strings.Repeat("cd", 32)
	other := strings.Repeat("ef", 32)
	for _, tc := range []struct {
		name    string
		self    buildStamp
		planner *drainBuild
		refuse  string
	}{
		{"same revision", buildStamp{Revision: "abc"}, &drainBuild{Revision: "abc"}, ""},
		{"same dirty revision", buildStamp{Revision: "abc", Modified: true}, &drainBuild{Revision: "abc", Modified: true}, ""},
		{"another revision", buildStamp{Revision: "abc"}, &drainBuild{Revision: "def"}, "was built from def"},
		{"clean against dirty", buildStamp{Revision: "abc"}, &drainBuild{Revision: "abc", Modified: true}, "abc+dirty"},
		{"planner predates the stamp", buildStamp{Revision: "abc"}, nil, "names no build"},
		{"launcher digests agree", buildStamp{Executable: "/c/tillr-view-" + digest}, &drainBuild{Digest: digest, Executable: "/c/tillr-drain-" + digest}, ""},
		{"launcher digests differ", buildStamp{Executable: "/c/tillr-view-" + digest}, &drainBuild{Digest: other, Executable: "/c/tillr-drain-" + other}, "was built from"},
		{"checkout server, launched planner", buildStamp{Revision: "abc"}, &drainBuild{Digest: other, Executable: "/c/tillr-drain-" + other}, "was built from no revision"},
		{"unidentified server compares nothing", buildStamp{}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPlannerBuild(tc.self, tc.planner, program)
			if tc.refuse == "" {
				if err != nil {
					t.Fatalf("refused a matching planner: %v", err)
				}
				return
			}
			var planner *plannerError
			if !errors.As(err, &planner) || planner.Kind != plannerMismatch || !strings.Contains(err.Error(), tc.refuse) || !strings.Contains(err.Error(), "just observatory") {
				t.Fatalf("want a mismatch naming %q and the fix, got %v", tc.refuse, err)
			}
		})
	}
}

// whyFixture is a current-schema store, initialized by the real planner, holding two
// unlanded items and one lane: the TestWhyAPIAndWaitingBeforeAnyPass fixture.
// gitRoot makes dir a repository. The planner reads refs/tillr/green under --root, and
// git exits 128 (a fault, not an absent pointer) outside a repository.
func gitRoot(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
}

func whyFixture(t *testing.T, drainBin string) (dir, path string) {
	t.Helper()
	dir = plannerFixtureDir(t)
	gitRoot(t, dir)
	path = filepath.Join(dir, "state.db")
	input := filepath.Join(dir, "pass.json")
	raw := `{"record_version":1,"kind":"drain.pass","id":"fixture","producer":{"system":"tillr","utility":"drain","version":"0.1.0"},"created_at":"2026-11-16T00:00:00Z","payload":{}}`
	if err := os.WriteFile(input, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(drainBin, "once", "--store", path, "--input", input, "--runner", "record-sink", "--runner-output", filepath.Join(dir, "sink.json")).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO items(item_id,title,state,rank,allowed_paths,updated_at) VALUES ('a','ready','in-queue','a','["tools/a/"]',0),('b','blocked','in-queue','b','["tools/b/"]',0); INSERT INTO item_edges(item_id,blocked_by) VALUES ('b','a'); INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle')`); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// buildPair builds tillr-view and tillr-drain from this tree into one directory, the layout
// `just observatory` produces. Both come from the same working tree, so they carry the
// same revision and the same dirty flag.
func buildPair(t *testing.T) (view, drain string) {
	t.Helper()
	bin := t.TempDir()
	view, drain = filepath.Join(bin, "tillr-view"), filepath.Join(bin, "tillr-drain")
	for _, b := range []*exec.Cmd{
		exec.Command("go", "build", "-o", view, "."),
		exec.Command("go", "-C", "../tillr-drain", "build", "-o", drain, "."),
	} {
		b.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := b.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v %s", b.Args, err, out)
		}
	}
	return view, drain
}

// The screenshot of 2026-11-17: a server whose PATH held no tillr-drain answered every
// Waiting read with `exec: "tillr-drain": executable file not found in $PATH`. And the
// other half of the same defect: a PATH whose stale copy came first ran that copy.
// The planner now needs Git to read the proved lane base; PATH supplies Git but
// deliberately no tillr-drain, so this still proves sibling resolution.
func TestBuiltServerAsksItsOwnSiblingWhateverPATHHolds(t *testing.T) {
	view, drain := buildPair(t)
	dir, path := whyFixture(t, drain)
	decoyDir := t.TempDir()
	_, marker := decoyPlanner(t, decoyDir, `{"revision":"0000000000000000000000000000000000000000"}`)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitDir := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(gitDir, "git")); err != nil {
		t.Fatal(err)
	}
	for name, pathEnv := range map[string]string{"only Git on PATH": gitDir, "decoy first on PATH": decoyDir + string(os.PathListSeparator) + gitDir} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(view, "waiting", "--store", path, "--root", dir, "--json")
			cmd.Env = []string{"PATH=" + pathEnv, "HOME=" + t.TempDir()}
			out, err := cmd.Output()
			if err != nil {
				stderr := ""
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					stderr = string(exit.Stderr)
				}
				t.Fatalf("waiting: %v %s %s", err, out, stderr)
			}
			var rows []waitingRow
			if err := json.Unmarshal(out, &rows); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if len(rows) != 2 || rows[0].ItemID != "a" || rows[1].Mover != "drain; a" {
				t.Fatalf("not the real planner's answer: %s", out)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("the decoy on PATH was run")
			}
		})
	}
}

func TestWaitingRefusesAPlannerOfAnotherBuild(t *testing.T) {
	view, drain := buildPair(t)
	dir, path := whyFixture(t, drain)
	for name, tc := range map[string]struct{ build, want string }{
		"another revision": {`{"revision":"0000000000000000000000000000000000000000"}`, "was built from 000000000000"},
		"no build at all":  {"", "names no build"},
	} {
		t.Run(name, func(t *testing.T) {
			decoy, _ := decoyPlanner(t, t.TempDir(), tc.build)
			cmd := exec.Command(view, "waiting", "--store", path, "--root", dir, "--json", "--tillr-drain", decoy)
			cmd.Env = []string{"PATH=", "HOME=" + t.TempDir()}
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), tc.want) || strings.Contains(string(out), "decoy-row") {
				t.Fatalf("a planner of another build must be refused (%q): %v %s", tc.want, err, out)
			}
		})
	}
}

// A planner that cannot be asked is a planner condition, not a store condition and not a
// statement about the drain: /why answers its own code, and the snapshot (which carries
// the loop's heartbeat) still answers.
func TestWhyAPIPlannerRefusalsAreNotStoreFailures(t *testing.T) {
	h := newServeHarness(t)
	h.srv.plannerFlag = filepath.Join(t.TempDir(), "absent-tillr-drain")
	status, body, raw := h.get("/api/v1/why")
	if status != 503 || body["error"].(map[string]any)["code"] != codePlannerUnavailable {
		t.Fatalf("missing planner: %d %s", status, raw)
	}
	if status, _, raw := h.get("/api/v1/snapshot"); status != 200 {
		t.Fatalf("the snapshot must not depend on the planner: %d %s", status, raw)
	}
	decoy, _ := decoyPlanner(t, t.TempDir(), "")
	h.srv.plannerFlag = decoy
	h.srv.build = buildStamp{Revision: "abc"}
	status, body, raw = h.get("/api/v1/why")
	if status != 503 || body["error"].(map[string]any)["code"] != codePlannerMismatch || strings.Contains(string(raw), "decoy-row") {
		t.Fatalf("mismatched planner: %d %s", status, raw)
	}
}

// The same store read from another absolute path: a container's `/workspace` is the host's
// `~/code/tillr`. Every lane file is found relative to the store the server was given, the
// recorded seat comes from the run's dispatch, and a log that truly is not there is a plain
// sentence rather than a raw `open` error.
func TestLanesReadFromAnotherMount(t *testing.T) {
	base := t.TempDir()
	recorded := filepath.Join(base, "workspace")
	h := &serveHarness{t: t, dir: recorded, store: filepath.Join(recorded, ".tillr", "state.db")}
	if err := os.MkdirAll(filepath.Dir(h.store), 0o755); err != nil {
		t.Fatal(err)
	}
	h.seedStore()
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN seat TEXT; ALTER TABLE items ADD COLUMN seat TEXT NOT NULL DEFAULT '';
		INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at, seat)
		VALUES ('decision-run-2', 'item-seed-04', 'lane-a', 'run-2', 'dispatch', 'ready', 2900, 'builder')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	writeLaneFile(t, filepath.Join(recorded, ".tillr", "lanes", "lane-a", "run-2.log"),
		`{"type":"tool_execution_start","toolName":"read","args":{"path":"docs/a.md"},"timestamp":1234}`+"\n")

	served := filepath.Join(base, "home", "matt", "tillr")
	if err := os.MkdirAll(filepath.Dir(served), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(recorded, served); err != nil {
		t.Fatal(err)
	}
	read := func() laneRow {
		srv := newServer(context.Background(), filepath.Join(served, ".tillr", "state.db"), "", "", filepath.Join(base, "no-index"), served, "", nil)
		defer srv.close()
		ts := httptest.NewServer(srv.routes())
		defer ts.Close()
		status, _, raw := (&serveHarness{t: t, http: ts}).get("/api/v1/lanes")
		var got struct {
			Data []laneRow `json:"data"`
		}
		if err := json.Unmarshal(raw, &got); err != nil || status != 200 || len(got.Data) != 1 {
			t.Fatalf("lanes: %d %v %s", status, err, raw)
		}
		return got.Data[0]
	}
	row := read()
	if row.RunID != "run-2" || row.Seat != "builder" || row.SeatSource != "dispatch" {
		t.Fatalf("seat: %+v", row)
	}
	if row.LatestAction.Kind != "action" || row.LatestAction.Text != "read: docs/a.md" || row.Note != "" {
		t.Fatalf("action from the moved tree's log: %+v", row)
	}

	if err := os.Remove(filepath.Join(served, ".tillr", "lanes", "lane-a", "run-2.log")); err != nil {
		t.Fatal(err)
	}
	row = read()
	if row.LatestAction.Kind != "unrecorded" || strings.Contains(row.Note, "open ") || !strings.Contains(row.Note, "no stream log at "+filepath.Join(served, ".tillr", "lanes", "lane-a", "run-2.log")) {
		t.Fatalf("a missing log must be a plain sentence naming the path: %+v", row)
	}
}

func TestServeNamesTheHolderOfItsPort(t *testing.T) {
	h := newServeHarness(t)
	h.srv.build = buildStamp{Revision: "abc", Digest: "d1", Executable: "/old/tillr-view"}
	addr := strings.TrimPrefix(h.http.URL, "http://")

	same := describeHolder(context.Background(), addr, buildStamp{Revision: "abc", Digest: "d1"}, h.store)
	if same.Kind != "same" || !strings.Contains(same.Message, "open http://"+addr) {
		t.Fatalf("the same build serving the same store: %+v", same)
	}
	newer := describeHolder(context.Background(), addr, buildStamp{Revision: "abd", Digest: "d2"}, h.store)
	if newer.Kind != "other" || !strings.Contains(newer.Message, "/old/tillr-view") || !strings.Contains(newer.Message, "fuser -k") {
		t.Fatalf("an older build on the port: %+v", newer)
	}

	plain, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	go func() {
		for {
			c, err := plain.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	foreign := describeHolder(context.Background(), plain.Addr().String(), buildStamp{Digest: "d1"}, h.store)
	if foreign.Kind != "foreign" || !strings.Contains(foreign.Message, "fuser -k") {
		t.Fatalf("a listener that is not an observatory: %+v", foreign)
	}
	if _, err := net.Listen("tcp", plain.Addr().String()); !addrInUse(err) {
		t.Fatalf("a second bind must read as address-in-use: %v", err)
	}
}
