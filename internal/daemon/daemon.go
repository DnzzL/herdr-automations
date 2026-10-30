// Package daemon is the long-running scheduler started by the plugin's startup
// hook. Every tick it re-reads automations.yaml, asks the schedule module what
// should happen now, and does it. It re-executes itself when the plugin binary
// is upgraded underneath it.
//
// Deciding is schedule's job; this package is the part with the side effects.
package daemon

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/host"
	"github.com/DnzzL/herdr-automations/internal/notify"
	"github.com/DnzzL/herdr-automations/internal/runner"
	"github.com/DnzzL/herdr-automations/internal/schedule"
)

// tickInterval is how often the wall clock is consulted. Short enough that a
// run resumes within a minute of the machine waking up.
const tickInterval = 30 * time.Second

// Run is the scheduler. With standby, a lock already held is waited out
// rather than reported: the service and the TUI's startup hook can both start
// one, and whichever comes second takes over when the first one stops.
func Run(standby bool) error {
	log.SetPrefix("[herdr-automations] ")

	release, err := acquireLock()
	for err != nil && standby && errors.Is(err, ErrAlreadyRunning) {
		log.Printf("%v; standing by", err)
		time.Sleep(tickInterval)
		release, err = acquireLock()
	}
	if err != nil {
		return err
	}
	defer release()

	log.Printf("daemon starting, config=%s", config.Path())
	state := loadState()
	// Resolve the path once: a reinstall moves the running image away, but the
	// new build lands here.
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	binary := binaryStamp(exe)
	// The backend is settled once: a run must be awaited where it was
	// started. Editing it in the file restarts the daemon (see the loop).
	backend := config.BackendHerdr
	if cfg, err := config.Load(); err == nil {
		backend = cfg.Backend
	}
	resolved := host.Resolve(backend)
	log.Printf("backend %s (%s)", backend, resolved)
	// Under hc no outside process can raise a toast in the app; the panel
	// announces failures from history instead (ADR 0011).
	var n *notify.Notifier
	if resolved == config.BackendHerdr {
		n = notify.New()
	}
	runs := runner.NewWith(host.For(resolved), n)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()

	evaluate(state, runs, n) // don't wait a full tick to notice what is already due

	for {
		select {
		case <-tick.C:
			// A run in flight records how it ended first, as in restart. Only a
			// daemon a TUI session started, running TUI automations, ends with
			// the last session (ADR 0010): hc needs no session, and the login
			// service owns its own lifetime (launchd would only relaunch it).
			if !standby && resolved == config.BackendHerdr && !runs.Busy() && noSessionLeft(herdr.Client{}.RunningSessions) {
				log.Printf("no Herdr session is running, shutting down")
				return nil
			}
			if stamp := binaryStamp(exe); stamp != binary && stamp != "" {
				restart(runs, exe, "binary changed")
			}
			if cfg, err := config.Load(); err == nil && cfg.Backend != backend {
				restart(runs, exe, "backend changed")
			}
			evaluate(state, runs, n)
		case s := <-sigs:
			log.Printf("received %v, shutting down", s)
			return nil
		}
	}
}

// noSessionLeft decides when the daemon is done. It outlives the session that
// started it — every run names its own, and a session already up never re-runs
// its startup hook, so dying with the first would leave the others without a
// scheduler. With no session at all there is nothing left to run in, and the
// next one to start brings a fresh daemon. A Herdr that cannot answer is not
// one that is gone.
func noSessionLeft(list func() ([]string, error)) bool {
	sessions, err := list()
	return err == nil && len(sessions) == 0
}

// evaluate asks the schedule what should happen now, and does it.
func evaluate(state *scheduleState, runs *runner.Runner, n *notify.Notifier) {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config error, leaving the schedule untouched: %v", err)
		return
	}
	dirty, invalid := reportInvalid(cfg, state)

	res := schedule.Plan(cfg, schedule.State{LastOccurrence: state.LastOccurrence}, time.Now())
	state.LastOccurrence = res.State.LastOccurrence
	dirty = dirty || res.Changed

	var missed []string
	for _, d := range res.Decisions {
		switch d.Kind {
		case schedule.Register:
			log.Printf("%s: scheduled, next run %s", d.Automation.Name, d.At.Format(time.RFC1123))
		case schedule.Missed:
			recordMissed(d.Automation.Name, d.Count, d.Reason)
			missed = append(missed, d.Automation.Name)
			log.Printf("%s: missed (%s)", d.Automation.Name, d.Reason)
		case schedule.Paused:
			recordPassedOver(d.Automation.Name, d.Count)
			log.Printf("%s: passed over, automations are paused", d.Automation.Name)
		case schedule.Fire:
			if d.Trigger == history.TriggerCatchup {
				log.Printf("%s: running %s late", d.Automation.Name,
					time.Since(d.At).Round(time.Minute))
			}
			go func(a config.Automation, trigger history.Trigger) {
				if err := runs.Run(a, trigger); err != nil {
					log.Printf("run %s: %v", a.Name, err)
				}
			}(d.Automation, d.Trigger)
		}
	}
	n.Tick(missed, invalid)

	if dirty {
		if err := state.save(); err != nil {
			log.Printf("saving schedule state: %v", err)
		}
	}
}

// reportInvalid records each broken entry once, and returns the automations
// newly reported this tick so evaluate can fold them into one Tick toast.
// Re-recording an unfixed typo every 30s would bury the log and the board
// under the same line all day; going quiet about it entirely is how a
// "missing" automation stays a mystery.
func reportInvalid(cfg *config.Config, state *scheduleState) (changed bool, newlyInvalid []string) {
	if state.Invalid == nil {
		state.Invalid = map[string]bool{}
	}
	seen := map[string]bool{}
	for _, d := range cfg.Invalid {
		line := d.String()
		seen[line] = true
		if state.Invalid[line] {
			continue
		}
		state.Invalid[line] = true
		changed = true
		log.Printf("not scheduled — %s", line)
		name := d.Name
		if name == "" {
			name = "(unnamed)"
		}
		newlyInvalid = append(newlyInvalid, name)
		err := history.Append(history.Record{
			RunID:      history.NewID(name, "invalid"),
			Automation: name, Status: history.StatusInvalid,
			At: time.Now(), Error: line,
		})
		if err != nil {
			log.Printf("history append failed: %v", err)
		}
	}
	// Forget the ones that were fixed, so breaking them again is reported again.
	for line := range state.Invalid {
		if !seen[line] {
			delete(state.Invalid, line)
			changed = true
		}
	}
	return changed, newlyInvalid
}

// recordPassedOver notes an occurrence that came due while every automation
// was paused. Skipped, not missed: nothing went wrong, so nothing is toasted.
func recordPassedOver(name string, count int) {
	detail := "paused"
	if count > 1 {
		detail = fmt.Sprintf("%d occurrences: paused", count)
	}
	err := history.Append(history.Record{
		RunID:      history.NewID(name, "paused"),
		Automation: name, Trigger: history.TriggerCron, Status: history.StatusSkipped,
		At: time.Now(), Error: detail,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

func recordMissed(name string, count int, why string) {
	detail := why
	if count > 1 {
		detail = fmt.Sprintf("%d occurrences: %s", count, why)
	}
	err := history.Append(history.Record{
		RunID:      history.NewID(name, "missed"),
		Automation: name, Trigger: history.TriggerCron, Status: history.StatusMissed,
		At: time.Now(), Error: detail,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

// restart re-executes the daemon so a plugin upgrade takes effect without
// waiting for the Herdr server to be restarted.
// execve is syscall.Exec, swappable so a test can make the re-exec fail.
var execve = syscall.Exec

func restart(runs *runner.Runner, exe, why string) {
	if runs.Busy() {
		return // let the in-flight run finish; we'll notice again next tick
	}
	log.Printf("%s, re-executing %s", why, exe)
	// No release here: the lock's descriptor is close-on-exec, so a successful
	// exec drops it for the new image, and a failed one leaves it held.
	if err := execve(exe, os.Args, os.Environ()); err != nil {
		log.Printf("re-exec failed, continuing with the old build: %v", err)
	}
}

func binaryStamp(exe string) string {
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
}
