package watch

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const bundleBudget = 8192

type bundleGuidance struct {
	Summary string   `json:"summary"`
	Check   []string `json:"check"`
	Do      []string `json:"do"`
}

type omission struct {
	Events     int  `json:"events"`
	Check      int  `json:"check"`
	Do         int  `json:"do"`
	SummaryCut bool `json:"summary_cut"`
	SubjectCut bool `json:"subject_cut"`
}

type Bundle struct {
	RuleID   string         `json:"rule_id"`
	Subject  string         `json:"subject"`
	Severity string         `json:"severity"`
	Guidance bundleGuidance `json:"guidance"`
	Events   []observation  `json:"events"`
	Omitted  omission       `json:"omitted"`
}

func makeBundle(r Rule, key, id string, c *condition) ([]byte, error) {
	events := slices.Clone(c.events)
	slices.Reverse(events)
	oldest, newest := events[0].TS, events[0].TS
	for _, event := range events[1:] {
		oldest = min(oldest, event.TS)
		newest = max(newest, event.TS)
	}
	elapsed := newest - oldest
	elapsedText := strconv.FormatInt(elapsed/1000, 10) + "s"
	if elapsed >= 60_000 {
		elapsedText = strconv.FormatInt(elapsed/60_000, 10) + "m"
	}
	substitutions := []string{"{{subject}}", key, "{{rule_id}}", r.ID, "{{alert_id}}", id, "{{count}}", strconv.Itoa(len(events)), "{{elapsed}}", elapsedText}
	if r.When.Pattern == AlternatingPair {
		substitutions = append(substitutions, "{{pair_a}}", c.pair[0], "{{pair_b}}", c.pair[1])
	}
	replace := strings.NewReplacer(substitutions...)
	render := func(lines []string) []string {
		out := make([]string, len(lines))
		for i, line := range lines {
			out[i] = replace.Replace(line)
		}
		return out
	}
	b := Bundle{RuleID: r.ID, Subject: key, Severity: r.Severity, Events: events, Guidance: bundleGuidance{Summary: replace.Replace(r.Guidance.Summary), Check: render(r.Guidance.Check), Do: render(r.Guidance.Do)}}
	return b.encode()
}

func (b Bundle) encode() ([]byte, error) {
	data, err := json.Marshal(b)
	if err != nil || len(data) <= bundleBudget {
		return data, err
	}
	// Length is monotone as each suffix is cut. Find the first cut that
	// fits without encoding the same large event or text once per element.
	trim := func(n int, cut func(int)) bool {
		i := sort.Search(n+1, func(i int) bool { cut(i); data, err = json.Marshal(b); return err != nil || len(data) <= bundleBudget })
		cut(min(i, n))
		data, err = json.Marshal(b)
		return err != nil || len(data) <= bundleBudget
	}
	events := b.Events
	if trim(len(events), func(n int) { b.Events = events[:len(events)-n]; b.Omitted.Events = n }) {
		return data, err
	}
	do := b.Guidance.Do
	if trim(len(do), func(n int) { b.Guidance.Do = do[:len(do)-n]; b.Omitted.Do = n }) {
		return data, err
	}
	check := b.Guidance.Check
	if trim(len(check), func(n int) { b.Guidance.Check = check[:len(check)-n]; b.Omitted.Check = n }) {
		return data, err
	}
	summary := []rune(b.Guidance.Summary)
	if trim(len(summary), func(n int) { b.Guidance.Summary = string(summary[:len(summary)-n]); b.Omitted.SummaryCut = n != 0 }) {
		return data, err
	}
	subject := []rune(b.Subject)
	trim(len(subject), func(n int) { b.Subject = string(subject[:len(subject)-n]); b.Omitted.SubjectCut = n != 0 })
	return data, err
}
