package drain

// The build a drain loop is running, stamped into its startup line and into the result
// record it writes.
//
// A loop pinned to a launch-time binary is indistinguishable in its own records from the
// current tree: `producer.version` is the release version, the same "0.1.0" for every
// build of that release, so two builds answered "which code produced this record?" with the
// same word. Measured 2026-11-14: a fence fix was committed, gated and pack-mirrored while
// the loop ran a binary built at 00:37 that morning, and three items were refused by the
// very trap the fix closed. This stamp is what makes "the loop is not the tree I am
// reading" a one-glance answer instead of a night of investigation.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// BuildStamp is the build a drain process is running. Every field is best-effort: a binary
// built outside a repository has no VCS information, and that absence is reported as empty
// rather than filled with a guess.
type BuildStamp struct {
	// Revision is the commit the binary was built from (`vcs.revision`), empty when the
	// build carried none. A binary the pack launcher builds from the rendered pack tree is
	// the common case: that tree is not a git checkout, so the launched build has no
	// revision while a `just tillr-build` build does.
	Revision string `json:"revision"`
	// Time is when that commit was made (`vcs.time`), RFC 3339, empty when unknown.
	Time string `json:"time"`
	// Modified is true when the working tree had uncommitted changes at build time, which
	// is the difference between "the current commit" and "something near it".
	Modified bool `json:"modified"`
	// Digest is the build's identity. It is the source digest the pack launcher names in
	// the executable's own path when the launcher built it, and otherwise the executable's
	// SHA-256: two builds of one revision are still two builds, and this is the field that
	// tells them apart.
	Digest string `json:"digest"`
	// Executable is the path of the running program, so "which of the installs am I looking
	// at" is answered by the record rather than by `ps`.
	Executable string `json:"executable"`
}

// named reports whether the stamp identifies a build at all. A stamp with neither a
// revision nor a digest is the absence of a stamp, which the record must not confuse with a
// build that has nothing to say.
func (b BuildStamp) named() bool { return b.Revision != "" || b.Digest != "" }

// String is the line a loop prints at startup, one line and never multi-line. An absent
// revision or digest is named rather than left blank, so a build that cannot be identified
// reads as unidentified instead of as a build with nothing to say.
func (b BuildStamp) String() string {
	rev := b.Revision
	if rev == "" {
		rev = "no-revision"
	} else if len(rev) > 12 {
		rev = rev[:12]
	}
	if b.Modified {
		rev += "+dirty"
	}
	digest := b.Digest
	if digest == "" {
		digest = "no-digest"
	} else if len(digest) > 12 {
		digest = digest[:12]
	}
	at := b.Time
	if at == "" {
		at = "unknown-time"
	}
	exe := b.Executable
	if exe == "" {
		exe = "unknown-exe"
	}
	return fmt.Sprintf("%s (%s) digest %s exe %s", rev, at, digest, exe)
}

// ReadBuildStamp reads the running program's stamp. It never fails: a build with no module
// or VCS information answers an empty revision, because a loop that refuses to say what it
// is running answers the question by not answering at all.
func ReadBuildStamp() BuildStamp {
	stamp := BuildStamp{}
	exe, err := os.Executable()
	if err == nil {
		stamp.Executable = exe
		stamp.Digest = buildDigest(exe)
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return stamp
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			stamp.Revision = setting.Value
		case "vcs.time":
			stamp.Time = setting.Value
		case "vcs.modified":
			stamp.Modified = setting.Value == "true"
		}
	}
	return stamp
}

// buildDigest answers the identity of one executable. The pack launcher names its cache
// file `<program>-<source digest>`, so a launched build carries the launcher's own source
// digest in its path; a build made any other way has no such name, and its executable's
// SHA-256 is the identity that still distinguishes two builds.
func buildDigest(exe string) string {
	if base := filepath.Base(exe); base != "" {
		if i := strings.LastIndexByte(base, '-'); i >= 0 {
			suffix := base[i+1:]
			if len(suffix) == sha256.Size*2 && isHex(suffix) {
				return suffix
			}
		}
	}
	file, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// isHex reports whether s is entirely lowercase hexadecimal, the spelling a sha256 digest
// is written in. An uppercase digest is not the launcher's, so it falls through to the
// executable hash rather than being trusted as one.
func isHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return s != ""
}
