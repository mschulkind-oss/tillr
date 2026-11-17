package run_test

import (
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestWallTimeoutDefaultIsUnbounded(t *testing.T) {
	backend := run.Backend{Adapter: "/bin/true", Model: "fixture"}
	request := backend.Request("item", "lane", "run", "fixture")
	if request.TimeoutMS != 0 {
		t.Fatalf("omitted backend wall = %d ms, want 0 (unbounded)", request.TimeoutMS)
	}
	if (run.Request{}).TimeoutMS != 0 {
		t.Fatal("omitted run wall must be unbounded")
	}
}
