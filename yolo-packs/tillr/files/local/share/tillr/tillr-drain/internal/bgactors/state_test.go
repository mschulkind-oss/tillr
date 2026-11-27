package bgactors

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStateCachesAndWakes(t *testing.T) {
	var s State
	s.Ensure()

	if changed := s.SetCachedTrunk(TrunkCache{
		Tip:         "aaa",
		Green:       "bbb",
		UnprovedSet: map[string]bool{"aaa": true},
		BlockBases:  map[string]string{"item-1": "ccc"},
		Valid:       true,
	}); !changed {
		t.Fatal("first SetCachedTrunk should report changed")
	}
	if s.TrunkGen.Load() != 1 {
		t.Fatalf("TrunkGen = %d, want 1", s.TrunkGen.Load())
	}
	if changed := s.SetCachedTrunk(TrunkCache{
		Tip:         "aaa",
		Green:       "bbb",
		UnprovedSet: map[string]bool{"aaa": true},
		BlockBases:  map[string]string{"item-1": "ccc"},
		Valid:       true,
	}); changed {
		t.Fatal("identical SetCachedTrunk should report unchanged")
	}
	tip, green, valid := s.CachedTrunkAndGreen()
	if !valid || tip != "aaa" || green != "bbb" {
		t.Fatalf("CachedTrunkAndGreen = (%q, %q, %v), want (aaa, bbb, true)", tip, green, valid)
	}

	if changed := s.SetCachedDocPrereqs(DocPrereqsCache{
		Unshared: map[string]bool{"item-1": true},
		Valid:    true,
	}); !changed {
		t.Fatal("first SetCachedDocPrereqs should report changed")
	}
	if changed := s.SetCachedDocPrereqs(DocPrereqsCache{
		Unshared: map[string]bool{"item-1": true},
		Valid:    true,
	}); changed {
		t.Fatal("identical SetCachedDocPrereqs should report unchanged")
	}

	if changed := s.SetCachedCommentReady(map[string]bool{"comment-1": true}); !changed {
		t.Fatal("first SetCachedCommentReady should report changed")
	}
	if !s.CachedCommentReady()["comment-1"] {
		t.Fatal("CachedCommentReady missing comment-1")
	}

	s.SignalPass()
	s.SignalSync()
	s.SignalTrunkProof()
	s.SignalGC()
	for name, ch := range map[string]<-chan struct{}{
		"pass":        s.PassWakeChan(),
		"sync":        s.SyncWakeChan(),
		"trunk-proof": s.TrunkProofWakeChan(),
		"gc":          s.GCWakeChan(),
	} {
		select {
		case <-ch:
		default:
			t.Fatalf("%s wake channel did not receive signal", name)
		}
	}
}

func TestDocsWatcherSignalsMarkdownEdit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux inotify test")
	}
	root := t.TempDir()
	docsDir := filepath.Join(root, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w := NewDocsWatcher(root)
	if w == nil {
		t.Fatal("NewDocsWatcher returned nil on linux")
	}
	defer w.Close()

	if err := os.WriteFile(filepath.Join(docsDir, "design.md"), []byte("# design\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	select {
	case <-w.Events():
	case <-timer.C:
		t.Fatal("timed out waiting for docs watcher event")
	}
}
