package schedule

import (
	"testing"
	"time"

	"github.com/DnzzL/herdr-automations/internal/config"
)

func at(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// weekday is an automation due at 09:00 every weekday, with the default
// two-hour catch-up window.
func weekday(name string) config.Automation {
	return config.Automation{Name: name, Cron: "0 9 * * 1-5", CatchUpMinutes: 120}
}

func cfgOf(as ...config.Automation) *config.Config {
	return &config.Config{Automations: as}
}

func state(pairs map[string]time.Time) State {
	s := State{LastOccurrence: map[string]time.Time{}}
	for k, v := range pairs {
		s.LastOccurrence[k] = v
	}
	return s
}

// only asserts the plan contains exactly one decision, and returns it.
func only(t *testing.T, res Result) Decision {
	t.Helper()
	if len(res.Decisions) != 1 {
		t.Fatalf("decisions = %+v, want exactly one", res.Decisions)
	}
	return res.Decisions[0]
}

// ── due ─────────────────────────────────────────────────────────────

func TestDueNotYet(t *testing.T) {
	sched, _ := config.CronParser.Parse("0 9 * * 1") // Mondays at 09:00
	_, _, ok := due(sched, at(t, "2026-08-10 09:00"), at(t, "2026-08-10 15:00"))
	if ok {
		t.Fatal("nothing should be due before the next Monday")
	}
}

func TestDueAfterSleepReturnsTheOccurrence(t *testing.T) {
	sched, _ := config.CronParser.Parse("0 9 * * 1")
	// Laptop asleep from Sunday evening; wakes Monday afternoon.
	occ, skipped, ok := due(sched, at(t, "2026-08-09 20:00"), at(t, "2026-08-10 17:13"))
	if !ok {
		t.Fatal("Monday 09:00 should be due")
	}
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0", skipped)
	}
	if want := at(t, "2026-08-10 09:00"); !occ.Equal(want) {
		t.Fatalf("occurrence = %s, want %s", occ, want)
	}
}

func TestDueCountsEveryMissedOccurrence(t *testing.T) {
	sched, _ := config.CronParser.Parse("0 9 * * *") // daily at 09:00
	// Away for three days: three occurrences passed, the latest is today's.
	occ, skipped, ok := due(sched, at(t, "2026-08-07 12:00"), at(t, "2026-08-10 10:00"))
	if !ok {
		t.Fatal("expected due")
	}
	if skipped != 2 {
		t.Fatalf("skipped = %d, want 2 (the 8th and the 9th)", skipped)
	}
	if want := at(t, "2026-08-10 09:00"); !occ.Equal(want) {
		t.Fatalf("occurrence = %s, want %s", occ, want)
	}
}

// ── the catch-up policy, which used to have no seam at all ──────────

func TestPlanFiresAnOccurrenceThatHasJustComeDue(t *testing.T) {
	res := Plan(cfgOf(weekday("triage")),
		state(map[string]time.Time{"triage": at(t, "2026-08-10 08:30")}),
		at(t, "2026-08-10 09:00"))

	d := only(t, res)
	if d.Kind != Fire || d.Trigger != "cron" {
		t.Fatalf("decision = %+v, want a cron fire", d)
	}
	if !res.Changed {
		t.Error("the occurrence was consumed; the state has to be written")
	}
	if got := res.State.LastOccurrence["triage"]; !got.Equal(at(t, "2026-08-10 09:00")) {
		t.Errorf("state advanced to %s", got)
	}
}

func TestPlanCatchesUpAWokenLaptopWithinTheWindow(t *testing.T) {
	// The whole point of the wall-clock scheduler: 09:00 passed while the lid
	// was shut, and 10:30 is still inside the two-hour window.
	res := Plan(cfgOf(weekday("triage")),
		state(map[string]time.Time{"triage": at(t, "2026-08-10 07:00")}),
		at(t, "2026-08-10 10:30"))

	d := only(t, res)
	if d.Kind != Fire {
		t.Fatalf("decision = %+v, want it to run", d)
	}
	if d.Trigger != "catchup" {
		t.Errorf("trigger = %q, want catchup: it is running late and should say so", d.Trigger)
	}
}

func TestPlanWritesOffAnOccurrencePastTheCatchUpWindow(t *testing.T) {
	// Lid shut all day. 17:00 is well past 09:00 + 2h, so running now would
	// start an unattended agent nobody is expecting.
	res := Plan(cfgOf(weekday("triage")),
		state(map[string]time.Time{"triage": at(t, "2026-08-10 07:00")}),
		at(t, "2026-08-10 17:00"))

	d := only(t, res)
	if d.Kind != Missed {
		t.Fatalf("decision = %+v, want it written off", d)
	}
	if d.Reason == "" {
		t.Error("a missed occurrence has to say why")
	}
	// The occurrence is still consumed: it must not fire the next tick either.
	if got := res.State.LastOccurrence["triage"]; !got.Equal(at(t, "2026-08-10 09:00")) {
		t.Errorf("state = %s, want the occurrence consumed anyway", got)
	}
}

func TestPlanNeverCatchesUpWhenTheWindowIsDisabled(t *testing.T) {
	a := weekday("triage")
	a.CatchUpMinutes = -1 // never
	res := Plan(cfgOf(a),
		state(map[string]time.Time{"triage": at(t, "2026-08-10 08:59")}),
		at(t, "2026-08-10 09:02"))

	d := only(t, res)
	if d.Kind != Missed {
		t.Fatalf("decision = %+v, want it missed: catch-up is off", d)
	}
}

func TestPlanFiresOnTheDotEvenWithCatchUpDisabled(t *testing.T) {
	// catch_up_minutes: -1 means "only on time", not "never at all".
	a := weekday("triage")
	a.CatchUpMinutes = -1
	res := Plan(cfgOf(a),
		state(map[string]time.Time{"triage": at(t, "2026-08-10 08:30")}),
		at(t, "2026-08-10 09:00"))

	if d := only(t, res); d.Kind != Fire {
		t.Fatalf("decision = %+v, want a fire", d)
	}
}

func TestPlanReportsTheOccurrencesItSkippedPastAndStillFires(t *testing.T) {
	// Away Monday to Wednesday: Monday and Tuesday are gone, Wednesday's is
	// the one in the window. Both facts get recorded.
	res := Plan(cfgOf(weekday("triage")),
		state(map[string]time.Time{"triage": at(t, "2026-08-10 08:00")}),
		at(t, "2026-08-12 09:30"))

	if len(res.Decisions) != 2 {
		t.Fatalf("decisions = %+v, want a missed and a fire", res.Decisions)
	}
	missed, fired := res.Decisions[0], res.Decisions[1]
	if missed.Kind != Missed || missed.Count != 2 {
		t.Errorf("missed = %+v, want two occurrences written off", missed)
	}
	if fired.Kind != Fire {
		t.Errorf("second decision = %+v, want a fire", fired)
	}
}

// ── bookkeeping ─────────────────────────────────────────────────────

func TestPlanRegistersANewAutomationWithoutFiringIt(t *testing.T) {
	// Adding an automation at noon must not retroactively run this morning's
	// occurrence.
	res := Plan(cfgOf(weekday("triage")), state(nil), at(t, "2026-08-10 12:00"))

	d := only(t, res)
	if d.Kind != Register {
		t.Fatalf("decision = %+v, want it registered, not fired", d)
	}
	if got := res.State.LastOccurrence["triage"]; !got.Equal(at(t, "2026-08-10 12:00")) {
		t.Errorf("state = %s, want it counting from now", got)
	}
}

func TestPlanIgnoresADisabledAutomation(t *testing.T) {
	a := weekday("triage")
	a.Disabled = true
	res := Plan(cfgOf(a), state(map[string]time.Time{"triage": at(t, "2026-08-10 07:00")}),
		at(t, "2026-08-10 09:30"))

	if len(res.Decisions) != 0 {
		t.Fatalf("decisions = %+v, want none", res.Decisions)
	}
}

func TestPlanForgetsAnAutomationThatIsGone(t *testing.T) {
	// Otherwise re-adding one later fires for every occurrence since it was
	// deleted.
	res := Plan(cfgOf(), state(map[string]time.Time{"deleted": at(t, "2026-01-01 09:00")}),
		at(t, "2026-08-10 09:00"))

	if _, still := res.State.LastOccurrence["deleted"]; still {
		t.Error("a deleted automation is still remembered")
	}
	if !res.Changed {
		t.Error("dropping it is a change worth persisting")
	}
}

func TestPlanIsQuietWhenNothingIsDue(t *testing.T) {
	res := Plan(cfgOf(weekday("triage")),
		state(map[string]time.Time{"triage": at(t, "2026-08-10 09:00")}),
		at(t, "2026-08-10 15:00"))

	if len(res.Decisions) != 0 {
		t.Fatalf("decisions = %+v, want none", res.Decisions)
	}
	if res.Changed {
		t.Error("nothing happened; nothing should be written")
	}
}

func TestPlanSkipsAnEntryWhoseCronCannotBeParsed(t *testing.T) {
	// Load diagnoses these, so reaching Plan means something bypassed it.
	// Guessing at a schedule is worse than doing nothing.
	a := weekday("triage")
	a.Cron = "0 99 * * *"
	res := Plan(cfgOf(a), state(nil), at(t, "2026-08-10 09:00"))

	if len(res.Decisions) != 0 {
		t.Fatalf("decisions = %+v, want none", res.Decisions)
	}
}

// ── the reports ─────────────────────────────────────────────────────

func TestCollisionsReportsSharedOccurrencesOnce(t *testing.T) {
	cfg := cfgOf(
		config.Automation{Name: "early", Cron: "0 6 * * *"},
		config.Automation{Name: "also-early", Cron: "0 6 * * *"},
		config.Automation{Name: "alone", Cron: "0 14 * * *"},
		config.Automation{Name: "off", Cron: "0 6 * * *", Disabled: true},
	)
	// Both are daily, so they clash seven times over the horizon — but that is
	// one fact about the schedule, not seven.
	clashes := Collisions(cfg, time.Now())
	if len(clashes) != 1 {
		t.Fatalf("expected a single deduped collision, got %d: %+v", len(clashes), clashes)
	}
	if len(clashes[0].Names) != 2 {
		t.Fatalf("disabled entries must not collide: %+v", clashes[0].Names)
	}
}

func TestCollidesWithNamesTheClashingAutomations(t *testing.T) {
	cfg := cfgOf(
		config.Automation{Name: "sprint", Cron: "0 9 * * 1"},
		config.Automation{Name: "nightly", Cron: "0 3 * * *"},
	)
	now := time.Now()
	if got := CollidesWith(cfg, "0 9 * * 1", now); len(got) != 1 || got[0] != "sprint" {
		t.Fatalf("expected sprint, got %v", got)
	}
	if got := CollidesWith(cfg, "30 9 * * 1", now); len(got) != 0 {
		t.Fatalf("expected no clash, got %v", got)
	}
}

func TestNextRunSaysSoWhenTheCronIsUnparseable(t *testing.T) {
	if _, ok := NextRun(config.Automation{Cron: "nope"}, time.Now()); ok {
		t.Fatal("want ok=false for a cron that does not parse")
	}
	if _, ok := NextRun(config.Automation{Cron: "@daily"}, time.Now()); !ok {
		t.Fatal("want ok=true for @daily")
	}
}

// A global pause holds every automation without forgetting where each one is:
// what came due while paused is recorded and passed over, so resuming does not
// replay the holiday.
func TestPlanPassesOverWhatIsDueWhileGloballyPaused(t *testing.T) {
	cfg := cfgOf(weekday("triage"))
	cfg.Paused = true
	res := Plan(cfg, state(map[string]time.Time{"triage": at(t, "2026-08-10 07:00")}),
		at(t, "2026-08-10 09:00"))

	if len(res.Decisions) != 1 || res.Decisions[0].Kind != Paused {
		t.Fatalf("decisions = %+v, want one Paused", res.Decisions)
	}
	if got := res.State.LastOccurrence["triage"]; !got.Equal(at(t, "2026-08-10 09:00")) {
		t.Fatalf("state = %s, want it moved past the paused occurrence", got)
	}

	cfg.Paused = false
	res = Plan(cfg, res.State, at(t, "2026-08-10 09:05"))
	if len(res.Decisions) != 0 {
		t.Fatalf("after resume: decisions = %+v, want none", res.Decisions)
	}
}
