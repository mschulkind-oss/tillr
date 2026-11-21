package drain

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func designContinuationWork(c item.Conflict, work *pendingWork) *pendingWork {
	if c.Status == "raised" && c.DesignRunID != "" && c.TriageVersion == "" && work != nil && work.DesignQuotaBackend == "" && work.Decision.RunRequest != nil && work.Decision.RunRequest.RunID == c.DesignRunID {
		return work
	}
	return nil
}

// A design repair is a new judged run, not a second examination of the refused run.
// Pi identifies its session within the original cwd, so plan reserves that lane.
func (l *Loop) prepareDesignContinuation(ctx context.Context, r *store.Reader, d *Decision, work *pendingWork, base *string) error {
	c := d.BlockConflict
	if c == nil || designContinuationWork(*c, work) == nil {
		return nil
	}
	if d.LaneID != work.Decision.LaneID && !isExpressLane(work.Decision.LaneID) {
		return Internalf("design continuation of %s was offered a different lane", c.DesignRunID)
	}
	origin := c.DesignRunID
	d.DesignContinuationFrom = origin
	dir := RunDir(l.cfg.Root, origin)
	session, err := ReadSessionID(dir)
	if err != nil {
		return err
	}
	rec, err := terminalRecord(r, origin)
	if err != nil {
		return err
	}
	old := work.Decision.RunRequest
	available := d.LaneID == work.Decision.LaneID && session != "" && designTranscriptAvailable(rec)
	why := "is unavailable"
	if d.LaneID != work.Decision.LaneID {
		why = "is on another lane"
	}
	if available && len(old.Command) > 0 {
		capability, err := os.ReadFile(filepath.Join(filepath.Dir(old.Command[0]), "pause-kind"))
		if err == nil && strings.TrimSpace(string(capability)) == run.PauseCannotResume {
			available = false
			why = "cannot be resumed by its adapter"
		}
	}
	next := *c
	next.Text += "\n\nThe recorded session for design run " + origin + " " + why + "; starting a fresh design session."
	if available {
		request := *d.RunRequest
		d.RunRequest = &request
		d.RunRequest.Backend, d.RunRequest.Command = old.Backend, old.Command
		d.RunRequest.SessionTurn = true
		d.RunRequest.ResumePrompt = "Continue the same design session. The current conflict and feedback follow:\n\n" + c.Text
		d.DesignerSessionID = session
		d.SelectSeat = false
		next.Text = c.Text + "\n\nContinuing the recorded designer session from run " + origin + "."
	}
	repairBase, expectedHead := work.BaseSHA, ""
	if work.Candidate != nil {
		expectedHead = work.Candidate.HeadSHA
	}
	_, refusal, err := designOutcome(r, origin, "", "")
	if err != nil {
		return err
	}
	if refusal.DesignHeadSHA != "" {
		repairBase, expectedHead = refusal.DesignBaseSHA, refusal.DesignHeadSHA
	}
	if repairBase != "" {
		pinRef := "refs/tillr/runs/" + origin + "^{commit}"
		pin, pinErr := l.git(ctx, l.cfg.Root, "rev-parse", "--verify", "--quiet", pinRef)
		pin = strings.TrimSpace(pin)
		if expectedHead != "" && (pinErr != nil || pin != expectedHead) {
			return Externalf("cannot stage design continuation of %s: %s does not name the recorded candidate %s", origin, pinRef, expectedHead)
		}
		if pinErr == nil {
			d.DesignContinuationHead = pin
			*base = repairBase
		}
	}
	d.BlockConflict = &next
	return nil
}

// Adapters that record a transcript path let us distinguish a retained UUID from
// a deleted session. Without that metadata the adapter checks on SessionTurn.
func designTranscriptAvailable(rec *run.Record) bool {
	if rec == nil {
		return false
	}
	transcript := ""
	for _, ref := range rec.ArtifactRefs {
		switch ref.Kind {
		case "transcript":
			transcript = ref.Ref
		case "log":
			f, err := os.Open(ref.Ref)
			if err != nil {
				continue
			}
			scan := bufio.NewScanner(f)
			scan.Buffer(make([]byte, 64<<10), 16<<20)
			for scan.Scan() {
				var event struct{ Type, Transcript string }
				if json.Unmarshal(scan.Bytes(), &event) == nil && event.Type == "session" && event.Transcript != "" {
					transcript = event.Transcript
				}
			}
			f.Close()
		}
	}
	if transcript == "" {
		return true
	}
	info, err := os.Stat(transcript)
	return err == nil && info.Mode().IsRegular()
}
