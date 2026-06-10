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

// refreshEventDetails scrapes the LeekDuck page of every active event in the
// feed that has no cached detail yet or whose detail is older than
// detailRefreshAge. Failures keep the previous copy. Details for events no
// longer in the feed are evicted. Caller must NOT hold s.mu.
func (s *Store) refreshEventDetails(feed json.RawMessage) {
	if !s.detailsRunning.CompareAndSwap(false, true) {
		return // a previous pass is still going
	}
	defer s.detailsRunning.Store(false)

	var entries []struct {
		EventID string  `json:"eventID"`
		Link    string  `json:"link"`
		End     *string `json:"end"`
	}
	if err := json.Unmarshal(feed, &entries); err != nil {
		log.Printf("pogodata: event details: feed parse: %v", err)
		return
	}

	now := time.Now()
	active := make(map[string]bool, len(entries))
	type job struct{ id, link string }
	var jobs []job

	s.mu.RLock()
	for _, e := range entries {
		if e.EventID == "" || e.Link == "" {
			continue
		}
		// Skip events that already ended; nobody opens their modal.
		if e.End != nil {
			if end, err := time.Parse("2006-01-02T15:04:05.000", *e.End); err == nil && end.Before(now) {
				continue
			}
		}
		active[e.EventID] = true
		if d, ok := s.eventDetails[e.EventID]; ok && now.Sub(d.FetchedAt) < detailRefreshAge {
			continue
		}
		jobs = append(jobs, job{e.EventID, e.Link})
	}
	s.mu.RUnlock()

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
	u, err := url.Parse(pageURL)
	if err != nil || u.Scheme != "https" || !strings.HasSuffix(u.Host, "leekduck.com") {
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
