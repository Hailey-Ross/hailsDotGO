package pogodata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// denver is a fixed negative offset zone. time.LoadLocation is deliberately not
// used: it needs tzdata on the host, which is not guaranteed on Windows or in a
// minimal build image, and the offset is the only property these tests need.
var denver = time.FixedZone("MDT", -6*3600)

// The feed sends two kinds of timestamp and reading them the same way is what
// went wrong. A value ending in Z is an instant; a value with no zone is a wall
// clock that means the same reading everywhere. The detail scraper used to know
// only the second layout and read it as UTC.
func TestParseFeedTime(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		loc      *time.Location
		wantOK   bool
		floating bool
		// wantUTC is the expected instant, as an RFC3339 string, when ok.
		wantUTC string
	}{
		{"floating read in UTC", "2026-08-05T18:00:00.000", time.UTC, true, true, "2026-08-05T18:00:00Z"},
		// The same wall clock in a negative offset zone is a LATER instant. This
		// is the whole bug: reading it as UTC made events look hours older than
		// they were, so live ones were treated as ended and their pages deleted.
		{"floating read in Denver", "2026-08-05T18:00:00.000", denver, true, true, "2026-08-06T00:00:00Z"},
		{"floating without milliseconds", "2026-08-05T18:00:00", denver, true, true, "2026-08-06T00:00:00Z"},
		{"utc with milliseconds", "2026-08-04T20:00:00.000Z", denver, true, false, "2026-08-04T20:00:00Z"},
		{"utc without milliseconds", "2026-08-04T20:00:00Z", denver, true, false, "2026-08-04T20:00:00Z"},
		{"utc with odd fractional digits", "2026-08-04T20:00:00.123456Z", denver, true, false, "2026-08-04T20:00:00.123456Z"},
		{"whitespace is trimmed", "  2026-08-05T18:00:00.000  ", time.UTC, true, true, "2026-08-05T18:00:00Z"},
		{"nil location falls back to UTC", "2026-08-05T18:00:00.000", nil, true, true, "2026-08-05T18:00:00Z"},
		{"empty", "", time.UTC, false, false, ""},
		{"garbage", "soon", time.UTC, false, false, ""},
		{"date only", "2026-08-05", time.UTC, false, false, ""},
		{"numeric offset is not a shape the feed sends", "2026-08-05T18:00:00+02:00", time.UTC, false, false, ""},
	}
	for _, tc := range cases {
		got, floating, ok := ParseFeedTime(tc.in, tc.loc)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if floating != tc.floating {
			t.Errorf("%s: floating = %v, want %v", tc.name, floating, tc.floating)
		}
		want, err := time.Parse(time.RFC3339Nano, tc.wantUTC)
		if err != nil {
			t.Fatalf("%s: bad test expectation %q: %v", tc.name, tc.wantUTC, err)
		}
		if !got.Equal(want) {
			t.Errorf("%s: ParseFeedTime(%q) = %s, want the same instant as %s",
				tc.name, tc.in, got.UTC().Format(time.RFC3339Nano), tc.wantUTC)
		}
	}
}

func feed(entries ...feedEntry) []feedEntry { return entries }

func strp(s string) *string { return &s }

// planDetailRefresh decides the active set, and the caller DELETES every cached
// page whose id is not in it. So these assertions matter more for what stays
// active than for what gets queued: a wrong answer here destroys scraped data.
func TestPlanDetailRefresh(t *testing.T) {
	// 18:00 in Denver on 5 August, expressed as the instant it really is.
	now := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)

	t.Run("a floating end later today stays active in a negative offset zone", func(t *testing.T) {
		// 20:00 local is still two hours away for someone in Denver. Read as UTC
		// it looks four hours past, which is exactly how live events used to be
		// evicted mid run.
		entries := feed(feedEntry{EventID: "spotlight", Link: "https://leekduck.com/events/spotlight/", End: strp("2026-08-05T20:00:00.000")})
		active, jobs := planDetailRefresh(entries, nil, now, denver)
		if !active["spotlight"] {
			t.Error("a still running event was dropped from active, so its cached page would be deleted")
		}
		if len(jobs) != 1 {
			t.Errorf("jobs = %d, want 1", len(jobs))
		}
	})

	t.Run("a floating end already past is skipped", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "over", Link: "https://leekduck.com/events/over/", End: strp("2026-08-05T10:00:00.000")})
		active, jobs := planDetailRefresh(entries, nil, now, denver)
		if active["over"] {
			t.Error("an ended event stayed active")
		}
		if len(jobs) != 0 {
			t.Errorf("jobs = %d, want 0 for an ended event", len(jobs))
		}
	})

	t.Run("a Z suffixed end in the past is skipped", func(t *testing.T) {
		// This used to be impossible: the single layout could not parse a Z at
		// all, so GO Battle League events were never recognised as finished.
		entries := feed(feedEntry{EventID: "gbl_old", Link: "https://leekduck.com/events/gbl/", End: strp("2026-08-04T20:00:00.000Z")})
		active, _ := planDetailRefresh(entries, nil, now, denver)
		if active["gbl_old"] {
			t.Error("a finished GO Battle League rotation stayed active")
		}
	})

	t.Run("a Z suffixed end in the future stays active", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "gbl_new", Link: "https://leekduck.com/events/gbl/", End: strp("2026-08-11T20:00:00.000Z")})
		active, _ := planDetailRefresh(entries, nil, now, denver)
		if !active["gbl_new"] {
			t.Error("a running GO Battle League rotation was dropped")
		}
	})

	t.Run("an unparseable end fails open and stays active", func(t *testing.T) {
		// Failing open costs one polite refetch. Failing closed would delete a
		// live event's page the day upstream changes a format.
		entries := feed(feedEntry{EventID: "weird", Link: "https://leekduck.com/events/weird/", End: strp("sometime next week")})
		active, _ := planDetailRefresh(entries, nil, now, denver)
		if !active["weird"] {
			t.Error("an event with an unreadable end time was dropped, which would delete its page")
		}
	})

	t.Run("a missing end stays active", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "openended", Link: "https://leekduck.com/events/openended/"})
		active, _ := planDetailRefresh(entries, nil, now, denver)
		if !active["openended"] {
			t.Error("an event with no end time was dropped")
		}
	})

	t.Run("a fresh cached page is active but not queued", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "cached", Link: "https://leekduck.com/events/cached/"})
		fetchedAt := map[string]time.Time{"cached": now.Add(-1 * time.Hour)}
		active, jobs := planDetailRefresh(entries, fetchedAt, now, denver)
		if !active["cached"] {
			t.Error("a cached event was dropped from active")
		}
		if len(jobs) != 0 {
			t.Errorf("jobs = %d, want 0: the page is still inside detailRefreshAge", len(jobs))
		}
	})

	t.Run("a stale cached page is queued again", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "stale", Link: "https://leekduck.com/events/stale/"})
		fetchedAt := map[string]time.Time{"stale": now.Add(-detailRefreshAge - time.Minute)}
		active, jobs := planDetailRefresh(entries, fetchedAt, now, denver)
		if !active["stale"] {
			t.Error("a stale event was dropped from active")
		}
		if len(jobs) != 1 {
			t.Errorf("jobs = %d, want 1", len(jobs))
		}
	})

	// Unusable entries are correctly not active: they describe no event that could
	// be scraped. The danger is entirely in what the CALLER does with an empty
	// result, which is why TestRefreshEventDetailsKeepsCacheOnJunkFeed exists. Do
	// not "fix" a cache wipe by making this return them as active.
	t.Run("entries missing an id or a link are not active, and the caller must not read that as delete everything", func(t *testing.T) {
		entries := feed(
			feedEntry{EventID: "", Link: "https://leekduck.com/events/x/"},
			feedEntry{EventID: "nolink", Link: ""},
		)
		active, jobs := planDetailRefresh(entries, nil, now, denver)
		if len(active) != 0 {
			t.Errorf("active = %v, want empty", active)
		}
		if len(jobs) != 0 {
			t.Errorf("jobs = %d, want 0", len(jobs))
		}
	})

	// A floating end is a wall clock, so the event is still running somewhere
	// until that reading arrives in the last zone on Earth. Reading it in the
	// server's own zone deletes a live event's page: at 21:00Z a UTC host would
	// call this ended while a Denver trainer has six hours of it left.
	t.Run("a floating end that has passed in UTC but not anywhere on earth stays active", func(t *testing.T) {
		// now is 2026-08-06T00:00:00Z. A floating end of 21:00 on the 5th is in
		// the past read as UTC, but 21:00 has not yet arrived at UTC-12.
		entries := feed(feedEntry{EventID: "cday", Link: "https://leekduck.com/events/cday/", End: strp("2026-08-05T21:00:00.000")})

		if active, _ := planDetailRefresh(entries, nil, now, time.UTC); active["cday"] {
			t.Log("read as UTC this event looks finished, which is the bug")
		}
		active, _ := planDetailRefresh(entries, nil, now, anywhereOnEarth)
		if !active["cday"] {
			t.Error("an event still running in western zones was dropped, so its scraped page would be deleted mid event")
		}
	})

	t.Run("a floating end long past is still dropped anywhere on earth", func(t *testing.T) {
		entries := feed(feedEntry{EventID: "ancient", Link: "https://leekduck.com/events/ancient/", End: strp("2026-08-01T10:00:00.000")})
		active, _ := planDetailRefresh(entries, nil, now, anywhereOnEarth)
		if active["ancient"] {
			t.Error("an event finished days ago stayed active, so nothing would ever be evicted")
		}
	})

	t.Run("a duplicated id produces one job, so the fresh count cannot go negative", func(t *testing.T) {
		entries := feed(
			feedEntry{EventID: "dupe", Link: "https://leekduck.com/events/dupe/"},
			feedEntry{EventID: "dupe", Link: "https://leekduck.com/events/dupe/"},
		)
		active, jobs := planDetailRefresh(entries, nil, now, denver)
		if len(jobs) > len(active) {
			t.Errorf("jobs %d exceeds active %d, which makes the cached-fresh log line negative", len(jobs), len(active))
		}
	})
}

// The test the review said was missing: the eviction loop is the only code in
// this feature that destroys data, and nothing exercised it.
//
// arrayLen stops a literally empty feed, but an array of junk clears that bar and
// used to empty the cache all the same. These are the realistic shapes: upstream
// renaming a field, a half built scrape emitting placeholders, or nulls.
func TestRefreshEventDetailsKeepsCacheOnJunkFeed(t *testing.T) {
	junkFeeds := map[string]string{
		"array of empty objects": `[{},{},{}]`,
		"array of nulls":         `[null,null]`,
		"eventID renamed":        `[{"id":"a","link":"https://leekduck.com/events/a/"}]`,
		"link renamed":           `[{"eventID":"a","url":"https://leekduck.com/events/a/"}]`,
		"link empty":             `[{"eventID":"a","link":""}]`,
		"unrelated shape":        `[{"foo":1},{"bar":2}]`,
	}
	for name, raw := range junkFeeds {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := &Store{
				cacheDir: dir,
				eventDetails: map[string]eventDetail{
					"raidhour20260805": {HTML: "<p>scraped</p>", FetchedAt: time.Now()},
					"cday-2026-08":     {HTML: "<p>scraped</p>", FetchedAt: time.Now()},
				},
			}
			s.refreshEventDetails(json.RawMessage(raw))

			if got := len(s.eventDetails); got != 2 {
				t.Errorf("%s wiped the detail cache: %d of 2 pages survived", name, got)
			}
			// The on disk copy must not have been rewritten as {} either.
			if b, err := os.ReadFile(filepath.Join(dir, "event_details.json")); err == nil {
				if strings.TrimSpace(string(b)) == "{}" {
					t.Errorf("%s overwrote event_details.json with an empty object", name)
				}
			}
		})
	}
}

// The other half of the contract: a feed that genuinely no longer lists an event
// SHOULD evict it, or the cache grows without bound.
func TestRefreshEventDetailsEvictsEventsThatLeftTheFeed(t *testing.T) {
	dir := t.TempDir()
	s := &Store{
		cacheDir: dir,
		eventDetails: map[string]eventDetail{
			"still-listed": {HTML: "<p>a</p>", FetchedAt: time.Now()},
			"gone":         {HTML: "<p>b</p>", FetchedAt: time.Now()},
		},
	}
	// One usable entry, so the feed is credible and eviction is allowed to run.
	s.refreshEventDetails(json.RawMessage(`[{"eventID":"still-listed","link":"https://leekduck.com/events/still-listed/"}]`))

	if _, ok := s.eventDetails["still-listed"]; !ok {
		t.Error("an event still in the feed lost its page")
	}
	if _, ok := s.eventDetails["gone"]; ok {
		t.Error("an event no longer in the feed kept its page, so the cache would grow without bound")
	}
}

// arrayLen is what stands between a bad upstream reply and losing every scraped
// event page. groupedCount cannot do this job: it reads a map, so it silently
// reports zero for a feed that is an array.
func TestArrayLen(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{"populated array", `[{"eventID":"a"},{"eventID":"b"}]`, 2, false},
		{"empty array", `[]`, 0, false},
		{"object is not an array", `{"events":[]}`, 0, true},
		{"null", `null`, 0, false},
		{"a github error page", `{"message":"Not Found"}`, 0, true},
		{"truncated", `[{"eventID":"a"}`, 0, true},
		{"a bare string", `"nope"`, 0, true},
	}
	for _, tc := range cases {
		got, err := arrayLen(json.RawMessage(tc.in))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("%s: arrayLen = %d, want %d", tc.name, got, tc.want)
		}
	}

	// The guard in fetchEvents refuses both the error cases and the zero count.
	// null unmarshals into a nil slice without error, so length is what catches
	// it, not the error. Worth pinning: it is the difference between refusing a
	// bad payload and writing it over the cache.
	if n, err := arrayLen(json.RawMessage(`null`)); err != nil || n != 0 {
		t.Errorf("null should parse to a zero length array, got n=%d err=%v", n, err)
	}
}
