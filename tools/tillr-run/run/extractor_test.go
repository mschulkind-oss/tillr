package run

import "testing"

// TestExtractorResolvesFromTheAdaptersDirectory pins the layout rule criterion 1 states: a
// run's extractor is the file beside the adapter its request names, so a backend's own name
// never becomes a directory name. A policy spells its adapter relative to the repository
// root while the backends directory is the same tree's, so the adapter's own directory is
// the last component of the path it was named by; an absolute adapter carries it already.
func TestExtractorResolvesFromTheAdaptersDirectory(t *testing.T) {
	cases := []struct{ name, backendsDir, adapter, want string }{
		{
			name:        "an absolute adapter",
			backendsDir: "/repo/tools/tillr-run/backends",
			adapter:     "/repo/tools/tillr-run/backends/pi/run-pi",
			want:        "/repo/tools/tillr-run/backends/pi/extract-report",
		},
		{
			name:        "the policy's repository-relative spelling",
			backendsDir: "/repo/tools/tillr-run/backends",
			adapter:     "tools/tillr-run/backends/pi/run-pi",
			want:        "/repo/tools/tillr-run/backends/pi/extract-report",
		},
		{
			name:        "a bare adapter name is on PATH, not in a known directory",
			backendsDir: "/repo/tools/tillr-run/backends",
			adapter:     "run-pi",
			want:        "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractorFor(c.backendsDir, c.adapter); got != c.want {
				t.Fatalf("extractorFor(%q, %q) = %q, want %q", c.backendsDir, c.adapter, got, c.want)
			}
		})
	}
}
