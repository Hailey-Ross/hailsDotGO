package handlers

import (
	"testing"
	"time"
)

func TestTradeCooldownLengths(t *testing.T) {
	want := map[int]time.Duration{
		1: time.Minute, 2: 10 * time.Minute, 3: time.Hour,
		4: 2 * time.Hour, 5: 4 * time.Hour, 6: 8 * time.Hour, 7: 16 * time.Hour,
		8: 32 * time.Hour, 9: 64 * time.Hour, 10: 128 * time.Hour,
		11: tradeCooldownCap, 50: tradeCooldownCap, 1000: tradeCooldownCap,
	}
	for level, d := range want {
		if got := tradeCooldownFor(level); got != d {
			t.Errorf("level %d: got %v, want %v", level, got, d)
		}
	}
	for level := 1; level < 60; level++ {
		if tradeCooldownFor(level+1) < tradeCooldownFor(level) {
			t.Errorf("cooldown shrinks from level %d to %d", level, level+1)
		}
	}
}

func TestNextTradeCooldownLevel(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		prev  int
		ended time.Time
		want  int
	}{
		{"first ever", 0, time.Time{}, 1},
		{"soon after level 1", 1, now.Add(-time.Hour), 2},
		{"soon after level 3", 3, now.Add(-23 * time.Hour), 4},
		{"a quiet day resets", 5, now.Add(-25 * time.Hour), 1},
		{"staff or unknown reason", 0, now.Add(-time.Minute), 1},
	}
	for _, c := range cases {
		if got := nextTradeCooldownLevel(c.prev, c.ended, now); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestAutoCooldownReasonRoundTrip(t *testing.T) {
	for _, n := range []int{1, 2, 4, 37} {
		if got := parseAutoCooldownLevel(autoCooldownReason(n)); got != n {
			t.Errorf("level %d read back as %d", n, got)
		}
	}
	for _, r := range []string{"staff", "", "auto_rapid_", "auto_rapid_0", "auto_cap"} {
		if got := parseAutoCooldownLevel(r); got != 0 {
			t.Errorf("%q read as level %d, want 0", r, got)
		}
	}
}

// feed sends attempts at the given offsets from start through rapidityStep and reports, for
// each one, whether it tripped a cooldown.
func feed(start time.Time, offsets []time.Duration, st tradeRapidity) ([]bool, tradeRapidity) {
	trips := make([]bool, len(offsets))
	for i, off := range offsets {
		st, trips[i] = rapidityStep(st, start.Add(off))
	}
	return trips, st
}

func every(n int, gap time.Duration, from time.Duration) []time.Duration {
	out := make([]time.Duration, n)
	for i := range out {
		out[i] = from + time.Duration(i)*gap
	}
	return out
}

func TestRapidityEagerTrainerIsNeverCooled(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	// Ten requests spread over a whole day: eager, not rapid.
	trips, st := feed(start, every(10, 40*time.Minute, 0), tradeRapidity{})
	for i, tr := range trips {
		if tr {
			t.Fatalf("attempt %d tripped a cooldown for a slow, eager trainer", i+1)
		}
	}
	if !st.watchFrom.IsZero() {
		t.Fatal("a slow trainer was put on watch")
	}
}

func TestRapidityBurstOnlyStartsTheWatch(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	// Ten requests in 18 minutes: watched, but that burst itself never cools them.
	trips, st := feed(start, every(10, 2*time.Minute, 0), tradeRapidity{})
	for i, tr := range trips {
		if tr {
			t.Fatalf("attempt %d of the first burst tripped a cooldown", i+1)
		}
	}
	if st.watchFrom.IsZero() {
		t.Fatal("ten requests in under 30 minutes did not start the watch")
	}
}

func TestRapidityWatchedTrainerTripsOnFiveInFifteen(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	_, st := feed(start, every(10, 2*time.Minute, 0), tradeRapidity{})
	// Two hours later, still watched: five attempts three minutes apart. The fifth trips.
	trips, st := feed(start, every(5, 3*time.Minute, 2*time.Hour), st)
	for i, tr := range trips {
		if tr != (i == 4) {
			t.Fatalf("attempt %d: trip=%v, want trip only on the fifth", i+1, tr)
		}
	}
	// The next attempt right after does not trip again on the same five.
	trips, _ = feed(start, []time.Duration{2*time.Hour + 13*time.Minute}, st)
	if trips[0] {
		t.Fatal("the attempt after a trip tripped again on the same attempts")
	}
}

func TestRapidityWatchExpiresAfterADay(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	_, st := feed(start, every(10, 2*time.Minute, 0), tradeRapidity{})
	// 25 hours later: no longer watched, five quick ones do nothing.
	trips, _ := feed(start, every(5, time.Minute, 25*time.Hour), st)
	for i, tr := range trips {
		if tr {
			t.Fatalf("attempt %d tripped after the watch should have expired", i+1)
		}
	}
}

// Review finding 2026-11-18: a trip used to restart the 24 hour watch, so a trainer who
// tripped once a day stayed watched forever. The watch ends 24 hours after it began.
func TestRapidityTripDoesNotExtendTheWatch(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	_, st := feed(start, every(10, time.Minute, 0), tradeRapidity{}) // watch begins at start+9m
	trips, st := feed(start, every(5, time.Minute, 23*time.Hour+50*time.Minute), st)
	if !trips[4] {
		t.Fatal("five quick attempts inside the watch did not trip")
	}
	// After the watch's 24 hours: five quick ones are not a burst of ten, so nothing trips.
	trips, st = feed(start, every(5, time.Minute, 24*time.Hour+30*time.Minute), st)
	for i, tr := range trips {
		if tr {
			t.Fatalf("attempt %d tripped after the watch should have ended", i+1)
		}
	}
	if !st.watchFrom.IsZero() {
		t.Fatal("the watch outlived its 24 hours")
	}
}

func TestRapidityWatchedButSlowIsFine(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	_, st := feed(start, every(10, 2*time.Minute, 0), tradeRapidity{})
	// Watched, then five attempts five minutes apart: 20 minutes, never five inside 15.
	trips, _ := feed(start, every(5, 5*time.Minute, time.Hour), st)
	for i, tr := range trips {
		if tr {
			t.Fatalf("attempt %d tripped though only four fit in any 15 minutes", i+1)
		}
	}
}

func TestValidateTradeMessage(t *testing.T) {
	ok := []tradeMessageInput{
		{Key: "agree"},
		{Key: "req_trade_open"},
		{Key: "req_trade_for", ShinyID: 7},
		{Key: "offer_mine", ShinyID: 3},
		{Key: "when", When: "this_weekend"},
		{Key: "friend_code"},
	}
	for _, in := range ok {
		if _, err := validateTradeMessage(in); err != nil {
			t.Errorf("%+v rejected: %v", in, err)
		}
	}
	bad := []struct {
		in   tradeMessageInput
		want error
	}{
		{tradeMessageInput{Key: "hello there"}, errTradeBadPreset},
		{tradeMessageInput{Key: ""}, errTradeBadPreset},
		{tradeMessageInput{Key: "sys_completed"}, errTradeSystemOnly},
		{tradeMessageInput{Key: "req_trade_for"}, errTradeBadParams},
		{tradeMessageInput{Key: "req_trade_for", ShinyID: 7, When: "today"}, errTradeBadParams},
		{tradeMessageInput{Key: "when", When: "midnight"}, errTradeBadParams},
		{tradeMessageInput{Key: "when", When: "today", ShinyID: 1}, errTradeBadParams},
		{tradeMessageInput{Key: "agree", ShinyID: 1}, errTradeBadParams},
		{tradeMessageInput{Key: "friend_code", When: "today"}, errTradeBadParams},
	}
	for _, c := range bad {
		if _, err := validateTradeMessage(c.in); err != c.want {
			t.Errorf("%+v: got %v, want %v", c.in, err, c.want)
		}
	}
}

func TestTradeLimitsGrowWithTheCommunity(t *testing.T) {
	if l := tradeLimitsFor(7); l.Daily != 10 || l.Waiting != 30 {
		t.Errorf("today's community (7) got %+v, want the owner's 10 a day and 30 waiting", l)
	}
	if l := tradeLimitsFor(0); l.Daily != 10 {
		t.Errorf("no count yet got %+v, want the smallest tier", l)
	}
	prev := tradeLimitsFor(0)
	for n := 0; n <= 100000; n += 50 {
		l := tradeLimitsFor(n)
		if l.Daily < prev.Daily || l.Waiting < prev.Waiting {
			t.Fatalf("limits shrank at %d members: %+v after %+v", n, l, prev)
		}
		if l.Waiting < l.Daily {
			t.Fatalf("at %d members waiting (%d) is below daily (%d)", n, l.Waiting, l.Daily)
		}
		prev = l
	}
	// Every tier boundary is a real step up: crossing it raises both limits.
	for _, tier := range tradeLimitTiers[:len(tradeLimitTiers)-1] {
		below, at := tradeLimitsFor(tier.Members-1), tradeLimitsFor(tier.Members)
		if at.Daily <= below.Daily || at.Waiting <= below.Waiting {
			t.Errorf("crossing %d members does not raise the limits: %+v then %+v", tier.Members, below, at)
		}
	}
	if l := tradeLimitsFor(1 << 40); l.Daily != 50 {
		t.Errorf("a huge community got %+v, want the top tier", l)
	}
}

func TestEveryTradePresetHasAKnownParam(t *testing.T) {
	known := map[string]bool{tradeParamNone: true, tradeParamTheirShiny: true, tradeParamMyShiny: true, tradeParamWhen: true, tradeParamCode: true}
	for key, p := range tradePresets {
		if !known[p.Param] {
			t.Errorf("preset %q has unknown param kind %q", key, p.Param)
		}
		if p.System != (len(key) > 4 && key[:4] == "sys_") {
			t.Errorf("preset %q: System=%v does not match its sys_ prefix", key, p.System)
		}
	}
}
