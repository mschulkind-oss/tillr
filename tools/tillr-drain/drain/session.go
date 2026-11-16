package drain

import (
	"crypto/rand"
	"fmt"

	"github.com/mschulkind-oss/tillr-run/run"
)

// SessionIDFile is the harness session this run was launched under, as staging minted it:
// one UUID, newline-terminated, beside the brief in the lane directory. It is an input the
// adapters read and tillr never interprets — `run-pi` hands it to `pi --session-id`, and
// `pi` uses the current directory's existing session with that id, creating it otherwise.
//
// The file is what makes a run resumable at all. Nothing wrote it before this face
// (measured 2026-11-14: its only writer was tests/tillr-muse-backend.test.py), so `pi`
// minted an id of its own, the driver never learned which session a run used, and a killed
// run's transcript — 234 KB of it, sitting under the harness's session directory — could
// not be named by the loop, let alone continued.
//
// The name and the form are run's, aliased rather than re-spelled: staging writes the file,
// `run exec` reads it back to prove a resume has a session to continue, and a second
// spelling is how a lane ends up with a session id nothing continues.
const SessionIDFile = run.SessionIDFile

// NewSessionID mints one harness session id: a version-4 UUID from the kernel's entropy.
//
// It is a UUID and not the run id because the harness owns the namespace it looks a session
// up in, and every harness tillr drives takes a UUID there (`pi --session-id`,
// `muse exec --session-id`). A run id in that flag would be a second naming scheme in a
// namespace this tree does not own.
func NewSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", Internalf("minting a session id: %v", err)
	}
	// RFC 4122 version 4 and its variant bits: the flag's consumer may not care, but a
	// versionless random string is a UUID-shaped thing that a validator elsewhere refuses.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// ReadSessionID answers the session id a lane directory carries, or "" when it carries none.
// A value that is not UUID-shaped is refused rather than returned: the file exists for the
// adapters, so a malformed one is a defect to report and not a value to pass on.
func ReadSessionID(laneDir string) (string, error) {
	return run.ReadSessionID(laneDir)
}

// sessionIDFor answers the session id this dispatch runs under: the caller's when it named
// one, else a fresh mint. It never reads the lane's own session id, and that is the rule
// rather than an omission.
//
// A lane is not a session. The lane directory is reused for every item the lane runs, so the
// `session-id` beside the brief describes the run that died or landed there last — reading it
// back is how two unrelated runs come to share one harness transcript. Measured 2026-11-15:
// `item-stateless-squash-lander` and `item-lockless-per-lane-drain` both ran on `lane-1` under
// `--session-id 70a4f422-ae04-4b91-a1c2-b63c9c9caa13`, so the second item's context carried
// the first item's words and the provider's prefix cache was billed for a conversation that
// belonged to different work.
//
// A continuation is the one thing that may reuse a lane's id, and it says so by naming the id
// it continues: `Resume` reads the lane's `session-id` and the request carries it. A caller
// that names nothing gets a session of its own.
func sessionIDFor(pinned string) (string, error) {
	if pinned == "" {
		return NewSessionID()
	}
	if !run.IsSessionID(pinned) {
		return "", Usagef("the session id %q is not a UUID: a session id is what a harness keys its session on, and every adapter passes it to one of their flags", pinned)
	}
	return pinned, nil
}
