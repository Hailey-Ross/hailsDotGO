package pogodata

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// parseFragment is the same one line goquery call the event page tests use, named
// here because this file needs it twice.
func parseFragment(t *testing.T, markup string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(markup))
	if err != nil {
		t.Fatalf("fixture markup: %v", err)
	}
	return doc
}

// Three rules in this file's suppression logic had no test that could tell them from
// their absence: deleting the additive exemption, turning the replacement veto into a
// vote, and dropping half the circuit breaker each left the whole package suite
// green. The tests that carry those rules' names pin fixtures in which the rule never
// decides anything, which is the failure mode the adversarial-review skill records
// for TestEventPageRaidWindowsSpansARangeHeading.
//
// Each test below is built on the SEPARATING input: the case where the rule and its
// absence give different answers.

// An additive window is exempt from a note outright. The existing test's window opens
// after the note, so the replacement rule already saves it and the exemption is dead
// code for that input. A long running event that was already open when the note was
// published separates them, and is not contrived: lego-pokemon-go-2026 really ran
// from 2026-08-03 to 2026-09-30, straight across the Mega Ascension note.
func TestAdditiveExemptionDecidesWhenTheReplacementRuleCannot(t *testing.T) {
	now := utc(t, "2026-09-01T12:00:00Z")
	note := liveSuppression(t)

	const openedFirst, closesLater = "2026-08-03T10:00:00.000", "2026-09-30T23:59:00.000"
	page := suppressWindow(t, "lego-pokemon-go-2026", "6", false, true, openedFirst, closesLater, "Mega Victreebel")
	feed := suppressWindow(t, "mega-victreebel-in-mega-raids", "6", false, false, openedFirst, closesLater, "Mega Victreebel")

	// The precondition: this window opened BEFORE the note, so it is not one of the
	// replacements the note promised and the veto does not reach it.
	if note.opensAfter(page) != true {
		t.Fatalf("fixture is not separating: the note does not open after the window, so the replacement rule would save it anyway")
	}
	if silencedBy([]RaidSuppression{note}, page, now) {
		t.Errorf("an additive window was silenced: a page that suspends a tier is the page that repopulates it")
	}
	// The same span read off the FEED instead is silenced, which is what proves the
	// exemption, and not something else, is doing the work.
	if !silencedBy([]RaidSuppression{note}, feed, now) {
		t.Errorf("the identical feed window was not silenced, so this test proves nothing about the exemption")
	}
}

// Being a replacement is a veto, not a vote: a rotation opening inside ANY live note
// stays, even when a second, later note would otherwise take it. The existing test
// has both notes answering the same way, so veto and vote cannot be distinguished.
// A rotation opening BETWEEN the two separates them.
func TestReplacementIsAVetoAndNotAVote(t *testing.T) {
	// Inside BOTH notes span, or the later one is not in force and cannot vote at all.
	now := utc(t, "2026-09-05T18:00:00Z")
	wide := RaidSuppression{
		EventID: "mega-ascension", Name: "Mega Ascension", Groups: []string{"6"},
		RawStart: "2026-08-31T00:01:00.000", RawEnd: "2026-09-06T23:59:00.000",
		StartsUTC: utc(t, "2026-08-31T12:01:00Z"), EndsUTC: utc(t, "2026-09-06T09:59:00Z"),
	}
	narrow := RaidSuppression{
		EventID: "some-later-page", Name: "A later page", Groups: []string{"6"},
		RawStart: "2026-09-05T00:01:00.000", RawEnd: "2026-09-06T23:59:00.000",
		StartsUTC: utc(t, "2026-09-05T12:01:00Z"), EndsUTC: utc(t, "2026-09-06T09:59:00Z"),
	}
	// Opens after the wide note and before the narrow one, so the two disagree.
	w := suppressWindow(t, "mega-beedrill", "6", false, false,
		"2026-09-02T06:00:00.000", "2026-09-08T22:00:00.000", "Mega Beedrill")

	if wide.opensAfter(w) || !narrow.opensAfter(w) {
		t.Fatalf("fixture is not separating: wide.opensAfter=%v narrow.opensAfter=%v, want false and true",
			wide.opensAfter(w), narrow.opensAfter(w))
	}
	if silencedBy([]RaidSuppression{wide, narrow}, w, now) {
		t.Error("a rotation that opened inside the wide note was silenced by the narrow one: the veto has become a vote")
	}
	// Order must not decide it either.
	if silencedBy([]RaidSuppression{narrow, wide}, w, now) {
		t.Error("the same pair in the other order silenced it, so the answer depends on slice order")
	}
	// And the narrow note alone really would take it, which is what makes the case above a veto.
	if !silencedBy([]RaidSuppression{narrow}, w, now) {
		t.Error("the narrow note alone did not silence it, so this test proves nothing about the veto")
	}
}

// The circuit breaker has two clauses: nothing live at all, OR no additive window
// anywhere. Both existing tests silence every governed window, so the first clause
// always decides and the second is never the reason. A note that empties ONE group
// while another tier stays live separates them.
func TestTheBreakerFiresOnAMissingPageReaderAlone(t *testing.T) {
	now := utc(t, "2026-09-03T12:00:00Z")
	// A note naming only the Mega group, so tier 5 stays populated and live.
	note := RaidSuppression{
		EventID: "mega-ascension", Name: "Mega Ascension", Groups: []string{"6"},
		RawStart: "2026-08-31T00:01:00.000", RawEnd: "2026-09-06T23:59:00.000",
		StartsUTC: utc(t, "2026-08-31T12:01:00Z"), EndsUTC: utc(t, "2026-09-06T09:59:00Z"),
	}
	const seasonStart, seasonEnd = "2026-08-26T06:00:00.000", "2026-09-08T22:00:00.000"
	windows := []RaidWindow{
		suppressWindow(t, "regis", "5", false, false, seasonStart, seasonEnd, "Regirock"),
		suppressWindow(t, "mega-gyarados", "6", false, false, seasonStart, seasonEnd, "Mega Gyarados"),
	}

	served, _, stats := reconcileRaids(raidReconcileInput{
		Upstream:     json.RawMessage(`{"5":[{"pokemon_name":"Regirock"}],"6":[{"pokemon_name":"Mega Gyarados"}]}`),
		Windows:      windows,
		Suppressions: []RaidSuppression{note},
		Now:          now,
		Lookup:       suppressLookup(t),
		CPMs:         testCPMs(t),
	})
	tiers := decodeTiers(t, served)

	// The note parsed, the Raids section reader found nothing on any page, and that
	// combination is one of the two event page readers working and the other not.
	// A page that suspends a tier names its replacements in the same breath.
	if !stats.SuppressionDisarmed {
		t.Fatalf("the breaker did not fire with a note in force and no additive window anywhere")
	}
	if !hasName(tiers["6"], "Mega Gyarados") {
		t.Errorf("tier 6 = %v, emptied on the strength of a note whose replacements nothing could read", names(tiers["6"]))
	}
	// Tier 5 was live throughout, so the "nothing live at all" clause could not have
	// been what fired. That is what makes this input separating.
	if !hasName(tiers["5"], "Regirock") {
		t.Errorf("tier 5 = %v, want the live rotation untouched", names(tiers["5"]))
	}

	// With ONE live additive window the second clause is satisfied: the page reader
	// is demonstrably working, so the note is believed and the tier really is emptied.
	withPage := append(windows, suppressWindow(t, "mega-ascension", "6", false, true,
		"2026-09-03T00:01:00.000", "2026-09-03T23:59:00.000", "Mega Victreebel"))
	served, _, stats = reconcileRaids(raidReconcileInput{
		Upstream:     json.RawMessage(`{"5":[{"pokemon_name":"Regirock"}],"6":[{"pokemon_name":"Mega Gyarados"}]}`),
		Windows:      withPage,
		Suppressions: []RaidSuppression{note},
		Now:          now,
		Lookup:       suppressLookup(t),
		CPMs:         testCPMs(t),
	})
	tiers = decodeTiers(t, served)
	if stats.SuppressionDisarmed {
		t.Errorf("the breaker fired although a live additive window proves the page reader works")
	}
	if hasName(tiers["6"], "Mega Gyarados") {
		t.Errorf("tier 6 = %v, kept a rotation the note suspends", names(tiers["6"]))
	}
}

// The breaker's comment claims that no additive window anywhere means the Raids
// section reader found nothing on any page. It does not: parseEventPageRaids falls
// back to the prose reader precisely when the structured roster is empty, so a
// LeekDuck list markup change leaves prose-only pages still producing windows and
// the stronger half of the breaker asleep. Pinned as it behaves, not as the comment
// reads, so that the next person to touch either finds this rather than the comment.
func TestAProseOnlyWindowStillCountsForTheBreaker(t *testing.T) {
	section := eventPageRaidSection(parseFragment(t, `<h2 class="event-section-header raids" id="raids">Raids</h2>
	<h2>Featured Pokemon</h2>
	<p>Mega Staraptor will make its Pokemon GO debut in Super Mega Raids.</p>
	<ul class="pkmn-list-flex"></ul>`))
	if section == nil {
		t.Fatal("fixture has no Raids section")
	}
	got := eventPageProseBosses(section)
	if len(got) != 1 || got[0].boss.Name != "Mega Staraptor" {
		t.Fatalf("prose reader produced %+v, want Mega Staraptor", got)
	}
	// So anyAdditiveWindow can be true on nothing but prose, which is the shape the
	// breaker cannot tell from a healthy page reader.
	w := RaidWindow{Tier: "6", Additive: true, Bosses: []WindowBoss{{Name: got[0].boss.Name}},
		StartsUTC: time.Now().Add(-time.Hour), EndsUTC: time.Now().Add(time.Hour)}
	if !anyAdditiveWindow([]RaidWindow{w}) {
		t.Error("a prose-read window does not count as additive, which would make the breaker's comment true")
	}
}
