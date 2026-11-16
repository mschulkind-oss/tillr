package drain

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

const (
	// RunStderrFile holds the run face's diagnostics, not the adapter's stream.
	RunStderrFile = "run.stderr"
	// RunRecordFile holds the record the run face prints on stdout.
	RunRecordFile = "run.record.json"
)

func (s *ProcessSteps) runOutputDir(runID string) string {
	if s.Root != "" {
		return RunDir(s.Root, runID)
	}
	// A caller without a repository still needs per-run files, not pipes.
	return filepath.Join(s.LaneRoot, ".runs", runID)
}

// runOutputTail reads only the bounded end of the diagnostics. Older launchers
// recorded none; only that absence is normal, not other filesystem failures.
func runOutputTail(dir string) (string, error) {
	path := filepath.Join(dir, RunStderrFile)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "no output recorded", nil
	}
	if err != nil {
		return "", Externalf("reading run output %s: %v", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", Externalf("reading run output %s: %v", path, err)
	}
	var tail stderrTail
	if offset := info.Size() - StderrTailBytes; offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return "", Externalf("reading run output %s: %v", path, err)
		}
		tail.dropped = true
	}
	if _, err := io.Copy(&tail, file); err != nil {
		return "", Externalf("reading run output %s: %v", path, err)
	}
	return tail.String(), nil
}
