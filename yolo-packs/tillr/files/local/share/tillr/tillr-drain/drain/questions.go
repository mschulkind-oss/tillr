package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/mschulkind-oss/tillr/vantage"
)

// The planner's question reader lives in .../vantage, not here, because the board and the
// planner must answer "what is still open" from one reader: a second parser beside this one
// is a second answer, and documents that number their questions already produced one
// disagreement. The aliases below keep the planner's own vocabulary — `Question`,
// `QuestionSource`, `VantageQuestions` — reading here as it always has, while the code that
// runs `vantage-check index --format json` and parses it has one home.

// Question is one question Vantage's index reports for a design document.
type Question = vantage.Question

// DocumentQuestions is one design document's answer: whether the tree holds it and what the
// index reports it asks.
type DocumentQuestions = vantage.DocumentQuestions

// QuestionSource answers, for a design document an item names, whether the tree holds it and
// what questions it carries.
type QuestionSource = vantage.QuestionSource

// VantageQuestions is the production QuestionSource: the tree decides whether a document is
// there, and Vantage's index decides what it asks.
type VantageQuestions = vantage.VantageQuestions

// VantageIndex is what `vantage-check index --format json` reported for one tree.
type VantageIndex = vantage.VantageIndex

// QuestionAnswered is the one state that settles a question.
const QuestionAnswered = vantage.QuestionAnswered

// IndexTimeout bounds the index command.
const IndexTimeout = vantage.IndexTimeout

// DefaultIndexCommand is the program that answers Vantage's index.
var DefaultIndexCommand = vantage.DefaultIndexCommand

// ReadVantageIndex runs the index command over root and answers the questions it reports.
func ReadVantageIndex(ctx context.Context, root string, command []string) VantageIndex {
	return vantage.ReadVantageIndex(ctx, root, command)
}

// firstUnanswered answers the first question in the document's own order that is not
// settled, and whether there is one.
func firstUnanswered(questions []Question) (Question, bool) {
	return vantage.FirstUnanswered(questions)
}

func withoutQuestions(questions []Question, ids []string) []Question {
	return vantage.WithoutQuestions(questions, ids)
}

// QuestionsIndexFile is where the loop saves its last valid Vantage index for reuse at the
// same trunk SHA.
func QuestionsIndexFile(root string) string {
	return filepath.Join(LoopDir(root), "questions-index.json")
}

type savedQuestionsIndex struct {
	TrunkSHA string          `json:"trunk_sha"`
	Command  string          `json:"command,omitempty"`
	Index    json.RawMessage `json:"index"`
}

// LoadSavedQuestionsIndex reads the loop's saved question index when it matches trunkSHA
// (and command, when requireCommandMatch is true) and parses as a valid index.
func LoadSavedQuestionsIndex(root, trunkSHA string, command []string, requireCommandMatch bool) (*VantageQuestions, bool) {
	if trunkSHA == "" {
		return nil, false
	}
	path := QuestionsIndexFile(root)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var saved savedQuestionsIndex
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, false
	}
	if saved.TrunkSHA == "" || saved.TrunkSHA != trunkSHA {
		return nil, false
	}
	if requireCommandMatch && saved.Command != vantage.IndexCommandName(command) {
		return nil, false
	}
	idx := vantage.ParseIndex(saved.Index, path)
	if _, err := idx.Questions(""); err != nil {
		return nil, false
	}
	return vantage.Preloaded(root, idx), true
}

// SaveQuestionsIndex atomically writes a valid question index beside the loop's records,
// tagged with the trunk SHA it was read at.
func SaveQuestionsIndex(root, trunkSHA string, command []string, raw []byte) error {
	if trunkSHA == "" {
		return nil
	}
	idx := vantage.ParseIndex(raw, vantage.IndexCommandName(command))
	if _, err := idx.Questions(""); err != nil {
		return err
	}
	payload, err := json.Marshal(savedQuestionsIndex{
		TrunkSHA: trunkSHA,
		Command:  vantage.IndexCommandName(command),
		Index:    json.RawMessage(raw),
	})
	if err != nil {
		return err
	}
	return writeQuestionsIndexAtomic(QuestionsIndexFile(root), payload)
}

func writeQuestionsIndexAtomic(path string, payload []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "questions-index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

type savedVantageQuestions struct {
	root     string
	trunkSHA string
	command  []string
	ctx      context.Context

	mu     sync.Mutex
	loaded *VantageQuestions
}

func newSavedQuestions(root, trunkSHA string, command []string, ctx context.Context) QuestionSource {
	if trunkSHA == "" {
		return &VantageQuestions{Root: root, Command: command, Context: ctx}
	}
	return &savedVantageQuestions{root: root, trunkSHA: trunkSHA, command: command, ctx: ctx}
}

func (s *savedVantageQuestions) RootDir() string { return s.root }

func (s *savedVantageQuestions) DocumentPresence(path string) (DocumentQuestions, error) {
	return (&VantageQuestions{Root: s.root}).DocumentPresence(path)
}

func (s *savedVantageQuestions) ensure() *VantageQuestions {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded != nil {
		return s.loaded
	}
	if preloaded, ok := LoadSavedQuestionsIndex(s.root, s.trunkSHA, s.command, true); ok {
		s.loaded = preloaded
		return s.loaded
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, err := vantage.RunIndexCommand(ctx, s.root, s.command)
	if err != nil {
		s.loaded = vantage.Preloaded(s.root, vantage.IndexError(err))
		return s.loaded
	}
	idx := vantage.ParseIndex(raw, vantage.IndexCommandName(s.command))
	if _, perr := idx.Questions(""); perr == nil {
		_ = SaveQuestionsIndex(s.root, s.trunkSHA, s.command, raw)
	}
	s.loaded = vantage.Preloaded(s.root, idx)
	return s.loaded
}

func (s *savedVantageQuestions) Document(path string) (DocumentQuestions, error) {
	present, err := s.DocumentPresence(path)
	if err != nil || !present.Present {
		return present, err
	}
	return s.ensure().Document(path)
}

func (s *savedVantageQuestions) Done(path string) (bool, error) {
	return s.ensure().Done(path)
}

func (s *savedVantageQuestions) syncIndexHint() ([]string, vantage.VantageIndex, bool) {
	if s == nil {
		return nil, vantage.VantageIndex{}, false
	}
	s.mu.Lock()
	loaded := s.loaded
	cmd := s.command
	s.mu.Unlock()
	if loaded != nil {
		idx, ok := loaded.LoadedIndex()
		return cmd, idx, ok
	}
	return cmd, vantage.VantageIndex{}, false
}
