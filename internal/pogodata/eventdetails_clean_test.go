package pogodata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// cleanFixtureHTML is real markup, trimmed down, from a LeekDuck event page. The
// awkward parts are the ones worth keeping verbatim: the header block that
// duplicates our own modal header, the GO Pass container that ships its reward
// list twice behind radio inputs bluemonday deletes, the line break paragraphs
// upstream leaves around an ad slot (including the one that still has the ad
// between its two breaks), the video box whose iframe never survives sanitizing,
// and the empty wrappers, of which some are litter and some are drawn by CSS.
const cleanFixtureHTML = `<div class="page-content">
<div class="header-page"><h1 class="page-title">Test Event</h1><div class="page-tags"><div class="tag event">Event</div></div><hr/><hr/></div>
<div class="event-description"><p>Body text.</p></div>
<p><br/><br/></p>
<p><br/><ins class="adsbygoogle"></ins><br/></p>
<p><br/></p>
<p id="prism-promenade"><br/></p>
<h2 id="bonuses" class="event-section-header bonuses">Bonuses <img src="/assets/img/events/icons/bonuses.png" loading="lazy"/></h2>
<div class="bonus-list"><div class="bonus-item"><div class="item-circle"><img src="/assets/img/events/bonuses/candy.png" alt="One extra Candy"/></div><div class="bonus-text">One extra Candy</div></div></div>
<div class="battle-pass-container -has-toggle"><input type="radio" name="bp-view-x" class="bp-view-radio bp-radio-detailed" checked="checked"/><div class="battle-pass-header"><div class="pass-info"><h3 class="pass-name">GO Pass: August</h3></div><div class="bp-header-controls"><span class="bp-points-pill"><span class="points-label">Points / rank</span><span class="points-value">100</span></span><div class="bp-seg">DetailedCompact</div></div></div><div class="battle-pass-rewards"><div class="rank-item"><div class="rank-label"><span class="rank-text">RANK</span><div class="rank-number">1</div></div></div></div><div class="battle-pass-compact"><div class="bp-compact-track"><div class="bp-chips"><div class="bp-chip"><span class="bp-chip-icon"><img src="/assets/img/pokemon_icons_crop/pm816.icon.png" alt="Sobble"/></span></div></div></div></div></div>
<div class="video-container"><iframe src="https://youtube.com/embed/x"></iframe></div>
<ul class="pkmn-list-flex"><li class="pkmn-list-item"><div class="pkmn-list-img psychic"><img src="/assets/img/pokemon_icons/pokemon_icon_150_00.png"/></div><div class="pkmn-name">Mewtwo</div></li></ul>
<h2 class="event-section-header">Max Pokemon Debuts</h2>
<ul class="pkmn-list-flex"></ul>
<div class="divider"></div>
<div class="special-research-list"><div class="step-item"><div class="step-background"></div><div class="bubble1"></div><div class="research-icon"><img src="/assets/img/icons/research.png"/></div></div></div>
<div class="bonus-features-wrapper"></div>
<p id="other-link"><a href="/events/other/">Other event</a></p>
<section class="author-box">by someone</section>
<div class="should-be-trimmed">gone</div>
</div>`

// cleanedFixture runs the fixture through cleanEventContent and hands back the
// cleaned selection plus its rendered HTML.
func cleanedFixture(t *testing.T) (*goquery.Selection, string) {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(cleanFixtureHTML))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	sel := doc.Find("div.page-content").First()
	if sel.Length() == 0 {
		t.Fatal("fixture has no div.page-content")
	}
	cleanEventContent(sel)
	html, err := sel.Html()
	if err != nil {
		t.Fatalf("render cleaned fixture: %v", err)
	}
	return sel, html
}

// Every one of these rendered badly in the modal: a duplicate title and tag, a
// second copy of the GO Pass rewards as unsized chips, a dead segmented toggle
// that reads as the bare text "DetailedCompact", and an embed box left empty
// once bluemonday drops the iframe inside it.
func TestCleanEventContentStripsUpstreamChrome(t *testing.T) {
	sel, _ := cleanedFixture(t)

	stripped := []string{
		"div.header-page",
		".page-title",
		".page-tags",
		".battle-pass-compact",
		".bp-chip-icon",
		".bp-seg",
		".video-container",
	}
	for _, s := range stripped {
		if n := sel.Find(s).Length(); n != 0 {
			t.Errorf("cleanEventContent left %d %s in the body, want 0", n, s)
		}
	}
	if sel.Find("hr").Length() != 0 {
		t.Error("the two stray rules from div.header-page survived")
	}
}

// The .bp-seg strip must not take its parent with it. .bp-header-controls also
// holds .bp-points-pill, which is the only place the points per rank figure
// appears, so removing the parent would delete information trainers want.
func TestCleanEventContentKeepsPointsPill(t *testing.T) {
	sel, html := cleanedFixture(t)

	for _, s := range []string{".bp-header-controls", ".bp-points-pill", ".points-label", ".points-value"} {
		if sel.Find(s).Length() == 0 {
			t.Errorf("%s was removed along with .bp-seg, but it carries the points per rank info", s)
		}
	}
	if got := strings.TrimSpace(sel.Find(".points-value").Text()); got != "100" {
		t.Errorf("points value = %q, want %q", got, "100")
	}
	if strings.Contains(html, "DetailedCompact") {
		t.Error("the dead toggle text DetailedCompact is still in the body")
	}
}

// bluemonday's UGC policy allows no iframe, so an embed reaches a trainer as an
// empty box and the strip deletes it. On the 10th Anniversary page that left the
// sentence promising a thank you message with nothing under it to watch. The box
// becomes a link instead, and because that happens before the absolutize pass the
// link is finished like every other one: absolute, target and rel set.
//
// The origin allowlist and detailPolicy are untouched by this. The href points at
// a third party host, which is ordinary for an anchor and inert until clicked,
// and the embed still never reaches the DOM.
func TestCleanEventContentTurnsAVideoEmbedIntoALink(t *testing.T) {
	sel, _ := cleanedFixture(t)

	if n := sel.Find(".video-container").Length(); n != 0 {
		t.Errorf("%d video boxes survived, want 0", n)
	}
	if n := sel.Find("iframe").Length(); n != 0 {
		t.Errorf("%d iframes survived, want 0: the embed must not reach the DOM", n)
	}
	a := sel.Find(".event-video-link a")
	if a.Length() != 1 {
		t.Fatalf("found %d video links, want 1: the video is unreachable again", a.Length())
	}
	if href, _ := a.Attr("href"); href != "https://youtube.com/embed/x" {
		t.Errorf("href = %q, want %q", href, "https://youtube.com/embed/x")
	}
	if target, _ := a.Attr("target"); target != "_blank" {
		t.Errorf("target = %q, want %q: the video link skipped the absolutize pass", target, "_blank")
	}
	if rel, _ := a.Attr("rel"); rel != "noopener nofollow" {
		t.Errorf("rel = %q, want %q: the video link skipped the absolutize pass", rel, "noopener nofollow")
	}
}

// A box we cannot get a usable https URL out of is removed as it always was.
// Leaving a link with no href behind would be worse than the empty box: the
// sanitizer drops the unusable href and the modal is left with the bare words
// "Watch the video" pointing nowhere.
func TestCleanEventContentDropsVideoBoxesWithNothingToLinkTo(t *testing.T) {
	const markup = `<div class="page-content">` +
		`<div class="video-container"></div>` +
		`<div class="video-container"><iframe></iframe></div>` +
		`<div class="video-container"><iframe src="javascript:alert(1)"></iframe></div>` +
		`<p>real body</p>` +
		`</div>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse markup: %v", err)
	}
	sel := doc.Find("div.page-content").First()
	cleanEventContent(sel)

	if n := sel.Find(".video-container").Length(); n != 0 {
		t.Errorf("%d video boxes survived, want 0", n)
	}
	if n := sel.Find(".event-video-link").Length(); n != 0 {
		html, _ := sel.Html()
		t.Errorf("%d links were written for videos with no usable URL, want 0: %s", n, html)
	}
	if !strings.Contains(sel.Text(), "real body") {
		t.Error("the rest of the body went with the video boxes")
	}
}

// Some wrappers arrive with nothing in them, and the reader can tell: both Raid
// Hour bodies end on an empty div.bonus-features-wrapper, and
// season-23-forever-forward puts an empty ul.pkmn-list-flex directly under a "Max
// Pokemon Debuts" heading, which reads as content that failed to load.
//
// The trap on the other side is that plenty of upstream elements are empty on
// purpose and carry their meaning in CSS. div.divider is drawn as a horizontal
// rule between sections, and .step-background and the bubbles are decoration
// behind a research step. A rule shaped as "remove empty divs" would take a
// section separator out of every page on the site.
func TestCleanEventContentDropsEmptyWrappersButKeepsStyledOnes(t *testing.T) {
	sel, _ := cleanedFixture(t)

	if n := sel.Find(".bonus-features-wrapper").Length(); n != 0 {
		t.Errorf("%d empty bonus feature wrappers survived, want 0", n)
	}
	// The populated list stays and the empty one goes, so the count tells them
	// apart on its own.
	if n := sel.Find("ul.pkmn-list-flex").Length(); n != 1 {
		html, _ := sel.Html()
		t.Errorf("%d pkmn lists survived, want 1: %s", n, html)
	}
	if n := sel.Find(".pkmn-list-item").Length(); n != 1 {
		t.Errorf("%d list items survived, want 1: the rule ate a list with content in it", n)
	}
	for _, keep := range []string{".divider", ".step-background", ".bubble1"} {
		if sel.Find(keep).Length() == 0 {
			t.Errorf("%s was removed, but it is empty by design and drawn by CSS", keep)
		}
	}
}

// The other half of the contract: the strip selector must not be greedy. These
// are the blocks the modal exists to show.
func TestCleanEventContentKeepsRealContent(t *testing.T) {
	sel, _ := cleanedFixture(t)

	kept := []string{
		".event-description",
		".event-section-header",
		".bonus-list",
		".bonus-item",
		".battle-pass-container",
		".battle-pass-rewards",
		".rank-item",
		".pass-name",
		".pkmn-list-flex",
		".pkmn-list-item",
		".special-research-list",
		".step-item",
	}
	for _, s := range kept {
		if sel.Find(s).Length() == 0 {
			t.Errorf("cleanEventContent removed %s, which is real event content", s)
		}
	}
	if !strings.Contains(sel.Find(".event-description").Text(), "Body text.") {
		t.Error("the event description lost its text")
	}
}

// The body is injected into a page served from our own origin, so a relative
// path would resolve against pogo.hails.app and 404. Links also have to open
// away from the modal and must not pass link equity upstream.
func TestCleanEventContentAbsolutizesURLs(t *testing.T) {
	sel, _ := cleanedFixture(t)

	a := sel.Find("#other-link a[href]")
	if a.Length() != 1 {
		t.Fatalf("found %d fixture links, want 1", a.Length())
	}
	if href, _ := a.Attr("href"); href != "https://leekduck.com/events/other/" {
		t.Errorf("href = %q, want %q", href, "https://leekduck.com/events/other/")
	}
	if target, _ := a.Attr("target"); target != "_blank" {
		t.Errorf("target = %q, want %q", target, "_blank")
	}
	if rel, _ := a.Attr("rel"); rel != "noopener nofollow" {
		t.Errorf("rel = %q, want %q", rel, "noopener nofollow")
	}

	sel.Find("img").Each(func(_ int, img *goquery.Selection) {
		src, _ := img.Attr("src")
		if !strings.HasPrefix(src, "https://leekduck.com/") {
			t.Errorf("img src = %q, want an absolute leekduck.com URL", src)
		}
		if loading, _ := img.Attr("loading"); loading != "lazy" {
			t.Errorf("img %q has loading = %q, want lazy", src, loading)
		}
	})
	if sel.Find("img").Length() == 0 {
		t.Error("every image was stripped, so the URL rewrite proved nothing")
	}
}

// Everything below the author box is site furniture: related events, a footer,
// share widgets. The trim is what stops it reaching the modal.
func TestCleanEventContentTrimsAtAuthorBox(t *testing.T) {
	sel, html := cleanedFixture(t)

	if sel.Find("section.author-box").Length() != 0 {
		t.Error("the author box itself survived the trim")
	}
	if sel.Find(".should-be-trimmed").Length() != 0 {
		t.Error("content after the author box survived the trim")
	}
	if strings.Contains(html, "by someone") || strings.Contains(html, "gone") {
		t.Error("trimmed text is still present in the rendered body")
	}
	// The trim must stop at the author box, not eat what came before it.
	if sel.Find(".pkmn-list-flex").Length() == 0 {
		t.Error("the trim removed content that sits above the author box")
	}
}

// The whole point of the schema version: a cache written before it existed, or
// under older cleanup rules, must not be trusted as fresh. Every page in it has
// to be KEPT, so the modal has something to show from the first second after a
// restart, and marked stale so the next refresh pass re-scrapes it.
//
// Both halves are asserted here. Keeping used to be the broken one: a bare map
// decodes into the envelope with Details nil, so the whole cache was silently
// thrown away while the log claimed it was merely stale.
//
// Only the first case is a shape that has ever been on a disk. The second is
// synthetic: no cache has ever been written carrying version 1, because the
// numbering was introduced already past it, and it is here to stand in for
// whatever the current version happens to be superseded by next.
func TestLoadEventDetailsFromDiskStalesOnSchemaMismatch(t *testing.T) {
	fresh := time.Now()
	cases := map[string]string{
		"pre versioning bare map":  `{"a":{"html":"<p>old</p>","fetchedAt":"` + fresh.Format(time.RFC3339Nano) + `"}}`,
		"synthetic older envelope": `{"schemaVersion":1,"details":{"a":{"html":"<p>old</p>","fetchedAt":"` + fresh.Format(time.RFC3339Nano) + `"}}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "event_details.json"), []byte(raw), 0644); err != nil {
				t.Fatalf("write cache: %v", err)
			}
			s := &Store{cacheDir: dir, eventDetails: map[string]eventDetail{}}
			s.loadEventDetailsFromDisk()

			// Survival first. Without it the assertions below are vacuous: an empty
			// map has no entry to find a stale timestamp on, and planDetailRefresh
			// queues the same single job either way.
			if len(s.eventDetails) != 1 {
				t.Fatalf("loaded %d pages, want 1: the cache was discarded instead of kept and marked stale", len(s.eventDetails))
			}
			d, ok := s.eventDetails["a"]
			if !ok {
				t.Fatal(`the cached page "a" did not survive the load`)
			}
			if d.HTML != "<p>old</p>" {
				t.Errorf("html = %q, want %q: the page survived but its body did not", d.HTML, "<p>old</p>")
			}
			if !d.FetchedAt.IsZero() {
				t.Errorf("entry %q kept fetchedAt %v, so it would not be re-scraped", "a", d.FetchedAt)
			}
			entries := []feedEntry{{EventID: "a", Link: "https://leekduck.com/events/a/"}}
			fetchedAt := map[string]time.Time{}
			for id, d := range s.eventDetails {
				fetchedAt[id] = d.FetchedAt
			}
			_, jobs := planDetailRefresh(entries, fetchedAt, time.Now(), anywhereOnEarth)
			if len(jobs) != 1 {
				t.Errorf("planned %d re-scrapes, want 1", len(jobs))
			}
		})
	}
}

// A cache file that carries no usable map at all must leave a usable map behind
// and no pages in it. Every path out of loadEventDetailsFromDisk is in here,
// including the ones that give up before the load proper: an unparseable file, an
// empty one, a JSON array, and a byte order mark in front of otherwise perfectly
// good JSON.
//
// The non nil half is the one the name is about. refreshEventDetails assigns into
// s.eventDetails on its first successful scrape, and assigning into a nil map
// panics the whole process.
//
// The count is the other half and it is not a formality. The legacy retry decodes
// the file a second time as a bare map of event id to eventDetail, and for
// {"schemaVersion":N,"details":null} that decode gets far enough to enter both
// top level keys as event ids before it fails on the value. Taking that half
// filled map instead of the error puts two phantom pages in the cache, keyed
// "details" and "schemaVersion", each with an empty body, and EventDetail hands
// both of them to the modal as a successful lookup.
func TestLoadEventDetailsFromDiskNeverLeavesANilMap(t *testing.T) {
	fresh := time.Now()
	cases := []struct {
		name      string
		raw       string
		wantPages int
	}{
		{"file is null", `null`, 0},
		{"details is null", fmt.Sprintf(`{"schemaVersion":%d,"details":null}`, detailSchemaVersion), 0},
		{"object with no keys", `{}`, 0},
		{"empty file", ``, 0},
		{"truncated file", fmt.Sprintf(`{"schemaVersion":%d,"details":{"a":`, detailSchemaVersion), 0},
		{"a JSON array", `[]`, 0},
		// Nothing this package writes starts with a byte order mark, but a hand
		// edit through a PowerShell redirect does, and that is a trap this project
		// has already been bitten by once, in the embedded locale JSON.
		{"byte order mark then a bare map", "\ufeff" + `{"a":{"html":"<p>x</p>","fetchedAt":"` + fresh.Format(time.RFC3339Nano) + `"}}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "event_details.json"), []byte(tc.raw), 0644); err != nil {
				t.Fatalf("write cache: %v", err)
			}
			s := &Store{cacheDir: dir}
			s.loadEventDetailsFromDisk()

			if s.eventDetails == nil {
				t.Fatal("the store was left with a nil map, so the first successful scrape would panic")
			}
			if len(s.eventDetails) != tc.wantPages {
				t.Errorf("loaded %d pages, want %d: %v", len(s.eventDetails), tc.wantPages, s.eventDetails)
			}
			s.eventDetails["written-by-the-test"] = eventDetail{HTML: "<p>x</p>", FetchedAt: time.Now()}
		})
	}
}

// A cache that parses as neither shape has to say so. The envelope decode cannot
// report on a bare map at all, because every top level key in one is an unknown
// field, so a file whose VALUES are corrupt still decodes as an empty envelope
// without complaint and the legacy retry is the only pass that sees the real
// error. Swallowing it there leaves a corrupted cache logging exactly what a
// first boot logs, which is nothing but "loaded 0 from disk cache".
func TestLoadEventDetailsFromDiskLogsACacheItCannotRead(t *testing.T) {
	const raw = `{"a":{"html":"<p>x</p>","fetchedAt":"not-a-time"}}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "event_details.json"), []byte(raw), 0644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	s := &Store{cacheDir: dir}
	s.loadEventDetailsFromDisk()

	if !strings.Contains(buf.String(), "cache parse") {
		t.Errorf("an unreadable cache was dropped without a word in the log, so it looks exactly like a first boot:\n%s", buf.String())
	}
	if len(s.eventDetails) != 0 {
		t.Errorf("loaded %d pages from a cache that does not parse, want 0: %v", len(s.eventDetails), s.eventDetails)
	}
	// And the line that follows it has to stay quiet. An empty cache has no
	// staleness to report, and announcing that it is keeping 0 pages reads like a
	// decision was made about something.
	if strings.Contains(buf.String(), "keeping") {
		t.Errorf("an empty cache announced what it was keeping:\n%s", buf.String())
	}
}

// A cache at the current version keeps its timestamps, or every restart would
// re-scrape every page for nothing.
func TestLoadEventDetailsFromDiskKeepsCurrentSchema(t *testing.T) {
	dir := t.TempDir()
	details := map[string]eventDetail{
		"a": {HTML: "<p>new</p>", FetchedAt: time.Now()},
	}
	data, err := json.Marshal(eventDetailsFile{SchemaVersion: detailSchemaVersion, Details: details})
	if err != nil {
		t.Fatalf("marshal cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "event_details.json"), data, 0644); err != nil {
		t.Fatalf("write cache: %v", err)
	}

	loaded := &Store{cacheDir: dir, eventDetails: map[string]eventDetail{}}
	loaded.loadEventDetailsFromDisk()

	d, ok := loaded.eventDetails["a"]
	if !ok {
		t.Fatal("a current version cache did not load")
	}
	if d.HTML != "<p>new</p>" {
		t.Errorf("html = %q, want %q", d.HTML, "<p>new</p>")
	}
	if d.FetchedAt.IsZero() {
		t.Error("a current version cache was wrongly marked stale")
	}
}

// The writer and the reader have to agree, and only a real round trip proves it.
// Reverting the writer to a bare map of the details, the shape the cache used to
// have, leaves every restart from then on loading pages with blanked timestamps
// and re-scraping the whole feed for nothing.
//
// The feed entry here is already cached and fresh, so the pass plans no scrape
// and never reaches the network; all it exercises is the persist path.
func TestEventDetailsSurviveAWriteAndReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Store{cacheDir: dir, eventDetails: map[string]eventDetail{
		"a": {HTML: "<p>cached</p>", FetchedAt: time.Now()},
	}}
	s.refreshEventDetails(json.RawMessage(`[{"eventID":"a","link":"https://leekduck.com/events/a/"}]`))

	raw, err := os.ReadFile(filepath.Join(dir, "event_details.json"))
	if err != nil {
		t.Fatalf("refreshEventDetails wrote no cache file: %v", err)
	}
	if !strings.Contains(string(raw), `"schemaVersion"`) {
		t.Errorf("the written cache has no schemaVersion envelope: %s", raw)
	}

	loaded := &Store{cacheDir: dir, eventDetails: map[string]eventDetail{}}
	loaded.loadEventDetailsFromDisk()

	d, ok := loaded.eventDetails["a"]
	if !ok {
		t.Fatal("the page did not survive the round trip")
	}
	if d.HTML != "<p>cached</p>" {
		t.Errorf("html = %q, want %q", d.HTML, "<p>cached</p>")
	}
	if d.FetchedAt.IsZero() {
		t.Error("the reloaded page came back stale, so the writer did not stamp the current schema version")
	}
}

// Upstream pads the ad slots the strip already removes with paragraphs holding
// nothing but line breaks. 23 of 25 sampled live event bodies carried a
// <p><br/><br/></p> and a <p><br/></p>, and each renders as roughly three blank
// lines right where the description meets the first section header. CSS cannot
// clear them, because p:empty does not match a paragraph with a br child.
//
// The rule has three other halves, so to speak, and each of them is a way of
// getting this wrong:
//
// A paragraph with anything real in it stays, which is why an image only
// paragraph is in here.
//
// A paragraph carrying an id stays even when it is nothing but a line break,
// because these pages link to their own sections and removing a fragment target
// breaks the link pointing at it.
//
// And the pass has to run AFTER the chrome strip. The paragraph that still has
// its ad between the two breaks is the one that proves it: while the ins is
// there the paragraph holds an element that is not a br, so this rule leaves it
// alone, and only the strip turns it into a spacer. Move the two passes past each
// other and that paragraph survives into the modal.
func TestCleanEventContentDropsLineBreakOnlyParagraphs(t *testing.T) {
	const markup = `<div class="page-content">` +
		`<p><br/><br/></p>` +
		`<p><br/></p>` +
		`<p>   </p>` +
		`<p><br/><ins class="adsbygoogle"></ins><br/></p>` +
		`<p id="jump"><br/></p>` +
		`<p id="keeps-text">text<br/>more</p>` +
		`<p id="keeps-image"><img src="/assets/img/x.png"/></p>` +
		`</div>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("parse markup: %v", err)
	}
	sel := doc.Find("div.page-content").First()
	cleanEventContent(sel)

	if n := sel.Find("p").Length(); n != 3 {
		html, _ := sel.Html()
		t.Errorf("%d paragraphs survived, want 3: %s", n, html)
	}
	for _, keep := range []string{"#keeps-text", "#keeps-image"} {
		if sel.Find(keep).Length() != 1 {
			t.Errorf("%s was removed, but it carries real content", keep)
		}
	}
	if sel.Find("#jump").Length() != 1 {
		t.Error("the paragraph carrying an id was removed, so any link to #jump now lands nowhere")
	}
	if sel.Find("#keeps-image img").Length() != 1 {
		t.Error("the image inside the surviving paragraph was removed")
	}
	if sel.Find("ins").Length() != 0 {
		t.Error("the ad slot itself survived the strip")
	}
	// Two brs are left: the one between "text" and "more", and the one inside the
	// paragraph that is kept for its id. Every other br belonged to a spacer
	// paragraph that should be gone, the ad padded one included.
	if n := sel.Find("br").Length(); n != 2 {
		html, _ := sel.Html()
		t.Errorf("%d line breaks survived, want 2: a spacer paragraph is still there: %s", n, html)
	}
}

// The processing chain that turns a fetched page into the stored body: find
// .page-content, run cleanEventContent over it, sanitize the result. This is the
// only call site of cleanEventContent, so gutting that call has to fail here.
//
// scrapeEventPage adds nothing but the origin allowlist and the HTTP request,
// neither of which can be driven from a test without a network or a weaker
// allowlist, so the allowlist stays untested from this side on purpose. Its
// refusals live in eventdetails_security_test.go.
func TestExtractEventBodyCleansAndSanitizes(t *testing.T) {
	page := `<html><body><article class="event-page">` + cleanFixtureHTML + `</article></body></html>`
	body, err := extractEventBody(strings.NewReader(page))
	if err != nil {
		t.Fatalf("extractEventBody: %v", err)
	}

	// cleanEventContent ran: upstream chrome, the tail past the author box and the
	// line break spacers are all gone.
	for _, gone := range []string{"header-page", "page-title", "battle-pass-compact", "bp-chip-icon", "bp-seg", "DetailedCompact", "video-container", "by someone", "should-be-trimmed", "<p><br"} {
		if strings.Contains(body, gone) {
			t.Errorf("extractEventBody kept %q, so the cleanup did not run over the body", gone)
		}
	}
	// It also absolutized, which nothing else in the chain does.
	if !strings.Contains(body, "https://leekduck.com/assets/img/events/bonuses/candy.png") {
		t.Errorf("image sources were not absolutized:\n%s", body)
	}
	// detailPolicy ran: bluemonday's UGC policy allows no input and no iframe. The
	// radio input is the real one upstream uses to pick between the two reward
	// lists, and losing it is exactly why both of them render for us.
	for _, gone := range []string{"<input", "bp-view-radio", "<iframe"} {
		if strings.Contains(body, gone) {
			t.Errorf("extractEventBody kept %q, so the sanitizer did not run", gone)
		}
	}
	// And the content the modal exists to show came through.
	for _, kept := range []string{"Body text.", "battle-pass-rewards", "bonus-list", "pkmn-list-flex", "points-value"} {
		if !strings.Contains(body, kept) {
			t.Errorf("extractEventBody dropped %q, which is real event content", kept)
		}
	}
}

// detailSchemaVersion is the only mechanism that gets a cleanup change onto a
// trainer's screen. The disk cache holds whole rendered bodies, so a page written
// under the old rules keeps its old body until the constant changes and marks it
// stale, which means an edit to cleanEventContent or detailPolicy that forgets
// the bump ships to nobody and looks like it worked.
//
// This test ties the two together so they cannot drift. cleanFixtureGolden is the
// exact output of the whole chain over cleanFixtureHTML, taken at the version
// recorded beside it.
//
// If it fails because the cleanup rules changed on purpose: update the golden,
// bump detailSchemaVersion, and bump goldenTakenAtSchemaVersion to match.
//
// If it fails after a goquery or bluemonday upgrade, with no rule of ours
// touched, then every trainer is already looking at the same body they were
// before: update the golden and leave both constants where they are.
const goldenTakenAtSchemaVersion = 5

const cleanFixtureGolden = `<div class="event-description"><p>Body text.</p></div>



<p id="prism-promenade"><br/></p>
<h2 id="bonuses" class="event-section-header bonuses">Bonuses <img src="https://leekduck.com/assets/img/events/icons/bonuses.png" loading="lazy"/></h2>
<div class="bonus-list"><div class="bonus-item"><div class="item-circle"><img src="https://leekduck.com/assets/img/events/bonuses/candy.png" alt="One extra Candy" loading="lazy"/></div><div class="bonus-text">One extra Candy</div></div></div>
<div class="battle-pass-container -has-toggle"><div class="battle-pass-header"><div class="pass-info"><h3 class="pass-name">GO Pass: August</h3></div><div class="bp-header-controls"><span class="bp-points-pill"><span class="points-label">Points / rank</span><span class="points-value">100</span></span></div></div><div class="battle-pass-rewards"><div class="rank-item"><div class="rank-label"><span class="rank-text">RANK</span><div class="rank-number">1</div></div></div></div></div>
<p class="event-video-link"><a href="https://youtube.com/embed/x" target="_blank" rel="noopener nofollow">Watch the video</a></p>
<ul class="pkmn-list-flex"><li class="pkmn-list-item"><div class="pkmn-list-img psychic"><img src="https://leekduck.com/assets/img/pokemon_icons/pokemon_icon_150_00.png" loading="lazy"/></div><div class="pkmn-name">Mewtwo</div></li></ul>
<h2 class="event-section-header">Max Pokemon Debuts</h2>

<div class="divider"></div>
<div class="special-research-list"><div class="step-item"><div class="step-background"></div><div class="bubble1"></div><div class="research-icon"><img src="https://leekduck.com/assets/img/icons/research.png" loading="lazy"/></div></div></div>

<p id="other-link"><a href="https://leekduck.com/events/other/" target="_blank" rel="noopener nofollow">Other event</a></p>`

func TestExtractEventBodyPinsTheSchemaVersion(t *testing.T) {
	page := `<html><body><article class="event-page">` + cleanFixtureHTML + `</article></body></html>`
	body, err := extractEventBody(strings.NewReader(page))
	if err != nil {
		t.Fatalf("extractEventBody: %v", err)
	}
	if body != cleanFixtureGolden {
		t.Errorf("the cleaned body changed.\n got: %s\nwant: %s", body, cleanFixtureGolden)
	}
	if detailSchemaVersion != goldenTakenAtSchemaVersion {
		t.Errorf("detailSchemaVersion = %d but the golden body above was taken at version %d. Either the cache version moved without the rules, which re-scrapes the whole feed for nothing, or the rules moved without the version, which means no cached page ever gets the change.", detailSchemaVersion, goldenTakenAtSchemaVersion)
	}
}

// The two guards that stop a redesigned or truncated upstream page from being
// cached as a blank modal body.
func TestExtractEventBodyRejectsUnusablePages(t *testing.T) {
	cases := map[string]string{
		"no page content": `<html><body><article class="event-page"><p>nothing here</p></article></body></html>`,
		"nothing survives": `<html><body><div class="page-content">` +
			`<div class="event-toc">links</div><script>alert(1)</script>` +
			`</div></body></html>`,
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := extractEventBody(strings.NewReader(page)); err == nil {
				t.Errorf("extractEventBody returned %q with no error, want a refusal", got)
			}
		})
	}
}

// The LEGO store finder is the .bp-seg defect one page further on: bluemonday
// unwraps select, option and button and keeps their text, so the widget arrives
// as a run together country list, a Search button reduced to the bare word, and
// a frame promising a map that cannot load. Only the two dead containers go.
func TestCleanEventContentStripsTheStoreMapWidgetButKeepsTheLink(t *testing.T) {
	const page = `<div class="page-content"><section class="lego-store-map-2026">` +
		`<h2 id="lego-store-map-title">Participating LEGO Store Locations</h2>` +
		`<p class="lego-store-map-2026__description">Use this map to find stores near you.</p>` +
		`<div class="lego-store-map-2026__controls">Search locations<div class="lego-store-map-2026__search-row">Search</div>` +
		`<div class="lego-store-map-2026__field">CountryAll countries (213)Australia (22)</div>213 locations available</div>` +
		`<div class="lego-store-map-2026__frame"><div class="lego-store-map-2026__canvas"></div>` +
		`<p class="lego-store-map-2026__message">The interactive map will load when it is near the screen.</p></div>` +
		`<p class="lego-store-map-2026__fallback"><a href="/maps/official">Open the official map</a></p></section></div>`

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sel := doc.Find("div.page-content").First()
	cleanEventContent(sel)
	html, err := sel.Html()
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, gone := range []string{"lego-store-map-2026__controls", "lego-store-map-2026__frame", "All countries", "213 locations available", "will load when it is near the screen"} {
		if strings.Contains(html, gone) {
			t.Errorf("the store finder kept %q, which reads as broken text", gone)
		}
	}
	for _, kept := range []string{"Participating LEGO Store Locations", "Use this map to find stores near you.", "Open the official map", "https://leekduck.com/maps/official"} {
		if !strings.Contains(html, kept) {
			t.Errorf("the store finder lost %q, which is real content", kept)
		}
	}
}

// Upstream publishes the GO Pass shell before it fills in the rewards, which
// leaves an empty pass name, an empty points value and no rank rows. Our own
// styling then draws a gold pill around "Points / rank" with no number after it.
func TestCleanEventContentDropsAGoPassWithNoRanks(t *testing.T) {
	const empty = `<div class="battle-pass-container"><div class="battle-pass-header">` +
		`<div class="pass-info"><h3 class="pass-name"></h3></div>` +
		`<div class="bp-header-controls"><span class="bp-points-pill"><span class="points-label">Points / rank</span>` +
		`<span class="points-value"></span></span></div></div>` +
		`<div class="battle-pass-rewards"><div class="battle-pass-paths"><div class="path-column basic">` +
		`<div class="path-header">BASIC</div></div></div></div></div>`
	const filled = `<div class="battle-pass-container"><div class="battle-pass-rewards">` +
		`<div class="rank-item"><div class="rank-number">1</div></div></div></div>`

	for name, tc := range map[string]struct {
		in   string
		want bool
	}{
		"no rank rows":  {empty, false},
		"has rank rows": {filled, true},
	} {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(`<div class="page-content">` + tc.in + `</div>`))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		sel := doc.Find("div.page-content").First()
		cleanEventContent(sel)
		if got := sel.Find(".battle-pass-container").Length() > 0; got != tc.want {
			t.Errorf("%s: container survived = %v, want %v", name, got, tc.want)
		}
	}
}

// A lazy embed ships src="" with the real URL in a data attribute. base.Parse("")
// returns the base itself, so without the guard that became a "Watch the video"
// link pointing at the LeekDuck home page.
func TestCleanEventContentIgnoresAnEmptyVideoSrc(t *testing.T) {
	for _, src := range []string{"", "   ", "#", "?", "./"} {
		page := `<div class="page-content"><div class="video-container"><iframe src="` + src + `"></iframe></div></div>`
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(page))
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		sel := doc.Find("div.page-content").First()
		cleanEventContent(sel)
		html, err := sel.Html()
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if strings.Contains(html, "Watch the video") {
			t.Errorf("src=%q produced a link to nothing: %s", src, html)
		}
	}
}

// The origin allowlist guarded only the URL we ask for. With Go's default policy
// a redirect could walk the scraper onto loopback or a metadata endpoint, and we
// would sanitize whatever came back and serve it as event content.
func TestDetailClientRefusesARedirectOffLeekduck(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://evil.test/x", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	via := []*http.Request{{}}
	if err := detailClient.CheckRedirect(req, via); err == nil {
		t.Error("a redirect to evil.test was allowed")
	}
	ok, err := http.NewRequest(http.MethodGet, "https://www.leekduck.com/events/x/", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := detailClient.CheckRedirect(ok, via); err != nil {
		t.Errorf("a redirect within leekduck.com was refused: %v", err)
	}
}
