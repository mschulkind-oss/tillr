package version

import "fmt"

// Set by goreleaser ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	Dirty     = ""
)

// String returns a formatted version string like "0.1.0 (abc1234)".
func String() string {
	if GitCommit == "unknown" {
		return Version
	}
	s := fmt.Sprintf("%s (%s)", Version, GitCommit)
	if Dirty != "" {
		s += " dirty"
	}
	return s
}
