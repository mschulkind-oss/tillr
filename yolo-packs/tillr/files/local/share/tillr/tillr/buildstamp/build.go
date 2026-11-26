package buildstamp

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// greenRevision is supplied by just tillr-build's linker from the pinned proof ref.
// This proof identity is separate from ordinary VCS metadata; never infer it from
// the runtime checkout.
var greenRevision string

//go:embed support-revision
var embeddedSupportRevision string

// supportRevision can be overridden via -ldflags "-X github.com/mschulkind-oss/tillr/buildstamp.supportRevision=...".
var supportRevision string

// Stamp is the build a tillr process is running. Every field is best-effort: a binary
// built outside a repository has no VCS information, and that absence is reported as empty
// rather than filled with a guess.
type Stamp struct {
	Revision        string `json:"revision"`
	GreenCommit     string `json:"green_commit,omitempty"`
	SupportRevision string `json:"support_revision,omitempty"`
	Time            string `json:"time"`
	Modified        bool   `json:"modified"`
	Digest          string `json:"digest"`
	Executable      string `json:"executable"`
}

// String is the one-line build stamp. An absent revision, time, green commit, digest, or
// executable is named rather than left blank.
func (b Stamp) String() string {
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

// Read reads the running program's build stamp. It never fails.
func Read() Stamp {
	sr := strings.TrimSpace(supportRevision)
	if sr == "" {
		sr = strings.TrimSpace(embeddedSupportRevision)
	}
	stamp := Stamp{
		Revision:        greenRevision,
		GreenCommit:     greenRevision,
		SupportRevision: sr,
	}
	exe, err := os.Executable()
	if err == nil {
		stamp.Executable = strings.TrimSuffix(exe, " (deleted)")
		if runtime.GOOS == "linux" {
			stamp.Digest = fileDigest("/proc/self/exe")
		} else {
			stamp.Digest = fileDigest(exe)
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
