// Package notify turns run and tick outcomes into Herdr toasts. One job: decide
// what deserves to interrupt somebody, and never let a toast fail a run.
package notify

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
)

// bodyLimit bounds a toast body to one short line. hwf recovers a delegated
// command's exit status by scraping a pane, so a "detail" can be a whole
// screen of terminal output.
const bodyLimit = 200

// Sink is the one Herdr call this package makes, behind an interface so the
// tests need no running Herdr.
type Sink interface {
	NotificationShow(title, body string, sound herdr.Sound) error
}

// Notifier turns run outcomes into toasts. A nil *Notifier says nothing, which
// is what a Runner has until the daemon gives it one.
type Notifier struct {
	sink       Sink
	complained sync.Once
}

// New returns a Notifier that toasts through the real Herdr, in every running
// session.
func New() *Notifier {
	return &Notifier{sink: everywhere{
		list: herdr.Client{}.RunningSessions,
		show: func(session, title, body string, sound herdr.Sound) error {
			return herdr.Client{Session: session}.NotificationShow(title, body, sound)
		},
	}}
}

// everywhere toasts in every running Herdr session. A toast lands only in the
// session it is sent to, and the session a run happens in — a dedicated one,
// say — is often the one nobody is looking at (ADR 0010).
type everywhere struct {
	list func() ([]string, error)
	// show toasts in one session; "" means wherever the environment points.
	show func(session, title, body string, sound herdr.Sound) error
}

func (e everywhere) NotificationShow(title, body string, sound herdr.Sound) error {
	sessions, err := e.list()
	if err != nil || len(sessions) == 0 {
		// One toast somewhere beats none: a herdr that cannot list sessions
		// can still show one where it always did.
		return e.show("", title, body, sound)
	}
	var errs []error
	for _, s := range sessions {
		if err := e.show(s, title, body, sound); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// With returns a Notifier that toasts through sink — the tests' seam.
func With(s Sink) *Notifier { return &Notifier{sink: s} }

// Outcome toasts a single run that ended badly, and returns immediately for
// one that did not, so callers do not have to filter.
func (n *Notifier) Outcome(name string, st history.Status, detail string) {
	if n == nil {
		return
	}
	if st != history.StatusFailed {
		return
	}
	n.show(name+" failed", oneLine(detail), herdr.SoundRequest)
}

// Tick toasts one line for everything a scheduler tick found wrong, instead of
// one toast per item: a laptop waking from a weekend can find twenty
// automations at once, and twenty stacked panels is not a notification.
//
// Nothing here escalates a cron running every minute into a storm of toasts —
// missed and invalid are both already deduplicated by the caller before they
// reach here, so the real ceiling is one toast per broken thing per tick. A
// broken automation that fires every minute forever is a pause, not a feature.
func (n *Notifier) Tick(missed, invalid []string) {
	if n == nil {
		return
	}
	n.group(missed, "%s missed a run", "%d automations did not run")
	n.group(invalid, "%s is not scheduled", "%d automations are not scheduled")
}

func (n *Notifier) group(names []string, singular, plural string) {
	if len(names) == 0 {
		return
	}
	if len(names) == 1 {
		n.show(fmt.Sprintf(singular, names[0]), "", herdr.SoundNone)
		return
	}
	title := fmt.Sprintf(plural, len(names))
	shown := names
	suffix := ""
	if len(shown) > 3 {
		shown = shown[:3]
		suffix = fmt.Sprintf(", and %d more", len(names)-3)
	}
	n.show(title, strings.Join(shown, ", ")+suffix, herdr.SoundNone)
}

// show degrades silently: a toast is worth trying, never worth breaking a run
// or a tick over. The error is logged once per process, then dropped — there
// is no latch, so notifications resume as soon as Herdr does.
func (n *Notifier) show(title, body string, sound herdr.Sound) {
	if err := n.sink.NotificationShow(title, body, sound); err != nil {
		n.complained.Do(func() {
			log.Printf("notification failed (will keep trying silently): %v", err)
		})
	}
}

// oneLine reduces detail to its first line, truncated on a rune boundary so a
// multi-byte character split mid-sequence does not turn into mojibake.
func oneLine(detail string) string {
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		detail = detail[:i]
	}
	r := []rune(detail)
	if len(r) <= bodyLimit {
		return string(r)
	}
	return string(r[:bodyLimit-1]) + "…"
}
