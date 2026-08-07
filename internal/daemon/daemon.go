// Package daemon is the long-running scheduler started by the plugin's
// startup hook. It reloads automations.yaml whenever its mtime changes, so
// edits (by hand, wizard or agent skill) take effect without a restart.
package daemon

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/DnzzL/herdr-automations/internal/config"
	"github.com/DnzzL/herdr-automations/internal/runner"
)

const reloadInterval = 30 * time.Second

func Run() error {
	log.SetPrefix("[herdr-automations] ")
	log.Printf("daemon starting, config=%s", config.Path())

	var (
		sched    *cron.Cron
		lastMod  time.Time
		lastSize int64
	)
	reload := func() {
		st, err := os.Stat(config.Path())
		switch {
		case os.IsNotExist(err):
			st = nil
		case err != nil:
			log.Printf("stat config: %v", err)
			return
		}
		if st == nil {
			if sched != nil && lastMod.IsZero() {
				return // still no config file, nothing to rebuild
			}
		} else if st.ModTime().Equal(lastMod) && st.Size() == lastSize {
			return
		}
		cfg, err := config.Load()
		if err != nil {
			log.Printf("config error, keeping previous schedule: %v", err)
			return
		}
		if sched != nil {
			sched.Stop()
		}
		sched = cron.New(cron.WithParser(config.CronParser))
		active := 0
		for _, a := range cfg.Automations {
			if a.Disabled {
				continue
			}
			a := a
			if _, err := sched.AddFunc(a.Cron, func() {
				log.Printf("cron fired: %s", a.Name)
				if err := runner.Run(a, "cron"); err != nil {
					log.Printf("run %s: %v", a.Name, err)
				}
			}); err != nil {
				log.Printf("schedule %s: %v", a.Name, err)
				continue
			}
			active++
		}
		sched.Start()
		if st != nil {
			lastMod, lastSize = st.ModTime(), st.Size()
		} else {
			lastMod, lastSize = time.Time{}, 0
		}
		log.Printf("schedule loaded: %d active automation(s)", active)
	}

	reload()
	tick := time.NewTicker(reloadInterval)
	defer tick.Stop()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-tick.C:
			reload()
		case s := <-sigs:
			log.Printf("received %v, shutting down", s)
			if sched != nil {
				sched.Stop()
			}
			return nil
		}
	}
}
