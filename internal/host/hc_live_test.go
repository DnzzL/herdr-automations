//go:build hc

// Runs against the real herdr-core and an open Herdr app window:
//
//	go test -tags hc ./internal/host/ -run Live
//
// It pins what ADR 0010 rests on and the unit tests can only script: that the
// app adopts a terminal by its labels, and how hc reports the two endings a
// run must tell apart.
package host

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func liveHost(t *testing.T) *hcHost {
	t.Helper()
	if exec.Command("herdr-app", "status").Run() != nil {
		t.Skip("needs an open Herdr app window")
	}
	h := NewHC().(*hcHost)
	h.knobs.waitSlice = 5 * time.Second
	return h
}

func appShows(t *testing.T, id string) bool {
	t.Helper()
	out, err := exec.Command("herdr-app", "ls").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(out), id)
}

func TestLiveSpaceIsAdoptedAndClosingItCancelsTheRun(t *testing.T) {
	h := liveHost(t)
	space := newID("sp")
	addr, err := h.ops.Run(t.TempDir(), map[string]string{"herdr.space": space, "herdr.tab": newID("tb")},
		[]string{"sh", "-c", "sleep 120"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("hc", "rm", addr).Run() })
	time.Sleep(time.Second)
	if !appShows(t, space) {
		t.Fatalf("the app did not adopt space %s", space)
	}

	done := make(chan error, 1)
	go func() { done <- h.Do(Session{WorkspaceID: space, PaneID: addr}, hcAutomation(), time.Minute) }()
	time.Sleep(time.Second)
	if out, err := exec.Command("herdr-app", "do", "space.close", "--id", space, "--confirmed", "true").CombinedOutput(); err != nil {
		t.Fatalf("space.close: %v %s", err, out)
	}
	if err := <-done; !errors.Is(err, ErrCancelled) {
		t.Fatalf("closing the space: want cancelled, got %v", err)
	}
}

func TestLiveAProgramThatExitsFails(t *testing.T) {
	h := liveHost(t)
	addr, err := h.ops.Run(t.TempDir(), map[string]string{"herdr.space": newID("sp"), "herdr.tab": newID("tb")},
		[]string{"sh", "-c", "sleep 1; exit 3"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("hc", "rm", addr).Run() })
	err = h.Do(Session{PaneID: addr}, hcAutomation(), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "code 3") {
		t.Fatalf("want an exit code 3 failure, got %v", err)
	}
}
