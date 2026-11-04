package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMain lets the test binary stand in for herdr-automations, so a test can
// drive the real CLI as a separate process. HERDR_TEST_CLI=1 runs main();
// HERDR_TEST_SLOW_RUN makes a plain `run <name>` sleep, then drop a marker
// file there, standing in for a Run that takes a while.
func TestMain(m *testing.M) {
	if os.Getenv("HERDR_TEST_CLI") == "1" {
		if marker := os.Getenv("HERDR_TEST_SLOW_RUN"); marker != "" && len(os.Args) == 3 && os.Args[1] == "run" {
			time.Sleep(2 * time.Second)
			_ = os.WriteFile(marker, []byte(os.Args[2]), 0o644)
			os.Exit(0)
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The panel cannot wait on a Run for an hour, and closing the app must not end
// it: `run --bg` returns at once and the Run carries on without it.
func TestRunBgReturnsWhileTheRunKeepsGoing(t *testing.T) {
	state := t.TempDir()
	marker := filepath.Join(t.TempDir(), "run-finished")
	cmd := exec.Command(os.Args[0], "run", "nightly", "--bg")
	cmd.Env = append(os.Environ(), "HERDR_TEST_CLI=1", "HERDR_TEST_SLOW_RUN="+marker, "HERDR_PLUGIN_STATE_DIR="+state)

	start := time.Now()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run --bg: %v (%s)", err, out)
	}
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Errorf("run --bg took %v; it must return before the Run ends", took)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the Run finished before run --bg returned; it did not run in the background")
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		if got, err := os.ReadFile(marker); err == nil {
			if string(got) != "nightly" {
				t.Errorf("background run got %q, want nightly", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the Run never finished after run --bg returned; it died with its parent")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
