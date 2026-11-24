package drain

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/settings"
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
	// The incremental fold never consumes optimistic loop notes. Only the
	// published copy receives those; the next store read reconciles them.
	var previous startSnapshot
	stop := view.watch(ctx, interval, func(ctx context.Context) startWatcher {
		paths := startWatchPaths{root: root, store: tick.StorePath, index: index}
		if root != "" && !l.nonLanding {
			gitDir, _ := l.git(ctx, root, "rev-parse", "--absolute-git-dir")
			commonDir, _ := l.git(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
			paths.gitDir, paths.commonDir = strings.TrimSpace(gitDir), strings.TrimSpace(commonDir)
		}
		return newStartWatcher(paths)
	}, func(ctx context.Context, change startWatchChange) (startSnapshot, error) {
		if lanes := l.lanes(); !slices.Equal(lanes, *tick.AvailableLanes) {
			tick.AvailableLanes = &lanes
			previous = startSnapshot{} // Rebuild entries, including lane-sensitive holds.
		}
		next, err := refresh.read(ctx, l.cfg.Now().UTC(), l.currentPolicy(), tick, root, cmd, index, previous, change)
		if err == nil {
			previous = next
		}
		return next, err
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
	if inspect && prev.pending != nil {
		var err error
		bases, err = refreshTrunk(ctx, root, prev.def)
		if err != nil {
			return prev, err
		}
	}
	var documents map[string]itemRow
	prepare := func(reader *store.Store) error {
		// Read only the document references here. In particular, the initial
		// index and the first index after a document is filed must not hold a
		// WAL snapshot across the indexer's filesystem walk or subprocess.
		var err error
		documents, err = readStartDesignDocs(ctx, reader)
		if err != nil {
			return err
		}
		if inspect || docs.questions == nil || designDocsChanged(documents, prev.itemsByID) {
			docsChanged = docs.refreshDocuments(ctx, root, cmd, index, documents)
		}
		return nil
	}
	tick.Now = at.Format(time.RFC3339Nano)
	next, err := s.store.syncStoreWithRefresh(ctx, tick.StorePath, at, policy, prev, inspect, prepare, func(r *store.Reader, next *startSnapshot, touched map[string]bool) (func() error, error) {
		if designDocsChanged(documents, next.itemsByID) {
			// A concurrent filing changed the input to the indexer. Leave the
			// published proposals alone until the watcher's next ordinary wake.
			return nil, errors.New("design document references changed during start-view refresh")
		}
		needTrunk := prev.pending == nil || next.definitionSHA != prev.definitionSHA
		if docsChanged || (!needTrunk && (bases.tip != prev.tip || bases.green != prev.green)) {
			touched = nil
		}
		if err := next.stageRepairInputs(r, at, policy, tick, docs.questions.Root, prev, touched, &bases, needTrunk); err != nil {
			return nil, err
		}
		return func() error {
			if needTrunk {
				var err error
				bases, err = refreshTrunk(ctx, root, next.def)
				if err != nil {
					return err
				}
				if bases.tip != prev.tip || bases.green != prev.green {
					touched = nil
				}
			}
			return next.repairEntriesAt(ctx, nil, at, policy, tick, docs.questions, prev, touched, &bases)
		}, nil
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

func readStartDesignDocs(ctx context.Context, reader *store.Store) (map[string]itemRow, error) {
	docs := map[string]itemRow{}
	err := reader.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id, design_doc FROM items WHERE design_doc <> ''`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, doc string
			if err := rows.Scan(&id, &doc); err != nil {
				return err
			}
			docs[id] = itemRow{DesignDoc: doc}
		}
		return rows.Err()
	})
	return docs, err
}

// docs contains only rows with nonempty design documents.
func designDocsChanged(docs, items map[string]itemRow) bool {
	for id, row := range docs {
		if items[id].DesignDoc != row.DesignDoc {
			return true
		}
	}
	for id, row := range items {
		if docs[id].DesignDoc != row.DesignDoc {
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
		cfgPath := filepath.Join(root, ".tillr.toml")
		stats[cfgPath] = statFile(cfgPath)
		if cfg, err := settings.Read(root); err == nil {
			for _, rel := range cfg.Sync.Roadmaps {
				full := plannerDocumentKey(root, rel)
				stats[full] = statFile(full)
			}
		}
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
