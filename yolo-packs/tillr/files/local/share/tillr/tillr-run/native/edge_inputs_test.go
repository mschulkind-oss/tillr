package native_test

import (
	"os"
	"testing"
)

func TestEdgeInputs(t *testing.T) {
	_ = os.Getenv("TILLR_GATE_EDGE_INPUTS")
}
