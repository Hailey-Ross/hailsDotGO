package pogodata

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/microcosm-cc/bluemonday"
)

// eventDetail holds the sanitized HTML body of one LeekDuck event page.
type eventDetail struct {
	HTML      string    `json:"html"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// detailRefreshAge is how old a scraped page may get before it is re-fetched.
// LeekDuck edits pages as Niantic updates details, so a slow cycle is enough.
const detailRefreshAge = 12 * time.Hour

// detailFetchDelay spaces out page requests to stay polite. ScrapedDuck itself
// refetches every page every 10 minutes, so this is far below tolerated load.
const detailFetchDelay = 1500 * time.Millisecond

const detailUserAgent = "hailsDotGO/1.0 (+https://hails.cc)"

// feedTimeUTCLayouts and feedTimeFloatingLayouts are every timestamp shape the
// upstream events feed has been observed to send.
var (
	feedTimeUTCLayouts      = []string{"2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z", time.RFC3339}
	feedTimeFloatingLayouts = []string{"2006-01-02T15:04:05.000", "2006-01-02T15:04:05"}
)

// ParseFeedTime parses an upstream event timestamp.
//
// The feed sends two kinds of time and the difference is load bearing. A value
// ending in "Z" is a real instant: GO Battle League rotations begin at the same
// moment everywhere, and 6 of the 39 events in a typical feed look like this. A
// value with no zone at all is a floating wall clock: Spotlight Hour is 6pm
// wherever the trainer is standing, so the same reading applies in every zone.
//
// floating timestamps are parsed in loc, so a caller comparing against a clock
// in that same zone gets the answer a trainer there would expect. Pass
// time.Local to ask "has this ended for someone here", or time.UTC when only
// the wall clock components matter and the zone is irrelevant.
//
// This lives here, in one place, because the two halves of the app used to
// disagree about the feed's own format: the calendar exporter handled all five
// layouts while the detail scraper knew only one of them and read a floating
// time as UTC.
func ParseFeedTime(raw string, loc *time.Location) (t time.Time, floating bool, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, false
	}
	if strings.HasSuffix(raw, "Z") {
		for _, layout := range feedTimeUTCLayouts {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed, false, true
			}
		}
		return time.Time{}, false, false
	}
	if loc == nil {
		loc = time.UTC
	}
	for _, layout := range feedTimeFloatingLayouts {
		if parsed, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsed, true, true
		}
	}
	return time.Time{}, false, false
}

// detailPolicy sanitizes scraped page HTML before it is stored and later
// injected into the client DOM. class attributes are kept so the site CSS can
// restyle LeekDuck components.
var detailPolicy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("class").Globally()
	p.AllowAttrs("src", "alt", "loading").OnElements("img")
	p.AllowAttrs("target", "rel").OnElements("a")
	p.RequireNoFollowOnLinks(true)
	p.AllowURLSchemes("https")
	return p
}()

// feedEntry is the slice of a feed record the detail refresher needs.
type feedEntry struct {
	EventID string  `json:"eventID"`
	Link    string  `json:"link"`
	End     *string `json:"end"`
}

// detailJob is one page waiting to be scraped.
type detailJob struct{ id, link string }

// anywhereOnEarth is UTC-12, the last zone on the planet to reach any given
// wall clock reading.
//
// A floating end time means "21:00 wherever you are", so the event is still
// running for somebody until 21:00 arrives in the westernmost zone. Asking the
// question in any other zone deletes a live event's page: a UTC server drops a
// Community Day's scraped page at 21:00Z while a Denver trainer still has six
// hours of it left, and the entry is excluded from the refetch queue at the same
// time, so it does not come back while the event is still listed.
var anywhereOnEarth = time.FixedZone("AoE", -12*60*60)

// planDetailRefresh decides, for one feed, which events are still active and
// which of their pages need scraping. It is pure so the rule can be tested
// without a network or a store, which matters more here than anywhere else in
// this file: the returned active set drives cache EVICTION in the caller, so a
// bug that wrongly drops an id destroys that event's stored page.
//
// fetchedAt is a snapshot of when each cached page was last scraped. now and loc
// decide what "already ended" means. Pass anywhereOnEarth: this is a global site
// and the server's own zone is nobody's local time, so the only safe reading of
// a floating wall clock on a delete path is the latest instant it could still
// mean.
//
// An entry whose end time will not parse is deliberately kept active. Failing
// open costs one polite refetch; failing closed would delete a live event's
// page because the upstream changed a format.
func planDetailRefresh(entries []feedEntry, fetchedAt map[string]time.Time, now time.Time, loc *time.Location) (active map[string]bool, jobs []detailJob) {
	active = make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.EventID == "" || e.Link == "" {
			continue
		}
		// Skip events that already ended; nobody opens their modal.
		if e.End != nil {
			if end, _, ok := ParseFeedTime(*e.End, loc); ok && end.Before(now) {
				continue
			}
		}
		if active[e.EventID] {
			continue // duplicate id in the feed; one job is enough
		}
		active[e.EventID] = true
		if at, ok := fetchedAt[e.EventID]; ok && now.Sub(at) < detailRefreshAge {
			continue
		}
		jobs = append(jobs, detailJob{e.EventID, e.Link})
	}
	return active, jobs
}

// refreshEventDetails scrapes the LeekDuck page of every active event in the
// feed that has no cached detail yet or whose detail is older than
// detailRefreshAge. Failures keep the previous copy. Details for events no
// longer in the feed are evicted. Caller must NOT hold s.mu.
func (s *Store) refreshEventDetails(feed json.RawMessage) {
	if !s.detailsRunning.CompareAndSwap(false, true) {
		return // a previous pass is still going
	}
	defer s.detailsRunning.Store(false)

	var entries []feedEntry
	if err := json.Unmarshal(feed, &entries); err != nil {
		log.Printf("pogodata: event details: feed parse: %v", err)
		return
	}

	now := time.Now()
	s.mu.RLock()
	fetchedAt := make(map[string]time.Time, len(s.eventDetails))
	for id, d := range s.eventDetails {
		fetchedAt[id] = d.FetchedAt
	}
	s.mu.RUnlock()

	active, jobs := planDetailRefresh(entries, fetchedAt, now, anywhereOnEarth)

	// "The feed parsed but described no event we can use" is NOT the same claim as
	// "no event is running any more", and only the second one justifies deleting
	// every scraped page below.
	//
	// fetchEvents already refuses an empty array, but an array of junk gets this
	// far: upstream renaming eventID or link, a half built scrape publishing
	// placeholder records, or literally [{}]. Each of those empties active, and
	// without this the eviction loop would take the whole cache with nothing to
	// restore it from. Keeping stale pages costs nothing by comparison; a healthy
	// feed evicts them on the next pass.
	if len(active) == 0 && len(entries) > 0 {
		log.Printf("pogodata: event details: feed had %d entries but none usable, keeping the cached pages", len(entries))
		return
	}

	fetched := 0
	for i, j := range jobs {
		if i > 0 {
			time.Sleep(detailFetchDelay)
		}
		html, err := s.scrapeEventPage(j.link)
		if err != nil {
			log.Printf("pogodata: event details: %s: %v", j.id, err)
			continue
		}
		s.mu.Lock()
		s.eventDetails[j.id] = eventDetail{HTML: html, FetchedAt: time.Now()}
		s.mu.Unlock()
		fetched++
	}

	// Evict details for events that left the feed, then persist.
	s.mu.Lock()
	for id := range s.eventDetails {
		if !active[id] {
			delete(s.eventDetails, id)
		}
	}
	data, err := json.Marshal(s.eventDetails)
	s.mu.Unlock()
	if err == nil {
		os.WriteFile(filepath.Join(s.cacheDir, "event_details.json"), data, 0644)
	}
	if len(jobs) > 0 || fetched > 0 {
		log.Printf("pogodata: event details: fetched %d of %d pages (%d cached fresh)", fetched, len(jobs), len(active)-len(jobs))
	}
}

// scrapeEventPage fetches one LeekDuck event page and returns the sanitized
// HTML of its main content block.
func (s *Store) scrapeEventPage(pageURL string) (string, error) {
	// HasSuffix alone was not a domain check: "evilleekduck.com" ends in
	// "leekduck.com" and sailed straight through it. The host must either BE the
	// domain or be a subdomain of it, which means matching on the dot.
	u, err := url.Parse(pageURL)
	host := ""
	if u != nil {
		host = strings.ToLower(u.Hostname())
	}
	if err != nil || u.Scheme != "https" || (host != "leekduck.com" && !strings.HasSuffix(host, ".leekduck.com")) {
		return "", fmt.Errorf("refusing non-leekduck link %q", pageURL)
	}

	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", detailUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", err
	}

	content := doc.Find("article.event-page div.page-content").First()
	if content.Length() == 0 {
		content = doc.Find("div.page-content").First()
	}
	if content.Length() == 0 {
		return "", fmt.Errorf("no .page-content found")
	}

	// Content ends at the author box; drop it and anything after it.
	authorBox := content.Find("section.author-box").First()
	if authorBox.Length() > 0 {
		authorBox.NextAll().Remove()
		authorBox.Remove()
	}
	// Drop navigation, the date box (we render times ourselves), ads, scripts.
	content.Find("div.event-toc, #event-time-date-box, .ad-slot-group, .display-ad, script, style, ins, iframe, noscript").Remove()

	base := &url.URL{Scheme: "https", Host: "leekduck.com"}
	content.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		if href, ok := a.Attr("href"); ok {
			if abs, err := base.Parse(href); err == nil {
				a.SetAttr("href", abs.String())
			}
		}
		a.SetAttr("target", "_blank")
		a.SetAttr("rel", "noopener nofollow")
	})
	content.Find("img[src]").Each(func(_ int, img *goquery.Selection) {
		if src, ok := img.Attr("src"); ok {
			if abs, err := base.Parse(src); err == nil {
				img.SetAttr("src", abs.String())
			}
		}
		img.SetAttr("loading", "lazy")
	})

	raw, err := content.Html()
	if err != nil {
		return "", err
	}
	sanitized := strings.TrimSpace(detailPolicy.Sanitize(raw))
	if sanitized == "" {
		return "", fmt.Errorf("sanitized content empty")
	}
	return sanitized, nil
}

// loadEventDetailsFromDisk restores the scraped page cache. Caller must hold s.mu.
func (s *Store) loadEventDetailsFromDisk() {
	data, err := os.ReadFile(filepath.Join(s.cacheDir, "event_details.json"))
	if err != nil {
		return
	}
	var m map[string]eventDetail
	if err := json.Unmarshal(data, &m); err != nil {
		log.Printf("pogodata: event_details cache parse: %v", err)
		return
	}
	s.eventDetails = m
	log.Printf("pogodata: event_details: loaded %d from disk cache", len(m))
}

// EventDetail returns the sanitized detail payload for one event, if scraped.
func (s *Store) EventDetail(id string) (json.RawMessage, bool) {
	s.mu.RLock()
	d, ok := s.eventDetails[id]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	out, err := json.Marshal(struct {
		EventID   string    `json:"eventID"`
		HTML      string    `json:"html"`
		FetchedAt time.Time `json:"fetchedAt"`
	}{id, d.HTML, d.FetchedAt})
	if err != nil {
		return nil, false
	}
	return out, true
}
