package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/robfig/cron/v3"
)

// due reports the most recent occurrence at or before now, how many earlier
// occurrences were skipped along the way, and whether anything is due at all.
//
// This is deliberately wall-clock arithmetic rather than a timer: on a laptop,
// macOS suspends the monotonic clock during sleep, so a timer armed for "in 15
// hours" fires 15 hours of *awake* time later — which is how a 9am Monday run
// silently never happened.
func due(sched cron.Schedule, last, now time.Time) (occ time.Time, skipped int, ok bool) {
	next := sched.Next(last)
	if next.After(now) {
		return time.Time{}, 0, false
	}
	occ = next
	for {
		n := sched.Next(occ)
		if n.After(now) {
			break
		}
		skipped++
		occ = n
	}
	return occ, skipped, true
}

// scheduleState remembers the last occurrence each automation was evaluated
// for, so restarts and sleeps don't replay or lose runs.
type scheduleState struct {
	LastOccurrence map[string]time.Time `json:"last_occurrence"`
}

func statePath() string {
	return filepath.Join(config.StateDir(), "schedule.json")
}

func loadState() *scheduleState {
	s := &scheduleState{LastOccurrence: map[string]time.Time{}}
	raw, err := os.ReadFile(statePath())
	if err != nil {
		return s
	}
	if json.Unmarshal(raw, s) != nil || s.LastOccurrence == nil {
		s.LastOccurrence = map[string]time.Time{}
	}
	return s
}

func (s *scheduleState) save() error {
	if err := os.MkdirAll(config.StateDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), raw, 0o644)
}
