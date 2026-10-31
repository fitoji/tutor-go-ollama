package conv

import (
	"fmt"
	"sort"
	"time"
)

// MistakeCount is one trouble spot and how often it showed up in a period.
type MistakeCount struct {
	Key   string
	Count int
}

// Progress is what the learner did in a period, not what they know.
type Progress struct {
	Period     string // week, month, all
	Since      time.Time
	ActiveDays int
	Turns      int
	Streak     int // consecutive days with a turn, ending today or yesterday
	TopErrors  []MistakeCount
	Facts      int
	Mnemonics  int
}

// Periods lists the accepted values for ComputeProgress.
func Periods() []string { return []string{"week", "month", "all"} }

// ComputeProgress counts activity in a period. now is passed in so the tests
// do not depend on the wall clock, and its location decides what "a day" means.
func ComputeProgress(d Data, period string, now time.Time) (Progress, error) {
	var since time.Time
	switch period {
	case "week":
		since = now.AddDate(0, 0, -7)
	case "month":
		since = now.AddDate(0, 0, -30)
	case "all":
		since = time.Time{}
	default:
		return Progress{}, fmt.Errorf("period inválido %q (usá week, month o all)", period)
	}
	p := Progress{Period: period, Since: since, Streak: streak(d, now)}
	loc := now.Location()
	days := map[string]bool{}
	inPeriod := func(t time.Time) bool {
		return !t.IsZero() && (since.IsZero() || !t.Before(since))
	}

	for _, t := range d.Turns {
		if !inPeriod(t.At) {
			continue
		}
		p.Turns++
		days[t.At.In(loc).Format("2006-01-02")] = true
	}
	p.ActiveDays = len(days)

	counts := map[string]int{}
	for _, e := range d.Events {
		if !inPeriod(e.At) {
			continue
		}
		switch e.Kind {
		case "mistake":
			counts[e.Key]++
		case "fact":
			p.Facts++
		case "mnemonic":
			p.Mnemonics++
		}
	}
	for k, n := range counts {
		p.TopErrors = append(p.TopErrors, MistakeCount{Key: k, Count: n})
	}
	sort.Slice(p.TopErrors, func(i, j int) bool {
		if p.TopErrors[i].Count != p.TopErrors[j].Count {
			return p.TopErrors[i].Count > p.TopErrors[j].Count
		}
		return p.TopErrors[i].Key < p.TopErrors[j].Key
	})
	if len(p.TopErrors) > 3 {
		p.TopErrors = p.TopErrors[:3]
	}
	return p, nil
}

// streak counts consecutive days with at least one turn. A day with no turn yet
// does not break it: if the last practice was yesterday, the streak is alive.
func streak(d Data, now time.Time) int {
	loc := now.Location()
	days := map[string]bool{}
	for _, t := range d.Turns {
		if !t.At.IsZero() {
			days[t.At.In(loc).Format("2006-01-02")] = true
		}
	}
	if len(days) == 0 {
		return 0
	}
	day := now.In(loc)
	if !days[day.Format("2006-01-02")] {
		day = day.AddDate(0, 0, -1)
	}
	n := 0
	for days[day.Format("2006-01-02")] {
		n++
		day = day.AddDate(0, 0, -1)
	}
	return n
}
