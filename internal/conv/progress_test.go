package conv

import (
	"strings"
	"testing"
	"time"
)

// progressFixture builds a profile with a known history around a fixed now.
func progressFixture(now time.Time) Data {
	d := Data{Profile: Profile{Level: "B1"}}
	addTurn := func(daysAgo int) {
		d.Turns = append(d.Turns, Turn{Role: "user", Content: "x", At: now.AddDate(0, 0, -daysAgo)})
	}
	for _, daysAgo := range []int{0, 1, 2, 4, 10} {
		addTurn(daysAgo)
	}
	// An old turn without a date must stay out of every count.
	d.Turns = append(d.Turns, Turn{Role: "user", Content: "old"})

	event := func(daysAgo int, kind, key string) {
		d.Events = append(d.Events, Event{At: now.AddDate(0, 0, -daysAgo), Kind: kind, Key: key})
	}
	for i := 0; i < 3; i++ {
		event(1, "mistake", "past simple")
	}
	event(2, "mistake", "articles")
	event(20, "mistake", "word order")
	event(1, "fact", "city")
	event(1, "mnemonic", "mnemonic-past-simple")
	event(40, "fact", "name")
	return d
}

func TestComputeProgressWeek(t *testing.T) {
	now := time.Date(2026, 3, 15, 18, 0, 0, 0, time.UTC)
	d := progressFixture(now)

	p, err := ComputeProgress(d, "week", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Turns != 4 { // 0, 1, 2 and 4 days ago; 10 is outside the week
		t.Errorf("turns = %d, want 4", p.Turns)
	}
	if p.ActiveDays != 4 {
		t.Errorf("active days = %d, want 4", p.ActiveDays)
	}
	if p.Streak != 3 { // today, yesterday, the day before
		t.Errorf("streak = %d, want 3", p.Streak)
	}
	if len(p.TopErrors) != 2 || p.TopErrors[0].Key != "past simple" || p.TopErrors[0].Count != 3 {
		t.Errorf("top errors = %+v", p.TopErrors)
	}
	if p.Facts != 1 || p.Mnemonics != 1 {
		t.Errorf("facts=%d mnemonics=%d", p.Facts, p.Mnemonics)
	}
}

func TestComputeProgressMonthAndAll(t *testing.T) {
	now := time.Date(2026, 3, 15, 18, 0, 0, 0, time.UTC)
	d := progressFixture(now)

	month, err := ComputeProgress(d, "month", now)
	if err != nil {
		t.Fatal(err)
	}
	if month.Turns != 5 || len(month.TopErrors) != 3 {
		t.Errorf("month: turns=%d errors=%+v", month.Turns, month.TopErrors)
	}
	// The third error is the one from 20 days ago.
	if month.TopErrors[2].Key != "word order" {
		t.Errorf("month errors = %+v", month.TopErrors)
	}

	all, err := ComputeProgress(d, "all", now)
	if err != nil {
		t.Fatal(err)
	}
	if all.Turns != 5 || all.Facts != 2 {
		t.Errorf("all: turns=%d facts=%d", all.Turns, all.Facts)
	}
}

// TestStreakSurvivesATodayWithoutPractice: opening the app in the morning must
// not show a broken streak.
func TestStreakSurvivesATodayWithoutPractice(t *testing.T) {
	now := time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC)
	d := Data{Profile: Profile{Level: "B1"}}
	for _, daysAgo := range []int{1, 2, 3} {
		d.Turns = append(d.Turns, Turn{Role: "user", Content: "x", At: now.AddDate(0, 0, -daysAgo)})
	}
	if got := streak(d, now); got != 3 {
		t.Errorf("streak = %d, want 3", got)
	}
	// A gap breaks it.
	d.Turns = append(d.Turns, Turn{Role: "user", Content: "x", At: now.AddDate(0, 0, -6)})
	if got := streak(d, now); got != 3 {
		t.Errorf("streak with a gap = %d, want 3", got)
	}
	if got := streak(Data{}, now); got != 0 {
		t.Errorf("empty streak = %d", got)
	}
}

func TestComputeProgressRejectsAPeriod(t *testing.T) {
	if _, err := ComputeProgress(Data{}, "year", time.Now()); err == nil || !strings.Contains(err.Error(), "week, month o all") {
		t.Errorf("error = %v", err)
	}
}
