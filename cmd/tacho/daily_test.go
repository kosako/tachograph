package main

import (
	"testing"
	"time"
)

// tacho daily's window counts back from today, so today is in it even when a
// DST switch skips the next date (Pacific/Apia, 2011-12-30), and -days counts
// the days that exist (#346).
func TestDailyRangeEndsToday(t *testing.T) {
	apia, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Skipf("no tz database entry for Pacific/Apia here: %v", err)
	}
	prev := time.Local
	time.Local = apia
	t.Cleanup(func() { time.Local = prev })

	for _, c := range []struct {
		now      time.Time
		days     int
		from, to string
	}{
		{time.Date(2011, 12, 29, 15, 0, 0, 0, apia), 1, "2011-12-29T00:00:00-10:00", "2011-12-31T00:00:00+14:00"},
		{time.Date(2011, 12, 29, 15, 0, 0, 0, apia), 3, "2011-12-27T00:00:00-10:00", "2011-12-31T00:00:00+14:00"},
		{time.Date(2011, 12, 31, 15, 0, 0, 0, apia), 2, "2011-12-29T00:00:00-10:00", "2012-01-01T00:00:00+14:00"},
	} {
		from, to := dailyRange(c.now, c.days)
		if got, want := from.Format(time.RFC3339)+" .. "+to.Format(time.RFC3339), c.from+" .. "+c.to; got != want {
			t.Errorf("dailyRange(%s, %d) = %s, want %s", c.now.Format(time.RFC3339), c.days, got, want)
		}
	}
}
