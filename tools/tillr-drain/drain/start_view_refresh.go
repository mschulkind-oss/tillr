package drain

import (
	"context"
	"crypto/sha256"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// startRefresh is owned by the shadow watcher. It never installs a question
// source in the legacy pass or performs document sync or answer settlement.
type startRefresh struct {
	store startStoreSync
	docs  startDocumentInputs
}

func (l *Loop) watchStartView(ctx context.Context, view *startView, interval time.Duration) func() {
	tick := l.startViewPass()
	root, cmd, index := l.cfg.Root, l.cfg.QuestionsCmd, l.questionsIndexPath()
	var refresh startRefresh
	stop := view.watch(ctx, interval, func(ctx context.Context) startWatcher {
		paths := startWatchPaths{root: root, store: tick.StorePath, index: index}
		if root != "" && !l.nonLanding {
			gitDir, _ := l.git(ctx, root, "rev-parse", "--absolute-git-dir")
			commonDir, _ := l.git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
			paths.gitDir, paths.commonDir = strings.TrimSpace(gitDir), strings.TrimSpace(commonDir)
		}
		return newStartWatcher(paths)
	}, func(ctx context.Context, change startWatchChange) (startSnapshot, error) {
		// Lease expiry uses wall time, not the legacy pass's clock hook.
		return refresh.read(ctx, time.Now(), l.currentPolicy(), tick, root, cmd, index, view.snapshot(), change)
	})
	return func() {
		stop()
		refresh.store.close()
	}
}

func (l *Loop) questionsIndexPath() string {
	if l.cfg.QuestionsIndex != "" {
		return l.cfg.QuestionsIndex
	}
	if !l.nonLanding && l.cfg.Store != "" {
		return l.cfg.Store + ".questions-index.json"
	}
	return ""
}

func (s *startRefresh) read(ctx context.Context, at time.Time, policy *Policy, tick Pass, root string, cmd []string, index string, prev startSnapshot, change startWatchChange) (startSnapshot, error) {
	inspect := change&startWatchExternal != 0
	docs, bases := s.docs, startBases{tip: prev.tip, green: prev.green}
	docsChanged := false
	// Ordinary external refreshes need not pin a SQLite read transaction
	// across a Git probe, directory walk or Vantage subprocess.
	if inspect && prev.pending != nil {
		var err error
		bases, err = refreshTrunk(ctx, root, prev.def)
		if err != nil {
			return prev, err
		}
		docsChanged = docs.refreshDocuments(ctx, root, cmd, index, prev.itemsByID)
	}
	tick.Now = at.Format(time.RFC3339Nano)
	next, err := s.store.syncStoreWithRefresh(ctx, tick.StorePath, at, policy, prev, inspect, func(r *store.Reader, next *startSnapshot, touched map[string]bool) error {
		if prev.pending == nil || next.definitionSHA != prev.definitionSHA {
			var err error
			bases, err = refreshTrunk(ctx, root, next.def)
			if err != nil {
				return err
			}
		}
		if docs.questions == nil || designDocsChanged(prev.itemsByID, next.itemsByID, touched) {
			docsChanged = docs.refreshDocuments(ctx, root, cmd, index, next.itemsByID) || docsChanged
		}
		if docsChanged || bases.tip != prev.tip || bases.green != prev.green {
			touched = nil
		}
		return next.repairEntriesAt(ctx, r, at, policy, tick, docs.questions, prev, touched, &bases)
	})
	if err == nil {
		s.docs = docs
	}
	return next, err
}

type startBases struct{ tip, green string }

func refreshTrunk(ctx context.Context, root string, def definition.Definition) (startBases, error) {
	if root == "" || def.Completes != "landing" {
		return startBases{}, nil
	}
	tip, err := heavyGitOutput(ctx, root, "rev-parse", TrunkRef)
	if err != nil {
		return startBases{}, err
	}
	policy, _, err := ReadGatePolicy(root)
	if err != nil {
		return startBases{}, err
	}
	green, err := provedLaneBaseAt(ctx, root, policy.LaneBase, tip)
	return startBases{tip: tip, green: green}, err
}

func designDocsChanged(prev, next map[string]itemRow, touched map[string]bool) bool {
	changed := func(id string) bool { return prev[id].DesignDoc != next[id].DesignDoc }
	if touched != nil {
		for id := range touched {
			if changed(id) {
				return true
			}
		}
		return false
	}
	for id := range prev {
		if changed(id) {
			return true
		}
	}
	for id := range next {
		if changed(id) {
			return true
		}
	}
	return false
}

type startDocumentInputs struct {
	stats     map[string]fileStatSnapshot
	indexStat fs.FileInfo
	indexHash [sha256.Size]byte // includes our own writes, to ignore their echo
	questions *VantageQuestions
	retry     bool
	lazy      bool // no document needed an index at seed time
}

func (d *startDocumentInputs) refreshDocuments(ctx context.Context, root string, cmd []string, path string, items map[string]itemRow) bool {
	stats := collectDocStats(root, items)
	index, _ := os.Stat(path)
	first := d.questions == nil
	edited := !first && !maps.Equal(stats, d.stats)
	indexChanged := !sameIndexStat(d.indexStat, index)
	// Record what was observed before reading/scanning, not after: changes
	// that race this refresh must still be visible on the next wake.
	d.stats, d.indexStat = stats, index
	if !edited && index != nil && (first || indexChanged) {
		if raw, err := os.ReadFile(path); err == nil {
			sum := sha256.Sum256(raw)
			if !first && !d.retry && sum == d.indexHash {
				return false
			}
			idx := vantage.ParseIndex(raw, path)
			if _, err := idx.Questions(""); err == nil {
				d.questions = vantage.Preloaded(root, idx)
				d.indexHash, d.retry, d.lazy = sum, false, false
				return true
			}
		}
	}
	needsIndex := false
	if first || d.lazy {
		for _, row := range items {
			if row.DesignDoc != "" {
				needsIndex = true
				break
			}
		}
	}
	if !first && !edited && !indexChanged && !d.retry && !(d.lazy && needsIndex) {
		return false
	}
	if first && !needsIndex && index == nil {
		d.questions = &VantageQuestions{Root: root, Command: cmd, Context: ctx}
		d.lazy = true
		return true
	}
	idx, raw, err := scanQuestionIndex(ctx, root, cmd)
	d.questions = vantage.Preloaded(root, idx)
	d.retry, d.lazy = err != nil, false
	if err == nil {
		d.indexHash = sha256.Sum256(raw)
		if path != "" {
			_ = writeQuestionsIndexAtomic(path, raw)
		}
	}
	return true
}

func sameIndexStat(a, b fs.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func scanQuestionIndex(ctx context.Context, root string, cmd []string) (VantageIndex, []byte, error) {
	raw, err := vantage.RunIndexCommand(ctx, root, cmd)
	if err != nil {
		return vantage.IndexError(err), nil, err
	}
	idx := vantage.ParseIndex(raw, vantage.IndexCommandName(cmd))
	_, err = idx.Questions("")
	return idx, raw, err
}

type fileStatSnapshot struct {
	modTime, size int64
	exists        bool
}

func statFile(path string) fileStatSnapshot {
	if info, err := os.Stat(path); err == nil {
		return fileStatSnapshot{info.ModTime().UnixNano(), info.Size(), true}
	}
	return fileStatSnapshot{}
}

func collectDocStats(root string, items map[string]itemRow) map[string]fileStatSnapshot {
	stats := map[string]fileStatSnapshot{}
	for _, row := range items {
		if row.DesignDoc != "" {
			path := plannerDocumentKey(root, row.DesignDoc)
			stats[path] = statFile(path)
		}
	}
	if root != "" {
		_ = filepath.WalkDir(filepath.Join(root, "docs"), func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".md") {
				if _, ok := stats[path]; !ok {
					stats[path] = statFile(path)
				}
			}
			return nil
		})
	}
	return stats
}
