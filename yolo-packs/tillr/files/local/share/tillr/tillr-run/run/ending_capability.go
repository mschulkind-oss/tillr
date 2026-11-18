package run

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	EndingFooter          = "footer"
	EndingCallSuspends    = "call-suspends"
	EndingCallThenMessage = "call-then-message"
)

// ReadEndingCapability reads the declaration beside the adapter, never from policy.
func ReadEndingCapability(backendsDir, adapter string) (string, error) {
	extractor := extractorFor(backendsDir, adapter)
	if extractor == "" {
		return "", Usagef("adapter %q has no directory for its ending-capability declaration", adapter)
	}
	path := filepath.Join(filepath.Dir(extractor), "ending-capability")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", Usagef("reading adapter ending-capability at %s: %v", path, err)
	}
	switch capability := strings.TrimSuffix(string(raw), "\n"); capability {
	case EndingFooter, EndingCallSuspends, EndingCallThenMessage:
		return capability, nil
	}
	return "", Usagef("%s holds %q: want one line, footer, call-suspends or call-then-message", path, raw)
}
