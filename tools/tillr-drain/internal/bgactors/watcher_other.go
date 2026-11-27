//go:build !linux

package bgactors

// DocsWatcher watches tracked Markdown directories for edits and signals
// changes to the document/comment sync actor.
type DocsWatcher interface {
	Events() <-chan struct{}
	UpdateDocs(dirs map[string]bool)
	Close()
}

// NewDocsWatcher returns nil on non-Linux platforms where fallback polling
// drives document sync.
func NewDocsWatcher(string) DocsWatcher { return nil }
