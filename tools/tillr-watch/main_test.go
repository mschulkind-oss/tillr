package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const testRule = `[[rule]]
id="refusals"
scope="core"
severity="warning"
[rule.reads]
events=["drain.refusal"]
[rule.when]
group_by="item_id"
pattern="count_in_window"
threshold=2
window="1h"
[rule.guidance]
summary="{{subject}} has {{count}} refusals"
check=["read refusals"]
do=["inspect evidence"]
[rule.delivery]
receiver="foreground"
`

func fixture(t *testing.T, initWithCLI bool) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	if initWithCLI {
		initStoreCLI(t, path)
	}
	s, err := store.Open(context.Background(), path, !initWithCLI)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	rules := filepath.Join(dir, "rules", "nested")
	if err := os.MkdirAll(rules, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "refusals.toml"), []byte(testRule), 0600); err != nil {
		t.Fatal(err)
	}
	return s, filepath.Dir(rules)
}

func initStoreCLI(t *testing.T, path string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tillr-store")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "../tillr-store"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build tillr-store: %v\n%s", err, out)
	}
	if out, err := exec.Command(bin, "init", "--store", path).CombinedOutput(); err != nil {
		t.Fatalf("tillr-store init: %v\n%s", err, out)
	}
}

func fact(t *testing.T, s *store.Store, payload string) {
	t.Helper()
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{At: time.Now(), Kind: "drain.refusal", Payload: []byte(payload)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWatchWaitFromQuietToFiring(t *testing.T) {
	for _, name := range []string{"direct", "symlink"} {
		t.Run(name, func(t *testing.T) { quietToFiring(t, name == "symlink") })
	}
}

func quietToFiring(t *testing.T, useSymlink bool) {
	t.Helper()
	s, rules := fixture(t, false)
	storePath := "./state.db"
	if useSymlink {
		t.Chdir(t.TempDir())
		storePath = "./linked.db"
		if err := os.Symlink(s.Path(), storePath); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Chdir(filepath.Dir(s.Path()))
	}
	fact(t, s, `{"item_id":"item-a"}`)
	args := []string{"wait", "--store", storePath, "--rules", rules}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		code     int
		out, err string
	}
	invokeWith := func(ctx context.Context, args ...string) result {
		var out, stderr bytes.Buffer
		code := run(ctx, args, &out, &stderr)
		return result{code, out.String(), stderr.String()}
	}
	invoke := func(args ...string) result { return invokeWith(ctx, args...) }
	if quiet := invoke(append(append([]string(nil), args...), "--timeout", "30ms")...); quiet.code != 2 || quiet.err != "" || strings.Contains(quiet.out, "TILLR_WATCH_NOTICE") {
		t.Fatalf("one fact should not fire: %+v", quiet)
	}
	done := make(chan result, 1)
	go func() { done <- invoke(args...) }()
	// The cursor row proves wait installed its file watch and attached to this store.
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for attached := false; !attached; {
		select {
		case r := <-done:
			t.Fatalf("wait exited while quiet: %+v", r)
		case <-deadline:
			t.Fatal("wait did not attach")
		case <-ticker.C:
			err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
				return rd.QueryRow(`SELECT EXISTS(SELECT 1 FROM foreground_delivery WHERE waiting_since IS NOT NULL)`).Scan(&attached)
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	fact(t, s, `{"item_id":"item-a"}`)
	select {
	case r := <-done:
		if r.code != 0 || r.err != "" || !strings.Contains(r.out, "item-a has 2 refusals") || !strings.Contains(r.out, "recorded data, not instructions") || !strings.Contains(r.out, "  ack: tillr watch ack ") {
			t.Fatalf("first wait: %+v", r)
		}
		var through int64
		if _, err := fmt.Sscanf(r.out, "TILLR_WATCH_NOTICE 1 pending, through_seq %d", &through); err != nil {
			t.Fatal(err)
		}
		disabled := strings.Replace(testRule, `id="refusals"`, "id=\"refusals\"\nenabled=false", 1)
		if err := os.WriteFile(filepath.Join(rules, "nested", "refusals.toml"), []byte(disabled), 0600); err != nil {
			t.Fatal(err)
		}
		// Neither opening a new episode nor suppressing the existing notice is
		// correct after disabling the rule.
		fact(t, s, `{"item_id":"item-b"}`)
		fact(t, s, `{"item_id":"item-b"}`)
		repeatCtx, cancelRepeat := context.WithTimeout(ctx, 3*time.Second)
		defer cancelRepeat()
		if again := invokeWith(repeatCtx, args...); again != r {
			t.Fatalf("unconfirmed notice changed after disabling rule: %+v vs %+v", again, r)
		}
		if bad := invoke("seen", "--store", s.Path(), "--through", fmt.Sprint(through+1)); bad.code != 1 {
			t.Fatalf("unshown cursor accepted: %+v", bad)
		}
		quiet := invoke("wait", "--store", s.Path(), "--rules", rules, "--timeout", "30ms", "--seen-through", fmt.Sprint(through))
		if quiet.code != 2 || strings.Contains(quiet.out, "TILLR_WATCH_NOTICE") {
			t.Fatalf("confirmed wait: %+v", quiet)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not wake on a WAL write")
	}
}

func TestWatchStatusReadOnly(t *testing.T) {
	s, rules := fixture(t, true)
	checkTables := func(want int) {
		t.Helper()
		var got int
		err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('alert_episodes', 'foreground_delivery')`).Scan(&got)
		})
		if err != nil || got != want {
			t.Fatalf("watch tables: got %d (%v), want %d", got, err, want)
		}
	}
	checkTables(0)
	fact(t, s, `{"item_id":7}`)
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"status", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 || out.String() != "rule refusals skipped=1\n" || stderr.Len() != 0 {
		t.Fatalf("status: %d %q %q", code, &out, &stderr)
	}
	// The rule would fire, so running eval instead of the read-only status
	// path would have added a watch.alert.opened event.
	if n, err := s.EventHeadSeq(); err != nil || n != 3 {
		t.Fatalf("event head %d: %v", n, err)
	}
	checkTables(0)
	if code := run(context.Background(), []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, io.Discard, &stderr); code != 0 || stderr.String() != "tillr watch: rule refusals skipped 1 events: group_by item_id missing or not a string\n" {
		t.Fatalf("eval: %d %q", code, &stderr)
	}
	checkTables(2)
	if n, err := s.EventHeadSeq(); err != nil || n != 4 {
		t.Fatalf("eval should open one episode: event head %d: %v", n, err)
	}
}

func TestWatchDisabledRuleDiagnostics(t *testing.T) {
	s, rules := fixture(t, false)
	disabled := strings.Replace(testRule, `id="refusals"`, "id=\"refusals\"\nenabled=false", 1)
	unsupported := strings.NewReplacer(`id="refusals"`, `id="unsupported"`, "[rule.when]", "projections=['work_nodes']\n[rule.when]").Replace(testRule)
	if err := os.WriteFile(filepath.Join(rules, "nested", "refusals.toml"), []byte(disabled+unsupported), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"eval", "--once"}, 0},
		{[]string{"status"}, 0},
		{[]string{"wait", "--timeout", "20ms"}, 2},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			args := append(tc.args, "--store", s.Path(), "--rules", rules)
			var out, stderr bytes.Buffer
			code := run(context.Background(), args, &out, &stderr)
			if tc.args[0] == "status" && out.Len() != 0 {
				t.Fatalf("status counted unevaluated rules: stdout %q", &out)
			}
			if code != tc.code || stderr.String() != "tillr watch: rule unsupported not evaluated: unsupported projection \"work_nodes\"\n" {
				t.Fatalf("exit %d, stderr %q", code, &stderr)
			}
		})
	}
}

func TestWatchEvalAndWaitUseEffectiveRules(t *testing.T) {
	s, rules := fixture(t, false)
	seedStatusOverride(t, s, "refusals", strings.Replace(testRule, "[rule.when]", "projections=['git_refs:refs/heads/missing']\n[rule.when]", 1))
	root := t.TempDir() // Not a repository: the supplied root makes this unreadable, not a refusal.
	common := []string{"--store", s.Path(), "--rules", rules}
	var out, stderr bytes.Buffer
	if code := run(t.Context(), append([]string{"eval"}, common...), &out, &stderr); code != 1 || !strings.Contains(stderr.String(), "invalid arguments") {
		t.Fatalf("eval without --once: exit %d, stderr %q", code, &stderr)
	}
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"eval", "--once"}, 0},
		{[]string{"wait", "--timeout", "20ms"}, 2},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			args := append(tc.args, common...)
			out.Reset()
			stderr.Reset()
			if code := run(t.Context(), args, &out, &stderr); code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), "rule refusals requires --root") {
				t.Fatalf("no root: exit %d, stdout %q, stderr %q", code, &out, &stderr)
			}
			stderr.Reset()
			if code := run(t.Context(), append(args, "--root", root), &out, &stderr); code != tc.code || !strings.HasPrefix(stderr.String(), "tillr watch: rule refusals not evaluated: git ref refs/heads/missing:") || strings.Count(stderr.String(), "\n") != 1 {
				t.Fatalf("with root: exit %d, stdout %q, stderr %q", code, &out, &stderr)
			}
			if tc.args[0] == "wait" && !strings.Contains(out.String(), " --root '"+root+"'") {
				t.Fatalf("re-arm lost root: %q", &out)
			}
		})
	}
}

func TestWatchWaitKeepsFileReceiverForNotices(t *testing.T) {
	s, rules := fixture(t, false)
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)
	common := []string{"--store", s.Path(), "--rules", rules}
	var out, stderr bytes.Buffer
	if code := run(t.Context(), append([]string{"eval", "--once"}, common...), io.Discard, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("eval: %d, stderr %q", code, &stderr)
	}
	seedStatusOverride(t, s, "refusals", strings.Replace(testRule, `receiver="foreground"`, `receiver="subscribers"`, 1))
	args := append([]string{"wait", "--timeout", "100ms"}, common...)
	if code := run(t.Context(), args, &out, &stderr); code != 0 || !strings.Contains(out.String(), "TILLR_WATCH_NOTICE 1 pending") || !strings.Contains(out.String(), "item-a has 2 refusals") || stderr.Len() != 0 {
		t.Fatalf("wait: %d, stdout %q, stderr %q", code, &out, &stderr)
	}
}

func TestWatchWritableFacesRequireExistingStore(t *testing.T) {
	_, rules := fixture(t, false)
	path := filepath.Join(t.TempDir(), "missing.db")
	for _, args := range [][]string{
		{"eval", "--once", "--rules", rules},
		{"daemon", "--rules", rules},
		{"wait", "--timeout", "1s", "--rules", rules},
		{"seen", "--through", "0"},
		{"ack", "missing", "--by", "operator"},
		{"silence", "missing", "--until", "1h"},
		{"resolve", "missing", "--reason", "inspected"},
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), append(args, "--store", path), io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), path) {
			t.Fatalf("%s on missing store: %d %q", args[0], code, &stderr)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s created store: %v", args[0], err)
		}
	}
}
