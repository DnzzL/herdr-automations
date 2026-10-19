package notify

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DnzzL/herdr-automations/internal/herdr"
	"github.com/DnzzL/herdr-automations/internal/history"
)

// fakeSink scripts Herdr's answers, in the shape of fakeOps (host/host_test.go).
type fakeSink struct {
	err    error
	toasts []toast
}

type toast struct {
	title, body string
	sound       herdr.Sound
}

func (f *fakeSink) NotificationShow(title, body string, sound herdr.Sound) error {
	f.toasts = append(f.toasts, toast{title, body, sound})
	return f.err
}

func TestOutcomeSaysNothingAboutARunThatWorked(t *testing.T) {
	sink := &fakeSink{}
	n := With(sink)
	for _, st := range []history.Status{history.StatusScheduled, history.StatusRunning, history.StatusDone} {
		n.Outcome("triage", st, "")
	}
	if len(sink.toasts) != 0 {
		t.Fatalf("toasts = %+v, want none for a run that worked", sink.toasts)
	}
}

func TestOutcomeSaysNothingAboutASkippedOccurrence(t *testing.T) {
	sink := &fakeSink{}
	With(sink).Outcome("triage", history.StatusSkipped, "previous run still in flight")
	if len(sink.toasts) != 0 {
		t.Fatalf("toasts = %+v, want none: ADR 0006 abandons a skipped occurrence on purpose", sink.toasts)
	}
}

func TestOutcomeSaysNothingAboutACancelledRun(t *testing.T) {
	sink := &fakeSink{}
	With(sink).Outcome("triage", history.StatusCancelled, "the run's workspace was closed")
	if len(sink.toasts) != 0 {
		t.Fatalf("toasts = %+v, want none: closing the workspace is its own gesture", sink.toasts)
	}
}

func TestOutcomeToastsAFailureWithTheReasonAndASound(t *testing.T) {
	sink := &fakeSink{}
	With(sink).Outcome("triage", history.StatusFailed, "start claude agent: exit status 1")
	if len(sink.toasts) != 1 {
		t.Fatalf("toasts = %+v, want exactly one", sink.toasts)
	}
	got := sink.toasts[0]
	if got.title != "triage failed" {
		t.Errorf("title = %q", got.title)
	}
	if got.body != "start claude agent: exit status 1" {
		t.Errorf("body = %q", got.body)
	}
	if got.sound != herdr.SoundRequest {
		t.Errorf("sound = %q, want request", got.sound)
	}
}

func TestTickToastsOneLineForManyMissedRuns(t *testing.T) {
	sink := &fakeSink{}
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("auto-%d", i)
	}
	With(sink).Tick(names, nil)
	if len(sink.toasts) != 1 {
		t.Fatalf("toasts = %+v, want exactly one for a whole tick", sink.toasts)
	}
	got := sink.toasts[0]
	if got.title != "20 automations did not run" {
		t.Errorf("title = %q", got.title)
	}
	want := "auto-0, auto-1, auto-2, and 17 more"
	if got.body != want {
		t.Errorf("body = %q, want %q: the first 3 names plus a count of the rest", got.body, want)
	}
}

func TestTickToastsTheAutomationByNameWhenThereIsOnlyOne(t *testing.T) {
	sink := &fakeSink{}
	With(sink).Tick([]string{"triage"}, nil)
	if len(sink.toasts) != 1 {
		t.Fatalf("toasts = %+v, want one", sink.toasts)
	}
	if got := sink.toasts[0].title; got != "triage missed a run" {
		t.Errorf("title = %q, want it to name the automation", got)
	}
}

func TestTickSaysNothingWhenTheTickFoundNothing(t *testing.T) {
	sink := &fakeSink{}
	With(sink).Tick(nil, nil)
	if len(sink.toasts) != 0 {
		t.Fatalf("toasts = %+v, want none", sink.toasts)
	}
}

func TestTickToastsInvalidEntriesSeparatelyFromMissedRuns(t *testing.T) {
	sink := &fakeSink{}
	With(sink).Tick([]string{"triage"}, []string{"broken"})
	if len(sink.toasts) != 2 {
		t.Fatalf("toasts = %+v, want one for missed and one for invalid", sink.toasts)
	}
}

func TestOutcomeKeepsTheBodyToOneShortLine(t *testing.T) {
	sink := &fakeSink{}
	dump := strings.Repeat("x", 4000) + "\nsecond line should never appear"
	With(sink).Outcome("triage", history.StatusFailed, dump)
	body := sink.toasts[0].body
	if strings.Contains(body, "\n") {
		t.Fatalf("body contains a newline: %q", body)
	}
	if r := []rune(body); len(r) > 200 {
		t.Fatalf("body is %d runes, want <= 200", len(r))
	}
}

func TestOutcomeTruncatesOnARuneBoundary(t *testing.T) {
	sink := &fakeSink{}
	dump := strings.Repeat("é", 300) // multi-byte rune, would mojibake on a byte cut
	With(sink).Outcome("triage", history.StatusFailed, dump)
	body := sink.toasts[0].body
	if !strings.HasSuffix(body, "…") {
		t.Fatalf("body = %q, want a truncation marker", body)
	}
	for _, r := range body {
		if r == '�' {
			t.Fatalf("body contains a replacement rune, cut mid multi-byte sequence: %q", body)
		}
	}
}

func TestOutcomeSurvivesAHerdrThatCannotToast(t *testing.T) {
	sink := &fakeSink{err: errors.New("herdr: connection refused")}
	n := With(sink)
	n.Outcome("triage", history.StatusFailed, "boom")
	n.Outcome("triage", history.StatusFailed, "boom again")
	if len(sink.toasts) != 2 {
		t.Fatalf("toasts = %+v, want the second event still tried", sink.toasts)
	}
}

func TestANilNotifierIsSilent(t *testing.T) {
	var n *Notifier
	n.Outcome("triage", history.StatusFailed, "boom")
	n.Tick([]string{"triage"}, []string{"broken"})
}

func TestAToastReachesEverySessionThatIsRunning(t *testing.T) {
	var shownIn []string
	e := everywhere{
		list: func() ([]string, error) { return []string{"default", "automations"}, nil },
		show: func(session, _, _ string, _ herdr.Sound) error {
			shownIn = append(shownIn, session)
			return nil
		},
	}
	With(e).Outcome("triage", history.StatusFailed, "boom")
	if fmt.Sprint(shownIn) != "[default automations]" {
		t.Fatalf("toasted in %q, want every running session: a dedicated one is where nobody looks", shownIn)
	}
}

func TestAToastStillGoesSomewhereWhenSessionsCannotBeListed(t *testing.T) {
	var shownIn []string
	e := everywhere{
		list: func() ([]string, error) { return nil, errors.New("herdr too old") },
		show: func(session, _, _ string, _ herdr.Sound) error {
			shownIn = append(shownIn, session)
			return nil
		},
	}
	With(e).Outcome("triage", history.StatusFailed, "boom")
	if len(shownIn) != 1 || shownIn[0] != "" {
		t.Fatalf("toasted in %q, want one toast wherever the environment points", shownIn)
	}
}
