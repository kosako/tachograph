package daily

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kosako/tachograph/internal/pricing"
)

var noPrices = pricing.Table{}

func writeFile(t *testing.T, path, content string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func claudeMsg(ts time.Time, in, cc, cr, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		ts.Format(time.RFC3339), in, cc, cr, out)
}

func claudeMsgID(ts time.Time, id, req string, in, cc, cr, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"requestId":%q,"message":{"id":%q,"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`,
		ts.Format(time.RFC3339), req, id, in, cc, cr, out)
}

func claudeMsgCacheCreation(ts time.Time, in, cc5m, cc1h, cr, out int64) string {
	return claudeMsgCacheCreationTotal(ts, in, cc5m+cc1h, cc5m, cc1h, cr, out)
}

func claudeMsgCacheCreationTotal(ts time.Time, in, totalCC, cc5m, cc1h, cr, out int64) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"model":"claude-fable-5","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		ts.Format(time.RFC3339), in, totalCC, cr, out, cc5m, cc1h)
}

// TestDayInLocalBoundary pins the local-day boundary used to slice "today":
// the first and last instant of the calendar day count, one second either side
// does not. The window is built from DayStart / DayStartFrom (local calendar
// days, no 24h arithmetic), so this stays correct across DST transitions.
// Instants are built explicitly in time.Local so the RFC 3339 offset matches
// what dayIn re-localizes to.
func TestDayInLocalBoundary(t *testing.T) {
	from := DayStart(time.Date(2026, 6, 28, 12, 0, 0, 0, time.Local))
	to := DayStartFrom(from, 1)
	cases := []struct {
		name string
		ts   time.Time
		want bool
	}{
		{"start of day", time.Date(2026, 6, 28, 0, 0, 0, 0, time.Local), true},
		{"end of day", time.Date(2026, 6, 28, 23, 59, 59, 0, time.Local), true},
		{"one second before", time.Date(2026, 6, 27, 23, 59, 59, 0, time.Local), false},
		{"start of next day", time.Date(2026, 6, 29, 0, 0, 0, 0, time.Local), false},
	}
	for _, c := range cases {
		ts := c.ts.Format(time.RFC3339)
		day, got := dayIn(ts, from, to)
		if got != c.want {
			t.Errorf("%s: dayIn(%q) = %v, want %v", c.name, ts, got, c.want)
		}
		if got && day != "2026-06-28" {
			t.Errorf("%s: dayIn(%q) day = %q, want 2026-06-28", c.name, ts, day)
		}
	}
	if _, got := dayIn("not a time", from, to); got {
		t.Error("dayIn(unparsable) = true, want false")
	}
}

// setLocal switches the local timezone for the rest of the test.
func setLocal(t *testing.T, loc *time.Location) {
	t.Helper()
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
}

func loadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("no tz database entry for %s here: %v", name, err)
	}
	return loc
}

// A day begins at its first instant: midnight, or — where a DST switch skips
// midnight (America/Santiago, 2026-09-06) — the switch itself, never the
// previous evening time.Date would give; stepping day by day lists each date
// once (#346). A 2 a.m. switch, a fall-back day, a skipped date, and month
// ends keep plain midnights.
func TestDayStartFrom(t *testing.T) {
	santiago := loadLocation(t, "America/Santiago")
	setLocal(t, santiago)
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, santiago) }
	cases := []struct {
		name string
		got  time.Time
		want string
	}{
		{"the day before the skipped midnight", DayStart(at(2026, 9, 5, 15)), "2026-09-05T00:00:00-04:00"},
		{"the day whose midnight is skipped", DayStart(at(2026, 9, 6, 12)), "2026-09-06T01:00:00-03:00"},
		{"next day into it", DayStartFrom(at(2026, 9, 5, 0), 1), "2026-09-06T01:00:00-03:00"},
		{"next day out of it", DayStartFrom(at(2026, 9, 6, 12), 1), "2026-09-07T00:00:00-03:00"},
		{"one day back into it", DayStartFrom(at(2026, 9, 7, 0), -1), "2026-09-06T01:00:00-03:00"},
		{"two days back across it", DayStartFrom(at(2026, 9, 7, 0), -2), "2026-09-05T00:00:00-04:00"},
		{"the day after a fall-back (24:00 → 23:00 on the 4th)", DayStart(at(2026, 4, 5, 12)), "2026-04-05T00:00:00-04:00"},
	}
	for _, c := range cases {
		if got := c.got.Format(time.RFC3339); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}

	var keys []string
	for d := DayStart(at(2026, 9, 4, 12)); d.Before(DayStart(at(2026, 9, 8, 12))); d = DayStartFrom(d, 1) {
		keys = append(keys, DayKey(d))
	}
	if want := []string{"2026-09-04", "2026-09-05", "2026-09-06", "2026-09-07"}; fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Errorf("stepping across the skipped midnight lists %v, want %v", keys, want)
	}

	// Offsets shown to the second: Asia/Manila's was -15:56:08 in 1844.
	const layout = "2006-01-02T15:04:05-07:00:00"
	for _, c := range []struct {
		zone       string
		at         time.Time
		days       int
		want, name string
	}{
		{"America/New_York", time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), 0, "2026-03-08T00:00:00-05:00:00", "a 2 a.m. switch keeps midnight"},
		{"America/New_York", time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), 1, "2026-03-09T00:00:00-04:00:00", "the day after a 2 a.m. switch"},
		// St. John's fell back from 00:01 to 23:01 the day before: the 2nd
		// begins at its first midnight, the hour after it is the 1st again.
		{"America/St_Johns", time.Date(2008, 11, 2, 2, 30, 30, 0, time.UTC), 0, "2008-11-02T00:00:00-02:30:00", "the first minute of a day that falls back after midnight"},
		{"America/St_Johns", time.Date(2008, 11, 2, 3, 0, 0, 0, time.UTC), 0, "2008-11-01T00:00:00-02:30:00", "the hour that is the day before again"},
		{"America/St_Johns", time.Date(2008, 11, 2, 15, 0, 0, 0, time.UTC), 0, "2008-11-02T00:00:00-02:30:00", "after the second midnight"},
		{"America/St_Johns", time.Date(2008, 11, 1, 12, 0, 0, 0, time.UTC), 1, "2008-11-02T00:00:00-02:30:00", "the next day is the first midnight"},
		{"Asia/Manila", time.Date(1844, 1, 3, 4, 0, 0, 0, time.UTC), 0, "1844-01-02T00:00:00-15:56:08", "an offset beyond ±15h"},
		{"Asia/Manila", time.Date(1844, 1, 3, 4, 0, 0, 0, time.UTC), 1, "1844-01-03T00:00:00-15:56:08", "the next day beyond ±15h"},
		{"Asia/Manila", time.Date(1844, 12, 31, 4, 0, 0, 0, time.UTC), 1, "1845-01-01T00:00:00+08:03:52", "1844-12-31 was skipped"},
		{"Pacific/Apia", time.Date(2011, 12, 29, 12, 0, 0, 0, time.UTC), 1, "2011-12-31T00:00:00+14:00:00", "a skipped date steps to the next one"},
		{"Pacific/Apia", time.Date(2011, 12, 30, 22, 0, 0, 0, time.UTC), -1, "2011-12-29T00:00:00-10:00:00", "and back to the one before"},
		{"Asia/Tokyo", time.Date(2026, 7, 31, 3, 0, 0, 0, time.UTC), 1, "2026-08-01T00:00:00+09:00:00", "month end"},
		{"Asia/Tokyo", time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC), -1, "2026-02-28T00:00:00+09:00:00", "back across a non-leap February"},
	} {
		setLocal(t, loadLocation(t, c.zone))
		if got := DayStartFrom(c.at, c.days).Format(layout); got != c.want {
			t.Errorf("%s (%s): %s, want %s", c.name, c.zone, got, c.want)
		}
	}
}

// Around every kind of switch, a day start is never after the instant it is
// for and falls on that instant's date, and stepping from it moves strictly
// forward or back, one existing date at a time — so day loops always end.
func TestDayStartFromInvariants(t *testing.T) {
	for _, c := range []struct {
		zone     string
		from, to time.Time // UTC span to probe, hour by hour
		dates    []string  // the dates the span's days step through
	}{
		{"America/Santiago", time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC), time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), []string{"2026-09-04", "2026-09-05", "2026-09-06", "2026-09-07"}},
		{"America/St_Johns", time.Date(2008, 10, 31, 12, 0, 0, 0, time.UTC), time.Date(2008, 11, 4, 0, 0, 0, 0, time.UTC), []string{"2008-10-31", "2008-11-01", "2008-11-02", "2008-11-03"}},
		{"Asia/Manila", time.Date(1844, 12, 30, 4, 0, 0, 0, time.UTC), time.Date(1845, 1, 2, 12, 0, 0, 0, time.UTC), []string{"1844-12-29", "1844-12-30", "1845-01-01", "1845-01-02"}},
		{"Pacific/Apia", time.Date(2011, 12, 28, 12, 0, 0, 0, time.UTC), time.Date(2012, 1, 1, 12, 0, 0, 0, time.UTC), []string{"2011-12-28", "2011-12-29", "2011-12-31", "2012-01-01"}},
	} {
		loc := loadLocation(t, c.zone)
		setLocal(t, loc)
		for at := c.from; at.Before(c.to); at = at.Add(30 * time.Minute) {
			if s := DayStart(at); s.After(at) || DayKey(s) != DayKey(at) {
				t.Errorf("%s: DayStart(%s) = %s, want a start on %s no later than it", c.zone, at.In(loc).Format(time.RFC3339), s.Format(time.RFC3339), DayKey(at))
			}
		}
		var forward []string
		for d := DayStart(c.from); len(forward) < len(c.dates); {
			forward = append(forward, DayKey(d))
			next := DayStartFrom(d, 1)
			if !next.After(d) {
				t.Fatalf("%s: DayStartFrom(%s, 1) = %s, not after it", c.zone, d.Format(time.RFC3339), next.Format(time.RFC3339))
			}
			if back := DayStartFrom(next, -1); !back.Equal(d) {
				t.Errorf("%s: DayStartFrom(%s, -1) = %s, want %s", c.zone, next.Format(time.RFC3339), back.Format(time.RFC3339), d.Format(time.RFC3339))
			}
			d = next
		}
		if fmt.Sprint(forward) != fmt.Sprint(c.dates) {
			t.Errorf("%s: stepping lists %v, want %v", c.zone, forward, c.dates)
		}
	}
}

// ruleOnlyZone is a zone with no transitions of its own, only the TZ rule —
// what a slim tz database file leaves for the years after its last listed
// transition — so Go works out every period from the rule.
func ruleOnlyZone(t *testing.T, name string, offset int32, rule string) *time.Location {
	t.Helper()
	var b bytes.Buffer
	header := func(types, chars uint32) {
		b.WriteString("TZif2")
		b.Write(make([]byte, 15))
		for _, n := range []uint32{0, 0, 0, 0, types, chars} { // ut, std, leap, transition, type, char counts
			binary.Write(&b, binary.BigEndian, n)
		}
	}
	header(0, 0) // the version 1 data, skipped by version 2 readers
	header(1, uint32(len(name)+1))
	binary.Write(&b, binary.BigEndian, offset)
	b.Write([]byte{0, 0}) // not DST, designation at 0
	b.WriteString(name + "\x00\n" + rule + "\n")
	loc, err := time.LoadLocationFromTZData(name, b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// Under a zone's TZ rule Go ends each period 365 days into the UTC year, a
// day short in a leap year, so a period can "end" at or before the instant
// asked about. Day starts around such a year end still come out right and
// stepping through it still ends (#346): with a rule-only zone (a slim tz
// file past its last transition) and, where the tz database has it, the
// system's America/New_York past its last listed year.
func TestDayStartFromRuleOnlyZone(t *testing.T) {
	ny := ruleOnlyZone(t, "EST", -5*60*60, "EST5EDT,M3.2.0,M11.1.0")
	setLocal(t, ny)
	got := dayStartsWithin(t, []dayStep{
		{time.Date(2028, 12, 31, 12, 0, 0, 0, ny), 0},
		{time.Date(2028, 12, 31, 12, 0, 0, 0, ny), 1},
		{time.Date(2029, 1, 1, 12, 0, 0, 0, ny), -1},
		{time.Date(2028, 3, 12, 12, 0, 0, 0, ny), 0}, // the rule's DST start
		{time.Date(2028, 3, 12, 12, 0, 0, 0, ny), 1},
	})
	want := []string{"2028-12-31T00:00:00-05:00", "2029-01-01T00:00:00-05:00", "2028-12-31T00:00:00-05:00", "2028-03-12T00:00:00-05:00", "2028-03-13T00:00:00-04:00"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rule-only zone: day starts = %v, want %v", got, want)
	}

	sys, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database entry for America/New_York here: %v", err)
	}
	setLocal(t, sys)
	got = dayStartsWithin(t, []dayStep{
		{time.Date(2040, 12, 31, 12, 0, 0, 0, sys), 0},
		{time.Date(2040, 12, 31, 12, 0, 0, 0, sys), 1},
	})
	if want := []string{"2040-12-31T00:00:00-05:00", "2041-01-01T00:00:00-05:00"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("America/New_York: day starts = %v, want %v", got, want)
	}
}

type dayStep struct {
	at   time.Time
	days int
}

// dayStartsWithin computes DayStartFrom for each step, failing the test
// rather than hanging it if the computation never returns.
func dayStartsWithin(t *testing.T, steps []dayStep) []string {
	t.Helper()
	done := make(chan []string, 1)
	go func() {
		var got []string
		for _, s := range steps {
			got = append(got, DayStartFrom(s.at, s.days).Format(time.RFC3339))
		}
		done <- got
	}()
	select {
	case got := <-done:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("DayStartFrom did not return")
		return nil
	}
}

// The day before a skipped midnight runs to the switch: its last hour counts
// toward today's total rather than falling past a window that time.Date
// would have closed at 23:00 (#346).
func TestClaudeTotalsBeforeSkippedMidnight(t *testing.T) {
	santiago := loadLocation(t, "America/Santiago")
	setLocal(t, santiago)
	root := t.TempDir()
	late := time.Date(2026, 9, 5, 23, 30, 0, 0, santiago)
	writeFile(t, filepath.Join(root, "projects", "p", "late.jsonl"), claudeMsg(late, 100, 0, 0, 10)+"\n", late)

	got := mustClaudeTotals(t, root, late.Add(10*time.Minute), noPrices)
	if got.Tokens != 110 {
		t.Errorf("ClaudeTotals at 23:40 on 2026-09-05 = %d tokens, want 110 (the 23:30 message)", got.Tokens)
	}
}

func TestClaudeTokens(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	// today.jsonl: two of today's messages + one yesterday (excluded).
	// New tokens exclude cache reads: msg1=100+5000+400=5500, msg2=2+673+481=1156.
	today := claudeMsg(now, 100, 5000, 30000, 400) + "\n" +
		claudeMsg(now, 2, 673, 39451, 481) + "\n" +
		claudeMsg(yesterday, 9, 9, 9, 9) + "\n"
	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"), today, now)

	// old.jsonl: only yesterday → skipped by the mtime filter.
	writeFile(t, filepath.Join(root, "projects", "p", "old.jsonl"),
		claudeMsg(yesterday, 1000, 0, 0, 1000)+"\n", yesterday)

	// Tokens is the billing volume: input + cache writes + cache reads + output
	// per message (#234), with the same breakdown as session.tokens.
	got := mustClaudeTotals(t, root, now, noPrices)
	if want := int64((100 + 5000 + 30000 + 400) + (2 + 673 + 39451 + 481)); got.Tokens != want {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d", got.Tokens, want)
	}
	if got.Input != (100+5000+30000)+(2+673+39451) || got.CachedInput != 30000+39451 || got.Output != 400+481 {
		t.Errorf("ClaudeTotals breakdown = in %d / cached %d / out %d, want %d / %d / %d",
			got.Input, got.CachedInput, got.Output, (100+5000+30000)+(2+673+39451), 30000+39451, 400+481)
	}

	// With a price for the model, cost is summed across today's messages.
	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	cost := mustClaudeTotals(t, root, now, prices).Cost
	// msg1: (100*15 + 5000*18.75 + 30000*1.5 + 400*75)/1e6
	// msg2: (2*15 + 673*18.75 + 39451*1.5 + 481*75)/1e6
	want := (100*15.0+5000*18.75+30000*1.5+400*75)/1e6 +
		(2*15.0+673*18.75+39451*1.5+481*75)/1e6
	if diff := cost - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, want)
	}
}

func TestClaudeTotalsUsesClaudeConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", root)
	now := time.Now()

	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"),
		claudeMsg(now, 10, 20, 100, 5)+"\n", now)

	if got := mustClaudeTotals(t, "", now, noPrices).Tokens; got != 10+20+100+5 {
		t.Errorf("mustClaudeTotals(t, empty root).Tokens = %d, want %d from CLAUDE_CONFIG_DIR", got, 10+20+100+5)
	}
}

func TestClaudeCostWithCacheCreationTTL(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"),
		claudeMsgCacheCreation(now, 100, 200, 300, 1000, 10)+"\n", now)

	prices := pricing.Table{"claude-fable": {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5}}
	got := mustClaudeTotals(t, root, now, prices)
	if got.Tokens != 100+200+300+1000+10 {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d", got.Tokens, 100+200+300+1000+10)
	}
	// API estimate: 5m cache write uses cache_write, 1h cache write uses 2x input,
	// cache read uses the API cache-read price.
	wantCost := (100*10.0 + 200*12.5 + 300*20.0 + 1000*1.0 + 10*50.0) / 1e6
	if diff := got.Cost - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", got.Cost, wantCost)
	}
}

func TestClaudeCostClampsInconsistentCacheCreationTTL(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(root, "projects", "p", "today.jsonl"),
		claudeMsgCacheCreationTotal(now, 100, 250, 200, 300, 1000, 10)+"\n", now)

	prices := pricing.Table{"claude-fable": {In: 10, Out: 50, CacheRead: 1, CacheWrite: 12.5}}
	got := mustClaudeTotals(t, root, now, prices)
	if got.Tokens != 100+250+1000+10 {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d", got.Tokens, 100+250+1000+10)
	}
	// The TTL split exceeds the top-level total, so the authoritative total is
	// counted once as unknown cache creation instead of over-counting the split.
	wantCost := (100*10.0 + 250*12.5 + 1000*1.0 + 10*50.0) / 1e6
	if diff := got.Cost - wantCost; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", got.Cost, wantCost)
	}
}

func TestClaudeSessionToday(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	// One session file with today's two messages + one from yesterday (excluded).
	// New tokens exclude cache reads: 100+5000+400=5500, 2+673+481=1156.
	path := filepath.Join(root, "sess.jsonl")
	content := claudeMsg(now, 100, 5000, 30000, 400) + "\n" +
		claudeMsg(now, 2, 673, 39451, 481) + "\n" +
		claudeMsg(yesterday, 9999, 0, 0, 9999) + "\n"
	writeFile(t, path, content, now)

	if got, want := ClaudeSessionToday(path, now, noPrices).Tokens, int64((100+5000+30000+400)+(2+673+39451+481)); got != want {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d", got, want)
	}
	// Empty path and missing file are both zero, not a crash.
	if got := ClaudeSessionToday("", now, noPrices); got.Tokens != 0 {
		t.Errorf("empty path = %+v, want zero", got)
	}
	if got := ClaudeSessionToday(filepath.Join(root, "nope.jsonl"), now, noPrices); got.Tokens != 0 {
		t.Errorf("missing file = %+v, want zero", got)
	}
}

func TestClaudeTotalsIncludesSubagentsAndWorkflows(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath,
		claudeMsg(now, 10, 20, 100, 5)+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "agent-a.jsonl"),
		claudeMsg(now, 1, 2, 50, 3)+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_123", "agent-b.jsonl"),
		claudeMsg(now, 4, 5, 60, 6)+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_123", "journal.jsonl"),
		`{"timestamp":"`+now.Format(time.RFC3339)+`","event":"started"}`+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "old-agent.jsonl"),
		claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n", yesterday)

	// main 10+20+100+5=135, subagent 1+2+50+3=56, workflow 4+5+60+6=75.
	if got := mustClaudeTotals(t, root, now, noPrices).Tokens; got != int64(135+56+75) {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d (main + subagent + workflow)", got, 135+56+75)
	}

	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	wantCost := (10*15.0+20*18.75+100*1.5+5*75)/1e6 +
		(1*15.0+2*18.75+50*1.5+3*75)/1e6 +
		(4*15.0+5*18.75+60*1.5+6*75)/1e6
	if cost := mustClaudeTotals(t, root, now, prices).Cost; cost-wantCost > 1e-9 || cost-wantCost < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, wantCost)
	}
	if got := ClaudeSessionToday(mainPath, now, noPrices).Tokens; got != int64(135+56+75) {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d (main + subagent + workflow)", got, 135+56+75)
	}
}

// A single response is written once per content block, each line repeating the
// same usage; it must be counted once. The same response duplicated across
// files (resume/compaction copies prior turns forward) must not double-count.
func TestClaudeDedup(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	// Response A as 3 content-block lines (identical usage) + distinct B once.
	// A=10+20+100+5=135, B=1+2+50+3=56.
	a := claudeMsgID(now, "msg_a", "req_a", 10, 20, 100, 5)
	b := claudeMsgID(now, "msg_b", "req_b", 1, 2, 50, 3)
	writeFile(t, filepath.Join(root, "projects", "p", "s1.jsonl"),
		a+"\n"+a+"\n"+a+"\n"+b+"\n", now)
	// A second file re-includes response A (resume copies the prior turn).
	writeFile(t, filepath.Join(root, "projects", "p", "s2.jsonl"), a+"\n", now)

	if got := mustClaudeTotals(t, root, now, noPrices).Tokens; got != int64(135+56) {
		t.Errorf("ClaudeTotals.Tokens = %d, want %d (A once + B once)", got, 135+56)
	}

	// Cost dedups identically: A + B priced once each.
	prices := pricing.Table{"claude-fable": {In: 15, Out: 75, CacheRead: 1.5, CacheWrite: 18.75}}
	wantCost := (10*15.0+20*18.75+100*1.5+5*75)/1e6 + (1*15.0+2*18.75+50*1.5+3*75)/1e6
	if cost := mustClaudeTotals(t, root, now, prices).Cost; cost-wantCost > 1e-9 || cost-wantCost < -1e-9 {
		t.Errorf("ClaudeTotals.Cost = %v, want %v", cost, wantCost)
	}

	// ClaudeSessionToday dedups within the file: A counted once.
	if got := ClaudeSessionToday(filepath.Join(root, "projects", "p", "s1.jsonl"), now, noPrices).Tokens; got != int64(135+56) {
		t.Errorf("ClaudeSessionToday.Tokens = %d, want %d", got, 135+56)
	}
}

func codexSession(total int64) string {
	return fmt.Sprintf(`{"type":"turn_context","payload":{"model":"gpt-5.5"}}`+"\n"+
		`{"timestamp":"x","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":0,"total_tokens":%d}}}}`+"\n", total, total)
}

// Codex tokens are the growth of the cumulative total as reported — cached
// input included — with the same breakdown as session.tokens (#234); the cost
// still prices cached input at the cache-read rate.
func TestCodexTotalsCountCachedInput(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	session := `{"type":"turn_context","payload":{"model":"gpt-5.5"}}` + "\n" +
		`{"timestamp":"x","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":200,"total_tokens":1200}}}}` + "\n"
	writeFile(t, filepath.Join(dayDir, "s1.jsonl"), session, now)

	got := mustCodexTotals(t, root, now, pricing.Table{"gpt-5.5": {In: 2, Out: 8, CacheRead: 0.5}})
	if got.Tokens != 1200 || got.Input != 1000 || got.CachedInput != 600 || got.Output != 200 {
		t.Errorf("CodexTotals = %+v, want tokens 1200 / in 1000 / cached 600 / out 200", got)
	}
	if want := (400*2.0 + 600*0.5 + 200*8.0) / 1e6; got.Cost != want {
		t.Errorf("CodexTotals.Cost = %v, want %v", got.Cost, want)
	}
}

func TestCodexTotals(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	writeFile(t, filepath.Join(dayDir, "s1.jsonl"), codexSession(1000), now)
	writeFile(t, filepath.Join(dayDir, "s2.jsonl"), codexSession(500), now)

	// Yesterday's folder must be ignored.
	y := now.Add(-24 * time.Hour)
	yDir := filepath.Join(root, "sessions", y.Local().Format("2006"), y.Local().Format("01"), y.Local().Format("02"))
	writeFile(t, filepath.Join(yDir, "old.jsonl"), codexSession(9999), y)

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != 1500 {
		t.Errorf("CodexTotals.Tokens = %d, want 1500", got)
	}
	// input=1000/500 all non-cached, priced at In=2/Mtok → (1000+500)*2/1e6.
	prices := pricing.Table{"gpt-5": {In: 2}}
	if cost := mustCodexTotals(t, root, now, prices).Cost; cost != 1500*2.0/1e6 {
		t.Errorf("CodexTotals.Cost = %v, want %v", cost, 1500*2.0/1e6)
	}
}

func TestCodexTotalsUsesCodexHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	now := time.Now()

	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	writeFile(t, filepath.Join(dayDir, "s1.jsonl"), codexSession(1000), now)

	if got := mustCodexTotals(t, "", now, noPrices).Tokens; got != 1000 {
		t.Errorf("mustCodexTotals(t, empty root).Tokens = %d, want 1000 from CODEX_HOME", got)
	}
}

// A Codex session that resumed into a second rollout carries its cumulative
// total forward; daily must count it once (the largest cumulative), not sum
// both files. Distinct sessions still add up.
func TestCodexTotalsDedupSession(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	id := "019e5933-2289-7e72-88fd-c494201693fa"
	// Same session id in two files: cumulative 1000 then 1500 (resumed).
	writeFile(t, filepath.Join(dayDir, "rollout-2026-05-24T10-00-00-"+id+".jsonl"), codexSession(1000), now)
	writeFile(t, filepath.Join(dayDir, "rollout-2026-05-24T11-00-00-"+id+".jsonl"), codexSession(1500), now)
	// A distinct session adds 500.
	writeFile(t, filepath.Join(dayDir, "rollout-2026-05-24T12-00-00-019e0000-0000-7000-8000-000000000000.jsonl"), codexSession(500), now)

	if got := mustCodexTotals(t, root, now, noPrices).Tokens; got != int64(1500+500) {
		t.Errorf("CodexTotals.Tokens = %d, want %d (resumed session counted once at max cumulative + distinct)", got, 1500+500)
	}
}

func TestEmptyRoots(t *testing.T) {
	if got := mustClaudeTotals(t, t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("mustClaudeTotals(t, empty) = %+v", got)
	}
	if got := mustCodexTotals(t, t.TempDir(), time.Now(), noPrices); got.Tokens != 0 || got.Cost != 0 {
		t.Errorf("mustCodexTotals(t, empty) = %+v", got)
	}
}

// mustClaudeTotals / mustCodexTotals unwrap the error for the happy-path
// tests, which all operate on readable roots.
func mustClaudeTotals(t *testing.T, root string, now time.Time, prices pricing.Table) Totals {
	t.Helper()
	tot, err := ClaudeTotals(root, now, prices)
	if err != nil {
		t.Fatalf("ClaudeTotals(%q) error: %v", root, err)
	}
	return tot
}

func mustCodexTotals(t *testing.T, root string, now time.Time, prices pricing.Table) Totals {
	t.Helper()
	tot, err := CodexTotals(root, now, prices)
	if err != nil {
		t.Fatalf("CodexTotals(%q) error: %v", root, err)
	}
	return tot
}

func TestClaudeTotalsZeroWhenProjectsMissing(t *testing.T) {
	now := time.Now()
	tot, err := ClaudeTotals(t.TempDir(), now, noPrices)
	if err != nil {
		t.Fatalf("ClaudeTotals error: %v (a missing projects dir is a real zero, not unknown)", err)
	}
	if tot.Tokens != 0 {
		t.Errorf("Tokens = %d, want 0", tot.Tokens)
	}
}

func TestClaudeTotalsErrorWhenProjectsUnreadable(t *testing.T) {
	root := t.TempDir()
	// projects as a regular file makes os.ReadDir fail with a non-NotExist
	// error on every platform — the "unknown, keep daily null" case.
	if err := os.WriteFile(filepath.Join(root, "projects"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaudeTotals(root, time.Now(), noPrices); err == nil {
		t.Fatal("ClaudeTotals error = nil, want error for unreadable projects dir")
	}
}

func TestCodexTotalsErrorWhenDayDirUnreadable(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	monthDir := filepath.Join(root, "sessions", now.Format("2006"), now.Format("01"))
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Today's day directory as a regular file: non-NotExist ReadDir error.
	if err := os.WriteFile(filepath.Join(monthDir, now.Format("02")), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexTotals(root, now, noPrices); err == nil {
		t.Fatal("CodexTotals error = nil, want error for unreadable day dir")
	}
}

// A transcript that is listed but can't be read must make the whole total
// unknown (error), not silently smaller (#187). A dangling symlink reproduces
// the read failure portably without chmod (which is a no-op as root).
func TestClaudeTotalsErrorWhenTranscriptUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	now := time.Now()
	writeFile(t, filepath.Join(root, "projects", "p", "ok.jsonl"),
		claudeMsg(now, 10, 0, 0, 0)+"\n", now)
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "projects", "p", "broken.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaudeTotals(root, now, noPrices); err == nil {
		t.Fatal("ClaudeTotals error = nil, want error when one transcript can't be read")
	}
}

// Same contract on the Codex side: one unreadable rollout in a scanned day
// directory means the total is unknown, not partial (#187).
func TestCodexTotalsErrorWhenRolloutUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	now := time.Now()
	dayDir := filepath.Join(root, "sessions", now.Local().Format("2006"), now.Local().Format("01"), now.Local().Format("02"))
	writeFile(t, filepath.Join(dayDir, "ok.jsonl"), codexSession(1000), now)
	if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(dayDir, "broken.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexTotals(root, now, noPrices); err == nil {
		t.Fatal("CodexTotals error = nil, want error when one rollout can't be read")
	}
}
