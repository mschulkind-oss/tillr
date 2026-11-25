package buildstamp_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/buildstamp"
)

func TestEmptyStampNamesAbsence(t *testing.T) {
	got := buildstamp.Stamp{}.String()
	for _, want := range []string{"no-revision", "unknown-time", "green unknown", "no-digest", "unknown-exe"} {
		if !strings.Contains(got, want) {
			t.Fatalf("empty stamp %q missing %q", got, want)
		}
	}
}

func TestStampFormatsFields(t *testing.T) {
	s := buildstamp.Stamp{
		Revision:    "1234567890abcdef",
		GreenCommit: "fedcba0987654321",
		Time:        "2026-11-25T00:00:00Z",
		Modified:    true,
		Digest:      "abcdef1234567890",
		Executable:  "/usr/local/bin/tillr",
	}
	want := "1234567890ab+dirty (2026-11-25T00:00:00Z) green fedcba0987654321 digest abcdef123456 exe /usr/local/bin/tillr"
	if got := s.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadReturnsDigestAndExecutable(t *testing.T) {
	s := buildstamp.Read()
	if s.Digest == "" {
		t.Fatal("Read().Digest is empty")
	}
	if s.Executable == "" {
		t.Fatal("Read().Executable is empty")
	}
	line := s.String()
	if strings.Contains(line, "\n") {
		t.Fatalf("Read().String() is multi-line: %q", line)
	}
	for _, want := range []string{"green ", "digest ", "exe "} {
		if !strings.Contains(line, want) {
			t.Fatalf("Read().String() = %q, missing %q", line, want)
		}
	}
}
