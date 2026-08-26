package pogodata

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
			// The on disk copy must not have been emptied either. Three shapes
			// count: the file used to be a bare map, and is now an envelope of
			// {"schemaVersion":N,"details":{...}}, so a wipe writes an empty
			// detail set inside that envelope and never equals "{}". Checking
			// only for "{}" quietly stopped catching anything the day the
			// envelope landed, which is exactly how a safety net dies.
			//
			// The schema version is deliberately not spelled out here. The
			// comment that did spell it out said 2, and went on saying 2 after
			// the bump to 3, so the number is left to the matcher, which does
			// not care what it is.
			//
			// The null shape is not hypothetical either: encoding/json writes a
			// nil map as null, so if s.eventDetails is ever nil rather than
			// empty the file reads {"schemaVersion":N,"details":null}. That is
			// an equally total wipe, and it used to slip straight through.
			if b, err := os.ReadFile(filepath.Join(dir, "event_details.json")); err == nil {
				flat := strings.Join(strings.Fields(string(b)), "")
				if flat == "{}" || strings.Contains(flat, `"details":{}`) || strings.Contains(flat, `"details":null`) {
					t.Errorf("%s overwrote event_details.json with an empty detail set: %s", name, b)
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

// The ScrapedDuck feed ships event titles already HTML escaped, e.g.
// "PokémonXP &amp; 2026 Worlds". The client writes the title with textContent,
// so an entity that survives ingest renders as five literal characters on the
// card, in the modal, in the .ics SUMMARY, in the Google Calendar link and in
// every img alt. normalizeEventsFeed is the one place that decodes them.
func TestNormalizeEventsFeedDecodesTitleFields(t *testing.T) {
	in := `[{"eventID":"pokemon-xp-2026-worlds","name":"PokémonXP &amp; 2026 Worlds","eventType":"event","heading":"Event &amp; More","link":"https://leekduck.com/events/pokemon-xp-2026-worlds/","image":"https://example.test/a.jpg","start":"2026-08-05T10:00:00.000","end":"2026-08-09T20:00:00.000"},` +
		`{"eventID":"trainers-choice","name":"Trainer&#39;s Choice","heading":"Community Day","link":"https://leekduck.com/events/trainers-choice/"}` +
		// The third entry is what makes the helper's narrowness testable rather
		// than merely stated. Widening the key list to include eventID and link
		// used to pass this whole suite, because no fixture carried an entity in
		// either field, so "identities and URLs are never rewritten" was
		// asserted by nothing but the comment above the helper.
		`,{"eventID":"a&amp;b","name":"Ampersand &amp; Cup","link":"https://x/?a=1&amp;b=2"}]`

	out := normalizeEventsFeed(json.RawMessage(in))

	var got []map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("normalized payload does not parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	if got[0]["name"] != "PokémonXP & 2026 Worlds" {
		t.Errorf("name = %q, want %q", got[0]["name"], "PokémonXP & 2026 Worlds")
	}
	if got[0]["heading"] != "Event & More" {
		t.Errorf("heading = %q, want %q", got[0]["heading"], "Event & More")
	}
	if got[1]["name"] != "Trainer's Choice" {
		t.Errorf("name = %q, want %q", got[1]["name"], "Trainer's Choice")
	}
	if got[1]["heading"] != "Community Day" {
		t.Errorf("an entity free heading was rewritten: %q", got[1]["heading"])
	}
	if got[2]["name"] != "Ampersand & Cup" {
		t.Errorf("name = %q, want %q", got[2]["name"], "Ampersand & Cup")
	}
	// These two must come back byte identical, entity and all. eventID keys the
	// detail cache, so decoding it orphans a scraped page; link is what the card
	// and the scraper both follow, and the feed does not entity escape URLs in
	// the first place, so a decode there could only ever do harm.
	if got[2]["eventID"] != "a&amp;b" {
		t.Errorf("eventID = %q, want it untouched as %q", got[2]["eventID"], "a&amp;b")
	}
	if got[2]["link"] != "https://x/?a=1&amp;b=2" {
		t.Errorf("link = %q, want it untouched as %q", got[2]["link"], "https://x/?a=1&amp;b=2")
	}

	// Everything that is not display text must survive untouched. eventID is an
	// identity: the detail cache is keyed on it, so rewriting it would orphan
	// every scraped page.
	for key, want := range map[string]string{
		"eventID":   "pokemon-xp-2026-worlds",
		"eventType": "event",
		"link":      "https://leekduck.com/events/pokemon-xp-2026-worlds/",
		"image":     "https://example.test/a.jpg",
		"start":     "2026-08-05T10:00:00.000",
		"end":       "2026-08-09T20:00:00.000",
	} {
		if got[0][key] != want {
			t.Errorf("%s = %v, want %q", key, got[0][key], want)
		}
	}
}

// The helper decodes exactly once. Unescaping twice would turn "&amp;lt;" into
// a real "<", which is how an already escaped payload becomes an injection
// vector the day someone renders a title as HTML instead of as text.
func TestNormalizeEventsFeedUnescapesOnlyOnce(t *testing.T) {
	in := `[{"eventID":"a","name":"&amp;lt;b&amp;gt; and &amp;amp;"}]`
	var got []map[string]any
	if err := json.Unmarshal(normalizeEventsFeed(json.RawMessage(in)), &got); err != nil {
		t.Fatalf("normalized payload does not parse: %v", err)
	}
	if got[0]["name"] != "&lt;b&gt; and &amp;" {
		t.Errorf("name = %q, want %q (a second pass would produce %q)",
			got[0]["name"], "&lt;b&gt; and &amp;", "<b> and &")
	}
}

// html.UnescapeString is not idempotent, so normalizeEventsFeed cannot be
// either, and that is the whole reason it may be called from exactly one place.
// This spells the divergence out: run it twice and the site serves a different
// string, up to and including turning an escaped payload into real markup.
//
// A pipeline that decodes twice is not a hypothetical. fetchEvents used to
// decode and hand the decoded bytes on, refreshEvents wrote those to
// cache/events.json, and loadFromCache decoded the same titles again on the
// next boot, so a title changed depending on whether the process had last
// fetched or last restarted.
func TestNormalizeEventsFeedIsNotIdempotent(t *testing.T) {
	cases := []struct {
		in, once, twice string
	}{
		{"Registeel &amp;reg; Raid Day", "Registeel &reg; Raid Day", "Registeel ® Raid Day"},
		{"&amp;lt;img src=x onerror=alert(1)&amp;gt;", "&lt;img src=x onerror=alert(1)&gt;", "<img src=x onerror=alert(1)>"},
		{"&#38;amp; numeric", "&amp; numeric", "& numeric"},
		{"&amp; and &amp;lt;b&amp;gt;", "& and &lt;b&gt;", "& and <b>"},
	}
	for _, tc := range cases {
		entry, err := json.Marshal([]map[string]string{{"eventID": "a", "name": tc.in}})
		if err != nil {
			t.Fatalf("could not build the payload: %v", err)
		}
		first := normalizeEventsFeed(json.RawMessage(entry))
		second := normalizeEventsFeed(first)

		var gotFirst, gotSecond []map[string]any
		if err := json.Unmarshal(first, &gotFirst); err != nil {
			t.Fatalf("one pass does not parse: %v", err)
		}
		if err := json.Unmarshal(second, &gotSecond); err != nil {
			t.Fatalf("two passes do not parse: %v", err)
		}
		if gotFirst[0]["name"] != tc.once {
			t.Errorf("one pass on %q = %q, want %q", tc.in, gotFirst[0]["name"], tc.once)
		}
		if gotSecond[0]["name"] != tc.twice {
			t.Errorf("two passes on %q = %q, want %q", tc.in, gotSecond[0]["name"], tc.twice)
		}
		if gotSecond[0]["name"] == gotFirst[0]["name"] {
			t.Errorf("%q survived a second pass unchanged, so this test no longer proves anything", tc.in)
		}
	}
}

// The real rule: only a genuine, properly terminated entity reference is
// decoded, and it is decoded exactly once. Anything else comes back exactly as
// the feed sent it, bare ampersands included.
//
// The comment that used to sit here claimed html.UnescapeString does that on its
// own. It does not. It implements the HTML5 spec, and the spec still decodes the
// legacy named set WITHOUT the closing semicolon, so a perfectly ordinary title
// came out as mojibake. The three original fixtures below passed on luck alone:
// every bare & in them happens to be followed by a space or by letters spelling
// no legacy entity.
//
// It took two guards to make the rule true, so the fixtures come in two groups.
// strictEntity handles the group missing its semicolon. The group that carries a
// semicolon needs the rune count check as well, because html.UnescapeString
// applies the very same legacy prefix rule inside the token the regexp handed
// it, which is how "&notarealentity;" used to come back as "¬arealentity;". And
// "&semi;" sits at the end as the deliberate counter example: it really does
// decode to a semicolon, so any guard that simply rejected a decoded value
// ending in ";" would break it.
func TestNormalizeEventsFeedLeavesBareAmpersandsAlone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Fire & Ice", "Fire & Ice"},
		{"AT&T", "AT&T"},
		{"Rock & Roll & More", "Rock & Roll & More"},
		// Each of these opens a legacy named entity that is missing its closing
		// semicolon, and html.UnescapeString alone mangles all four, into
		// "Raid Day ¬ify me", "Tour: Unova ©right", "Fire & Ice" and
		// "Season of ®ion" respectively.
		{"Raid Day &notify me", "Raid Day &notify me"},
		{"Tour: Unova &copyright", "Tour: Unova &copyright"},
		{"Fire &amp Ice", "Fire &amp Ice"},
		{"Season of &region", "Season of &region"},
		// These four DO carry a closing semicolon, so strictEntity matches them
		// and hands them straight to html.UnescapeString, which then decodes a
		// legacy prefix out of each one anyway: "¬arealentity;", "GO Fest
		// ×2026;", "&amp; B" and "Tour: Unova ©right;". None of them names a
		// real entity, so none of them may be touched. Only the rune count
		// check stops these, which is the whole reason they are here.
		{"&notarealentity;", "&notarealentity;"},
		{"GO Fest &times2026;", "GO Fest &times2026;"},
		{"A &ampamp; B", "A &ampamp; B"},
		{"Tour: Unova &copyright;", "Tour: Unova &copyright;"},
		// The counter examples, so "strict" is not misread as "never decodes a
		// short name". Both of these ARE decoded, because both are genuine and
		// properly terminated, and the second is the one that rules out testing
		// for a trailing semicolon instead of counting runes.
		{"Wild Area &sect;1", "Wild Area §1"},
		{"Bonus &semi; Bonus", "Bonus ; Bonus"},
	}
	entries := make([]map[string]string, 0, len(cases))
	for i, c := range cases {
		entries = append(entries, map[string]string{"eventID": fmt.Sprintf("e%d", i), "name": c.in})
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("could not build the payload: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(normalizeEventsFeed(json.RawMessage(payload)), &got); err != nil {
		t.Fatalf("normalized payload does not parse: %v", err)
	}
	if len(got) != len(cases) {
		t.Fatalf("got %d entries, want %d", len(got), len(cases))
	}
	for i, c := range cases {
		if got[i]["name"] != c.want {
			t.Errorf("%q normalized to %q, want %q", c.in, got[i]["name"], c.want)
		}
	}
}

// extraData is a free form nested blob the events page reads directly. It is
// carried as raw bytes precisely so it cannot be damaged, most importantly so
// integers do not come back as float64 and re-encode in scientific notation.
func TestNormalizeEventsFeedPreservesNestedExtraData(t *testing.T) {
	in := `[{"eventID":"comm-day","name":"Community Day &amp; Friends","heading":"Community Day",` +
		`"extraData":{"communityday":{"spawns":[{"name":"Chikorita","image":"https://example.test/152.png"}],` +
		`"bonuses":[{"text":"3x Catch XP","template":"3xcatchxp"}],` +
		`"bonusDisclaimers":[],"shinies":[{"name":"Chikorita","image":"https://example.test/152s.png"}],` +
		`"specialresearch":[{"name":"Verdant Wonders","step":1,"tasks":[{"text":"Catch 15 Pokemon","reward":{"name":"Chikorita","image":"https://example.test/152.png","count":1}}],` +
		`"rewards":[{"name":"Stardust","image":"https://example.test/dust.png","count":3000}]}]},` +
		`"generic":{"hasSpawns":true,"hasFieldResearchTasks":false},` +
		`"raidbattles":{"bosses":[{"name":"Mewtwo","image":"https://example.test/150.png","canBeShiny":true}],"shinies":[]}}}]`

	out := normalizeEventsFeed(json.RawMessage(in))

	// Semantically identical: compare the decoded trees, since re-marshalling
	// legitimately reorders object keys.
	var gotEntries, wantEntries []map[string]any
	if err := json.Unmarshal(out, &gotEntries); err != nil {
		t.Fatalf("normalized payload does not parse: %v", err)
	}
	if err := json.Unmarshal([]byte(in), &wantEntries); err != nil {
		t.Fatalf("bad test fixture: %v", err)
	}
	gotExtra, err := json.Marshal(gotEntries[0]["extraData"])
	if err != nil {
		t.Fatalf("re-marshal extraData: %v", err)
	}
	wantExtra, err := json.Marshal(wantEntries[0]["extraData"])
	if err != nil {
		t.Fatalf("re-marshal expected extraData: %v", err)
	}
	if string(gotExtra) != string(wantExtra) {
		t.Errorf("extraData changed\n got: %s\nwant: %s", gotExtra, wantExtra)
	}

	// Those counts are integers in the feed. Routed through map[string]any they
	// would come back as float64, so assert on the emitted bytes, not the tree.
	for _, want := range []string{`"count":3000`, `"count":1`, `"step":1`, `"hasSpawns":true`, `"hasFieldResearchTasks":false`, `"bonusDisclaimers":[]`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("normalized payload lost %s\ngot: %s", want, out)
		}
	}
	if gotEntries[0]["name"] != "Community Day & Friends" {
		t.Errorf("name = %q, want %q", gotEntries[0]["name"], "Community Day & Friends")
	}
}

// A feed with nothing to decode comes back as the very same bytes, not a
// re-encoded copy.
//
// That byte identity is NOT what makes drift reporting honest, whatever this
// comment used to say. CheckScrapers compares the freshly fetched payload
// against the cache file before persistAndApply is ever called, so both sides of
// that comparison are raw regardless of what this helper returns. The fast path
// is kept for two smaller reasons that are actually true: it avoids a pointless
// re-encode on every payload upstream ships without an entity, and a helper that
// hands back its exact input when it has nothing to do is far easier to reason
// about at the single call site allowed to run it.
func TestNormalizeEventsFeedReturnsInputWhenNothingChanges(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"no entities", `[{"eventID":"a","name":"Community Day","heading":"Community Day","start":"2026-08-05T10:00:00.000"}]`},
		{"bare ampersand only", `[{"eventID":"a","name":"Fire & Ice"}]`},
		{"no name or heading at all", `[{"eventID":"a","link":"https://example.test/"}]`},
		{"empty array", `[]`},
	}
	for _, tc := range cases {
		in := json.RawMessage(tc.in)
		out := normalizeEventsFeed(in)
		// The len(in) > 0 guard is not decoration: the sibling bad shapes table
		// carries an "empty" case, and copying it across here would index into a
		// zero length slice and panic instead of failing.
		if len(out) != len(in) || (len(in) > 0 && &out[0] != &in[0]) {
			t.Errorf("%s: payload was re-encoded when nothing changed\n got: %s\nwant the same bytes: %s", tc.name, out, in)
		}
	}
}

// Any shape the helper does not understand degrades to today's behaviour: the
// input is handed back untouched. Losing the feed would be far worse than
// serving an escaped title.
func TestNormalizeEventsFeedReturnsInputUnchangedOnBadShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"an object, not an array", `{}`},
		{"the raids shape", `{"1":[{"name":"Mewtwo"}]}`},
		{"truncated", `[{"eventID":"a","name":"Fire &amp; Ice"`},
		{"malformed", `[{"eventID":,}]`},
		{"a bare string", `"nope"`},
		{"a github error page", `{"message":"Not Found"}`},
		{"null", `null`},
		{"empty", ""},
		{"an array of strings", `["a","b"]`},
	}
	for _, tc := range cases {
		out := normalizeEventsFeed(json.RawMessage(tc.in))
		if string(out) != tc.in {
			t.Errorf("%s: normalizeEventsFeed(%s) = %s, want the input unchanged", tc.name, tc.in, out)
		}
	}
}

// A name that is not a JSON string is skipped rather than treated as a failure,
// so one odd entry cannot cost the whole feed its decoding.
func TestNormalizeEventsFeedSkipsNonStringTitleFields(t *testing.T) {
	// Nothing here is decodable, so the input comes back byte for byte.
	only := json.RawMessage(`[{"eventID":"a","name":42,"heading":null}]`)
	if got := normalizeEventsFeed(only); string(got) != string(only) {
		t.Errorf("got %s, want the input unchanged", got)
	}

	// A numeric name alongside decodable ones: the bad field is left as it
	// arrived and the good ones are still fixed.
	mixed := `[{"eventID":"a","name":42},{"eventID":"b","name":"Fire &amp; Ice"},{"eventID":"c","heading":["Event"],"name":"AT&amp;T"}]`
	var got []map[string]any
	if err := json.Unmarshal(normalizeEventsFeed(json.RawMessage(mixed)), &got); err != nil {
		t.Fatalf("normalized payload does not parse: %v", err)
	}
	if got[0]["name"] != float64(42) {
		t.Errorf("numeric name = %v, want it left alone as 42", got[0]["name"])
	}
	if got[1]["name"] != "Fire & Ice" {
		t.Errorf("name = %q, want %q", got[1]["name"], "Fire & Ice")
	}
	if got[2]["name"] != "AT&T" {
		t.Errorf("name = %q, want %q", got[2]["name"], "AT&T")
	}
	if h, ok := got[2]["heading"].([]any); !ok || len(h) != 1 || h[0] != "Event" {
		t.Errorf("array heading = %v, want it left alone", got[2]["heading"])
	}
}

// The regression test for the double decode, at the level it actually happened:
// the pipeline, not the helper. Fetch, write to disk, restart, load, and the
// titles the site serves must be identical either way.
//
// It also pins what lands in cache/events.json, which must be the upstream
// bytes. Normalizing at fetch time meant the file held a re-encoded payload
// instead: keys reordered alphabetically, and every literal & rewritten to its
// escaped form.
//
// Not that this produced a standing stream of false drift, and overstating it
// would be its own kind of wrong. Under that layout CheckScrapers compared a
// normalized cache against a normalized fetch, so it agreed with itself on every
// run but one: the single transitional run straight after a deploy, when a raw
// cache file met a normalized fetch. Keeping the file raw removes even that, and
// leaves the cache a faithful record of what upstream actually said.
//
// There is no migration to worry about in either direction: normalizeEventsFeed
// is entirely new against HEAD, so the deployed cache/events.json is already
// raw.
func TestEventsPipelineDecodesExactlyOnceAcrossARestart(t *testing.T) {
	// Escaped twice over, so one pass and two passes give visibly different
	// text. Without that this test would pass on a broken pipeline.
	raw := json.RawMessage(`[{"eventID":"regi","name":"Registeel &amp;reg; Raid Day",` +
		`"heading":"&amp;lt;img src=x onerror=alert(1)&amp;gt;",` +
		`"link":"https://leekduck.com/events/regi/","start":"2026-08-05T10:00:00.000"}]`)
	const wantName = "Registeel &reg; Raid Day"
	const wantHeading = "&lt;img src=x onerror=alert(1)&gt;"

	titles := func(t *testing.T, when string, blob json.RawMessage) (string, string) {
		t.Helper()
		var got []map[string]any
		if err := json.Unmarshal(blob, &got); err != nil {
			t.Fatalf("%s: the stored events blob does not parse: %v", when, err)
		}
		if len(got) != 1 {
			t.Fatalf("%s: stored %d entries, want 1", when, len(got))
		}
		name, _ := got[0]["name"].(string)
		heading, _ := got[0]["heading"].(string)
		if name != wantName {
			t.Errorf("%s: name = %q, want %q", when, name, wantName)
		}
		if heading != wantHeading {
			t.Errorf("%s: heading = %q, want %q", when, heading, wantHeading)
		}
		return name, heading
	}

	dir := t.TempDir()

	// The fetch path. fetchEvents hands back the upstream bytes untouched and
	// persistAndApply writes them to disk, then applies them, which is what both
	// refreshEvents and CheckScrapers do.
	fetched := &Store{cacheDir: dir}
	fetched.persistAndApply("events", raw)
	titles(t, "after a fetch", fetched.Events())

	onDisk, err := os.ReadFile(filepath.Join(dir, "events.json"))
	if err != nil {
		t.Fatalf("events.json was never written: %v", err)
	}
	if string(onDisk) != string(raw) {
		t.Errorf("cache/events.json is not the upstream payload, so CheckScrapers would report spurious drift\n got: %s\nwant: %s", onDisk, raw)
	}

	// The boot path. A restart reads that same file back through loadFromCache.
	rebooted := &Store{cacheDir: dir}
	rebooted.loadFromCache()
	titles(t, "after a restart", rebooted.Events())

	if string(rebooted.Events()) != string(fetched.Events()) {
		t.Errorf("a restart changed what the site serves\nafter fetch  : %s\nafter restart: %s", fetched.Events(), rebooted.Events())
	}
}

// The boundary the round 1 bug actually lived on, driven end to end for the
// first time.
//
// Until eventsFeedURL became a var this was untestable, and nothing anywhere in
// the package called refreshEvents or fetchEvents at all. Two mutants measured
// what that gap cost: making fetchEvents normalize a second time, and
// normalizing just before the write inside refreshEvents, each kept the entire
// suite green. The second is the literal round 1 bug, and under it
// "&amp;lt;img src=x onerror=alert(1)&amp;gt;" reaches s.Events() as real markup.
//
// So this pins both halves of the contract in one pass: what the site serves is
// decoded exactly once, and what lands in cache/events.json is the raw upstream
// payload, byte for byte.
func TestRefreshEventsDecodesOnceAndCachesTheRawPayload(t *testing.T) {
	// Escaped twice over, so one pass and two passes are visibly different
	// strings. Without that a double decoding pipeline would satisfy these
	// assertions just as well as a correct one.
	const raw = `[{"eventID":"regi","name":"Registeel &amp;amp; Friends","heading":"&amp;lt;img src=x onerror=alert(1)&amp;gt;"}]`
	const wantName = "Registeel &amp; Friends"
	const wantHeading = "&lt;img src=x onerror=alert(1)&gt;"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, raw)
	}))
	defer srv.Close()

	// eventsFeedURL is package state, so leaking a dead httptest address out of
	// this test would point every later test at a closed listener. Cleanup runs
	// even when an assertion below calls t.Fatalf.
	orig := eventsFeedURL
	t.Cleanup(func() { eventsFeedURL = orig })
	eventsFeedURL = srv.URL

	dir := t.TempDir()
	s := &Store{cacheDir: dir, client: srv.Client(), eventDetails: map[string]eventDetail{}}
	// No entry carries a link, so the detail scrape that refreshEvents kicks off
	// finds nothing usable and returns without touching the network or the disk.
	// That keeps this test hermetic; the eviction guard is covered above.
	if err := s.refreshEvents(); err != nil {
		t.Fatalf("refreshEvents: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal(s.Events(), &got); err != nil {
		t.Fatalf("the served events blob does not parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("served %d entries, want 1", len(got))
	}
	if got[0]["name"] != wantName {
		t.Errorf("name = %q, want %q (a second pass gives %q)", got[0]["name"], wantName, "Registeel & Friends")
	}
	if got[0]["heading"] != wantHeading {
		t.Errorf("heading = %q, want %q (a second pass gives a live img tag)", got[0]["heading"], wantHeading)
	}

	onDisk, err := os.ReadFile(filepath.Join(dir, "events.json"))
	if err != nil {
		t.Fatalf("events.json was never written: %v", err)
	}
	if string(onDisk) != raw {
		t.Errorf("cache/events.json is not the upstream payload\n got: %s\nwant: %s", onDisk, raw)
	}

	// The timestamp moved into its own critical section when the two disk
	// writers were folded into one, so pin that a success still stamps it.
	if age, ok := s.eventsAge(); !ok || age > time.Minute {
		t.Errorf("a successful refresh did not stamp eventsFetchedAt (age=%v known=%v)", age, ok)
	}
}
