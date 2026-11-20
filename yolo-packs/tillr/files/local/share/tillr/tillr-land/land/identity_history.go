package land

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr-land/identityhistory"
	"github.com/mschulkind-oss/tillr/store"
)

// ReasonForeignIdentity holds publication, not execution, for an unanswered trunk identity.
const ReasonForeignIdentity = identityhistory.ReasonForeignIdentity
const identityDDL = `
CREATE TABLE IF NOT EXISTS trunk_identity (id INTEGER PRIMARY KEY CHECK(id=1), tip TEXT NOT NULL, pending TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS trunk_identity_acceptances (sha TEXT PRIMARY KEY, accepted_by TEXT NOT NULL, reason TEXT NOT NULL);
`

type ForeignCommit = identityhistory.ForeignCommit
type IdentityHistory = identityhistory.IdentityHistory

func ReadIdentityHistory(r rowQueryer) (IdentityHistory, error) {
	return identityhistory.ReadIdentityHistory(r)
}

func SaveIdentityHistory(tx *store.Tx, h IdentityHistory) error {
	raw, err := json.Marshal(h.Pending)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO trunk_identity VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET tip=excluded.tip,pending=excluded.pending`, h.Tip, string(raw))
	return err
}

// AcceptIdentity records an explicit decision for exactly one immutable commit id.
func AcceptIdentity(tx *store.Tx, sha, by, why string) error {
	if len(sha) != 40 && len(sha) != 64 {
		return Usagef("accept-identity needs a full commit id")
	}
	for _, c := range sha {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return Usagef("accept-identity needs a full commit id")
		}
	}
	if strings.TrimSpace(by) == "" || strings.TrimSpace(why) == "" {
		return Usagef("accept-identity needs --by and --why")
	}
	_, err := tx.Exec(`INSERT INTO trunk_identity_acceptances VALUES(?,?,?) ON CONFLICT(sha) DO NOTHING`, sha, by, why)
	return err
}

func identityAccepted(r rowQueryer, sha string) (bool, error) {
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='trunk_identity_acceptances' AND type='table')`).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	err := r.QueryRow(`SELECT 1 FROM trunk_identity_acceptances WHERE sha=?`, sha).Scan(new(int))
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// PublishedRefPrefix holds one pointer per remote branch, naming a commit this clone
// confirmed on that remote (docs/design/tillr-refused-push.md, Section 4.3). A commit
// reachable from one was already published by someone with push access.
const PublishedRefPrefix = "refs/tillr/published/"

// publishedTips lists the commits the published pointers name.
func (t *Trunk) publishedTips() ([]string, error) {
	raw, err := t.git("for-each-ref", "--format=%(objectname)", PublishedRefPrefix)
	if err != nil {
		return nil, err
	}
	return strings.Fields(raw), nil
}

// onPublished reports whether sha is reachable from any published pointer.
func (t *Trunk) onPublished(tips []string, sha string) (bool, error) {
	for _, tip := range tips {
		on, err := t.historyAncestor(sha, tip)
		if err != nil || on {
			return on, err
		}
	}
	return false, nil
}

// CheckIdentities includes all parents in old..tip and rechecks durable unanswered
// commits. On first observation only the current tip is checked; earlier history
// predates monitoring. A rewrite checks the replacement range, never the lost side.
// A commit reachable from a published pointer is trusted: it arrived from the remote.
func (t *Trunk) CheckIdentities(r rowQueryer, h IdentityHistory, old, tip string) (IdentityHistory, error) {
	identity, err := t.LandingIdentity()
	if err != nil {
		return h, err
	}
	rangeArg := tip
	args := []string{"rev-list", "--reverse", rangeArg}
	if old != "" {
		args = []string{"rev-list", "--reverse", old + ".." + tip}
	} else {
		args = append(args, "--max-count=1")
	}
	raw, err := t.git(args...)
	if err != nil {
		return h, err
	}
	ids := strings.Fields(raw)
	for _, c := range h.Pending {
		on, err := t.historyAncestor(c.SHA, tip)
		if err != nil {
			return h, err
		}
		if on {
			ids = append(ids, c.SHA)
		}
	}
	published, err := t.publishedTips()
	if err != nil {
		return h, err
	}
	next := IdentityHistory{Tip: tip}
	seen := map[string]bool{}
	for _, sha := range ids {
		if seen[sha] {
			continue
		}
		seen[sha] = true
		accepted, err := identityAccepted(r, sha)
		if err != nil {
			return h, err
		}
		if accepted {
			continue
		}
		if on, err := t.onPublished(published, sha); err != nil {
			return h, err
		} else if on {
			continue
		}
		raw, err := t.git("show", "-s", "--format=%an%x00%ae%x00%cn%x00%ce", sha)
		if err != nil {
			return h, err
		}
		fields := strings.Split(strings.TrimSuffix(raw, "\n"), "\x00")
		if len(fields) != 4 {
			return h, Externalf("invalid identities for %s", sha)
		}
		if fields[0] != identity.Name || fields[1] != identity.Email || fields[2] != identity.Name || fields[3] != identity.Email {
			next.Pending = append(next.Pending, ForeignCommit{SHA: sha, Author: fields[0] + " <" + fields[1] + ">", Committer: fields[2] + " <" + fields[3] + ">"})
		}
	}
	return next, nil
}

// IdentityRewrite compares the full ordered parent graph. Only author and committer
// headers may differ: trees, messages, other headers and parent order must survive.
// The returned mapping also carries proof pointers onto their unchanged trees.
func (t *Trunk) IdentityRewrite(old, current string) (map[string]string, bool, error) {
	mapped := map[string]string{}
	reverse := map[string]string{}
	var compare func(string, string) (bool, error)
	compare = func(a, b string) (bool, error) {
		if prior, ok := mapped[a]; ok {
			return prior == b, nil
		}
		if prior, ok := reverse[b]; ok && prior != a {
			return false, nil
		}
		if a == b {
			mapped[a], reverse[b] = b, a
			return true, nil
		}
		// A commit retained on both sides cannot also be substituted by an
		// identity twin elsewhere. That would collapse or reorder the graph.
		for _, pair := range [][2]string{{a, current}, {b, old}} {
			shared, err := t.historyAncestor(pair[0], pair[1])
			if err != nil || shared {
				return false, err
			}
		}
		x, err := t.git("cat-file", "commit", a)
		if err != nil {
			return false, err
		}
		y, err := t.git("cat-file", "commit", b)
		if err != nil {
			return false, err
		}
		strip := func(raw string) (string, []string) {
			header, msg, _ := strings.Cut(raw, "\n\n")
			var keep, parents []string
			for _, line := range strings.Split(header, "\n") {
				switch {
				case strings.HasPrefix(line, "author "), strings.HasPrefix(line, "committer "):
				case strings.HasPrefix(line, "parent "):
					parents = append(parents, strings.TrimPrefix(line, "parent "))
				default:
					keep = append(keep, line)
				}
			}
			return strings.Join(keep, "\n") + "\n\n" + msg, parents
		}
		xx, ap := strip(x)
		yy, bp := strip(y)
		if xx != yy || len(ap) != len(bp) {
			return false, nil
		}
		mapped[a] = b
		reverse[b] = a
		for i := range ap {
			ok, err := compare(ap[i], bp[i])
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	}
	ok, err := compare(old, current)
	return mapped, ok, err
}

// CarryIdentityAcceptances copies an accepted commit's acceptance to the commit that replaced
// it, for each old -> new pair of a rebase of the trunk, so replaying an accepted local commit
// onto a moved remote does not hold landings on its foreign identity again
// (docs/design/tillr-refused-push.md, Section 4.3). A store with no acceptances table has
// nothing to carry.
func CarryIdentityAcceptances(tx *store.Tx, pairs map[string]string) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='trunk_identity_acceptances' AND type='table')`).Scan(&exists); err != nil || !exists {
		return err
	}
	for old, now := range pairs {
		if now == "" || now == old {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO trunk_identity_acceptances(sha,accepted_by,reason) SELECT ?, accepted_by, reason || ' (carried from ' || sha || ')' FROM trunk_identity_acceptances WHERE sha=? ON CONFLICT(sha) DO NOTHING`, now, old); err != nil {
			return err
		}
	}
	return nil
}
