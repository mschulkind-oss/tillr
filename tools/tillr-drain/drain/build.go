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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/mschulkind-oss/tillr/buildstamp"
)

// greenRevision is supplied by just tillr-build's linker from the pinned proof ref.
// This proof identity is separate from ordinary VCS metadata; never infer it from
// the runtime checkout.
var greenRevision string

// BuildStamp is the build a drain process is running. Every field is best-effort: a binary
// built outside a repository has no VCS information, and that absence is reported as empty
// rather than filled with a guess.
type BuildStamp struct {
	// Revision is the source commit: the linker-pinned green commit for archived builds,
	// otherwise `vcs.revision`, or empty when neither was supplied. Pack-rendered sources
	// have no Git metadata and do not imply a proof commit.
	Revision string `json:"revision"`
	// GreenCommit is the proof commit pinned by the green-tree build recipe. Empty
	// means a build made by another path, not an assertion that the checkout is green.
	GreenCommit string `json:"green_commit,omitempty"`
	// SupportRevision is the SHA-256 digest of tillr's runtime support files embedded at build time.
	SupportRevision string `json:"support_revision,omitempty"`
	// Time is when that commit was made (`vcs.time`), RFC 3339, empty when unknown.
	Time string `json:"time"`
	// Modified is true when the working tree had uncommitted changes at build time, which
	// is the difference between "the current commit" and "something near it".
	Modified bool `json:"modified"`
	// Digest is the SHA-256 of the executable image, not its source-cache filename.
	// Reading the image makes a rename racing the last restart check observable.
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
	green := b.GreenCommit
	if green == "" {
		green = "unknown"
	}
	return fmt.Sprintf("%s (%s) green %s digest %s exe %s", rev, at, green, digest, exe)
}

// ReadBuildStamp reads the running program's stamp. It never fails: a build with no module
// or VCS information answers an empty revision, because a loop that refuses to say what it
// is running answers the question by not answering at all.
func ReadBuildStamp() BuildStamp {
	stamp := BuildStamp{
		Revision:        greenRevision,
		GreenCommit:     greenRevision,
		SupportRevision: buildstamp.Read().SupportRevision,
	}
	exe, err := os.Executable()
	if err == nil {
		// A running binary replaced on disk by a rename reads back as "<path> (deleted)".
		// Its path is the new file's, so the suffix is dropped for the record, and the
		// running image is hashed through /proc so the digest is the code in memory.
		stamp.Executable = strings.TrimSuffix(exe, " (deleted)")
		if runtime.GOOS == "linux" {
			stamp.Digest = fileDigest("/proc/self/exe")
		} else {
			stamp.Digest = buildDigest(exe)
		}
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

// buildDigest reads the bytes on disk, even for a digest-named cache path.
func buildDigest(exe string) string { return fileDigest(exe) }

// fileDigest is the SHA-256 of one file, empty when it cannot be read.
func fileDigest(path string) string {
	file, err := os.Open(path)
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

// DiskDigest is the identity of the binary now at path, which a restart compares with the
// running build's. It is the same function the running build's digest comes from.
func DiskDigest(path string) string { return buildDigest(path) }

// Hash is the build's fact in `drain.inputs`: revision, modified flag and digest. The
// executable path is left out, because a relocation of the same executable is not a new build.
func (b BuildStamp) Hash() (string, error) {
	raw, err := json.Marshal([]any{b.Revision, b.Modified, b.Digest})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
