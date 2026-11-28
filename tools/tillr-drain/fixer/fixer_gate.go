package fixer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// FixerCheckOverlaps recognizes exact ids and bare kinds, which select all
// labelled checks of that kind. Two different labelled checks do not overlap.
func FixerCheckOverlaps(a, b, root string, declID func(kind, label, root string) string) bool {
	canonical := func(id string) (kind, name string) {
		if k, label, ok := strings.Cut(id, ":"); ok {
			if strings.HasPrefix(id, goTestPrefix) {
				return k, id
			}
			if declID != nil {
				return k, declID(k, label, root)
			}
			return k, label
		}
		if k, _, ok := strings.Cut(id, "/"); ok {
			return k, id
		}
		return id, ""
	}
	ak, an := canonical(a)
	bk, bn := canonical(b)
	return ak == bk && (an == "" || bn == "" || an == bn)
}

// FixerWaitReaches reports whether from's waiting chain reaches target.
func FixerWaitReaches(r *store.Reader, from, target string) (bool, error) {
	seen := map[string]bool{}
	for IsTrunkFixer(from) && !seen[from] {
		if from == target {
			return true, nil
		}
		seen[from] = true
		wait, _, err := item.ReadWaitingOn(r, from)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if wait == nil || !strings.HasPrefix(wait.Waiter, "item:") {
			break
		}
		from = strings.TrimPrefix(wait.Waiter, "item:")
	}
	return false, nil
}

// FixerHeavyDeclaration reports whether id names a tree-scoped heavy declaration file in dir.
func FixerHeavyDeclaration(dir, id string) bool {
	if filepath.Base(id) != id {
		return false
	}
	checksDir, err := settings.ReadChecksDir(dir)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(checksDir), id+".json"))
	var decl struct{ ID, Scope, Tier string }
	return err == nil && json.Unmarshal(raw, &decl) == nil && decl.ID == id && decl.Scope == "tree" && decl.Tier == "heavy"
}

// PendingFixerVerdict preserves the ordinary gate's tree/plan binding (and any
// inherited failures), while marking it non-landable during the named phase.
func PendingFixerVerdict(path string) (func(string, []map[string]string) error, string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) { // Steps that do not publish a verdict (e.g. test stubs).
		return func(string, []map[string]string) error { return nil }, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	env, err := record.Decode(raw)
	if err != nil {
		return nil, "", err
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil, "", err
	}
	originalResult := payload["result"]
	var ordinary []json.RawMessage
	if err := json.Unmarshal(payload["checks"], &ordinary); err != nil {
		return nil, "", err
	}
	wholeLog := ""
	for _, raw := range ordinary {
		var check struct {
			LogRef string `json:"log_ref"`
		}
		if json.Unmarshal(raw, &check) == nil && filepath.Dir(check.LogRef) == filepath.Join(filepath.Dir(path), "logs") && strings.HasSuffix(check.LogRef, ".triage.log") {
			wholeLog = strings.TrimSuffix(check.LogRef, ".triage.log") + ".log"
			break
		}
	}
	write := func(result string, named []map[string]string) error {
		payload["result"] = originalResult
		if result != "" {
			payload["result"], _ = json.Marshal(result)
		}
		checks := append([]json.RawMessage(nil), ordinary...)
		for _, check := range named {
			raw, _ := json.Marshal(check)
			checks = append(checks, raw)
		}
		payload["checks"], _ = json.Marshal(checks)
		env.Payload, err = json.Marshal(payload)
		if err != nil {
			return err
		}
		raw, err := record.Encode(env)
		if err != nil {
			return err
		}
		return os.WriteFile(path, raw, 0644)
	}
	return write, wholeLog, write("error", nil)
}
