package drain

import (
	"bufio"
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

// runOutputFiles is where Run puts the face's two streams (runStreamLocator). Run
// creates both before it starts the face, and the face and every descendant that
// inherits them hold them open for writing while they live.
func (s *ProcessSteps) runOutputFiles(runID string) []string {
	dir := s.runOutputDir(runID)
	return []string{filepath.Join(dir, RunRecordFile), filepath.Join(dir, RunStderrFile)}
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
	var reader io.Reader = file
	if offset := info.Size() - StderrTailBytes; offset > 0 {
		// Seek one byte earlier so a cut that already lands on a line boundary
		// consumes that newline and keeps the complete line starting at offset.
		if _, err := file.Seek(offset-1, io.SeekStart); err != nil {
			return "", Externalf("reading run output %s: %v", path, err)
		}
		tail.Dropped = true
		buffered := bufio.NewReader(file)
		if _, err := buffered.ReadBytes('\n'); err != nil && !errors.Is(err, io.EOF) {
			return "", Externalf("reading run output %s: %v", path, err)
		}
		reader = buffered
	}
	if _, err := io.Copy(&tail, reader); err != nil {
		return "", Externalf("reading run output %s: %v", path, err)
	}
	return tail.String(), nil
}
