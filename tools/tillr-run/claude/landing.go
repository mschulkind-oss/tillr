package claude

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	landTool     = "mcp__tillr__land"
	obstacleTool = "mcp__tillr__obstacle"
	noChangeTool = "mcp__tillr__no_change"
	serverName   = "tillr"
	tailWindow   = 1 << 20 // the window Claude Code scans for a deferred-tool marker

	instructions = "`mcp__tillr__land` is the brief's `land` call and `mcp__tillr__obstacle` is its `obstacle` call. " +
		"Call one of them by itself, never in a batch with another tool. The call returns at once when a " +
		"check you can fix fails. Otherwise this session ends with the call open, and the gate's answer " +
		"arrives later as that call's result."
)

// ObstacleRequired maps each closed obstacle kind to its required uppercase field names.
var ObstacleRequired = map[string][]string{
	"definition": {"CRITERIA", "EVIDENCE"},
	"trunk":      {"CHECKS", "BASE"},
	"tooling":    {"COMMAND", "EVIDENCE", "PATHS"},
	"seat":       {"TRIED", "MISSING"},
	"owner":      {"QUESTION"},
}

var obstacleRequired = ObstacleRequired

var obstacleFields = []string{
	"criteria", "evidence", "paths", "checks", "base", "command", "tried", "missing", "question",
}

var (
	savedWordsRe = regexp.MustCompile(`(?m)^## Saved (?:idea|request) words \(verbatim; \d+ bytes\)$`)
	unchangedRe  = regexp.MustCompile(`(?m)^## Workflow node\n\nRun only node .+ on the .+ seat; access: write\.\nAn unchanged piece may finish without a commit\.$`)
	nodeNoChgRe  = regexp.MustCompile(`(?m)^\nRun only node .+ on the .+ seat; access: (?:read_only|write)\.\nA reproduced no-change finding may finish with no_change\.$`)
	plainNoChgRe = regexp.MustCompile(`(?m)^A reproduced no-change finding may finish with no_change\.$`)
	shSafeRe     = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)
)

// RefusalError indicates a local ending-tool validation failure that the
// model can fix in-turn.
type RefusalError struct {
	msg string
}

func (e *RefusalError) Error() string { return e.msg }

type refusalError = RefusalError

// ReadJSONRecord reads a JSON object from path, returning (nil, nil) when the
// file does not exist.
func ReadJSONRecord(path string) (map[string]any, error) {
	return readRecord(path)
}

func readRecord(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func blocks(row map[string]any) []map[string]any {
	msg, ok := row["message"].(map[string]any)
	if !ok {
		return nil
	}
	content, ok := msg["content"].([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(content))
	for _, item := range content {
		if b, ok := item.(map[string]any); ok {
			out = append(out, b)
		}
	}
	return out
}

func readTranscriptRows(path string, tail int64) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if tail > 0 {
		size, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, err
		}
		start := size - tail
		if start < 0 {
			start = 0
		}
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
		r := bufio.NewReader(f)
		if size > tail {
			// Discard partial first line.
			_, _ = r.ReadBytes('\n')
		}
		return scanJSONRows(r)
	}
	return scanJSONRows(bufio.NewReader(f))
}

func scanJSONRows(r *bufio.Reader) ([]map[string]any, error) {
	var out []map[string]any
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var row map[string]any
			if json.Unmarshal(line, &row) == nil && row != nil {
				out = append(out, row)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out, err
		}
	}
	return out, nil
}

func answered(transcript, callID string) bool {
	if transcript == "" || callID == "" {
		return false
	}
	rows, err := readTranscriptRows(transcript, 0)
	if err != nil {
		return false
	}
	for _, row := range rows {
		for _, b := range blocks(row) {
			if strField(b, "type") == "tool_result" && strField(b, "tool_use_id") == callID {
				return true
			}
		}
	}
	return false
}

func strField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func gitRun(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// HeadOf returns the resolved HEAD commit SHA of cwd.
func HeadOf(cwd string) (string, error) {
	return headOf(cwd)
}

// DirtyPaths returns tracked modified, staged, and untracked non-ignored paths in cwd.
func DirtyPaths(cwd string) ([]string, error) {
	return dirtyPaths(cwd)
}

// UnformattedPaths runs tools/gate/checks/format.json when present in cwd and
// returns any reported unformatted paths.
func UnformattedPaths(cwd string) []string {
	return unformattedPaths(cwd)
}

func headOf(cwd string) (string, error) {
	out, err := gitRun(cwd, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func dirtyPaths(cwd string) ([]string, error) {
	seen := make(map[string]struct{})
	var paths []string
	cmds := [][]string{
		{"diff", "--name-only", "--no-renames", "-z"},
		{"diff", "--cached", "--name-only", "--no-renames", "-z"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	}
	for _, args := range cmds {
		out, err := gitRun(cwd, args...)
		if err != nil {
			return nil, err
		}
		for _, entry := range strings.Split(out, "\x00") {
			if entry == "" {
				continue
			}
			if _, ok := seen[entry]; !ok {
				seen[entry] = struct{}{}
				paths = append(paths, entry)
			}
		}
	}
	return paths, nil
}

func unformattedPaths(cwd string) []string {
	decl, err := readRecord(filepath.Join(cwd, "tools/gate/checks/format.json"))
	if err != nil || decl == nil {
		return nil
	}
	rawCmd, ok := decl["command"].([]any)
	if !ok || len(rawCmd) == 0 {
		return nil
	}
	command := make([]string, 0, len(rawCmd))
	for _, arg := range rawCmd {
		s, ok := arg.(string)
		if !ok || s == "" {
			return nil
		}
		command = append(command, s)
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = cwd
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return nil
	}
	if lines := nonEmptyLines(stdout.String()); len(lines) > 0 {
		return lines
	}
	if lines := nonEmptyLines(stderr.String()); len(lines) > 0 {
		return lines
	}
	code := 1
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	}
	return []string{fmt.Sprintf("%s exited %d", strings.Join(command, " "), code)}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ValidateObstacle validates obstacle tool arguments and returns the canonical
// {"kind": ..., "fields": ...} map, or *RefusalError when invalid.
func ValidateObstacle(args map[string]any) (map[string]any, error) {
	return validateObstacle(args)
}

// ValidateNoChange validates no_change tool arguments and returns the canonical
// {"cause": ..., "evidence": ..., "why_no_change": ...} map, or *RefusalError.
func ValidateNoChange(args map[string]any) (map[string]string, error) {
	return validateNoChange(args)
}

// WriteLandingOffer atomically writes offer as JSON to path (mode 0600).
func WriteLandingOffer(path string, offer map[string]any) error {
	return writeOffer(path, offer)
}

// RecordOpenLandingState updates landing-state.json in durable so the newly
// parked offer appears in "open" while preserving any existing "open" and
// "refused" entries.
func RecordOpenLandingState(durable string, offer map[string]any) error {
	if durable == "" || offer == nil {
		return nil
	}
	path := filepath.Join(durable, "landing-state.json")
	state, err := readRecord(path)
	if err != nil {
		return err
	}
	if state == nil {
		state = map[string]any{}
	}
	callID := strField(offer, "call_id")
	openList, _ := state["open"].([]any)
	for _, rawEntry := range openList {
		if entry, ok := rawEntry.(map[string]any); ok && strField(entry, "call_id") == callID {
			return nil
		}
	}
	tool := strField(offer, "tool")
	if tool == "" {
		tool = "land"
	}
	entry := map[string]any{
		"call_id": callID,
		"tool":    tool,
		"base":    strField(offer, "base"),
	}
	if commit := strField(offer, "commit"); commit != "" {
		entry["commit"] = commit
	}
	openList = append(openList, entry)
	state["open"] = openList
	if _, ok := state["refused"]; !ok {
		state["refused"] = []any{}
	}
	return writeOffer(path, state)
}

// EndingOfferRequest carries the inputs needed to validate and build a
// landing-offer.json record for land, obstacle, or no_change.
type EndingOfferRequest struct {
	Tool          string // "land", "obstacle", or "no_change"
	LandLabel     string // display name for land in error messages (default "land")
	CallID        string
	CWD           string
	Transcript    string
	DurableDir    string
	BaseSHA       string
	AllowNoChange bool
	Input         map[string]any
	Answered      func(callID string) bool
	Answer        map[string]any
}

// BuildEndingOffer validates a land, obstacle, or no_change call against the
// worktree and landing-state.json, returning the offer map to write, (nil, nil)
// when callID is already open in landing-state.json, or *RefusalError for a
// local mistake the model can fix in-turn.
func BuildEndingOffer(req EndingOfferRequest) (map[string]any, error) {
	landLabel := req.LandLabel
	if landLabel == "" {
		landLabel = "land"
	}
	isAnswered := req.Answered
	if isAnswered == nil {
		isAnswered = func(string) bool { return false }
	}
	if req.Tool == "no_change" && !req.AllowNoChange {
		return nil, &RefusalError{"no_change is not allowed on this stage."}
	}
	state, err := readRecord(filepath.Join(req.DurableDir, "landing-state.json"))
	if err != nil {
		return nil, err
	}
	if state == nil {
		state = map[string]any{}
	}
	openList, _ := state["open"].([]any)
	for _, rawEntry := range openList {
		entry, _ := rawEntry.(map[string]any)
		entryID := strField(entry, "call_id")
		if entryID != "" && entryID != req.CallID && !isAnswered(entryID) {
			return nil, &RefusalError{fmt.Sprintf("Another ending call is open: %s.", entryID)}
		}
	}
	for _, rawEntry := range openList {
		entry, _ := rawEntry.(map[string]any)
		if strField(entry, "call_id") == req.CallID {
			return nil, nil // already open; defer without rewriting offer
		}
	}
	if req.Answer != nil && !isAnswered(strField(req.Answer, "call_id")) {
		return nil, &RefusalError{"The staged landing answer belongs to a different call."}
	}

	toolInput := req.Input
	if toolInput == nil {
		toolInput = map[string]any{}
	}
	head, err := headOf(req.CWD)
	if err != nil {
		return nil, err
	}
	refusedList, _ := state["refused"].([]any)

	switch req.Tool {
	case "land":
		if head == req.BaseSHA {
			return nil, &RefusalError{"There is no commit to offer: HEAD is still the staged base."}
		}
		dirty, err := dirtyPaths(req.CWD)
		if err != nil {
			return nil, err
		}
		if len(dirty) > 0 {
			return nil, &RefusalError{"Commit or remove these uncommitted paths:\n" + strings.Join(dirty, "\n")}
		}
		if unformatted := unformattedPaths(req.CWD); len(unformatted) > 0 {
			return nil, &RefusalError{
				"Format these files (and run `just pack-sync` if `tools/tillr*` changed), then amend the commit before calling land:\n" +
					strings.Join(unformatted, "\n"),
			}
		}
		for _, rawEntry := range refusedList {
			entry, _ := rawEntry.(map[string]any)
			if strField(entry, "commit") == head {
				if ro, ok := entry["report_only"].(bool); ok && ro {
					continue
				}
				return nil, &RefusalError{fmt.Sprintf("This commit was already refused. A new commit is required.\n%s", strField(entry, "answer"))}
			}
		}
		return map[string]any{
			"call_id":    req.CallID,
			"commit":     head,
			"base":       req.BaseSHA,
			"transcript": req.Transcript,
		}, nil

	case "no_change":
		parsed, err := validateNoChange(toolInput)
		if err != nil {
			return nil, err
		}
		if head != req.BaseSHA {
			return nil, &RefusalError{fmt.Sprintf("A commit exists at HEAD (HEAD is not the staged base); call `%s` instead.", landLabel)}
		}
		dirty, err := dirtyPaths(req.CWD)
		if err != nil {
			return nil, err
		}
		if len(dirty) > 0 {
			return nil, &RefusalError{
				fmt.Sprintf("The worktree is not clean; commit and call `%s`, or remove uncommitted paths before calling no_change:\n", landLabel) +
					strings.Join(dirty, "\n"),
			}
		}
		for _, rawEntry := range refusedList {
			entry, _ := rawEntry.(map[string]any)
			if strField(entry, "tool") != "no_change" || strField(entry, "base") != req.BaseSHA {
				continue
			}
			finding, ok := entry["finding"].(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(strField(finding, "cause")) == parsed["cause"] &&
				strings.TrimSpace(strField(finding, "evidence")) == parsed["evidence"] &&
				strings.TrimSpace(strField(finding, "why_no_change")) == parsed["why_no_change"] {
				return nil, &RefusalError{
					fmt.Sprintf("This no_change finding was already refused. Provide new evidence or make the required change.\n%s", strField(entry, "answer")),
				}
			}
		}
		return map[string]any{
			"call_id":    req.CallID,
			"tool":       "no_change",
			"base":       req.BaseSHA,
			"finding":    parsed,
			"transcript": req.Transcript,
		}, nil

	default: // "obstacle"
		parsed, err := validateObstacle(toolInput)
		if err != nil {
			return nil, err
		}
		if parsed["kind"] == "seat" && head == req.BaseSHA {
			dirty, err := dirtyPaths(req.CWD)
			if err != nil {
				return nil, err
			}
			if len(dirty) == 0 {
				return nil, &RefusalError{"A seat obstacle needs an attempt: make a change or commit first, then report what you tried."}
			}
		}
		return map[string]any{
			"call_id":    req.CallID,
			"tool":       "obstacle",
			"base":       req.BaseSHA,
			"obstacle":   parsed,
			"transcript": req.Transcript,
		}, nil
	}
}

func validateObstacle(args map[string]any) (map[string]any, error) {
	rawKind := args["kind"]
	kindStr, _ := rawKind.(string)
	kind := strings.ToLower(strings.TrimSpace(kindStr))
	required, ok := obstacleRequired[kind]
	if !ok {
		if rawKind == nil {
			rawKind = ""
		}
		encoded, _ := json.Marshal(rawKind)
		kinds := make([]string, 0, len(obstacleRequired))
		for k := range obstacleRequired {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		return nil, &refusalError{fmt.Sprintf("Invalid obstacle kind %s; expected one of %s.", string(encoded), strings.Join(kinds, ", "))}
	}
	fields := make(map[string]string, len(required))
	var missing []string
	for _, name := range required {
		lower := strings.ToLower(name)
		raw, _ := args[lower].(string)
		val := strings.TrimSpace(raw)
		if val != "" {
			fields[name] = val
		} else {
			missing = append(missing, lower)
		}
	}
	if len(missing) > 0 {
		return nil, &refusalError{fmt.Sprintf("The %s obstacle is missing required field(s): %s.", kind, strings.Join(missing, ", "))}
	}
	if kind == "tooling" {
		rawPaths := strings.ReplaceAll(fields["PATHS"], ",", " ")
		var entries []string
		for _, tok := range strings.Fields(rawPaths) {
			p := strings.Trim(tok, "`*-")
			if p != "" {
				entries = append(entries, p)
			}
		}
		if len(entries) == 0 {
			return nil, &refusalError{fmt.Sprintf("The %s obstacle is missing required field(s): paths.", kind)}
		}
		for _, p := range entries {
			if strings.HasPrefix(p, "/") || p == ".." || strings.HasPrefix(p, "../") {
				return nil, &refusalError{fmt.Sprintf("The %s obstacle's paths must be repository-relative, and %s is not.", kind, p)}
			}
		}
	}
	return map[string]any{"kind": kind, "fields": fields}, nil
}

func validateNoChange(args map[string]any) (map[string]string, error) {
	cause := strings.TrimSpace(strField(args, "cause"))
	evidence := strings.TrimSpace(strField(args, "evidence"))
	why := strings.TrimSpace(strField(args, "why_no_change"))
	var missing []string
	if cause == "" {
		missing = append(missing, "cause")
	}
	if evidence == "" {
		missing = append(missing, "evidence")
	}
	if why == "" {
		missing = append(missing, "why_no_change")
	}
	if len(missing) > 0 {
		return nil, &refusalError{fmt.Sprintf("no_change is missing required field(s): %s.", strings.Join(missing, ", "))}
	}
	return map[string]string{"cause": cause, "evidence": evidence, "why_no_change": why}, nil
}

func randomHex16() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeOffer(path string, offer map[string]any) error {
	raw, err := json.Marshal(offer)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temp := fmt.Sprintf("%s.%s", path, randomHex16())
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(temp)
	}()
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func preToolUse(event map[string]any, durable, base string, allowNoChange bool) (string, string, error) {
	name := strField(event, "tool_name")
	callID := strField(event, "tool_use_id")
	cwd := strField(event, "cwd")
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	transcript := strField(event, "transcript_path")
	ending := name == landTool || name == obstacleTool || name == noChangeTool

	answer, err := readRecord(filepath.Join(durable, "landing-answer.json"))
	if err != nil {
		return "", "", err
	}
	if ending && answer != nil && strField(answer, "call_id") == callID {
		return "allow", "delivering the gate's answer as this call's result", nil
	}

	offerPath := filepath.Join(durable, "landing-offer.json")
	offer, err := readRecord(offerPath)
	if err != nil {
		return "", "", err
	}
	if offer != nil {
		offered := strField(offer, "call_id")
		if ending && offered == callID {
			return "defer", "", nil // replay of a call already parked
		}
		if answered(transcript, offered) {
			_ = os.Remove(offerPath) // a call that has its result never parked
		} else {
			return "deny", fmt.Sprintf("Another ending call is open: %s. Make no other tool call; this session is ending.", offered), nil
		}
	}
	if !ending {
		return "allow", "", nil
	}

	record, err := buildEndingOffer(event, name, callID, cwd, transcript, durable, base, allowNoChange, answer)
	if err != nil {
		var ref *refusalError
		if errors.As(err, &ref) {
			return "deny", ref.msg, nil
		}
		return "", "", err
	}
	if record == nil {
		// Already open in landing-state.json for this call_id.
		return "defer", "", nil
	}
	if err := writeOffer(offerPath, record); err != nil {
		return "", "", err
	}
	return "defer", "", nil
}

func buildEndingOffer(
	event map[string]any,
	name, callID, cwd, transcript, durable, base string,
	allowNoChange bool,
	answer map[string]any,
) (map[string]any, error) {
	tool := "obstacle"
	switch name {
	case landTool:
		tool = "land"
	case noChangeTool:
		tool = "no_change"
	}
	toolInput, _ := event["tool_input"].(map[string]any)
	return BuildEndingOffer(EndingOfferRequest{
		Tool:          tool,
		LandLabel:     landTool,
		CallID:        callID,
		CWD:           cwd,
		Transcript:    transcript,
		DurableDir:    durable,
		BaseSHA:       base,
		AllowNoChange: allowNoChange,
		Input:         toolInput,
		Answered: func(id string) bool {
			return answered(transcript, id)
		},
		Answer: answer,
	})
}

func readBriefBeforeSavedWordsPath(brief string) (string, bool) {
	if brief == "" {
		return "", false
	}
	raw, err := os.ReadFile(brief)
	if err != nil {
		return "", false
	}
	text := string(raw)
	if loc := savedWordsRe.FindStringIndex(text); loc != nil {
		text = text[:loc[0]]
	}
	return text, true
}

func readBriefBeforeSavedWords() (string, bool) {
	return readBriefBeforeSavedWordsPath(os.Getenv("BOARD_DRAIN_BRIEF"))
}

// AllowsUnchangedPiece reports whether briefPath authorizes finishing an
// unchanged write piece at its clean base with RESULT: done.
func AllowsUnchangedPiece(briefPath string) bool {
	text, ok := readBriefBeforeSavedWordsPath(briefPath)
	if !ok {
		return false
	}
	return unchangedRe.MatchString(text)
}

// AllowsNoChange reports whether briefPath authorizes a no_change ending.
func AllowsNoChange(briefPath string) bool {
	text, ok := readBriefBeforeSavedWordsPath(briefPath)
	if !ok {
		return false
	}
	if body, ok := markdownSectionBody(text, "Workflow node"); ok {
		if nodeNoChgRe.MatchString(body) {
			return true
		}
	}
	if body, ok := markdownSectionBody(text, "Finishing", "No-change ending"); ok {
		return plainNoChgRe.MatchString(body)
	}
	return false
}

func markdownSectionBody(text string, headings ...string) (string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		matched := false
		for _, h := range headings {
			if line == "## "+h {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") {
				end = j
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n"), true
	}
	return "", false
}

func allowsUnchangedPiece() bool {
	return AllowsUnchangedPiece(os.Getenv("BOARD_DRAIN_BRIEF"))
}

func allowsNoChange() bool {
	return AllowsNoChange(os.Getenv("BOARD_DRAIN_BRIEF"))
}

func stopHook(event map[string]any, durable, base string, allowNoChange bool) (string, error) {
	if pause := os.Getenv("TILLR_PAUSE_REQUEST"); pause != "" {
		if _, err := os.Stat(pause); err == nil {
			return "", nil
		}
	}
	cwd := strField(event, "cwd")
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(filepath.Join(durable, "landing-offer.json")); err == nil {
		return "", nil
	}
	text := ""
	if transcript := strField(event, "transcript_path"); transcript != "" {
		if rows, err := readTranscriptRows(transcript, 0); err == nil {
			for _, row := range rows {
				if strField(row, "type") == "assistant" {
					var sb strings.Builder
					for _, b := range blocks(row) {
						if strField(b, "type") == "text" {
							sb.WriteString(strField(b, "text"))
						}
					}
					if sb.Len() > 0 {
						text = sb.String()
					}
				}
			}
		}
	}
	allowUnchanged := allowsUnchangedPiece()
	state := ""
	if head, err := headOf(cwd); err == nil {
		if status, err := gitRun(cwd, "status", "--porcelain=v1", "--untracked-files=all"); err == nil {
			trimmedText := strings.TrimRightFunc(text, unicode.IsSpace)
			lastLine := trimmedText
			if idx := strings.LastIndex(trimmedText, "\n"); idx >= 0 {
				lastLine = trimmedText[idx+1:]
			}
			if allowUnchanged && head == base && status == "" && strings.TrimSpace(lastLine) == "RESULT: done" {
				return "", nil
			}
			if diff, err := gitRun(cwd, "diff", "HEAD"); err == nil {
				state = strings.Join([]string{head, status, diff}, "\x00")
			}
		}
	}
	mark := filepath.Join(durable, "stop-nudge")
	sum := sha256.Sum256([]byte(state + "\x00" + text))
	fingerprint := hex.EncodeToString(sum[:])
	if prev, err := os.ReadFile(mark); err == nil {
		if strings.TrimSpace(string(prev)) == fingerprint {
			return "", nil
		}
	}
	if err := os.WriteFile(mark, []byte(fingerprint+"\n"), 0644); err != nil {
		return "", err
	}
	var reason string
	if allowNoChange {
		reason = "You ended without calling `mcp__tillr__land`, `mcp__tillr__obstacle`, or `mcp__tillr__no_change`. " +
			"Call land when your commit is ready, no_change when reproductions show nothing in the repository needs changing, " +
			"or obstacle when an obstacle holds."
	} else {
		reason = "You ended without calling `mcp__tillr__land` or `mcp__tillr__obstacle`. Call land " +
			"when your commit is ready, or obstacle when an obstacle holds."
	}
	if allowUnchanged {
		reason += " If the piece already holds at its unchanged, clean base, end with RESULT: done instead."
	}
	return reason, nil
}

// Held reports whether a session transcript holds a deferred call:
// 0 when held, 3 when already resolved, 1 otherwise.
func Held(transcript, callID string) int {
	if callID == "" {
		return 1
	}
	rows, err := readTranscriptRows(transcript, tailWindow)
	if err != nil {
		return 1
	}
	deferred, resolved := false, false
	for _, row := range rows {
		if att, ok := row["attachment"].(map[string]any); ok {
			if strField(att, "type") == "hook_deferred_tool" && strField(att, "toolUseID") == callID {
				deferred, resolved = true, false
			}
		}
		for _, b := range blocks(row) {
			if strField(b, "type") == "tool_result" && strField(b, "tool_use_id") == callID {
				resolved = true
			}
		}
	}
	if resolved {
		return 3
	}
	if deferred {
		return 0
	}
	return 1
}

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shSafeRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func loadSettingsSpec(spec string) (map[string]any, error) {
	stripped := strings.TrimSpace(spec)
	var raw []byte
	if strings.HasPrefix(stripped, "{") {
		raw = []byte(stripped)
	} else {
		var err error
		raw, err = os.ReadFile(spec)
		if err != nil {
			return nil, err
		}
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	obj, ok := body.(map[string]any)
	if !ok {
		return nil, errors.New("settings must be a JSON object")
	}
	return obj, nil
}

func mergeSettings(target, incoming map[string]any) {
	for key, value := range incoming {
		if key == "hooks" {
			incHooks, incOK := value.(map[string]any)
			tgtHooks, tgtOK := target["hooks"].(map[string]any)
			if incOK && tgtOK {
				for eventName, entries := range incHooks {
					if list, ok := entries.([]any); ok {
						existing, _ := tgtHooks[eventName].([]any)
						tgtHooks[eventName] = append(existing, list...)
					} else {
						tgtHooks[eventName] = entries
					}
				}
				continue
			}
		}
		incMap, incOK := value.(map[string]any)
		tgtMap, tgtOK := target[key].(map[string]any)
		if incOK && tgtOK {
			mergeSettings(tgtMap, incMap)
			continue
		}
		target[key] = value
	}
}

func prepare(durable, base, baseSettings, extraSettingsFile string) (map[string]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return nil, err
	}
	launch := func(words ...string) string {
		parts := make([]string, 0, len(words)+2)
		parts = append(parts, shQuote(exe), shQuote("claude-landing"))
		for _, w := range words {
			parts = append(parts, shQuote(w))
		}
		return strings.Join(parts, " ")
	}
	var extra []string
	if allowsNoChange() {
		extra = append(extra, "--allow-no-change")
	}
	args := append([]string{"--durable", durable, "--base", base}, extra...)
	mcpArgs := append([]string{"claude-landing", "mcp", "--durable", durable}, extra...)
	mcp := map[string]any{
		"mcpServers": map[string]any{
			serverName: map[string]any{
				"type":    "stdio",
				"command": exe,
				"args":    mcpArgs,
			},
		},
	}
	settings := map[string]any{}
	if baseSettings != "" {
		spec, err := loadSettingsSpec(baseSettings)
		if err != nil {
			return nil, err
		}
		mergeSettings(settings, spec)
	}
	if extraSettingsFile != "" {
		raw, err := os.ReadFile(extraSettingsFile)
		if err != nil {
			return nil, err
		}
		for _, part := range strings.Split(string(raw), "\x00") {
			if part == "" {
				continue
			}
			spec, err := loadSettingsSpec(part)
			if err != nil {
				return nil, err
			}
			mergeSettings(settings, spec)
		}
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	preList, _ := hooks["PreToolUse"].([]any)
	hooks["PreToolUse"] = append(preList, map[string]any{
		"matcher": "*",
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": launch(append([]string{"pre-tool-use"}, args...)...),
			},
		},
	})
	stopList, _ := hooks["Stop"].([]any)
	hooks["Stop"] = append(stopList, map[string]any{
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": launch(append([]string{"stop"}, args...)...),
			},
		},
	})
	paths := make(map[string]string, 2)
	for _, item := range []struct {
		name string
		body any
	}{
		{"claude-mcp.json", mcp},
		{"claude-settings.json", settings},
	} {
		outPath := filepath.Join(durable, item.name)
		paths[item.name] = outPath
		encoded, err := json.Marshal(item.body)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, '\n')
		if err := os.WriteFile(outPath, encoded, 0644); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func mcpTools(allowNoChange bool) []map[string]any {
	kinds := make([]string, 0, len(obstacleRequired))
	for k := range obstacleRequired {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	obsProps := map[string]any{
		"kind": map[string]any{"type": "string", "enum": kinds},
	}
	for _, f := range obstacleFields {
		obsProps[f] = map[string]any{"type": "string"}
	}

	items := []map[string]any{
		{
			"name":        "land",
			"description": "Offer the commit at HEAD for landing, ending this run.",
			"inputSchema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			"name":        "obstacle",
			"description": "Report an obstacle that prevents finishing this run, ending the run until resolved. Provide kind and that kind's required fields.",
			"inputSchema": map[string]any{
				"type":                 "object",
				"required":             []string{"kind"},
				"properties":           obsProps,
				"additionalProperties": false,
			},
		},
	}
	if allowNoChange {
		items = append(items, map[string]any{
			"name":        "no_change",
			"description": "Report a reproduced finding that nothing in the repository needs changing, ending this run.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"cause", "evidence", "why_no_change"},
				"properties": map[string]any{
					"cause":         map[string]any{"type": "string"},
					"evidence":      map[string]any{"type": "string"},
					"why_no_change": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
		})
	}
	return items
}

func callTool(params map[string]any, durable string) map[string]any {
	meta, _ := params["_meta"].(map[string]any)
	callID := strField(meta, "claudecode/toolUseId")
	answer, err := readRecord(filepath.Join(durable, "landing-answer.json"))
	if err == nil && answer != nil && callID != "" && strField(answer, "call_id") == callID {
		if text, ok := answer["text"].(string); ok && strings.TrimSpace(text) != "" {
			return map[string]any{
				"content": []any{map[string]any{"type": "text", "text": text}},
				"isError": true,
			}
		}
	}
	// The harness ran the call without a deferral, or without an answer: nothing was offered.
	offerPath := filepath.Join(durable, "landing-offer.json")
	if offer, err := readRecord(offerPath); err == nil && offer != nil {
		if callID == "" || strField(offer, "call_id") == callID {
			_ = os.Remove(offerPath)
		}
	}
	return map[string]any{
		"content": []any{map[string]any{
			"type": "text",
			"text": "This call was not parked. Call mcp__tillr__land or mcp__tillr__obstacle by itself, with no other tool call in the same message.",
		}},
		"isError": true,
	}
}

func serveMCP(durable string, allowNoChange bool, stdin io.Reader, stdout io.Writer) {
	r := bufio.NewReader(stdin)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var message map[string]any
			if json.Unmarshal(line, &message) == nil && message != nil {
				ident, hasID := message["id"]
				if hasID && ident != nil {
					method := strField(message, "method")
					var reply map[string]any
					switch method {
					case "initialize":
						params, _ := message["params"].(map[string]any)
						proto := strField(params, "protocolVersion")
						if proto == "" {
							proto = "2024-11-05"
						}
						reply = map[string]any{
							"jsonrpc": "2.0",
							"id":      ident,
							"result": map[string]any{
								"protocolVersion": proto,
								"capabilities":    map[string]any{"tools": map[string]any{}},
								"serverInfo":      map[string]any{"name": serverName, "version": "1"},
								"instructions":    instructions,
							},
						}
					case "tools/list":
						reply = map[string]any{
							"jsonrpc": "2.0",
							"id":      ident,
							"result":  map[string]any{"tools": mcpTools(allowNoChange)},
						}
					case "tools/call":
						params, _ := message["params"].(map[string]any)
						if params == nil {
							params = map[string]any{}
						}
						reply = map[string]any{
							"jsonrpc": "2.0",
							"id":      ident,
							"result":  callTool(params, durable),
						}
					case "ping":
						reply = map[string]any{
							"jsonrpc": "2.0",
							"id":      ident,
							"result":  map[string]any{},
						}
					default:
						reply = map[string]any{
							"jsonrpc": "2.0",
							"id":      ident,
							"error":   map[string]any{"code": -32601, "message": "method not found"},
						}
					}
					if encoded, err := json.Marshal(reply); err == nil {
						_, _ = stdout.Write(append(encoded, '\n'))
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
}

type landingOptions struct {
	command           string
	rest              []string
	durable           string
	base              string
	settingsJSON      string
	extraSettingsFile string
	allowNoChange     bool
}

func parseLandingArgs(argv []string) (landingOptions, error) {
	opts := landingOptions{
		durable: os.Getenv("YOLO_DURABLE_DIR"),
		base:    os.Getenv("TILLR_GOAL_BASE"),
	}
	var positionals []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--allow-no-change":
			opts.allowNoChange = true
		case arg == "--durable":
			if i+1 >= len(argv) {
				return opts, errors.New("--durable needs a value")
			}
			i++
			opts.durable = argv[i]
		case strings.HasPrefix(arg, "--durable="):
			opts.durable = strings.TrimPrefix(arg, "--durable=")
		case arg == "--base":
			if i+1 >= len(argv) {
				return opts, errors.New("--base needs a value")
			}
			i++
			opts.base = argv[i]
		case strings.HasPrefix(arg, "--base="):
			opts.base = strings.TrimPrefix(arg, "--base=")
		case arg == "--settings-json":
			if i+1 >= len(argv) {
				return opts, errors.New("--settings-json needs a value")
			}
			i++
			opts.settingsJSON = argv[i]
		case strings.HasPrefix(arg, "--settings-json="):
			opts.settingsJSON = strings.TrimPrefix(arg, "--settings-json=")
		case arg == "--extra-settings-file":
			if i+1 >= len(argv) {
				return opts, errors.New("--extra-settings-file needs a value")
			}
			i++
			opts.extraSettingsFile = argv[i]
		case strings.HasPrefix(arg, "--extra-settings-file="):
			opts.extraSettingsFile = strings.TrimPrefix(arg, "--extra-settings-file=")
		case arg == "--":
			positionals = append(positionals, argv[i+1:]...)
			i = len(argv)
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown argument %q", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) == 0 {
		return opts, errors.New("a subcommand is required: mcp, pre-tool-use, stop, held, prepare, answer")
	}
	opts.command = positionals[0]
	opts.rest = positionals[1:]
	switch opts.command {
	case "mcp", "pre-tool-use", "stop", "held", "prepare", "answer":
		return opts, nil
	default:
		return opts, fmt.Errorf("invalid choice %q (choose from mcp, pre-tool-use, stop, held, prepare, answer)", opts.command)
	}
}

// RunLanding is the CLI entry point for `tillr-run claude-landing`.
func RunLanding(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, "usage: tillr-run claude-landing {mcp,pre-tool-use,stop,held,prepare,answer} [options]")
		return 0
	}
	opts, err := parseLandingArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "landing: %v\n", err)
		return 2
	}
	if opts.command == "held" {
		if len(opts.rest) < 2 {
			return 1
		}
		return Held(opts.rest[0], opts.rest[1])
	}
	if opts.command == "answer" {
		if len(opts.rest) < 3 {
			return 1
		}
		raw, err := os.ReadFile(opts.rest[0])
		if err != nil {
			return 1
		}
		var ans map[string]any
		if err := json.Unmarshal(raw, &ans); err != nil {
			return 1
		}
		call, callOK := ans["call_id"].(string)
		text, textOK := ans["text"].(string)
		if !callOK || call == "" || !textOK || text == "" {
			return 1
		}
		if err := os.WriteFile(opts.rest[1], []byte(call), 0644); err != nil {
			return 1
		}
		if err := os.WriteFile(opts.rest[2], []byte(text), 0644); err != nil {
			return 1
		}
		return 0
	}
	if opts.durable == "" {
		fmt.Fprintln(stderr, "landing: the run's scratch directory is not staged")
		return 2
	}
	switch opts.command {
	case "mcp":
		serveMCP(opts.durable, opts.allowNoChange, stdin, stdout)
		return 0
	case "prepare":
		paths, err := prepare(opts.durable, opts.base, opts.settingsJSON, opts.extraSettingsFile)
		if err != nil {
			fmt.Fprintf(stderr, "landing: invalid settings: %v\n", err)
			return 2
		}
		encoded, err := json.Marshal(paths)
		if err != nil {
			fmt.Fprintf(stderr, "landing: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
		return 0
	case "pre-tool-use":
		var event map[string]any
		if err := json.NewDecoder(stdin).Decode(&event); err != nil {
			fmt.Fprintf(stderr, "landing: %v\n", err)
			return 1
		}
		decision, reason, err := preToolUse(event, opts.durable, opts.base, opts.allowNoChange)
		if err != nil {
			fmt.Fprintf(stderr, "landing: %v\n", err)
			return 1
		}
		if decision == "allow" && reason == "" {
			return 0 // not an ending call: no opinion
		}
		out := map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": decision,
		}
		if reason != "" {
			out["permissionDecisionReason"] = reason
		}
		encoded, err := json.Marshal(map[string]any{"hookSpecificOutput": out})
		if err != nil {
			fmt.Fprintf(stderr, "landing: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
		return 0
	default: // stop
		var event map[string]any
		if err := json.NewDecoder(stdin).Decode(&event); err != nil {
			fmt.Fprintf(stderr, "landing: %v\n", err)
			return 1
		}
		reason, err := stopHook(event, opts.durable, opts.base, opts.allowNoChange)
		if err != nil {
			fmt.Fprintf(stderr, "landing: %v\n", err)
			return 1
		}
		if reason != "" {
			encoded, err := json.Marshal(map[string]any{"decision": "block", "reason": reason})
			if err != nil {
				fmt.Fprintf(stderr, "landing: %v\n", err)
				return 1
			}
			fmt.Fprintln(stdout, string(encoded))
		}
		return 0
	}
}
