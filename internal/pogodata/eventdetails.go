package pogodata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
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

// detailSchemaVersion identifies the cleanup rules that produced a cached page.
// The disk cache stores whole rendered HTML, so a change to cleanEventContent or
// to detailPolicy does not reach a trainer until that page happens to age past
// detailRefreshAge. Bumping this constant marks every cached page stale at load
// time instead, so the next refresh pass re-scrapes them under the new rules.
//
// Only two file shapes have ever reached a disk. The one that was in service
// before this constant existed is a bare map of event id to eventDetail with no
// version anywhere in it, which loads as 0 and is therefore always stale.
// Everything written since is an envelope stamped with the value below. There is
// no cache in the field carrying any other number, so the numbering is internal
// bookkeeping rather than a history to migrate through, and a bump costs one
// polite re-scrape of the feed and nothing else.
//
// What the current rules do, over and above the original scrape: strip
// div.header-page (a duplicate title, tag and rules, since the modal renders its
// own header), the .battle-pass-compact chip track (a second copy of the reward
// list whose 186 unsized icons were the worst image offender on the site), the
// dead .bp-seg toggle and the .video-container box; leave a plain link behind
// where an embedded video was, so the trailer is still reachable; drop the
// paragraphs holding nothing but line breaks, which upstream uses to pad the ad
// slots we already remove (23 of 25 sampled pages carried two of them, worth
// roughly three blank lines each, sitting between the description and the first
// section header); and drop the wrappers upstream leaves behind empty.
//
// TestExtractEventBodyPinsTheSchemaVersion ties this number to the output of
// those rules, so the two cannot drift apart unnoticed.
const detailSchemaVersion = 5

// eventDetailsFile is the on disk shape of the scraped page cache.
//
// The cache used to be a bare map of event id to eventDetail, and files in that
// shape are still in service. Decoding one into this struct reports no error but
// fills in nothing: every top level key is an event id, which is an unknown field
// here, so Details comes back nil and SchemaVersion stays 0. That is why
// loadEventDetailsFromDisk retries the bare map shape whenever Details is nil.
// The pages then survive the restart and are merely marked stale, because 0 never
// matches detailSchemaVersion, so the next refresh pass re-scrapes them under the
// current cleanup rules.
type eventDetailsFile struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Details       map[string]eventDetail `json:"details"`
}

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
		body, err := s.scrapeEventPage(j.link)
		if err != nil {
			log.Printf("pogodata: event details: %s: %v", j.id, err)
			continue
		}
		s.mu.Lock()
		s.eventDetails[j.id] = eventDetail{HTML: body, FetchedAt: time.Now()}
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
	data, err := json.Marshal(eventDetailsFile{SchemaVersion: detailSchemaVersion, Details: s.eventDetails})
	s.mu.Unlock()
	if err == nil {
		os.WriteFile(filepath.Join(s.cacheDir, "event_details.json"), data, 0644)
	}
	if len(jobs) > 0 || fetched > 0 {
		log.Printf("pogodata: event details: fetched %d of %d pages (%d cached fresh)", fetched, len(jobs), len(active)-len(jobs))
	}
}

// detailStripSelector lists the upstream chrome that must not reach the modal.
//
// div.event-toc is a page local table of contents, and #event-time-date-box is
// the upstream clock (we render event times ourselves, in the trainer's zone).
//
// div.header-page repeats the page title and the event type tag and closes with
// two rules. The modal already prints both above the scraped body, as
// .event-modal-name and .event-badge, so keeping it means every event opens with
// its own name twice.
//
// .battle-pass-compact is the second, chip shaped copy of a GO Pass reward list.
// Upstream ships both it and the rank by rank .battle-pass-rewards table and
// picks between them with a pure CSS radio hack: a pair of hidden
// input.bp-view-radio siblings, one checked, and sibling selectors that hide the
// copy the trainer did not pick. bluemonday's UGC policy allows no input element
// at all, so both radios are dropped and both copies render for us. The table is
// the readable one, so the chip track goes; that also takes 186 unsized chip
// icons with it. .bp-seg is the label strip for those radios and, with the inputs
// gone, renders as the bare text "DetailedCompact". Its parent
// .bp-header-controls stays, because the .bp-points-pill beside it carries the
// points per rank figure.
//
// .video-container wraps an embed. bluemonday drops the iframe, so the wrapper
// arrives with nothing to show; the aspect ratio rule that would have held it
// open lives in an inline style block we strip too. cleanEventContent swaps the
// box for a link to the video before this strip runs, so the box that reaches the
// selector is one we found nothing playable in and removing it is simply less
// noise in the body.
//
// The lego-store-map entries are the same defect as .bp-seg, one page further
// on. bluemonday unwraps select, option, button and label and keeps their text,
// so the store finder arrives as a run together country list
// ("All countries (213)Australia (22)France (26)..."), a Search button reduced
// to the bare word, and a frame promising a map that can never load because the
// script driving it is stripped. Only the two dead containers go: the section
// heading, the description and the .lego-store-map-2026__fallback link to the
// official map are real content and stay.
const detailStripSelector = "div.event-toc, #event-time-date-box, .ad-slot-group, .display-ad, " +
	"div.header-page, .battle-pass-compact, .bp-seg, .video-container, " +
	"div.lego-store-map-2026__controls, div.lego-store-map-2026__frame, " +
	"script, style, ins, iframe, noscript"

// detailEmptyWrapperSelector lists the containers that mean nothing at all once
// they have nothing in them. Both shapes are in the live corpus: an empty
// div.bonus-features-wrapper closes both Raid Hour bodies, and
// season-23-forever-forward carries an empty ul.pkmn-list-flex directly under a
// "Max Pokemon Debuts" heading, which reads as content that failed to load rather
// than as no content. Some of them only turn empty here, once the strip above has
// taken an ad slot or a chip track out of them.
//
// This is an allowlist on purpose. A rule shaped as "remove empty divs" would
// also take the upstream elements that are empty by design and carry their whole
// meaning in CSS: div.divider is drawn as a horizontal rule between sections, and
// .step-background and .bubble1 through .bubble4 are decoration behind a research
// step. The rule is therefore "remove these, and only when they are empty".
const detailEmptyWrapperSelector = "ul, ol, div.bonus-features-wrapper"

// detailEmbeddedContentSelector matches the elements that are worth keeping a
// wrapper open for even though they contribute no text.
const detailEmbeddedContentSelector = "img, picture, source, video, audio, canvas, svg, iframe, input, hr"

// cleanEventContent rewrites one scraped .page-content block in place: it trims
// the tail, turns an embedded video into a link, strips upstream chrome, drops
// the spacers and empty wrappers that leaves behind, and makes every link and
// image absolute.
//
// The passes are ordered, and each comment below says what it depends on. The
// short version is that removals run outside in, because a spacer or a wrapper
// only looks contentless once whatever it was padding has already gone.
//
// It is deliberately pure DOM work with no network and no store, so the rules
// can be tested against fixture markup.
func cleanEventContent(sel *goquery.Selection) {
	// Content ends at the author box; drop it and anything after it.
	authorBox := sel.Find("section.author-box").First()
	if authorBox.Length() > 0 {
		authorBox.NextAll().Remove()
		authorBox.Remove()
	}

	base := &url.URL{Scheme: "https", Host: "leekduck.com"}

	// An embedded video never survives sanitizing, because bluemonday's UGC policy
	// allows no iframe, so a .video-container reaches the strip below as an empty
	// box and disappears. On 10th-anniversary-celebration that leaves a sentence
	// promising a thank you message for ten years of Pokemon GO with nothing at all
	// to watch. Leaving a link behind is the whole fix: an anchor to a third party
	// host is ordinary content, and it is written before the absolutize pass at the
	// bottom of this function so it picks up the same target and rel every other
	// link gets.
	//
	// This deliberately does NOT relax detailPolicy or the origin allowlist in
	// scrapeEventPage. The embed still never reaches a trainer's DOM, and the href
	// is inert until somebody clicks it.
	sel.Find("div.video-container").Each(func(_ int, box *goquery.Selection) {
		src, ok := box.Find("iframe[src]").First().Attr("src")
		if !ok {
			return
		}
		// A real embed always carries an absolute URL. Resolving against the base
		// first would hide the shapes that carry no destination at all: "" is the
		// lazy embed placeholder (the URL lives in a data attribute), and "", "#",
		// "?" and "./" all resolve to the LeekDuck home page, which would become a
		// "Watch the video" link pointing at nothing in particular. So the raw
		// value is what has to be absolute, not the resolved one.
		raw, err := url.Parse(strings.TrimSpace(src))
		if err != nil || !raw.IsAbs() {
			return
		}
		abs, err := base.Parse(raw.String())
		// Anything we cannot turn into an https URL is left for the strip below:
		// half a link is worse than the empty box it replaced, because the
		// sanitizer would drop the unusable href and leave bare text behind.
		if err != nil || abs.Scheme != "https" || abs.Host == "" {
			return
		}
		box.ReplaceWithHtml(`<p class="event-video-link"><a href="` + html.EscapeString(abs.String()) + `">Watch the video</a></p>`)
	})

	sel.Find(detailStripSelector).Remove()

	// A GO Pass container upstream has published but not yet filled in: an empty
	// pass name, an empty points value, a lone BASIC header and no rank rows at
	// all, which is go-pass-september-2026 today. Our own styling then draws a
	// gold pill around the words "Points / rank" with no number after them. The
	// empty wrapper pass below cannot catch it, because those labels make the
	// container's text non empty, so it is matched on the rows that matter.
	sel.Find("div.battle-pass-container").Each(func(_ int, box *goquery.Selection) {
		if box.Find(".rank-item").Length() == 0 {
			box.Remove()
		}
	})

	// Upstream pads its ad slots with paragraphs that hold nothing but line
	// breaks, typically <p><br/><br/></p> followed by <p><br/></p>. The ads
	// themselves went out with the strip above and these spacers stay behind, each
	// one roughly three blank lines tall once .event-detail-html p adds its
	// margins, right where the description meets the first section header. CSS
	// cannot reach them: p:empty does not match a paragraph that has a br child.
	//
	// This has to run after the strip, not before it. A spacer is only recognisable
	// as one once the ad it was padding is gone: <p><br/><ins/><br/></p> holds an
	// element that is not a br, so the rule below leaves it alone, and it is the
	// strip that reduces it to a pair of line breaks.
	//
	// Only a paragraph whose entire content is line breaks and whitespace goes. A
	// paragraph carrying any text, or any other element such as a lone image, is
	// left alone, and so is one carrying an id: these pages link to their own
	// sections (#appearing-in-5-star-shadow-raids and #prism-promenade are both
	// live upstream), and removing a fragment target breaks the link that points at
	// it.
	sel.Find("p").Each(func(_ int, p *goquery.Selection) {
		if _, isTarget := p.Attr("id"); isTarget {
			return
		}
		if strings.TrimSpace(p.Text()) != "" {
			return
		}
		kids := p.Children()
		if kids.Length() != kids.Filter("br").Length() {
			return
		}
		p.Remove()
	})

	// Last of the removals, because the two passes above are what empty some of
	// these: a list whose only child was an ad slot, or a wrapper holding a single
	// spacer paragraph, is only contentless by the time we get here.
	sel.Find(detailEmptyWrapperSelector).Each(func(_ int, w *goquery.Selection) {
		if strings.TrimSpace(w.Text()) != "" {
			return
		}
		if w.Find(detailEmbeddedContentSelector).Length() > 0 {
			return
		}
		w.Remove()
	})

	sel.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		if href, ok := a.Attr("href"); ok {
			if abs, err := base.Parse(href); err == nil {
				a.SetAttr("href", abs.String())
			}
		}
		a.SetAttr("target", "_blank")
		a.SetAttr("rel", "noopener nofollow")
	})
	sel.Find("img[src]").Each(func(_ int, img *goquery.Selection) {
		if src, ok := img.Attr("src"); ok {
			if abs, err := base.Parse(src); err == nil {
				img.SetAttr("src", abs.String())
			}
		}
		img.SetAttr("loading", "lazy")
	})
}

// allowedDetailHost reports whether a URL is one we will fetch an event page
// from. HasSuffix alone was not a domain check: "evilleekduck.com" ends in
// "leekduck.com" and sailed straight through it. The host must either BE the
// domain or be a subdomain of it, which means matching on the dot.
func allowedDetailHost(u *url.URL) bool {
	if u == nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "leekduck.com" || strings.HasSuffix(host, ".leekduck.com")
}

// detailMaxBody caps what we will read from an event page. The largest real
// body in the live corpus is a little over 100 KB, so this is roughly a
// twentyfold margin. It matters because only the User Agent is set, which leaves
// the transport free to add Accept-Encoding: gzip and decompress transparently:
// a small compressed response can expand by several hundred times, and what
// comes out is parsed into a DOM, held in memory, written to the disk cache and
// re-encoded on every detail request.
const detailMaxBody = 2 << 20

// detailClient fetches event pages. It is deliberately separate from the store's
// shared client, which also fetches GitHub raw and needs ordinary redirects: the
// origin check above guards the URL we ask for, but with the default policy Go
// will follow up to ten hops without re-checking, so a compromised upstream or
// an open redirect could walk the scraper onto loopback, a private range or a
// cloud metadata endpoint and we would sanitize the result and serve it as event
// content. Re-applying the check on every hop closes that.
var detailClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if !allowedDetailHost(req.URL) {
			return fmt.Errorf("refusing redirect to non-leekduck host %q", req.URL.Hostname())
		}
		return nil
	},
}

// scrapeEventPage fetches one LeekDuck event page and returns the sanitized
// HTML of its main content block.
func (s *Store) scrapeEventPage(pageURL string) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil || !allowedDetailHost(u) {
		return "", fmt.Errorf("refusing non-leekduck link %q", pageURL)
	}

	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", detailUserAgent)
	resp, err := detailClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return extractEventBody(io.LimitReader(resp.Body, detailMaxBody))
}

// extractEventBody turns one fetched LeekDuck page into the body the modal shows:
// it picks the main content block, runs cleanEventContent over it, and sanitizes
// the result.
//
// It takes a reader rather than a URL on purpose. scrapeEventPage keeps the
// origin allowlist and the HTTP call, which cannot be exercised from a test
// without either a network or a weaker allowlist, while everything downstream of
// the response body lives here where real markup can be fed through the whole
// chain: strip, absolutize, sanitize.
func extractEventBody(r io.Reader) (string, error) {
	doc, err := goquery.NewDocumentFromReader(r)
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

	cleanEventContent(content)

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
//
// It leaves s.eventDetails usable on every path out, including the early ones
// that give up on an unreadable or unparseable file. New() allocates the map
// already, so this is a second lock on the same door rather than the only thing
// holding it, but it makes the guarantee local to the function that has all the
// ways of failing in it: refreshEventDetails assigns into this map on its first
// successful scrape, and assigning into a nil map panics the process.
func (s *Store) loadEventDetailsFromDisk() {
	if s.eventDetails == nil {
		s.eventDetails = make(map[string]eventDetail)
	}
	data, err := os.ReadFile(filepath.Join(s.cacheDir, "event_details.json"))
	if err != nil {
		return
	}
	// We never write a byte order mark, but a hand edit through a PowerShell
	// redirect leaves one, and encoding/json refuses the file on its first byte.
	// This is the same trap that has taken the locale JSON down before.
	data = bytes.TrimPrefix(data, []byte("\ufeff"))

	var file eventDetailsFile
	if err := json.Unmarshal(data, &file); err != nil {
		log.Printf("pogodata: event_details cache parse: %v", err)
		return
	}
	m := file.Details
	if m == nil {
		// A pre versioning cache is a bare map of event id to eventDetail. It
		// decodes into the envelope without complaint, because every top level key
		// looks like an unknown field, and leaves Details nil. Reading it again in
		// its own shape is what actually rescues those pages. A versioned envelope
		// cannot be swallowed by this second pass: its "schemaVersion" number
		// refuses to unmarshal into an eventDetail, so the attempt errors out and
		// the guard below still applies.
		var legacy map[string]eventDetail
		if err := json.Unmarshal(data, &legacy); err != nil {
			// Falling through in silence made a corrupted cache indistinguishable
			// from a first boot: both go on to log "loaded 0 from disk cache" and
			// nothing else. Say so instead. The failure is still not fatal, because
			// a scrape pass rebuilds the cache from upstream either way.
			log.Printf("pogodata: event_details cache parse: %v", err)
		} else {
			m = legacy
		}
	}
	if m == nil {
		// Still nil for a file that is literally null, for an envelope with a null
		// details key, and for the legacy retry above failing. The assignment at the
		// bottom of this function would otherwise put a nil map back over the one
		// allocated at the top.
		m = make(map[string]eventDetail)
	}
	// A cache written before this constant existed, or under older cleanup rules,
	// decodes with the wrong version. Keep the pages so the modal still has
	// something to show, but blank the timestamps so planDetailRefresh treats
	// every one of them as stale and queues a re-scrape on the next pass.
	//
	// Dropping them instead would cost roughly detailFetchDelay times the length of
	// the feed, about 90 seconds on a 57 event feed, in which every modal 404s and
	// falls back to the raw feed fields. The client caches that miss, so a trainer
	// who opens a modal inside that window keeps the fallback until they reload.
	// And if LeekDuck happens to be unreachable there is then nothing left to serve
	// at all, which is the same failure the feed's empty array guard exists to
	// prevent.
	//
	// len(m) is checked because there is nothing to say about the staleness of an
	// empty cache: a null file, an empty object and a version 0 file with no pages
	// in it all used to announce that they were keeping 0 pages.
	if file.SchemaVersion != detailSchemaVersion && len(m) > 0 {
		for id, d := range m {
			d.FetchedAt = time.Time{}
			m[id] = d
		}
		log.Printf("pogodata: event_details: cache schema %d, want %d, keeping %d pages but marking them stale", file.SchemaVersion, detailSchemaVersion, len(m))
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
