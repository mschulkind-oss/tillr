package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/stop"
	"github.com/mschulkind-oss/tillr/record"
)

// The layer-A record files one drain tick leaves in `.tillr/loop/<run-id>/`, and the
// exchange each one records: the program that wrote it, and the program that reads it.
// A kind absent here is absent from the observatory rather than guessed at.
type exchangeFile struct {
	Kind     string // the artifact kind the file is also reachable as
	Name     string // the file name, or a glob when FileGlob is set
	FileGlob bool
	Caller   string
	Callee   string
	Channel  string
}

// The channel spellings design §4.1 closes: a hand-off happens over an argv, a file the
// callee reads, a record on a stream, a store transaction, or a subprocess boundary.
const (
	channelArgv         = "argv"
	channelFileRecord   = "file_record"
	channelStdoutRecord = "stdout_record"
	channelSQLiteTx     = "sqlite_tx"
	channelJSONLTurn    = "jsonl_turn"
	channelJSONLTool    = "jsonl_tool"
	channelSubprocess   = "subprocess"
	channelGitTrace2    = "git_trace2"
)

// exchangeFiles is the Layer A set, in the order a tick produces them.
var exchangeFiles = []exchangeFile{
	{Kind: "gate.change", Name: "gate.change.json", Caller: "tillr-loop", Callee: "tillr-gate", Channel: channelFileRecord},
	{Kind: "gate.plan", Name: "gate.plan.json", Caller: "tillr-gate", Callee: "tillr-loop", Channel: channelFileRecord},
	{Kind: "gate.verdict", Name: "gate.verdict.json", Caller: "tillr-gate", Callee: "tillr-loop", Channel: channelStdoutRecord},
	{Kind: "land.candidate", Name: "land.candidate.json", Caller: "tillr-land", Callee: "tillr-loop", Channel: channelFileRecord},
	{Kind: "item.verdict", Name: "item.verdict.*.json", FileGlob: true, Caller: "tillr-item", Callee: "tillr-loop", Channel: channelFileRecord},
	{Kind: "request", Name: "request.json", Caller: "tillr-drain", Callee: "tillr-run", Channel: channelArgv},
}

// artifactKinds is the closed set the artifact endpoint answers. A kind outside it is
// a caller's mistake (400); a kind inside it that no writer produced is a 200 saying so.
var artifactKinds = map[string]string{
	"brief":          "brief.md",
	"fence":          "fence.txt",
	"base_sha":       "base_sha",
	"request":        "request.json",
	"gate.change":    "gate.change.json",
	"gate.plan":      "gate.plan.json",
	"gate.verdict":   "gate.verdict.json",
	"land.candidate": "land.candidate.json",
	"land.attempt":   "land.attempt.json",
}

// recordedArtifact is one file as the observatory found it: its bytes, its provenance
// and the epistemic state that provenance supports.
type recordedArtifact struct {
	Path           string  `json:"path"`
	SHA256         string  `json:"sha256"`
	SizeBytes      int64   `json:"size_bytes"`
	MTimeMS        int64   `json:"mtime_ms"`
	EpistemicState string  `json:"epistemic_state"`
	ClobberWarning *string `json:"clobber_warning,omitempty"`
}

// artifactPayload is the artifact endpoint's answer for one kind. Present is false when
// no writer produced it, and Note says which writers were looked for: an absent brief
// before OBS-1 lands is a fact about the emitters, not a client error.
type artifactPayload struct {
	Kind           string             `json:"kind"`
	Present        bool               `json:"present"`
	Path           string             `json:"path,omitempty"`
	SHA256         string             `json:"sha256,omitempty"`
	SizeBytes      int64              `json:"size_bytes,omitempty"`
	MTimeMS        *int64             `json:"mtime_ms,omitempty"`
	EpistemicState string             `json:"epistemic_state"`
	ClobberWarning *string            `json:"clobber_warning,omitempty"`
	ContentType    string             `json:"content_type,omitempty"`
	Body           *string            `json:"body,omitempty"`
	Files          []recordedArtifact `json:"files,omitempty"`
	Note           string             `json:"note,omitempty"`
	SearchedPaths  []string           `json:"searched_paths,omitempty"`
}

// readArtifact reads one file and its provenance. A file that is not there answers an
// absent artifact naming where it was looked for; a file that is there and cannot be
// read answers unreadable, which is a different fact and a different remedy.
func readArtifact(kind, path, contentType string, withBody bool) artifactPayload {
	p := artifactPayload{Kind: kind, Path: path, ContentType: contentType, SearchedPaths: []string{path}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			p.EpistemicState = "absent"
			p.Note = "no writer produced this file at this path"
			return p
		}
		p.EpistemicState = stateUnreadable
		p.Note = err.Error()
		return p
	}
	info, statErr := os.Stat(path)
	p.Present = true
	sum := sha256.Sum256(raw)
	p.SHA256 = hex.EncodeToString(sum[:])
	p.SizeBytes = int64(len(raw))
	if statErr == nil {
		ms := info.ModTime().UnixMilli()
		p.MTimeMS = &ms
		// The clock is the file's mtime, not a recorded instant: the writers of these
		// records do not stamp them, so any time read here is inferred.
		p.EpistemicState = stateDerivedClock
	} else {
		p.EpistemicState = stateObserved
	}
	if withBody {
		body := string(raw)
		p.Body = &body
	}
	return p
}

// runDir is the directory one tick's record files live in.
func (obs *observations) runDir(runID string) string {
	return filepath.Join(obs.loopDir, runID)
}

// loopResultKind is the record a loop's outcome is written as, at
// <root>/.tillr/loop/drain-<stamp>.json (tillr-drain's LoopResultKind). It is spelled
// here for the same reason assemble.go spells the other record kinds: the reader must not
// link the writer's package to read its records.
const loopResultKind = "drain.loop"

// drainBuild is the build a loop result names: the same stamp tillr-drain writes, so the
// board can say which drain build produced the record it is reading. It is declared here
// rather than imported, for the reason loopResultKind is: a reader must not link the
// writer's package to read its records.
type drainBuild struct {
	Revision   string `json:"revision"`
	Time       string `json:"time"`
	Modified   bool   `json:"modified"`
	Digest     string `json:"digest"`
	Executable string `json:"executable"`
}

// named reports whether the build carries an identity at all. A record written before the
// stamp existed, or one whose stamp is empty, is a build that cannot be named; it is not a
// build with nothing to say, and the board draws the two differently.
func (b *drainBuild) named() bool {
	return b != nil && (b.Revision != "" || b.Digest != "")
}

type repeatedStop struct {
	Count     int    `json:"count"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail"`
	ItemID    string `json:"item_id"`
	Step      string `json:"step"`
	FirstAtMS int64  `json:"first_at_ms"`
	LastAtMS  int64  `json:"last_at_ms"`
	Sentence  string `json:"sentence"`
	Next      string `json:"next"`
}

// loopResult is the newest drain.loop record under the loop directory: the record, the
// path it was read from, its stop reason, and the reason it could not be read when it
// could not. It is one value rather than four returns so a reader that needs the record's
// clock (the running face's stale test) and a reader that needs only its reason cannot
// read two different files.
type loopResult struct {
	Path        string
	Env         record.Envelope
	Stop        string
	Explanation *stopExplanation
	Build       *drainBuild
	Note        string
	BuildNote   string
}

// newestLoopResult reads the newest loop result under the loop directory. Applied
// loops use numeric generation ranges; legacy timestamp reports and startup faults
// have no comparable generation, so their file modification times order them.
// A file that is absent, unreadable, not a record, or a record of another kind answers an
// absence with a reason and never a guessed stop or build.
func loopGenerationRange(path string) (first, last uint64, ok bool) {
	name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "drain-"), ".json")
	a, b, found := strings.Cut(name, "-")
	if !found {
		return 0, 0, false
	}
	first, errA := strconv.ParseUint(a, 10, 64)
	last, errB := strconv.ParseUint(b, 10, 64)
	return first, last, errA == nil && errB == nil
}

// newestLoopResultPath compares applied ranges numerically before comparing the
// winning applied report with legacy reports and startup faults by file time.
// Selecting separately avoids a non-transitive sort when file time and generation
// order disagree. Unreadable selected files still report their error, never a fallback.
func newestLoopResultPath(paths []string) string {
	applied, other := "", ""
	var first, last uint64
	newerFile := func(a, b string) bool {
		if b == "" {
			return true
		}
		infoA, errA := os.Stat(a)
		infoB, errB := os.Stat(b)
		if errA == nil && errB == nil && !infoA.ModTime().Equal(infoB.ModTime()) {
			return infoA.ModTime().After(infoB.ModTime())
		}
		return a > b
	}
	for _, path := range paths {
		a, b, ok := loopGenerationRange(path)
		if ok && b > 0 {
			if applied == "" || b > last || (b == last && a > first) {
				applied, first, last = path, a, b
			}
		} else if newerFile(path, other) {
			other = path
		}
	}
	if applied == "" {
		return other
	}
	if other == "" || newerFile(applied, other) {
		return applied
	}
	return other
}

func (obs *observations) newestLoopResult() loopResult {
	matches, err := filepath.Glob(filepath.Join(obs.loopDir, "drain-*.json"))
	if err != nil {
		return loopResult{
			Note:      "the loop directory could not be read: " + err.Error(),
			BuildNote: "the loop directory could not be read",
		}
	}
	if len(matches) == 0 {
		return loopResult{
			Note:      "no drain result file has been written under the loop directory",
			BuildNote: "no drain result file names a build",
		}
	}
	path := newestLoopResultPath(matches)
	raw, err := os.ReadFile(path)
	if err != nil {
		return loopResult{Path: path,
			Note:      "the loop result file could not be read: " + err.Error(),
			BuildNote: "the loop result file could not be read",
		}
	}
	env, err := record.Decode(raw)
	if err != nil {
		return loopResult{Path: path,
			Note:      path + " is not a decodable record: " + err.Error(),
			BuildNote: "the newest loop result is not a decodable record",
		}
	}
	if env.Kind != loopResultKind {
		return loopResult{Path: path,
			Note:      path + " is a " + env.Kind + " record, not " + loopResultKind,
			BuildNote: "the newest loop result is not a " + loopResultKind + " record",
		}
	}
	var payload struct {
		Stop        string           `json:"stop"`
		Detail      string           `json:"detail"`
		ItemID      string           `json:"stop_item_id"`
		Step        string           `json:"stop_step"`
		Explanation *stopExplanation `json:"stop_explanation"`
		Build       *drainBuild      `json:"build"`
	}
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return loopResult{Path: path,
			Note:      path + " carries a payload this face cannot read: " + err.Error(),
			BuildNote: "the loop result payload could not be read",
		}
	}
	out := loopResult{Path: path, Env: env, Stop: payload.Stop, Explanation: payload.Explanation, Build: payload.Build}
	// Legacy records already carry a cause and creation time. Keep those facts,
	// and name the missing item/step rather than borrowing the newest decision.
	if out.Explanation == nil && payload.Stop != "" && payload.Detail != "" {
		step := payload.Step
		if step == "" {
			step = "not recorded (older loop result)"
		}
		out.Explanation = stop.Explain(payload.Stop, payload.Detail, payload.ItemID, step, env.CreatedAt)
	}
	if !out.Build.named() {
		out.BuildNote = "the loop result names no build, so the board cannot vouch for the drain that wrote it"
	}
	if out.Stop == "" {
		out.Note = path + " names no stop reason"
	}
	return out
}

// readLoopResult answers the reason the loop last stopped and which build wrote the record.
// The build note is the empty string only when the record names a build. A record written
// before the stamp existed names none, and that absence is reported as the reason the board
// cannot vouch for the drain, not as an empty stamp a client would render as current.
func (obs *observations) readLoopResult() (reason, note string, build *drainBuild, buildNote string) {
	r := obs.newestLoopResult()
	return r.Stop, r.Note, r.Build, r.BuildNote
}

func isCleanLoopStop(stopReason string) bool {
	return stopReason == "queue-drained" || stopReason == "operator-stop"
}

func normalizeRepeatedDetail(detail string, expl *stopExplanation) string {
	s := strings.Join(strings.Fields(detail), " ")
	if s == "" && expl != nil {
		s = strings.Join(strings.Fields(expl.Sentence), " ")
	}
	return s
}

func parseZeroGenArchive(path string) (isZeroGen, isCurrent bool, archiveSeq uint64, hasSeq bool) {
	base := filepath.Base(path)
	if base == "drain-0-0.json" {
		return true, true, 0, false
	}
	if strings.HasPrefix(base, "drain-0-0-") && strings.HasSuffix(base, ".json") {
		mid := strings.TrimSuffix(strings.TrimPrefix(base, "drain-0-0-"), ".json")
		seq, err := strconv.ParseUint(mid, 10, 64)
		return true, false, seq, err == nil
	}
	return false, false, 0, false
}

// repeatedLoopStop scans drain-*.json (drain.loop) records chronologically (oldest to newest)
// and returns a repeatedStop account when the trailing streak of abnormal stops sharing the
// same cause has Count >= 2. A clean stop (queue-drained or operator-stop) resets the streak.
func (obs *observations) repeatedLoopStop() *repeatedStop {
	if obs == nil || obs.loopDir == "" {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(obs.loopDir, "drain-*.json"))
	if err != nil || len(matches) == 0 {
		return nil
	}
	type loopEntry struct {
		base        string
		modTime     time.Time
		firstGen    uint64
		lastGen     uint64
		hasGen      bool
		isZeroGen   bool
		isCurrent00 bool
		archiveSeq  uint64
		hasArchSeq  bool
		createdAt   time.Time
		stop        string
		detail      string
		itemID      string
		step        string
		explanation *stopExplanation
	}
	entries := make([]loopEntry, 0, len(matches))
	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		env, err := record.Decode(raw)
		if err != nil || env.Kind != loopResultKind {
			continue
		}
		var payload struct {
			Stop        string           `json:"stop"`
			Detail      string           `json:"detail"`
			ItemID      string           `json:"stop_item_id"`
			Step        string           `json:"stop_step"`
			Explanation *stopExplanation `json:"stop_explanation"`
		}
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			continue
		}
		var modTime time.Time
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime()
		}
		createdAt := env.CreatedAt
		if createdAt.IsZero() && payload.Explanation != nil && payload.Explanation.AtMS > 0 {
			createdAt = time.UnixMilli(payload.Explanation.AtMS).UTC()
		}
		if createdAt.IsZero() {
			createdAt = modTime.UTC()
		}
		expl := payload.Explanation
		if expl == nil && payload.Stop != "" {
			step := payload.Step
			if step == "" {
				step = "not recorded (older loop result)"
			}
			expl = stop.Explain(payload.Stop, payload.Detail, payload.ItemID, step, createdAt)
		}
		firstGen, lastGen, okGen := loopGenerationRange(path)
		isZero, isCurrent00, archSeq, hasArchSeq := parseZeroGenArchive(path)
		entries = append(entries, loopEntry{
			base:        filepath.Base(path),
			modTime:     modTime,
			firstGen:    firstGen,
			lastGen:     lastGen,
			hasGen:      okGen && lastGen > 0,
			isZeroGen:   isZero,
			isCurrent00: isCurrent00,
			archiveSeq:  archSeq,
			hasArchSeq:  hasArchSeq,
			createdAt:   createdAt,
			stop:        payload.Stop,
			detail:      payload.Detail,
			itemID:      payload.ItemID,
			step:        payload.Step,
			explanation: expl,
		})
	}
	if len(entries) < 2 {
		return nil
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.hasGen && b.hasGen {
			if a.lastGen != b.lastGen {
				return a.lastGen < b.lastGen
			}
			if a.firstGen != b.firstGen {
				return a.firstGen < b.firstGen
			}
		}
		if a.isZeroGen && b.isZeroGen {
			if a.isCurrent00 != b.isCurrent00 {
				return !a.isCurrent00
			}
			if !a.isCurrent00 && !b.isCurrent00 && a.hasArchSeq && b.hasArchSeq && a.archiveSeq != b.archiveSeq {
				return a.archiveSeq < b.archiveSeq
			}
		}
		if !a.createdAt.Equal(b.createdAt) {
			return a.createdAt.Before(b.createdAt)
		}
		if !a.modTime.Equal(b.modTime) {
			return a.modTime.Before(b.modTime)
		}
		return a.base < b.base
	})

	last := entries[len(entries)-1]
	if last.stop == "" || isCleanLoopStop(last.stop) {
		return nil
	}
	wantDetail := normalizeRepeatedDetail(last.detail, last.explanation)
	count := 1
	firstAtMS := last.createdAt.UnixMilli()
	lastAtMS := firstAtMS
	for i := len(entries) - 2; i >= 0; i-- {
		cur := entries[i]
		if cur.stop == "" || isCleanLoopStop(cur.stop) {
			break
		}
		if cur.stop != last.stop || normalizeRepeatedDetail(cur.detail, cur.explanation) != wantDetail {
			break
		}
		count++
		atMS := cur.createdAt.UnixMilli()
		if atMS < firstAtMS {
			firstAtMS = atMS
		}
		if atMS > lastAtMS {
			lastAtMS = atMS
		}
	}
	if count < 2 {
		return nil
	}
	itemID := last.itemID
	step := last.step
	sentence := wantDetail
	next := ""
	if last.explanation != nil {
		if last.explanation.ItemID != "" {
			itemID = last.explanation.ItemID
		}
		if last.explanation.Step != "" {
			step = last.explanation.Step
		}
		if last.explanation.Sentence != "" {
			sentence = last.explanation.Sentence
		}
		next = last.explanation.Next
	}
	if next == "" {
		next = "Owner resolves the failure described above, then restarts with tillr-drain loop."
	}
	return &repeatedStop{
		Count:     count,
		Reason:    last.stop,
		Detail:    wantDetail,
		ItemID:    itemID,
		Step:      step,
		FirstAtMS: firstAtMS,
		LastAtMS:  lastAtMS,
		Sentence:  sentence,
		Next:      next,
	}
}

// observations is the on-disk evidence beside the store: where a run's record files and
// preserved staging inputs are, and the reader of each run's recorded streams.
type observations struct {
	loopDir  string
	laneRoot string
	trace    *traceReader
}

// readRunArtifact answers one artifact kind for one run. The kinds OBS-1 preserves
// (brief, fence, base_sha) are looked for under the run's own directory first and the
// lane directory second, because until OBS-1 lands the lane copy is the only one there
// is — and a lane copy is overwritten by the next tick, which is a clobber warning
// rather than a silent substitution.
func (obs *observations) readRunArtifact(runID, kind string, run storeRun, withBody bool) artifactPayload {
	if kind == "turn" || kind == "tool" {
		return obs.readTraceArtifact(runID, kind)
	}
	if kind == "item.verdict" {
		return obs.readVerdictArtifacts(runID)
	}
	if kind == "gate.log" {
		dir := filepath.Join(obs.runDir(runID), "logs")
		return readArtifact(kind, dir, "inode/directory", false)
	}
	if kind == "diff" {
		// No writer produces a diff for a run: the change is a tree in git, and the
		// store records its sha. Saying so is the answer.
		p := artifactPayload{Kind: kind, EpistemicState: "absent",
			Note: "no writer preserves a run's diff; the store records the candidate's head_sha and tree_sha"}
		return p
	}
	name, known := artifactKinds[kind]
	if !known {
		return artifactPayload{Kind: kind, EpistemicState: "absent", Note: "unknown artifact kind"}
	}
	staged := kind == "brief" || kind == "fence" || kind == "base_sha"
	primary := filepath.Join(obs.runDir(runID), name)
	p := readArtifact(kind, primary, contentTypeOf(name), withBody)
	if p.Present {
		return p
	}
	if !staged || run.LaneID == "" || obs.laneRoot == "" {
		return p
	}
	// The lane copy: the same staging inputs, in the directory the next tick reuses.
	laneCopy := filepath.Join(obs.laneRoot, run.LaneID, name)
	p = readArtifact(kind, laneCopy, contentTypeOf(name), withBody)
	if !p.Present {
		p.SearchedPaths = []string{primary, laneCopy}
		return p
	}
	p.SearchedPaths = []string{primary, laneCopy}
	warning := "this is the lane's copy, not a preserved one: the next tick for this lane overwrites it (OBS-1 preserves the per-run copy)"
	p.ClobberWarning = &warning
	p.Note = "the per-run copy is absent; the lane copy is the only one there is"
	return p
}

// readVerdictArtifacts answers the item verdict records: one file per criterion, so the
// kind is a set rather than a single file.
func (obs *observations) readVerdictArtifacts(runID string) artifactPayload {
	dir := obs.runDir(runID)
	matches, err := filepath.Glob(filepath.Join(dir, "item.verdict.*.json"))
	p := artifactPayload{Kind: "item.verdict", SearchedPaths: []string{filepath.Join(dir, "item.verdict.*.json")}}
	if err != nil {
		p.EpistemicState = stateUnreadable
		p.Note = err.Error()
		return p
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		p.EpistemicState = "absent"
		p.Note = "no item.verdict record was written for this run"
		return p
	}
	p.Present = true
	p.EpistemicState = stateDerivedClock
	for _, path := range matches {
		one := readArtifact("item.verdict", path, "application/json", false)
		if !one.Present {
			continue
		}
		file := recordedArtifact{
			Path: one.Path, SHA256: one.SHA256, SizeBytes: one.SizeBytes,
			EpistemicState: one.EpistemicState,
		}
		if one.MTimeMS != nil {
			file.MTimeMS = *one.MTimeMS
		}
		p.Files = append(p.Files, file)
	}
	return p
}

// readTraceArtifact answers a turn or tool kind from the run's own log, which tillr-trace
// reads on demand. With no tillr-trace program the answer says so.
func (obs *observations) readTraceArtifact(runID, kind string) artifactPayload {
	p := artifactPayload{Kind: kind, SearchedPaths: []string{obs.trace.bin}}
	if !obs.trace.available() {
		p.EpistemicState = "absent"
		p.Note = obs.trace.note
		return p
	}
	kinds := []string{"llm.turn"}
	if kind == "tool" {
		kinds = []string{"tool.call"}
	}
	read, err := obs.trace.run(runID)
	events := read.Events
	if err != nil {
		p.EpistemicState = stateUnreadable
		p.Note = err.Error()
		return p
	}
	var kept []map[string]any
	for _, e := range events {
		for _, want := range kinds {
			if e.Kind == want {
				kept = append(kept, map[string]any{
					"obs_id": e.ObsID, "kind": e.Kind, "ts_start": e.TSStart, "ts_end": e.TSEnd,
					"duration_ms": e.DurationMS, "summary": e.Summary, "payload": json.RawMessage(e.Payload),
				})
			}
		}
	}
	if len(kept) == 0 {
		p.EpistemicState = "absent"
		p.Note = "the run's recorded artifacts hold no " + strings.Join(kinds, "/") + " row"
		return p
	}
	encoded, err := json.MarshalIndent(kept, "", " ")
	if err != nil {
		p.EpistemicState = stateUnreadable
		p.Note = err.Error()
		return p
	}
	body := string(encoded)
	p.Present = true
	p.EpistemicState = stateObserved
	p.ContentType = "application/json"
	p.Body = &body
	return p
}

func contentTypeOf(name string) string {
	if strings.HasSuffix(name, ".json") {
		return "application/json"
	}
	return "text/plain; charset=utf-8"
}

// exchange is one causal hand-off between two programs in a drain tick (design §4.1).
type exchange struct {
	ExchangeID     string  `json:"exchange_id"`
	Seq            int     `json:"seq"`
	Layer          string  `json:"layer"`
	Caller         string  `json:"caller"`
	Callee         string  `json:"callee"`
	Channel        string  `json:"channel"`
	StartedAtMS    *int64  `json:"started_at_ms"`
	FinishedAtMS   *int64  `json:"finished_at_ms"`
	TSSource       string  `json:"ts_source"`
	EpistemicState string  `json:"epistemic_state"`
	ExitCode       *int    `json:"exit_code"`
	Summary        string  `json:"summary"`
	RequestRef     string  `json:"request_ref"`
	ResponseRef    string  `json:"response_ref"`
	InlinePreview  *string `json:"inline_preview"`
}

// exchanges answers the ordered inter-tool edges of one run: the Layer A record files the
// tick left, the Layer B observations tillr-trace reads from the run's artifacts, and an explicit
// unobserved_seam row for every stream no emitter wrote.
func (obs *observations) exchanges(runID string) []exchange {
	out := []exchange{}
	seq := 0
	dir := obs.runDir(runID)
	// Layer A: the records the supervisor's programs handed each other.
	for _, f := range exchangeFiles[:len(exchangeFiles)-1] {
		path := filepath.Join(dir, f.Name)
		if f.FileGlob {
			matches, _ := filepath.Glob(path)
			if len(matches) == 0 {
				continue
			}
			path = matches[len(matches)-1]
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		seq++
		ms := info.ModTime().UnixMilli()
		preview := previewOf(path)
		out = append(out, exchange{
			ExchangeID: runID + "/" + f.Kind, Seq: seq, Layer: "supervisor",
			Caller: f.Caller, Callee: f.Callee, Channel: f.Channel,
			StartedAtMS: &ms, FinishedAtMS: &ms, TSSource: "file_mtime",
			EpistemicState: stateDerivedClock, Summary: "the " + f.Kind + " record this tick wrote",
			RequestRef: path, InlinePreview: preview,
		})
	}

	// The run request: the lane's request record, when the caller kept one.
	requestPath := filepath.Join(dir, "request.json")
	if info, err := os.Stat(requestPath); err == nil {
		seq++
		ms := info.ModTime().UnixMilli()
		out = append(out, exchange{
			ExchangeID: runID + "/request", Seq: seq, Layer: "supervisor",
			Caller: "tillr-drain", Callee: "tillr-run", Channel: channelArgv,
			StartedAtMS: &ms, FinishedAtMS: &ms, TSSource: "file_mtime",
			EpistemicState: stateDerivedClock, Summary: "the run request this tick handed the runner",
			RequestRef: requestPath, InlinePreview: previewOf(requestPath),
		})
	}

	// Layer B: what the harness recorded inside the run.
	read, err := obs.trace.run(runID)
	indexed := err == nil && read.Found
	row := read.Row
	if indexed {
		for _, e := range read.Events {
			channel, layer, caller, callee := layerBOf(e.Kind)
			if channel == "" {
				continue
			}
			seq++
			start := parseEpochMS(e.TSStart)
			end := parseEpochMS(derefString(e.TSEnd))
			state := stateObserved
			if channel == channelJSONLTurn && start == nil {
				state = stateDerivedClock
			}
			out = append(out, exchange{
				ExchangeID: e.ObsID, Seq: seq, Layer: layer, Caller: caller, Callee: callee,
				Channel: channel, StartedAtMS: start, FinishedAtMS: end,
				TSSource: "obs_event", EpistemicState: state, Summary: e.Summary,
			})
		}
	}

	// The seams: a stream no emitter wrote is a row, so the gap is visible as a gap.
	for _, seam := range obs.seams(runID, row, indexed, err) {
		seq++
		seam.Seq = seq
		out = append(out, seam)
	}
	return out
}

// layerBOf maps one recorded observation kind to the exchange it represents. A kind
// with no exchange (a claim, a tick decision) answers empty.
func layerBOf(kind string) (channel, layer, caller, callee string) {
	switch kind {
	case "llm.turn":
		return channelJSONLTurn, "harness", "pi", "model"
	case "tool.call":
		return channelJSONLTool, "harness", "pi", "tool"
	case "proc.exec":
		return channelSubprocess, "harness", "bash", "subprocess"
	case "git.trace2":
		return channelGitTrace2, "harness", "git", "git-trace2"
	}
	return "", "", "", ""
}

// seams answers the unobserved seams of one run: the Layer B stream that no emitter
// wrote. Before OBS-4 lands there are always two — the subprocess and the git stream —
// and the rows say exactly that rather than rendering zero.
//
// readErr is the reader's failure, if it had one: a run tillr-trace could not read is a
// seam with the reason, not an empty run.
func (obs *observations) seams(runID string, row runIndex, indexed bool, readErr error) []exchange {
	var out []exchange
	if !obs.trace.available() {
		out = append(out, exchange{
			ExchangeID: runID + "/seam/proc", Layer: "harness", Caller: "pi", Callee: "subprocess",
			Channel: channelSubprocess, TSSource: "none", EpistemicState: stateUnobservedSeam,
			Summary: "the run's recorded streams were not read (" + obs.trace.note + "), so nothing inside bash is observable",
		}, exchange{
			ExchangeID: runID + "/seam/git2", Layer: "harness", Caller: "git", Callee: "git-trace2",
			Channel: channelGitTrace2, TSSource: "none", EpistemicState: stateUnobservedSeam,
			Summary: "the run's recorded streams were not read, so the git trace2 stream is unobserved",
		})
		return out
	}
	if !indexed {
		summary := "tillr-trace knows no record of this run: every Layer B row is unknown, not empty"
		if readErr != nil {
			summary = "tillr-trace could not read this run (" + readErr.Error() + "): every Layer B row is unknown, not empty"
		}
		out = append(out, exchange{
			ExchangeID: runID + "/seam/index", Layer: "harness", Caller: "tillr-trace", Callee: runID,
			Channel: channelSubprocess, TSSource: "none", EpistemicState: stateUnobservedSeam,
			Summary: summary,
		})
		return out
	}
	if row.ProcState == "missing" {
		out = append(out, exchange{
			ExchangeID: runID + "/seam/proc", Layer: "harness", Caller: "bash", Callee: "subprocess",
			Channel: channelSubprocess, TSSource: "none", EpistemicState: stateUnobservedSeam,
			Summary: "no proc-exec.jsonl for this run: a subprocess inside bash is unobserved until OBS-4's emitters land",
		})
	}
	if row.GitState == "missing" {
		out = append(out, exchange{
			ExchangeID: runID + "/seam/git2", Layer: "harness", Caller: "git", Callee: "git-trace2",
			Channel: channelGitTrace2, TSSource: "none", EpistemicState: stateUnobservedSeam,
			Summary: "no git-trace2.jsonl for this run: ref movement and writes are unobserved until OBS-4 wires GIT_TRACE2_EVENT",
		})
	}
	return out
}

// span is one bar of the timeline waterfall (design §4.1).
type span struct {
	FirstOutputMS  *float64 `json:"first_output_ms,omitempty"`
	OutputTPS      *float64 `json:"output_tokens_per_second,omitempty"`
	Model          *string  `json:"model,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	SpanID         string   `json:"span_id"`
	ParentSpanID   *string  `json:"parent_span_id"`
	LaneID         string   `json:"lane_id"`
	Category       string   `json:"category"`
	Label          string   `json:"label"`
	StartedAtMS    *int64   `json:"started_at_ms"`
	FinishedAtMS   *int64   `json:"finished_at_ms"`
	TSSource       string   `json:"ts_source"`
	EpistemicState string   `json:"epistemic_state"`
	CostUSD        *float64 `json:"cost_usd"`
	Status         string   `json:"status"`
	ExchangeID     *string  `json:"exchange_id"`
	CheckID        string   `json:"check_id,omitempty"`
	StartUnixNano  string   `json:"start_unix_nano,omitempty"`
	EndUnixNano    string   `json:"end_unix_nano,omitempty"`
	AttributesJSON string   `json:"attributes_json,omitempty"`
}

// timeline answers the run's ordered spans: the store's own record of the run, the
// recorded observations, the tick's record files, and one gap span per unobserved seam.
// Spans with no clock are kept and carry a null start rather than a made-up one.
func (obs *observations) timeline(run storeRun) []span {
	out := []span{}
	read, readErr := obs.trace.run(run.RunID)
	indexed := readErr == nil && read.Found
	row := read.Row
	lane := run.LaneID
	runID := run.RunID
	finished := run.FinishedAtMS
	out = append(out, span{
		SpanID: runID + "/run", LaneID: lane, Category: "stage", Label: "run " + run.State,
		StartedAtMS: &run.StartedAtMS, FinishedAtMS: &finished, TSSource: "store",
		EpistemicState: stateObserved, CostUSD: run.CostUSD, Status: run.State,
	})

	// The gate's three records are the tick's own account of the gate span.
	var gateStart, gateEnd *int64
	for _, f := range exchangeFiles {
		if f.Kind != "gate.change" && f.Kind != "gate.plan" && f.Kind != "gate.verdict" {
			continue
		}
		info, err := os.Stat(filepath.Join(obs.runDir(runID), f.Name))
		if err != nil {
			continue
		}
		ms := info.ModTime().UnixMilli()
		if gateStart == nil || ms < *gateStart {
			gateStart = &ms
		}
		if gateEnd == nil || ms > *gateEnd {
			gateEnd = &ms
		}
	}
	if gateStart != nil {
		id := runID + "/gate"
		out = append(out, span{
			SpanID: id, LaneID: lane, Category: "gate_check", Label: "gate (from the tick's records)",
			StartedAtMS: gateStart, FinishedAtMS: gateEnd, TSSource: "file_mtime",
			EpistemicState: stateDerivedClock, Status: statusOfGate(obs, runID), ExchangeID: &id,
		})
	}

	if indexed {
		for _, e := range read.Events {
			category := ""
			switch e.Kind {
			case "llm.turn":
				category = "llm_turn"
			case "tool.call":
				category = "tool_call"
			case "proc.exec":
				category = "proc_exec"
			case "git.trace2":
				category = "git_trace2"
			case "gate.check":
				category = "gate_check"
			}
			if category == "" {
				continue
			}
			parent := runID + "/run"
			spanID := e.ObsID
			out = append(out, span{
				SpanID: spanID, ParentSpanID: &parent, LaneID: lane, Category: category,
				Label: labelOf(e), StartedAtMS: parseEpochMS(e.TSStart), FinishedAtMS: parseEpochMS(derefString(e.TSEnd)),
				StartUnixNano: parseEpochNS(e.TSStart), EndUnixNano: parseEpochNS(derefString(e.TSEnd)),
				TSSource: "obs_event", EpistemicState: stateObserved, Status: statusOfEvent(e),
			})
		}
	}

	// One gap span per seam, so the waterfall shows the hole where the work happened.
	for _, seam := range obs.seams(runID, row, indexed, readErr) {
		id := seam.ExchangeID
		out = append(out, span{
			SpanID: id + "/gap", LaneID: lane, Category: "gap", Label: seam.Summary,
			TSSource: "none", EpistemicState: stateUnobservedSeam, Status: "unobserved", ExchangeID: &id,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAtMS == nil || out[j].StartedAtMS == nil {
			return out[i].StartedAtMS == nil && out[j].StartedAtMS != nil
		}
		return *out[i].StartedAtMS < *out[j].StartedAtMS
	})
	return out
}

// statusOfGate answers the gate's outcome from the verdict record's presence: the
// verdict carries a result only in its own body, which the artifact endpoint serves, so
// the span states what the file's presence supports and no more.
func statusOfGate(obs *observations, runID string) string {
	if _, err := os.Stat(filepath.Join(obs.runDir(runID), "gate.verdict.json")); err == nil {
		return "recorded"
	}
	if _, err := os.Stat(filepath.Join(obs.runDir(runID), "gate.plan.json")); err == nil {
		return "planned, no verdict"
	}
	return "change recorded, no plan"
}

func labelOf(e obsEvent) string {
	if e.Summary != "" {
		return e.Summary
	}
	return e.Kind
}

// statusOfEvent answers an observed event's outcome. The exit code lives in the
// payload's own record; a payload this face cannot read is unknown, not a pass.
func statusOfEvent(e obsEvent) string {
	var payload struct {
		ExitCode *int `json:"exit_code"`
		IsError  bool `json:"isError"`
	}
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
		return "unknown"
	}
	if payload.ExitCode != nil {
		if *payload.ExitCode == 0 {
			return "ok"
		}
		return "failed"
	}
	if payload.IsError {
		return "failed"
	}
	return "ok"
}

// previewOf answers the first bytes of a record, so an exchange renders without a second
// request. A file that cannot be read has no preview; that absence is not a preview.
func previewOf(path string) *string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	const limit = 400
	text := string(raw)
	if len(text) > limit {
		text = text[:limit] + "…"
	}
	return &text
}

// derefString reads an optional string as the empty string when it is absent.
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// parseEpochNS preserves the index's exact recorded clock for trace downloads.
func parseEpochNS(value string) string {
	stamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(stamp.UnixNano(), 10)
}

// parseEpochMS reads the design's `2026-11-12T19:04:12.104Z` form, and answers nil for
// anything it cannot read: an unreadable clock is a gap, not a zero.
func parseEpochMS(value string) *int64 {
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, value); err == nil {
			ms := t.UnixMilli()
			return &ms
		}
	}
	return nil
}
