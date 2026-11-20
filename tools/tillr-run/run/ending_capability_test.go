package run

import "testing"

func TestShippedEndingCapabilities(t *testing.T) {
	for _, tc := range []struct {
		adapter, want string
	}{
		{"pi", EndingFooter},
		{"claude", EndingCallSuspends},
		{"muse", EndingFooter},
		{"agy", EndingFooter},
	} {
		t.Run(tc.adapter, func(t *testing.T) {
			capability, err := ReadEndingCapability("../backends", tc.adapter+"/run")
			if err != nil || capability != tc.want {
				t.Fatalf("capability = %q (want %q), err = %v", capability, tc.want, err)
			}
		})
	}
}
