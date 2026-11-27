package native

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-run/claude"
	"github.com/mschulkind-oss/tillr-run/run"
)

const (
	toolLand     = "land"
	toolObstacle = "obstacle"
	toolNoChange = "no_change"

	landingOfferFile  = "landing-offer.json"
	landingAnswerFile = "landing-answer.json"
)

type endingConfig struct {
	Capability          string
	HasEndingContract   bool
	WorkDir             string
	DurableDir          string
	BaseSHA             string
	ReadOnly            bool
	NonLanding          bool
	AllowNoChange       bool
	AllowUnchangedPiece bool
	ArtifactsDir        string
}

func resolveEndingConfig(cfg Config) endingConfig {
	capStr := strings.TrimSpace(cfg.EndingCapability)
	if capStr == "" && cfg.ArtifactDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cfg.ArtifactDir, "ending-capability")); err == nil {
			capStr = strings.TrimSpace(string(raw))
		}
	}
	hasCap := capStr != ""
	if capStr == "" {
		capStr = run.EndingFooter
	}

	baseSHA := strings.TrimSpace(cfg.BaseSHA)
	if baseSHA == "" && cfg.ArtifactDir != "" {
		if raw, err := os.ReadFile(filepath.Join(cfg.ArtifactDir, "base_sha")); err == nil {
			baseSHA = strings.TrimSpace(string(raw))
		}
	}

	allowNoChange := claude.AllowsNoChange(cfg.BriefPath)
	if cfg.AllowNoChange != nil {
		allowNoChange = *cfg.AllowNoChange
	}
	allowUnchanged := claude.AllowsUnchangedPiece(cfg.BriefPath)
	if cfg.AllowUnchangedPiece != nil {
		allowUnchanged = *cfg.AllowUnchangedPiece
	}

	artifactsDir := strings.TrimSpace(cfg.ArtifactsDir)
	if artifactsDir == "" && cfg.ArtifactDir != "" {
		artifactsDir = filepath.Join(cfg.ArtifactDir, "artifacts")
	}

	return endingConfig{
		Capability:          capStr,
		HasEndingContract:   hasCap || baseSHA != "",
		WorkDir:             strings.TrimSpace(cfg.WorkDir),
		DurableDir:          strings.TrimSpace(cfg.DurableDir),
		BaseSHA:             baseSHA,
		ReadOnly:            cfg.ReadOnly,
		NonLanding:          cfg.NonLanding,
		AllowNoChange:       allowNoChange,
		AllowUnchangedPiece: allowUnchanged,
		ArtifactsDir:        artifactsDir,
	}
}

// CallSuspends reports whether this run uses suspending ending tools.
func (ec endingConfig) CallSuspends() bool {
	return ec.Capability == run.EndingCallSuspends && !ec.NonLanding && ec.BaseSHA != "" && ec.DurableDir != ""
}

func isEndingTool(name string) bool {
	return name == toolLand || name == toolObstacle || name == toolNoChange
}

func endingToolDefinitions(ec endingConfig) []ToolDef {
	if !ec.HasEndingContract {
		return nil
	}
	var defs []ToolDef
	if ec.CallSuspends() && !ec.ReadOnly {
		defs = append(defs, ToolDef{
			Name:        toolLand,
			Description: "Offer the commit at HEAD for landing, suspending this run until the gate answers.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		})
	}
	defs = append(defs, ToolDef{
		Name:        toolObstacle,
		Description: "Report an obstacle that prevents finishing this run. Provide kind and that kind's required fields.",
		InputSchema: json.RawMessage(`{"type":"object","required":["kind"],"properties":{"kind":{"type":"string","enum":["definition","owner","seat","tooling","trunk"]},"criteria":{"type":"string"},"evidence":{"type":"string"},"paths":{"type":"string","description":"For a tooling obstacle, name the tool's repository-relative paths."},"checks":{"type":"string"},"base":{"type":"string"},"command":{"type":"string"},"tried":{"type":"string"},"missing":{"type":"string"},"question":{"type":"string"}},"additionalProperties":false}`),
	})
	if ec.AllowNoChange {
		defs = append(defs, ToolDef{
			Name:        toolNoChange,
			Description: "Report a reproduced finding that nothing in the repository needs changing.",
			InputSchema: json.RawMessage(`{"type":"object","required":["cause","evidence","why_no_change"],"properties":{"cause":{"type":"string"},"evidence":{"type":"string"},"why_no_change":{"type":"string"}},"additionalProperties":false}`),
		})
	}
	return defs
}

func mergeEndingToolDefinitions(base []ToolDef, ec endingConfig) []ToolDef {
	seen := make(map[string]struct{}, len(base))
	out := make([]ToolDef, 0, len(base)+3)
	for _, d := range base {
		seen[d.Name] = struct{}{}
		out = append(out, d)
	}
	for _, d := range endingToolDefinitions(ec) {
		if _, exists := seen[d.Name]; !exists {
			out = append(out, d)
		}
	}
	return out
}

func decodeToolInputMap(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// executeEndingTool validates and executes land, obstacle, or no_change.
// When parked is true, the caller must leave the tool_use unanswered in the
// session transcript, emit {"type":"parked","toolCallId":call.ID}, and exit 0.
func executeEndingTool(ec endingConfig, sess *Session, call ContentBlock, batched bool) (resText string, isErr bool, parked bool) {
	input, err := decodeToolInputMap(call.Input)
	if err != nil {
		return fmt.Sprintf("%s: %v", call.Name, err), true, false
	}

	if !ec.CallSuspends() {
		text, footerErr := executeFooterEndingTool(ec, call.Name, input)
		return text, footerErr, false
	}

	if call.Name == toolLand && ec.ReadOnly {
		return "land is not available on a read-only stage.", true, false
	}
	if call.Name == toolNoChange && !ec.AllowNoChange {
		return "no_change is not allowed on this stage.", true, false
	}
	if batched {
		return fmt.Sprintf("Call `%s` by itself, never in a batch with another tool.", call.Name), true, false
	}

	cwd := ec.WorkDir
	if cwd == "" {
		var wdErr error
		cwd, wdErr = os.Getwd()
		if wdErr != nil {
			return fmt.Sprintf("%s: %v", call.Name, wdErr), true, false
		}
	}

	answer, err := claude.ReadJSONRecord(filepath.Join(ec.DurableDir, landingAnswerFile))
	if err != nil {
		return fmt.Sprintf("%s: reading landing answer: %v", call.Name, err), true, false
	}
	if answer != nil {
		if ansID, _ := answer["call_id"].(string); ansID == call.ID {
			text, _ := answer["text"].(string)
			if strings.TrimSpace(text) == "" {
				return fmt.Sprintf("%s: the staged landing answer has no text", call.Name), true, false
			}
			_ = os.Remove(filepath.Join(ec.DurableDir, landingAnswerFile))
			return text, true, false
		}
	}

	offerPath := filepath.Join(ec.DurableDir, landingOfferFile)
	offer, err := claude.ReadJSONRecord(offerPath)
	if err != nil {
		return fmt.Sprintf("%s: reading landing offer: %v", call.Name, err), true, false
	}
	if offer != nil {
		offeredID, _ := offer["call_id"].(string)
		if offeredID == call.ID {
			return "", false, true
		}
		if sess != nil && sess.HasToolResult(offeredID) {
			_ = os.Remove(offerPath)
		} else {
			return fmt.Sprintf("Another ending call is open: %s. Make no other tool call; this session is ending.", offeredID), true, false
		}
	}

	transcript := ""
	if sess != nil {
		transcript = sess.Path
	}

	record, err := claude.BuildEndingOffer(claude.EndingOfferRequest{
		Tool:          call.Name,
		LandLabel:     toolLand,
		CallID:        call.ID,
		CWD:           cwd,
		Transcript:    transcript,
		DurableDir:    ec.DurableDir,
		BaseSHA:       ec.BaseSHA,
		AllowNoChange: ec.AllowNoChange,
		Input:         input,
		Answered: func(id string) bool {
			return sess != nil && sess.HasToolResult(id)
		},
		Answer: answer,
	})
	if err != nil {
		var ref *claude.RefusalError
		if errors.As(err, &ref) {
			return ref.Error(), true, false
		}
		return fmt.Sprintf("%s: %v", call.Name, err), true, false
	}

	if record != nil {
		if err := claude.WriteLandingOffer(offerPath, record); err != nil {
			return fmt.Sprintf("%s: writing landing offer: %v", call.Name, err), true, false
		}
		if err := claude.RecordOpenLandingState(ec.DurableDir, record); err != nil {
			return fmt.Sprintf("%s: updating landing state: %v", call.Name, err), true, false
		}
	}
	return "", false, true
}

func executeFooterEndingTool(ec endingConfig, name string, input map[string]any) (string, bool) {
	switch name {
	case toolLand:
		return "`land` is not available in footer mode; commit your work and end your final message with `RESULT: done`.", true
	case toolObstacle:
		parsed, err := claude.ValidateObstacle(input)
		if err != nil {
			return err.Error(), true
		}
		kind, _ := parsed["kind"].(string)
		if kind == "seat" && ec.WorkDir != "" && ec.BaseSHA != "" {
			head, herr := claude.HeadOf(ec.WorkDir)
			dirty, derr := claude.DirtyPaths(ec.WorkDir)
			if herr == nil && derr == nil && head == ec.BaseSHA && len(dirty) == 0 {
				return "A seat obstacle needs an attempt: make a change or commit first, then report what you tried.", true
			}
		}
		fields, _ := parsed["fields"].(map[string]string)
		var b strings.Builder
		fmt.Fprintf(&b, "OBSTACLE: %s\n", kind)
		for _, reqField := range claude.ObstacleRequired[kind] {
			val := fields[reqField]
			if strings.Contains(val, "\n") {
				fmt.Fprintf(&b, "%s:\n%s\n", reqField, val)
			} else {
				fmt.Fprintf(&b, "%s: %s\n", reqField, val)
			}
		}
		b.WriteString("RESULT: blocked")
		return b.String(), false
	case toolNoChange:
		if !ec.AllowNoChange {
			return "no_change is not allowed on this stage.", true
		}
		parsed, err := claude.ValidateNoChange(input)
		if err != nil {
			return err.Error(), true
		}
		if ec.WorkDir != "" && ec.BaseSHA != "" {
			if head, err := claude.HeadOf(ec.WorkDir); err == nil && head != ec.BaseSHA {
				return "A commit exists at HEAD (HEAD is not the staged base); end with `RESULT: done` instead.", true
			}
			if dirty, err := claude.DirtyPaths(ec.WorkDir); err == nil && len(dirty) > 0 {
				return "The worktree is not clean; commit or remove uncommitted paths before ending with no_change:\n" + strings.Join(dirty, "\n"), true
			}
		}
		return fmt.Sprintf("CAUSE: %s\nEVIDENCE: %s\nWHY_NO_CHANGE: %s\nRESULT: no_change",
			parsed["cause"], parsed["evidence"], parsed["why_no_change"]), false
	default:
		return fmt.Sprintf("unknown ending tool %q", name), true
	}
}

// replayLandingAnswer checks whether landing-answer.json exists in durableDir
// and matches an unanswered trailing tool_use in sess. When it does, it appends
// the refusal as an is_error: true tool_result (plus any distinct resume prompt
// text in the same user message), removes landing-answer.json, and returns true.
func replayLandingAnswer(sess *Session, durableDir, resumePromptPath string) (bool, error) {
	if sess == nil || durableDir == "" || len(sess.Messages) == 0 {
		return false, nil
	}
	answerPath := filepath.Join(durableDir, landingAnswerFile)
	answer, err := claude.ReadJSONRecord(answerPath)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", answerPath, err)
	}
	if answer == nil {
		return false, nil
	}
	callID, _ := answer["call_id"].(string)
	answerText, _ := answer["text"].(string)
	if strings.TrimSpace(callID) == "" || strings.TrimSpace(answerText) == "" {
		return false, nil
	}

	last := sess.Messages[len(sess.Messages)-1]
	if last.Role != RoleAssistant {
		return false, nil
	}
	matched := false
	for _, blk := range last.Content {
		if blk.Type == BlockToolUse && blk.ID == callID {
			matched = true
			break
		}
	}
	if !matched {
		return false, nil
	}

	blocks := make([]ContentBlock, 0, len(last.Content)+1)
	for _, blk := range last.Content {
		if blk.Type != BlockToolUse || blk.ID == "" {
			continue
		}
		if blk.ID == callID {
			blocks = append(blocks, ContentBlock{
				Type:      BlockToolResult,
				ToolUseID: blk.ID,
				Content:   answerText,
				IsError:   true,
			})
		} else {
			blocks = append(blocks, ContentBlock{
				Type:      BlockToolResult,
				ToolUseID: blk.ID,
				Content:   interruptedToolResultText,
				IsError:   true,
			})
		}
	}

	if resumePromptPath != "" {
		raw, err := os.ReadFile(resumePromptPath)
		if err != nil {
			return false, fmt.Errorf("reading resume prompt %s: %w", resumePromptPath, err)
		}
		promptText := strings.TrimSpace(string(raw))
		if promptText != "" && promptText != strings.TrimSpace(answerText) {
			blocks = append(blocks, ContentBlock{
				Type: BlockText,
				Text: string(raw),
			})
		}
	}

	if err := sess.AppendMessage(Message{
		Role:    RoleUser,
		Content: blocks,
	}); err != nil {
		return false, err
	}
	if err := os.Remove(answerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, fmt.Errorf("removing %s: %w", answerPath, err)
	}
	return true, nil
}
