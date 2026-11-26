package drainmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// BuildStamp is the build a drain process is running, field-identical to drain.BuildStamp.
type BuildStamp struct {
	Revision        string `json:"revision"`
	GreenCommit     string `json:"green_commit,omitempty"`
	SupportRevision string `json:"support_revision,omitempty"`
	Time            string `json:"time"`
	Modified        bool   `json:"modified"`
	Digest          string `json:"digest"`
	Executable      string `json:"executable"`
}

// Named reports whether the stamp identifies a build at all.
func (b BuildStamp) Named() bool { return b.Revision != "" || b.Digest != "" }

// String formats the build stamp as a single startup/record line.
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

// Hash computes the build's fact hash for drain.inputs.
func (b BuildStamp) Hash() (string, error) {
	raw, err := json.Marshal([]any{b.Revision, b.Modified, b.Digest})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// FileDigest is the SHA-256 of one file, empty when it cannot be read.
func FileDigest(path string) string {
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

// DiskDigest is the identity of the binary now at path.
func DiskDigest(path string) string { return FileDigest(path) }

// InputHash hashes a JSON-serializable value for input-binding records.
func InputHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
