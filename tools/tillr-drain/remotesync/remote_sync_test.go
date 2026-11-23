package remotesync

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRemoteSyncItemHelpers(t *testing.T) {
	id := RemoteSyncItemID("  ABCDEF0123456789abcdef0123456789abcdef01 ")
	if id != "sync-remote-abcdef012345" {
		t.Fatalf("RemoteSyncItemID = %q", id)
	}
	if !IsRemoteSyncItem(id) {
		t.Fatalf("IsRemoteSyncItem(%q) = false, want true", id)
	}
	if IsRemoteSyncItem("item-123") {
		t.Fatal("IsRemoteSyncItem(\"item-123\") = true, want false")
	}
	if got := RemoteSyncPinRef(id); got != "refs/tillr/sync-remote/sync-remote-abcdef012345" {
		t.Fatalf("RemoteSyncPinRef = %q", got)
	}
}

func TestParseMergeTreeConflict(t *testing.T) {
	raw := strings.Join([]string{
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"b.txt",
		"a.txt",
		"b.txt",
		"",
		"1",
		"a.txt",
		"Auto-merging",
		"  CONFLICT (content): Merge conflict in a.txt  ",
		"2",
		"b.txt",
		"b.txt",
		"CONFLICT",
		"  CONFLICT (add/add): Merge conflict in b.txt  ",
	}, "\x00")
	paths, msg := ParseMergeTreeConflict(raw)
	wantPaths := []string{"a.txt", "b.txt"}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Fatalf("paths = %v, want %v", paths, wantPaths)
	}
	wantMsg := "CONFLICT (content): Merge conflict in a.txt\nCONFLICT (add/add): Merge conflict in b.txt"
	if msg != wantMsg {
		t.Fatalf("msg = %q, want %q", msg, wantMsg)
	}
}

func TestRemoteSyncProtectedPathsAndConflict(t *testing.T) {
	isGate := func(p string) bool { return strings.HasPrefix(p, "tools/gate/") }
	if got := RemoteSyncProtectedPaths([]string{"README.md", "tools/tillr-drain/main.go"}, isGate); got != "" {
		t.Fatalf("unexpected problem: %q", got)
	}
	badName := string([]byte{'b', 'a', 'd', 0xff})
	got := RemoteSyncProtectedPaths([]string{"tools/gate/check.py", badName}, isGate)
	if !strings.Contains(got, "protected gate code paths (tools/gate/check.py)") {
		t.Fatalf("missing gate code problem in %q", got)
	}
	if !strings.Contains(got, "paths that cannot be recorded losslessly") {
		t.Fatalf("missing invalid utf8 problem in %q", got)
	}
	msg := ProtectedRemoteSyncConflict("sync-remote-abc", []string{"tools/gate/check.py"}, isGate)
	if !strings.Contains(msg, ScopeEscape) || !strings.Contains(msg, "sync-remote-abc") || !strings.Contains(msg, "tools/gate/check.py") {
		t.Fatalf("ProtectedRemoteSyncConflict = %q", msg)
	}
	if clean := ProtectedRemoteSyncConflict("sync-remote-abc", []string{"README.md"}, isGate); clean != "" {
		t.Fatalf("expected empty clean conflict, got %q", clean)
	}
}

func TestRemoteSyncStateStoreAndReplay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "store.db")

	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	st := RemoteSyncState{
		ItemID:         "sync-remote-1234567890ab",
		Remote:         "origin",
		Ref:            "refs/heads/main",
		RemoteTip:      "1234567890abcdef1234567890abcdef12345678",
		MergeBase:      "aaaa1111bbbb2222cccc3333dddd4444eeee5555",
		BaseSHA:        "1234567890abcdef1234567890abcdef12345678",
		ConflictCommit: "9999888877776666555544443333222211110000",
		ConflictParent: "8888777766665555444433332222111100009999",
		ConflictPaths:  []string{"a.txt", "b.txt"},
		ConflictOutput: "CONFLICT (content) in a.txt",
		LandingSummary: "item-1: test",
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := WriteRemoteSyncReplay(tx, "main", Utility, &RemoteSyncReplay{
			Rewrite: RewriteRecord{Tip: "feedface", Order: []string{"c1", "c2"}},
			Tree:    "cafebabe",
			Stage:   st,
		}, time.Unix(100, 0).UTC()); err != nil {
			return err
		}
		_, _, err := ReadRemoteSyncState(tx.Reader(), st.ItemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	replay, err := ReadRemoteSyncReplay(ctx, dbPath, "main")
	if err != nil || replay == nil {
		t.Fatalf("ReadRemoteSyncReplay = (%+v, %v)", replay, err)
	}
	pub := replay.Publication()
	if pub.Replayed != 2 || pub.RemoteTip != st.RemoteTip || pub.Base != st.MergeBase || pub.NewTip != "feedface" {
		t.Fatalf("Publication = %+v", pub)
	}

	if _, found, err := ReadRemoteSyncStateAt(ctx, dbPath, st.ItemID); err != nil || found {
		t.Fatalf("expected not found before staged event, got (%v, %v)", found, err)
	}
	if _, err := RemoteSyncBriefFacts(ctx, dbPath, st.ItemID); err == nil {
		t.Fatal("expected error for missing staged state")
	}
	if dest := RemoteSyncDestination(st); dest != "origin/main (1234567890ab)" {
		t.Fatalf("RemoteSyncDestination = %q", dest)
	}
	if crit := RemoteSyncCriterionText(st); !strings.Contains(crit, "origin/main (1234567890ab)") || !strings.Contains(crit, "a.txt, b.txt") {
		t.Fatalf("RemoteSyncCriterionText = %q", crit)
	}
	errStr := RemoteSyncConflictError(st, "main", "owner", "protected")
	if !strings.Contains(errStr, "escalated (protected)") || !strings.Contains(errStr, "mover: owner; resolve sync-remote-1234567890ab") {
		t.Fatalf("RemoteSyncConflictError = %q", errStr)
	}
	// Verify empty storePath returns not-found without error.
	if _, found, err := ReadRemoteSyncStateAt(ctx, "", st.ItemID); err != nil || found {
		t.Fatalf("empty storePath = (%v, %v), want (false, nil)", found, err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}
}
