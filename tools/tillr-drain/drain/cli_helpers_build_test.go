package drain_test

import (
	"os"
	"path/filepath"
	"testing"
)

// The shared binary is built by whichever case asks for it first. A case that has put a
// refusing `git` first on PATH must not decide whether that build succeeds: the build
// stamps the revision by running `git`, and one failed build fails every later case.
func TestSharedBinaryBuildIgnoresAStubbedGit(t *testing.T) {
	refusing := stub(t, "git", "exit 99")
	t.Setenv("PATH", filepath.Dir(refusing)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := buildDrainBinary(filepath.Join(scratch(t, "build"), "tillr-drain")); err != nil {
		t.Fatalf("the build ran the case's git stub: %v\n%s", err, out)
	}
}
