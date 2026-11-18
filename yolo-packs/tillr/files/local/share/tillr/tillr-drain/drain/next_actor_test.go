package drain

import "testing"

// A next-actor line that goes on to say why still names its actor: the 2026-11-18 trunk
// fixer wrote `NEXT-ACTOR: owner — resolve the obsolete fixer item ...` seven times, and
// each was read as naming no actor and sent back to its worker.
func TestNextActorLineWithItsReasonStillNamesTheActor(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"NEXT-ACTOR: owner", DesignNextOwner},
		{"NEXT-ACTOR: owner — resolve the obsolete fixer item and arrange the next heavy run.", DesignNextOwner},
		{"**NEXT-ACTOR:** `owner` - reconcile the stale fixer", DesignNextOwner},
		{"NEXT-ACTOR: build (the block stands as written)", DesignNextBuild},
		{"NEXT-ACTOR: owner, because only the owner can rule", DesignNextOwner},
		{"NEXT-ACTOR: ownership is unclear", ""},
		{"NEXT-ACTOR: the owner should decide", ""},
	} {
		if got := designNextActor("report\n" + tc.line + "\nRESULT: blocked"); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.line, got, tc.want)
		}
	}
}
