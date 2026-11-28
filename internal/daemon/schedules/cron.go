package schedules

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a five-field cron expression: minute, hour, day of month, month
// and day of week (0 or 7 is Sunday). Each field is *, a number, a range a-b,
// a step */n or a-b/n, or a comma-separated list of those. When both day
// fields are restricted, a day matching either one matches, as in cron.
type Cron struct {
	minute, hour, dom, month, dow uint64 // bit i set: value i matches
	domAny, dowAny                bool
}

var cronFields = []struct {
	name     string
	min, max int
}{{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7}}

// ParseCron reads a five-field cron expression, or one of @hourly, @daily,
// @weekly and @monthly.
func ParseCron(expr string) (*Cron, error) {
	switch strings.TrimSpace(expr) {
	case "@hourly":
		expr = "0 * * * *"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@monthly":
		expr = "0 0 1 * *"
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields (minute hour day month weekday)", expr)
	}
	var sets [5]uint64
	for i, f := range fields {
		set, err := parseField(f, cronFields[i].min, cronFields[i].max)
		if err != nil {
			return nil, fmt.Errorf("cron %q: %s: %w", expr, cronFields[i].name, err)
		}
		sets[i] = set
	}
	c := &Cron{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domAny: fields[2] == "*", dowAny: fields[4] == "*"}
	if c.dow&(1<<7) != 0 {
		c.dow |= 1 // 7 is Sunday too
	}
	return c, nil
}

func parseField(f string, lo, hi int) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(f, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step %q", stepText)
			}
			step = n
		}
		from, to := lo, hi
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err1, err2 error
			from, err1 = strconv.Atoi(a)
			to, err2 = strconv.Atoi(b)
			if err1 != nil || err2 != nil || from > to {
				return 0, fmt.Errorf("bad range %q", rng)
			}
		default:
			n, err := strconv.Atoi(rng)
			if err != nil {
				return 0, fmt.Errorf("bad value %q", rng)
			}
			from, to = n, n
			if hasStep {
				to = hi
			}
		}
		if from < lo || to > hi {
			return 0, fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for v := from; v <= to; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

// Next is the first minute after t that the expression matches, in t's
// location; the zero time when none comes within five years.
func (c *Cron) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	end := t.AddDate(5, 0, 0)
	for t.Before(end) {
		if c.month&(1<<int(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if c.hour&(1<<t.Hour()) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if c.minute&(1<<t.Minute()) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (c *Cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<t.Day()) != 0
	dow := c.dow&(1<<int(t.Weekday())) != 0
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}
