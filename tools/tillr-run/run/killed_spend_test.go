package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCanceledFaceExtractsUsageAlreadyWritten(t *testing.T) {
	dir := scratch(t)
	backends, adapter := backendWithExtractor(t, "", "0")
	marker := filepath.Join(dir, "usage-written")
	worker := "#!/bin/sh\nprintf 'tokens: 1500\\ncost usd: 0.030000\\n---\\n'\nprintf ready > '" + marker + "'\nexec sleep 300\n"
	if err := os.WriteFile(adapter, []byte(worker), 0755); err != nil {
		t.Fatal(err)
	}
	extractor := "#!/bin/sh\nprev=; out=; log=\nfor arg in \"$@\"; do [ \"$prev\" != --out ] || out=$arg; prev=$arg; log=$arg; done\ncp \"$log\" \"$out\"\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(adapter), "extract-report"), []byte(extractor), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var rec run.Record
	go func() {
		env, err := run.Exec(ctx, s, run.Request{RunID: "ended", Backend: "fixture", Command: []string{adapter}, WorkingDir: "."}, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), BackendsDir: backends})
		if err == nil {
			err = json.Unmarshal(env.Payload, &rec)
		}
		done <- err
	}()
	deadline := time.Now().Add(hangGuard)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker never wrote usage")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatal("face did not stop")
	}
	if rec.Usage == nil || rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != .03 || rec.Usage.Tokens == nil || *rec.Usage.Tokens != 1500 {
		t.Fatalf("ended run lost emitted usage: %+v", rec)
	}
	saved, found := storedRun(t, s, "ended")
	if !found || saved.Usage == nil || saved.Usage.CostUSD == nil || *saved.Usage.CostUSD != .03 {
		t.Fatalf("terminal row lost spend: %+v", saved)
	}
}
