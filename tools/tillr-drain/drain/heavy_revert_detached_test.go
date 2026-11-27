package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestDetachedHeavyEarlyRedRevertsCulpritAndRunsReplay(t *testing.T) {
	config := filepath.Join(scratchUnderDurable(t), "gitconfig")
	writeForkFile(t, config, "[user]\n\tname = Fixture\n\temail = fixture@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checksDir, "unit.json"), `{"id":"unit","scope":"tree","tier":"heavy","reads":["broken"]}`)
	base := heavyCommit(t, root, "base-with-check")
	heavyGit(t, root, "update-ref", GreenRef, base)

	if err := os.WriteFile(filepath.Join(root, "broken"), []byte("bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "add", "broken")
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "culprit")
	culprit := heavyGit(t, root, "rev-parse", "HEAD")

	s, err := store.InitFixture(context.Background(), filepath.Join(work, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := writeFixture(s.Path(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,rank,updated_at) VALUES(?,?,?,?,0)`, "item-broke", "item-broke", StateLanded, "item-broke"); err != nil {
			return err
		}
		return recordFixtureLanding(tx, "item-broke", "run-item-broke", culprit, 1)
	}); err != nil {
		t.Fatal(err)
	}

	callsLog := filepath.Join(work, "heavy-calls.log")
	heavyGateBin := executable(t, fmt.Sprintf(`only=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = --only ]; then shift; only=$1; fi
  shift
done
sha=$(git rev-parse HEAD)
echo "SHA=$sha ONLY=$only" >> %s
if [ -e broken ]; then
  printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"unit","result":"fail","reads":["broken"],"detail":"broken exists"}]}}\n'
else
  printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"unit","result":"pass","reads":["broken"]}]}}\n'
fi`, callsLog))
	fastGateBin := executable(t, `printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"fast","result":"pass"}]}}\n'`)
	landBin := executable(t, fmt.Sprintf(`head=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = --head ]; then shift; head=$1; fi
  shift
done
git -C %s update-ref refs/heads/main "$head"
printf '{"record_version":1,"kind":"land.landing","id":"landing-revert","producer":{"system":"tillr","utility":"land","version":"0.1.0"},"payload":{"result":"landed","trunk_after":"%%s"}}\n' "$head"`, root))

	steps := &ProcessSteps{
		Root:         root,
		LaneRoot:     work,
		Store:        s.Path(),
		GateProgram:  fastGateBin,
		HeavyGateBin: heavyGateBin,
		LandProgram:  landBin,
		Now:          time.Now,
	}
	report := &bytes.Buffer{}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge", LaneBase: "green", AutoRevert: true},
		cfg: LoopConfig{
			Root:     root,
			Store:    s.Path(),
			LaneRoot: work,
			Steps:    steps,
			Now:      time.Now,
			Report:   report,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	if err := l.startHeavy(ctx, culprit); err != nil {
		t.Fatalf("startHeavy(culprit): %v", err)
	}
	l.heavyWG.Wait()

	trunk := heavyGit(t, root, "rev-parse", TrunkRef)
	if trunk == culprit {
		t.Fatalf("trunk stayed at culprit %s; expected revert to land\nreport:\n%s", shortSHA(culprit), report.String())
	}
	if exec.Command("git", "-C", root, "cat-file", "-e", trunk+":broken").Run() == nil {
		t.Fatalf("broken file still present on trunk %s", shortSHA(trunk))
	}

	rawCalls, err := os.ReadFile(callsLog)
	if err != nil {
		t.Fatalf("reading heavy calls log: %v", err)
	}
	if !strings.Contains(string(rawCalls), "ONLY=unit") {
		t.Fatalf("expected replay with --only unit to run; calls:\n%s", rawCalls)
	}

	rs, err := store.Open(ctx, s.Path(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var payloadRaw string
	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1`, RevertKind).Scan(&payloadRaw)
	}); err != nil {
		t.Fatalf("querying %s event: %v", RevertKind, err)
	}
	var payload revertPayload
	if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
		t.Fatalf("unmarshaling %s payload: %v", RevertKind, err)
	}
	if payload.Result != "reverted" || payload.Landing != culprit {
		t.Fatalf("revert payload = %+v, want Result=reverted for %s", payload, shortSHA(culprit))
	}
}
