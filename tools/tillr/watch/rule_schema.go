package watch

import (
	"slices"
	"strings"
	"unicode"
)

// Check Git ref syntax in-process: stored rules are also validated inside a
// store transaction, where no Git command may run.
func validRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/") || len(ref) > 255 || strings.HasSuffix(ref, ".") ||
		strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.ContainsAny(ref, `,~^:?*[\`) ||
		strings.IndexFunc(ref, func(r rune) bool { return r < ' ' || r == 0x7f || unicode.IsSpace(r) }) >= 0 {
		return false
	}
	for part := range strings.SplitSeq(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func loadLag(k keys, w When, refs map[string]bool) When {
	k.only("group_by", "pattern", "leader", "follower", "threshold_commits", "threshold_duration", "mode", "reopen_when")
	if len(w.GroupByList) >= 2 || w.GroupBy != "global" {
		k.reject("group_by", "lag_behind requires global")
	}
	w.Leader, w.Follower = k.text("leader"), k.text("follower")
	for _, key := range []string{"leader", "follower"} {
		ref := w.Leader
		if key == "follower" {
			ref = w.Follower
		}
		if !validRef(ref) || !refs[ref] {
			k.reject(key, "expected a full ref name listed in reads.projections")
		}
	}
	if w.Leader == w.Follower {
		k.reject("follower", "must differ from leader")
	}
	_, commits := k.values["threshold_commits"]
	_, duration := k.values["threshold_duration"]
	if !commits && !duration {
		k.reject("threshold_commits", "threshold_commits or threshold_duration is required")
	}
	if commits {
		w.ThresholdCommits = k.positiveInteger("threshold_commits")
	}
	if duration {
		w.ThresholdDuration = k.duration("threshold_duration")
	}
	w.Mode = "any"
	if _, present := k.values["mode"]; present {
		w.Mode = k.text("mode")
		if !commits || !duration {
			k.reject("mode", "requires both thresholds")
		}
		if w.Mode != "any" && w.Mode != "all" {
			k.reject("mode", "expected any or all")
		}
	}
	w.ReopenWhen = k.expression("reopen_when", false)
	return w
}

func checkProjectionOperands(k keys, r Rule, refs, proofRefs map[string]bool) {
	for _, expr := range []struct {
		key string
		exp *Expression
	}{{"match", r.When.Match}, {"reset", r.When.Reset}, {"clear_when", r.When.ClearWhen}, {"after", r.When.After}, {"reopen_when", r.When.ReopenWhen}} {
		if expr.exp == nil {
			continue
		}
		for _, operand := range expr.exp.projections {
			switch {
			case operand.name == "git.ref":
				if !refs[operand.ref] {
					k.reject(expr.key, "git.ref(%q) requires a listed ref in reads.projections", operand.ref)
				}
			case operand.name == "item.fixer_proved":
				if r.When.GroupBy == "global" || !slices.Contains(r.Reads.Projections, "fixer_checks") || !proofRefs[operand.ref] {
					k.reject(expr.key, "item.fixer_proved(%q) requires a non-global subject, fixer_checks, and green_proof:%s in reads.projections", operand.ref, operand.ref)
				}
			case strings.HasPrefix(operand.name, "item."):
				switch operand.name {
				case "item.id", "item.state", "item.seat", "item.waiting_on":
					if r.When.GroupBy == "global" || !slices.Contains(r.Reads.Projections, "items") {
						k.reject(expr.key, "%s requires an items projection and a non-global subject", operand.name)
					}
				default:
					k.reject(expr.key, "unknown items operand %s", operand.name)
				}
			case strings.HasPrefix(operand.name, "charter."):
				switch operand.name {
				case "charter.name", "charter.ready", "charter.blocked", "charter.closed", "charter.states", "charter.finished":
					if !slices.Contains(r.Reads.Projections, "charter") {
						k.reject(expr.key, "%s requires a charter projection", operand.name)
					}
				default:
					k.reject(expr.key, "unknown charter operand %s", operand.name)
				}
			}
		}
	}
}
