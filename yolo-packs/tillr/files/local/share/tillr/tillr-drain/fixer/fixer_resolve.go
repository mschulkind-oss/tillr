package fixer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// FixerProvedKind records a trunk fixer resolved by a green heavy proof: the proved
// commit, its proof time and the fixer's checks the proof passed.
const FixerProvedKind = "drain.fixer-proved"

func init() {
	if err := event.Register(FixerProvedKind, Utility); err != nil {
		panic(err)
	}
}

// FixerProof is the payload of FixerProvedKind.
type FixerProof struct {
	FixerItem string   `json:"fixer_item"`
	Green     string   `json:"green"`
	ProvedAt  string   `json:"proved_at"`
	Checks    []string `json:"checks"`
}

// GreenProof aliases heavy.GreenProof.
type GreenProof = heavy.GreenProof

const goTestPrefix = "go-test:"

var goToolModule = regexp.MustCompile(`^tools/[a-z0-9-]+$`)

// GoSuiteProofID is the legacy declaration id for the suite that emitted a named test.
func GoSuiteProofID(id string) string {
	test, ok := strings.CutPrefix(id, goTestPrefix)
	if !ok {
		return ""
	}
	module, _, ok := strings.Cut(test, ":")
	if !ok || !goToolModule.MatchString(module) {
		return ""
	}
	slug := strings.Join(strings.FieldsFunc(module, func(r rune) bool { return r == '/' || r == '-' }), "-")
	return "go-tool/" + slug + "-s-whole-suite-passes"
}

// ProvableChecks is the checks a proof must have passed to cover a fixer.
func ProvableChecks(checks []string) []string {
	out := []string{}
	for _, check := range checks {
		labelled := false
		if !strings.Contains(check, "/") {
			for _, other := range checks {
				labelled = labelled || strings.HasPrefix(other, check+"/")
			}
		}
		if !labelled {
			out = append(out, check)
		}
	}
	return out
}

// ReadGreenProof answers the heavy runner's green pointer and its recorded proof, or a
// zero proof when there is no pointer or no proof for it.
func ReadGreenProof(ctx context.Context, root string) (heavy.GreenProof, error) {
	green, err := heavy.Green(ctx, root)
	if err != nil || green == "" {
		return heavy.GreenProof{}, err
	}
	dir, err := heavy.Directory(ctx, root)
	if err != nil {
		return heavy.GreenProof{}, err
	}
	return ReadGreenProofFile(dir, green)
}

// ReadGreenProofFile reads the record for an exact commit, even after the green pointer
// has advanced. The caller supplies a full commit SHA, not a ref or path.
func ReadGreenProofFile(dir, sha string) (heavy.GreenProof, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "green-"+sha+".json"))
	if os.IsNotExist(err) {
		return heavy.GreenProof{}, nil
	}
	if err != nil {
		return heavy.GreenProof{}, err
	}
	var proof heavy.GreenProof
	if err := json.Unmarshal(raw, &proof); err != nil {
		return heavy.GreenProof{}, err
	}
	if proof.Commit != sha {
		return heavy.GreenProof{}, nil
	}
	return proof, nil
}

// CheckProof is one commit's recorded passes, by check, each with the time it passed.
type CheckProof struct {
	Commit   string
	At       map[string]time.Time
	Whole    bool
	ProvedAt time.Time
}

// Passed answers whether every named check passed in this proof after seen (unix ms).
// A partial proof never covers a fixer that names no check.
func (p CheckProof) Passed(checks []string, seen int64) bool {
	if p.Whole && p.ProvedAt.UnixMilli() <= seen {
		return false
	}
	if !p.Whole && len(checks) == 0 {
		return false
	}
	for _, check := range checks {
		if at, ok := p.At[check]; ok && at.UnixMilli() > seen {
			continue
		}
		if suite := GoSuiteProofID(check); suite != "" && p.At[suite].UnixMilli() > seen {
			continue
		}
		if p.Whole && !strings.Contains(check, "/") {
			matched := false
			prefix := check + "/"
			for k, at := range p.At {
				if strings.HasPrefix(k, prefix) && at.UnixMilli() > seen {
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}
		return false
	}
	return true
}

// Describe is the reason a fixer this proof covers is resolved with.
func (p CheckProof) Describe(checks []string) string {
	named := "no named check"
	if len(checks) > 0 {
		named = strings.Join(checks, ", ")
	}
	if p.Whole {
		return "heavy proof green at " + p.Commit + " (" + p.ProvedAt.UTC().Format("2006-01-02T15:04:05Z") + "), which contains the red commit and passed " + named
	}
	return "the heavy run of " + p.Commit + ", which contains the red commit, passed " + named + " (" + p.ProvedAt.UTC().Format("2006-01-02T15:04:05Z") + "); the rest of that run is not needed to show this fixer's check repaired"
}

// ReadCheckProofs answers the green proof and the current tip run's recorded passes.
func ReadCheckProofs(ctx context.Context, root string) ([]CheckProof, error) {
	proofs := []CheckProof{}
	green, err := ReadGreenProof(ctx, root)
	if err != nil {
		return nil, err
	}
	if green.Commit != "" && !green.ProvedAt.IsZero() && green.Passed != nil {
		at := map[string]time.Time{}
		for _, id := range green.Passed {
			if ClassifyCheck(checkresult.Observation{Check: id, Source: checkresult.CachedRecord, Word: "pass"}).Outcome == checkresult.Pass {
				at[id] = green.ProvedAt
			}
		}
		proofs = append(proofs, CheckProof{Commit: green.Commit, At: at, Whole: true, ProvedAt: green.ProvedAt})
	}
	dir, err := heavy.Directory(ctx, root)
	if err != nil {
		return nil, err
	}
	progress, err := ReadProgressCheckProof(dir, "")
	if err != nil {
		return nil, err
	}
	if progress.Commit != "" {
		proofs = append(proofs, progress)
	}
	return proofs, nil
}

// ReadProgressCheckProof reads the tip run's passes. When rev is set, ignore progress
// made on any other commit, including when native and legacy checks report different tips.
func ReadProgressCheckProof(dir, rev string) (CheckProof, error) {
	tip, at := "", map[string]time.Time{}
	add := func(sha, id string, when time.Time) {
		if sha == "" || when.IsZero() || (rev != "" && sha != rev) || (tip != "" && sha != tip) {
			return
		}
		tip = sha
		if old, ok := at[id]; !ok || when.Before(old) {
			at[id] = when
		}
	}
	var progress HeavyProgress
	if raw, err := os.ReadFile(filepath.Join(dir, "heavy-progress.json")); err == nil {
		if err := json.Unmarshal(raw, &progress); err != nil {
			return CheckProof{}, err
		}
		for id, check := range progress.Checks {
			if ClassifyCheck(checkresult.Observation{Check: id, Source: checkresult.CachedRecord, Word: check.Result}).Outcome == checkresult.Pass {
				add(progress.TipSHA, id, progress.PassedAt[id])
			}
		}
	} else if !os.IsNotExist(err) {
		return CheckProof{}, err
	}
	var legacy struct {
		TipSHA string `json:"tip_sha"`
		Checks map[string]struct {
			ProvedAt string `json:"proved_at"`
		} `json:"checks"`
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "heavy-legacy-progress.json")); err == nil {
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return CheckProof{}, err
		}
		for id, check := range legacy.Checks {
			if when, err := time.Parse(time.RFC3339, check.ProvedAt); err == nil &&
				ClassifyCheck(checkresult.Observation{Check: id, Source: checkresult.CachedRecord, Word: "pass"}).Outcome == checkresult.Pass {
				add(legacy.TipSHA, id, when)
			}
		}
	} else if !os.IsNotExist(err) {
		return CheckProof{}, err
	}
	latest := time.Time{}
	for _, when := range at {
		if when.After(latest) {
			latest = when
		}
	}
	return CheckProof{Commit: tip, At: at, ProvedAt: latest}, nil
}

// FixerProvedGreen answers the green commit a FixerProvedKind event recorded for a
// fixer, or "" when no proof resolved it.
func FixerProvedGreen(r *store.Reader, fixer string) (string, error) {
	var raw []byte
	err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, FixerProvedKind, fixer).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var p FixerProof
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", err
	}
	return p.Green, nil
}

// FixerProvedAfter answers whether a FixerProvedKind event was already recorded for
// fixer at or after seen.
func FixerProvedAfter(r *store.Reader, fixer string, seen int64) (bool, error) {
	var ok bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_id=? AND ts>=?)`, FixerProvedKind, fixer, seen).Scan(&ok)
	return ok, err
}

// FixerSeen answers the timestamp of the fixer's latest red observation (filing or a
// newer red attachment).
func FixerSeen(r *store.Reader, fixer string) (int64, error) {
	var seen sql.NullInt64
	if err := r.QueryRow(`SELECT MAX(ts) FROM events WHERE object_id=? AND kind IN (?, ?)`, fixer, item.AddKind, FixerAttachedKind).Scan(&seen); err != nil {
		return 0, err
	}
	return seen.Int64, nil
}

// FixerProvedProof answers the latest FixerProvedKind recorded for fixer at or after its
// latest red observation, or nil when none exists.
func FixerProvedProof(r *store.Reader, fixer string) (*FixerProof, error) {
	seen, err := FixerSeen(r, fixer)
	if err != nil {
		return nil, err
	}
	var raw []byte
	err = r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? AND ts>=? ORDER BY seq DESC LIMIT 1`, FixerProvedKind, fixer, seen).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p FixerProof
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// FixerProvedGreenAfterSeen answers the green commit a FixerProvedKind event recorded at
// or after the fixer's latest red observation, or "" when none exists.
func FixerProvedGreenAfterSeen(r *store.Reader, fixer string) (string, error) {
	p, err := FixerProvedProof(r, fixer)
	if err != nil || p == nil {
		return "", err
	}
	return p.Green, nil
}

// ReadIntermittentFixer answers whether a fixer has a FixerProvedKind record at or after
// its latest red observation (a rerun passed its checks) and the checks named in that proof.
func ReadIntermittentFixer(r *store.Reader, fixer string) (bool, []string, error) {
	p, err := FixerProvedProof(r, fixer)
	if err != nil || p == nil {
		return false, nil, err
	}
	return true, p.Checks, nil
}
