// Package daemon is the long-running scheduler started by the plugin's startup
// hook. It re-reads automations.yaml as it changes, fires occurrences off the
// wall clock (so a sleeping laptop delays runs instead of losing them), and
// re-executes itself when the plugin binary is upgraded underneath it.
package daemon

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/history"
	"github.com/DnzzL/herdr-automations/internal/runner"
)

// tickInterval is how often the wall clock is consulted. Short enough that a
// run resumes within a minute of the machine waking up.
const tickInterval = 30 * time.Second

func Run() error {
	log.SetPrefix("[herdr-automations] ")

	release, err := acquireLock()
	if err != nil {
		return err
	}
	defer release()

	log.Printf("daemon starting, config=%s", config.Path())
	state := loadState()
	binary := binaryStamp()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()

	evaluate(state) // don't wait a full tick to notice what is already due

	for {
		select {
		case <-tick.C:
			if stamp := binaryStamp(); stamp != binary && stamp != "" {
				restart(release)
			}
			evaluate(state)
		case s := <-sigs:
			log.Printf("received %v, shutting down", s)
			return nil
		}
	}
}

// evaluate fires every automation whose occurrence has come due, and records
// the ones that came due too long ago to still be worth running.
func evaluate(state *scheduleState) {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config error, leaving the schedule untouched: %v", err)
		return
	}

	now := time.Now()
	live := map[string]bool{}
	dirty := false

	for _, a := range cfg.Automations {
		live[a.Name] = true
		if a.Disabled {
			continue
		}
		sched, err := config.CronParser.Parse(a.Cron)
		if err != nil {
			log.Printf("%s: %v", a.Name, err) // Load validated it; be defensive
			continue
		}

		last, seen := state.LastOccurrence[a.Name]
		if !seen {
			// A new automation starts counting from now: adding one should
			// never retroactively fire this morning's occurrence.
			state.LastOccurrence[a.Name] = now
			dirty = true
			log.Printf("%s: scheduled, next run %s",
				a.Name, sched.Next(now).Format(time.RFC1123))
			continue
		}

		occ, skipped, ok := due(sched, last, now)
		if !ok {
			continue
		}
		state.LastOccurrence[a.Name] = occ
		dirty = true

		if skipped > 0 {
			recordMissed(a.Name, skipped, "machine unavailable")
			log.Printf("%s: %d earlier occurrence(s) missed", a.Name, skipped)
		}

		lateness := now.Sub(occ)
		if lateness > a.CatchUp() {
			window := fmt.Sprintf("past the %s catch-up window", a.CatchUp())
			if a.CatchUp() == 0 {
				window = "catch-up disabled"
			}
			recordMissed(a.Name, 1, fmt.Sprintf("due %s ago, %s",
				lateness.Round(time.Minute), window))
			log.Printf("%s: missed (%s late)", a.Name, lateness.Round(time.Minute))
			continue
		}

		trigger := "cron"
		if lateness > time.Minute {
			trigger = "catchup"
			log.Printf("%s: running %s late", a.Name, lateness.Round(time.Minute))
		}
		go func(a config.Automation, trigger string) {
			if err := runner.Run(a, trigger); err != nil {
				log.Printf("run %s: %v", a.Name, err)
			}
		}(a, trigger)
	}

	// Forget automations that are gone, so re-adding one later starts clean.
	for name := range state.LastOccurrence {
		if !live[name] {
			delete(state.LastOccurrence, name)
			dirty = true
		}
	}
	if dirty {
		if err := state.save(); err != nil {
			log.Printf("saving schedule state: %v", err)
		}
	}
}

func recordMissed(name string, count int, why string) {
	detail := why
	if count > 1 {
		detail = fmt.Sprintf("%d occurrences: %s", count, why)
	}
	err := history.Append(history.Record{
		RunID:      fmt.Sprintf("%s-missed-%d", name, time.Now().UnixNano()),
		Automation: name, Trigger: "cron", Status: history.StatusMissed,
		At: time.Now(), Error: detail,
	})
	if err != nil {
		log.Printf("history append failed: %v", err)
	}
}

// restart re-executes the daemon so a plugin upgrade takes effect without
// waiting for the Herdr server to be restarted.
func restart(release func()) {
	if runner.Busy() {
		return // let the in-flight run finish; we'll notice again next tick
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("cannot locate the new binary: %v", err)
		return
	}
	log.Printf("binary changed, re-executing %s", exe)
	release() // the new process takes the lock
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		log.Printf("re-exec failed, continuing with the old build: %v", err)
	}
}

func binaryStamp() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	st, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
}
