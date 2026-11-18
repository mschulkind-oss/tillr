package item

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// OwnerDecision recognizes a question with explicit alternatives in a report. It
// preserves their wording rather than interpreting or answering the decision.
// NEXT-ACTOR alone is not a decision: in particular, restaging has no alternatives.
func OwnerDecision(report string) (string, bool) {
	lines := strings.Split(report, "\n")
	title := ""
	options, optionIndent := 0, -1
	inOptions := false
	between := regexp.MustCompile(`(?i)\bbetween\s+.+\s+and\s+.+`)
	choice := regexp.MustCompile(`(?i)\b(ruling|choose|choice|decision|decide|which|whether|should)\b`)
	option := regexp.MustCompile(`^\s*(?:[-*]|[0-9]+[.)]|[A-Z][.)])\s+\S`)
	labelled := regexp.MustCompile(`(?i)^\s*(?:[-*]|[0-9]+[.)])\s+(?:\*\*)?(?:option\s+)?[A-Z](?:[.:)—-]|\s+[—-])`)
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, "NEXT-ACTOR:") || strings.HasPrefix(text, "RESULT:") {
			continue
		}
		heading := strings.ToLower(strings.Trim(strings.TrimLeft(text, "# "), "*: "))
		if heading == "options" || heading == "alternatives" || heading == "choices" {
			inOptions = true
			continue
		}
		if strings.HasPrefix(text, "#") {
			inOptions = false
		}
		if options == 0 && (strings.Contains(text, "?") || (between.MatchString(text) && choice.MatchString(text))) {
			title = strings.TrimSpace(strings.TrimLeft(text, "# *"))
		}
		if option.MatchString(line) && (inOptions || labelled.MatchString(line)) {
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if optionIndent < 0 {
				optionIndent = indent
			}
			if indent == optionIndent {
				options++
			}
		}
		if between.MatchString(text) && choice.MatchString(text) {
			options = 2
		}
	}
	return title, title != "" && options >= 2
}

// OwnerQuestionID reads the loop's origin marker, not question state. It is the
// identity needed to recover a commit whose candidate record was interrupted.
func OwnerQuestionID(body, itemID, runID string) string {
	marker := regexp.MustCompile(`^\s*<!-- tillr-owner-question item=` + regexp.QuoteMeta(strconv.Quote(itemID)) + ` run=` + regexp.QuoteMeta(strconv.Quote(runID)) + ` question="(OQ-[A-Z]*[0-9]+)" -->\s*$`)
	fence := fenceState{}
	for _, line := range splitLines([]byte(body)) {
		if fence.step(line) {
			continue
		}
		if m := marker.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

// OwnerQuestion places the report's decision after the item's criteria and before
// the next block or heading. It writes no file or store. The origin marker makes a
// repeated migration or settlement return the existing question without rewriting it.
func OwnerQuestion(body, itemID, runID, report string, at time.Time) (string, string, error) {
	title, ok := OwnerDecision(report)
	if !ok {
		return body, "", fmt.Errorf("report states no question with alternatives")
	}
	lines := splitLines([]byte(body))
	blocks, _ := parseItemBlocks("owner.md", []byte(body))
	start, end := -1, len(lines)
	for _, b := range blocks {
		if b.payload.ItemID == itemID {
			start = b.directiveEnd
			break
		}
	}
	if start < 0 {
		return body, "", fmt.Errorf("no document block exists for %s", itemID)
	}
	indent := len(lines[start-1]) - len(strings.TrimLeft(lines[start-1], " \t"))
	fence := fenceState{}
	for i := start; i < len(lines); i++ {
		if fence.step(lines[i]) || strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if headingLine.MatchString(lines[i]) || directiveStart.MatchString(strings.TrimSpace(lines[i])) || (indent > 0 && len(lines[i])-len(strings.TrimLeft(lines[i], " \t")) < indent) {
			end = i
			break
		}
	}
	marker := fmt.Sprintf("<!-- tillr-owner-question item=%q run=%q", itemID, runID)
	if id := OwnerQuestionID(strings.Join(lines[start:end], "\n"), itemID, runID); id != "" {
		return body, id, nil
	}
	// Different blocks in one document can land concurrently. Document-local
	// counters would allocate the same anchor on their shared base. The OC
	// (owner conflict) number is derived from the unique run, not the clock.
	digest := sha256.Sum256([]byte(runID))
	n := binary.BigEndian.Uint64(digest[:8]) & ((1 << 63) - 1)
	id := fmt.Sprintf("OQ-OC%d", n)
	for regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\b`).MatchString(body) {
		n++
		id = fmt.Sprintf("OQ-OC%d", n)
	}
	prefix := strings.Repeat(" ", indent)
	question := fmt.Sprintf("1. 💬 **%s: %s**\n\n   <!-- vantage: question id=%s -->\n\n   %s question=%q -->\n\n   Report from run `%s`, %s:\n\n", id, title, id, marker, id, runID, at.UTC().Format("2006-01-02"))
	for _, line := range strings.Split(strings.TrimSpace(report), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "NEXT-ACTOR:") || strings.HasPrefix(strings.TrimSpace(line), "RESULT:") {
			continue
		}
		question += "   > " + line + "\n"
	}
	question += "\n   **Answer:**\n   > _(empty — fill in when decided)_\n"
	qlines := strings.Split(strings.TrimRight(question, "\n"), "\n")
	for i := range qlines {
		qlines[i] = prefix + qlines[i]
	}
	inserted := append([]string{}, lines[:end]...)
	inserted = append(inserted, "", strings.Join(qlines, "\n"), "")
	inserted = append(inserted, lines[end:]...)
	return strings.Join(inserted, "\n"), id, nil
}
