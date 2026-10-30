// Package history persists one JSONL record per run under the plugin state
// dir. Append-only: readers reconstruct the latest state per run ID.
package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
)

type Status string

const (
	StatusScheduled Status = "scheduled"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusFailed    Status = "failed"
	StatusSkipped   Status = "skipped"
	// StatusMissed records an occurrence the scheduler could not run at all —
	// typically the machine was asleep past the catch-up window. Recording it
	// is the point: a silently absent run is worse than a visible failure.
	StatusMissed Status = "missed"
	// StatusInvalid records an automation the scheduler could not even read —
	// a broken entry in automations.yaml. It is not a run that failed, it is a
	// run that was never possible, and it belongs in the log for the same
	// reason StatusMissed does: the board is where you look.
	StatusInvalid Status = "invalid"
	// StatusCancelled records a run whose workspace was closed under it.
	// Closing a run's workspace is how you call one off, so it is not a
	// failure: nothing broke, somebody decided.
	StatusCancelled Status = "cancelled"
)

// Trigger is why a run started. CONTEXT.md names three.
type Trigger string

const (
	// TriggerCron: it came due and ran on time.
	TriggerCron Trigger = "cron"
	// TriggerCatchup: it came due while the machine was unavailable and ran
	// late, inside the catch-up window.
	TriggerCatchup Trigger = "catchup"
	// TriggerManual: somebody asked for it — `r` on the board, or `run`.
	TriggerManual Trigger = "manual"
)

// NewID names a run in the log. Everything that appends here built the same
// name-nanos shape by hand, three times, with three different infixes.
func NewID(automation, kind string) string {
	if kind != "" {
		automation += "-" + kind
	}
	return fmt.Sprintf("%s-%d", automation, time.Now().UnixNano())
}

type Record struct {
	RunID       string    `json:"run_id"`
	Automation  string    `json:"automation"`
	Status      Status    `json:"status"`
	At          time.Time `json:"at"`
	Trigger     Trigger   `json:"trigger,omitempty"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	PaneID      string    `json:"pane_id,omitempty"`
	Error       string    `json:"error,omitempty"`
	// Seen marks a finished run as opened, for the panel's inbox. On disk it
	// is a copy of the run's latest record with the flag set; Runs folds it
	// into the run rather than letting it stand in for the status.
	Seen bool `json:"seen,omitempty"`
	// Started is when the run's first record was written. Filled in by Runs,
	// never stored: the first record already says it.
	Started time.Time `json:"-"`
}

func path() string { return filepath.Join(config.StateDir(), "history.jsonl") }

// Append writes one record; failures are returned but callers generally just
// log them — history must never break a run.
func Append(r Record) error {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, string(line))
	return err
}

// Runs returns the latest record per run, newest first, optionally filtered
// by automation name, capped at limit (0 = no cap).
func Runs(automation string, limit int) ([]Record, error) {
	f, err := os.Open(path())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	latest := map[string]Record{}
	order := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue // tolerate a torn write rather than losing the file
		}
		if automation != "" && r.Automation != automation {
			continue
		}
		prev, ok := latest[r.RunID]
		if r.Seen {
			if ok {
				prev.Seen = true
				latest[r.RunID] = prev
			}
			continue
		}
		if !ok {
			order = append(order, r.RunID)
			r.Started = r.At
		} else {
			r.Started, r.Seen = prev.Started, prev.Seen
		}
		latest[r.RunID] = r
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]Record, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		out = append(out, latest[order[i]])
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// LastRun returns the most recent record for an automation, or nil.
func LastRun(automation string) (*Record, error) {
	runs, err := Runs(automation, 1)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}

// MarkSeen records that a run was opened.
func MarkSeen(runID string) error {
	runs, err := Runs("", 0)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.RunID == runID {
			// A full copy, not just the flag: a reader that predates Seen
			// takes the last record per run as its state, and must still
			// find the run's status there.
			r.Seen = true
			return Append(r)
		}
	}
	return fmt.Errorf("no run %q", runID)
}

// MarkAllSeen marks every finished run as opened. A run still going is left
// alone: it lands in the inbox when it ends.
func MarkAllSeen() error {
	runs, err := Runs("", 0)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.Seen || r.Status == StatusScheduled || r.Status == StatusRunning {
			continue
		}
		r.Seen = true
		if err := Append(r); err != nil {
			return err
		}
	}
	return nil
}
