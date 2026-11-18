package run

import "testing"

func TestShippedEndingCapabilities(t *testing.T) {
	for _, adapter := range []string{"pi", "claude", "muse", "agy"} {
		t.Run(adapter, func(t *testing.T) {
			capability, err := ReadEndingCapability("../backends", adapter+"/run")
			if err != nil || capability != EndingFooter {
				t.Fatalf("capability = %q, err = %v", capability, err)
			}
		})
	}
}
