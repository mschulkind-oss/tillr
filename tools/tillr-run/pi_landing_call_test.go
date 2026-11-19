package main_test

import (
	"os/exec"
	"testing"
)

func TestPiLandingCallAdapterTests(t *testing.T) {
	for _, argv := range [][]string{
		{"node", "--test", "land.test.mjs", "keep-going.test.mjs"},
		{"python3", "extract-report-park.test.py"},
		{"python3", "parked-exit.test.py"},
	} {
		t.Run(argv[len(argv)-1], func(t *testing.T) {
			path, err := exec.LookPath(argv[0])
			if err != nil {
				t.Fatalf("find %s: %v", argv[0], err)
			}
			cmd := exec.Command(path, argv[1:]...)
			cmd.Dir = "backends/pi/testdata"
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %v\n%s", argv, err, output)
			}
		})
	}
}
