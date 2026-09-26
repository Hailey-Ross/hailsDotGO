package pogodata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The tier 3 roster upstream was serving during the Horizons event: the event's own
// bosses, which upstream mirrors, plus a shadow card that belongs to nobody's event
// and must never be touched by any of this.
const eventCardUpstream = `{"3":[
{"pokemon_name":"Meowscarada","cp":1538,"cp_max":1614,"image_url":"https://x/pm908.icon.png","types":["Grass","Dark"]},
{"pokemon_name":"Skeledirge","cp":1651,"cp_max":1729,"image_url":"https://x/pm911.icon.png","types":["Fire","Ghost"]},
{"pokemon_name":"Shadow Quagsire","cp":1075,"cp_max":1138,"image_url":"https://x/pm195.icon.png","types":["Water","Ground"]}]}`

func eventCardWindow(startsUTC, endsUTC time.Time) RaidWindow {
	return RaidWindow{
		EventID: "pokemon-horizons-the-series-celebration-event-2026",
		Name:    "Pokemon Horizons: The Series Celebration Event 2026",
		Tier:    "3",
		Bosses: []WindowBoss{
			{Name: "Meowscarada", Image: "https://cdn.leekduck.com/assets/img/pokemon_icons/pm908.icon.png", CanBeShiny: true},
			{Name: "Skeledirge", Image: "https://cdn.leekduck.com/assets/img/pokemon_icons/pm911.icon.png", CanBeShiny: true},
		},
		RawStart: "2026-09-16T10:00:00.000", RawEnd: "2026-09-22T20:00:00.000",
		StartsUTC: startsUTC, EndsUTC: endsUTC,
		Additive: true,
	}
}

func reconcileCards(t *testing.T, upstream string, now time.Time, cards []RaidEventCard, windows ...RaidWindow) (json.RawMessage, raidReconcileStats) {
	t.Helper()
	served, _, stats := reconcileRaids(raidReconcileInput{
		Upstream: json.RawMessage(upstream), Windows: windows, Now: now,
		Lookup: testLookup(t), CPMs: testCPMs(t), EventCards: cards,
	})
	return served, stats
}

// The whole point, in one test: while the event runs the cards are served and
// recorded, and once it has ended they leave the grid instead of sitting there
// undated until pokemon-go-api happens to rebuild.
//
// Tier 3 is reached ONLY by additive event page windows, so the ordinary drop rule
// can never fire for it: without this, the Horizons bosses were still being served
// two days after the event, with their window annotation silently gone.
func TestEventIntroducedCardsLeaveTheGridWhenTheEventEnds(t *testing.T) {
	live := utc(t, "2026-09-21T13:00:00Z")
	ends := utc(t, "2026-09-23T08:00:00Z")
	w := eventCardWindow(utc(t, "2026-09-15T20:00:00Z"), ends)

	served, stats := reconcileCards(t, eventCardUpstream, live, nil, w)
	cards := servedTier(t, served, "3")
	if !hasName(cards, "Meowscarada") || !hasName(cards, "Skeledirge") {
		t.Fatalf("tier 3 = %v, want the event's bosses while it is running", names(cards))
	}
	if len(stats.EventCards) != 2 {
		t.Fatalf("recorded %d cards, want 2: both were annotated by an additive window in a group nothing governs", len(stats.EventCards))
	}
	if stats.EventCardsDropped != 0 {
		t.Errorf("dropped %d while the event was still live", stats.EventCardsDropped)
	}

	// Two days later. The feed has moved on, upstream has not.
	after := utc(t, "2026-09-25T12:00:00Z")
	served, stats = reconcileCards(t, eventCardUpstream, after, stats.EventCards, w)
	cards = servedTier(t, served, "3")
	if hasName(cards, "Meowscarada") || hasName(cards, "Skeledirge") {
		t.Errorf("tier 3 = %v, still carries bosses whose only evidence ended two days ago", names(cards))
	}
	if stats.EventCardsDropped != 2 {
		t.Errorf("EventCardsDropped = %d, want 2", stats.EventCardsDropped)
	}
	// A card no event ever spoke for is none of this rule's business.
	if !hasName(cards, "Shadow Quagsire") {
		t.Errorf("tier 3 = %v, lost a card no event window ever named", names(cards))
	}
}

// The first sign that upstream has anything to say, the hold ends. Its roster
// changing is that sign, and it is the only one that does not depend on this app
// having guessed right.
func TestAHoldEndsAsSoonAsUpstreamRevisesTheGroup(t *testing.T) {
	live := utc(t, "2026-09-21T13:00:00Z")
	ends := utc(t, "2026-09-23T08:00:00Z")
	w := eventCardWindow(utc(t, "2026-09-15T20:00:00Z"), ends)
	_, stats := reconcileCards(t, eventCardUpstream, live, nil, w)

	// The same bosses, but upstream has published a new tier 3 roster around them.
	const revised = `{"3":[
	{"pokemon_name":"Meowscarada","cp":1538,"image_url":"https://x/pm908.icon.png","types":["Grass","Dark"]},
	{"pokemon_name":"Skeledirge","cp":1651,"image_url":"https://x/pm911.icon.png","types":["Fire","Ghost"]},
	{"pokemon_name":"Shadow Quagsire","cp":1075,"image_url":"https://x/pm195.icon.png","types":["Water","Ground"]},
	{"pokemon_name":"Lapras","cp":1131,"image_url":"https://x/pm131.icon.png","types":["Water","Ice"]}]}`

	after := utc(t, "2026-09-25T12:00:00Z")
	served, stats := reconcileCards(t, revised, after, stats.EventCards, w)
	cards := servedTier(t, served, "3")
	if !hasName(cards, "Meowscarada") {
		t.Errorf("tier 3 = %v, dropped a card upstream has just republished", names(cards))
	}
	if stats.EventCardsDropped != 0 {
		t.Errorf("EventCardsDropped = %d, want 0 once upstream has revised the group", stats.EventCardsDropped)
	}
	if len(stats.EventCards) != 0 {
		t.Errorf("kept %d records against a roster that has moved on", len(stats.EventCards))
	}
}

// The fingerprint must come from the UPSTREAM roster, not the served one. If it were
// read from what is served, the first drop would change the roster, the next rebuild
// would read that as "upstream rebuilt", the record would be discarded and the card
// would come back, for ever, at whatever rate the site rebuilds.
func TestAHoldDoesNotOscillateAcrossRebuilds(t *testing.T) {
	live := utc(t, "2026-09-21T13:00:00Z")
	ends := utc(t, "2026-09-23T08:00:00Z")
	w := eventCardWindow(utc(t, "2026-09-15T20:00:00Z"), ends)
	_, stats := reconcileCards(t, eventCardUpstream, live, nil, w)

	held := stats.EventCards
	for i, at := range []time.Time{
		utc(t, "2026-09-25T12:00:00Z"),
		utc(t, "2026-09-25T12:05:00Z"),
		utc(t, "2026-09-26T00:00:00Z"),
		utc(t, "2026-09-30T00:00:00Z"),
	} {
		var served json.RawMessage
		served, stats = reconcileCards(t, eventCardUpstream, at, held, w)
		held = stats.EventCards
		if cards := servedTier(t, served, "3"); hasName(cards, "Meowscarada") {
			t.Fatalf("rebuild %d at %s: tier 3 = %v, the held card came back", i, at.Format(time.RFC3339), names(cards))
		}
		if len(held) != 2 {
			t.Fatalf("rebuild %d: %d records survived, want 2", i, len(held))
		}
	}
}

// A hold is not allowed to be permanent. A frozen upstream that never republishes
// would otherwise keep a tier short for ever, and its roster staying byte identical
// means the fingerprint test can never end it.
func TestAHoldExpiresSoAFrozenUpstreamCannotHideATierForEver(t *testing.T) {
	live := utc(t, "2026-09-21T13:00:00Z")
	ends := utc(t, "2026-09-23T08:00:00Z")
	w := eventCardWindow(utc(t, "2026-09-15T20:00:00Z"), ends)
	_, stats := reconcileCards(t, eventCardUpstream, live, nil, w)

	// A day inside the cap: still held.
	inside := ends.Add(eventCardDropMax - 24*time.Hour)
	served, mid := reconcileCards(t, eventCardUpstream, inside, stats.EventCards, w)
	if hasName(servedTier(t, served, "3"), "Meowscarada") {
		t.Errorf("the hold lapsed early, inside the cap")
	}

	// An hour past it: upstream is simply frozen, and one stale card is a smaller
	// harm than a tier that stays short for ever.
	outside := ends.Add(eventCardDropMax + time.Hour)
	served, late := reconcileCards(t, eventCardUpstream, outside, mid.EventCards, w)
	if !hasName(servedTier(t, served, "3"), "Meowscarada") {
		t.Errorf("tier 3 is still short %s after the event ended", eventCardDropMax)
	}
	if late.EventCardsDropped != 0 || len(late.EventCards) != 0 {
		t.Errorf("dropped=%d records=%d past the cap, want both zero", late.EventCardsDropped, len(late.EventCards))
	}
}

// A group the feed governs is the ordinary drop rule's business, and two rules
// counting the same card would double count it and confuse the admin screen.
func TestAGroupTheFeedGovernsIsNeverHeld(t *testing.T) {
	const upstream = `{"5":[{"pokemon_name":"Lunala","cp":2219,"image_url":"https://x/pm792.icon.png","types":["Psychic","Ghost"]}]}`
	live := utc(t, "2026-09-21T13:00:00Z")
	// An ordinary feed rotation: not additive, so it governs tier 5.
	feed := RaidWindow{
		EventID: "lunala-in-5-star-raid-battles-september-2026", Name: "Lunala in 5-star Raid Battles",
		Tier: "5", Bosses: []WindowBoss{{Name: "Lunala", Image: "https://x/pm792.icon.png"}},
		RawStart: "2026-09-16T06:00:00.000", RawEnd: "2026-09-22T22:00:00.000",
		StartsUTC: live.Add(-48 * time.Hour), EndsUTC: live.Add(24 * time.Hour),
	}
	_, stats := reconcileCards(t, upstream, live, nil, feed)
	if len(stats.EventCards) != 0 {
		t.Errorf("recorded %+v for a group the feed governs", stats.EventCards)
	}
}

// And a hold that is already running ends the moment the feed starts governing that
// group, rather than sitting alongside the ordinary drop rule. Two rules acting on
// one card would count it twice on the admin screen, and worse, this one would keep
// removing a card the authoritative rule had decided to keep.
func TestAHoldStepsAsideWhenTheFeedTakesOverTheGroup(t *testing.T) {
	live := utc(t, "2026-09-21T13:00:00Z")
	ends := utc(t, "2026-09-23T08:00:00Z")
	page := eventCardWindow(utc(t, "2026-09-15T20:00:00Z"), ends)
	_, stats := reconcileCards(t, eventCardUpstream, live, nil, page)
	if len(stats.EventCards) != 2 {
		t.Fatalf("recorded %d cards, want 2", len(stats.EventCards))
	}

	// Later, upstream publishes a real three star rotation, which governs tier 3 the
	// ordinary way and names Meowscarada among its bosses.
	after := utc(t, "2026-09-25T12:00:00Z")
	feed := RaidWindow{
		EventID: "meowscarada-in-3-star-raid-battles-september-2026",
		Name:    "Meowscarada in 3-star Raid Battles", Tier: "3",
		Bosses:   []WindowBoss{{Name: "Meowscarada", Image: "https://x/pm908.icon.png"}},
		RawStart: "2026-09-24T06:00:00.000", RawEnd: "2026-09-30T22:00:00.000",
		StartsUTC: after.Add(-24 * time.Hour), EndsUTC: after.Add(120 * time.Hour),
	}
	served, governed := reconcileCards(t, eventCardUpstream, after, stats.EventCards, page, feed)
	cards := servedTier(t, served, "3")
	if !hasName(cards, "Meowscarada") {
		t.Errorf("tier 3 = %v, a hold removed a boss the feed says is live", names(cards))
	}
	if governed.EventCardsDropped != 0 {
		t.Errorf("EventCardsDropped = %d, want 0 once the feed governs the group", governed.EventCardsDropped)
	}
	if len(governed.EventCards) != 0 {
		t.Errorf("kept %d records in a group the feed now governs", len(governed.EventCards))
	}
	// Skeledirge is not on the feed's roster, so the ORDINARY rule drops it, which is
	// the whole point of handing authority back.
	if hasName(cards, "Skeledirge") {
		t.Errorf("tier 3 = %v, kept a boss the governing rotation does not name", names(cards))
	}
}

// The set has to survive a restart, or the first rebuild after one puts every held
// card back. It is also loaded before anything rebuilds; see Start.
func TestEventCardsRoundTripThroughTheCache(t *testing.T) {
	s := New()
	s.cacheDir = t.TempDir()

	now := time.Now().UTC()
	live := RaidEventCard{Group: "3", Boss: "meowscarada", Species: "Meowscarada",
		EventID: "pokemon-horizons-the-series-celebration-event-2026", Name: "Horizons",
		EndsUTC: now.Add(-time.Hour), Fingerprint: "abc123", FirstSeen: now.Add(-72 * time.Hour)}
	stale := RaidEventCard{Group: "3", Boss: "skeledirge", Species: "Skeledirge",
		EndsUTC: now.Add(-eventCardDropMax - time.Hour), Fingerprint: "abc123"}

	s.mu.Lock()
	s.setRaidsEventCardsLocked([]RaidEventCard{live, stale}, now)
	s.mu.Unlock()
	s.persistRaidsEventCards()

	got := s.RaidEventCards()
	if len(got) != 1 || got[0].Boss != "meowscarada" {
		t.Fatalf("held set = %+v, want only the record still inside the cap", got)
	}
	if !got[0].Holds(now) {
		t.Errorf("%+v is not holding, but its window ended an hour ago", got[0])
	}

	raw, err := os.ReadFile(filepath.Join(s.cacheDir, raidsEventCardsFile))
	if err != nil {
		t.Fatalf("hold set was not written to disk: %v", err)
	}
	var round []RaidEventCard
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("hold cache is not readable JSON: %v", err)
	}
	s2 := New()
	s2.cacheDir = s.cacheDir
	s2.loadRaidsEventCards()
	restored := s2.RaidEventCards()
	if len(restored) != 1 || restored[0].Fingerprint != "abc123" {
		t.Errorf("a restart read back %+v, want the record with its original fingerprint", restored)
	}
}

// The instant a hold begins has to be a boundary, or the drop waits for whatever
// wakes the rebuild next. The window it came from may already have been pruned from
// the feed by then, so nothing else knows the instant.
func TestNextRaidBoundaryIncludesAHoldBeginning(t *testing.T) {
	now := utc(t, "2026-09-25T12:00:00Z")
	ends := utc(t, "2026-09-25T18:00:00Z")
	cards := []RaidEventCard{{Group: "3", Boss: "meowscarada", EndsUTC: ends}}
	if got := nextRaidBoundary(nil, nil, cards, now); !got.Equal(ends) {
		t.Errorf("nextRaidBoundary = %s, want the instant the hold begins (%s)",
			got.Format(time.RFC3339), ends.Format(time.RFC3339))
	}
}
