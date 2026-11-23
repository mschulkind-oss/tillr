package remotesync

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/checkresult"
)

const (
	// Utility names the tillr utility recorded on drain events.
	Utility = "drain"
	// ScopeEscape prefixes boundary/scope violation diagnostics.
	ScopeEscape = "boundary-scope-escape"
	// ForkPatchPrefix is the branch prefix used for fork delivery refs.
	ForkPatchPrefix = "refs/heads/patch/"
)

// ClassifyCheck is the check-result classifier used by remotesync.
// Drain tests override it via site_classify_test.go's withClassifier.
var ClassifyCheck = checkresult.Classify

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func readsCoverPath(reads []string, path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
	for _, r := range reads {
		r = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(r)), "./")
		if r == "." || r == "" || path == r || strings.HasPrefix(path, r+"/") {
			return true
		}
	}
	return false
}
