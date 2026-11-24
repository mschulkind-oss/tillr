package run_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// The stand-in `pi` programs these cases write do not answer `pi --features`, so the
	// features they stand for are stated to the adapter instead of probed.
	os.Setenv("TILLR_PI_FEATURES", "parked-tool-calls")
	os.Exit(m.Run())
}
