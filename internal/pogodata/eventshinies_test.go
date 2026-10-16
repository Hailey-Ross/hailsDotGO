package pogodata

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// Cut from the real Harvest Festival 2026 and Twilight Trails pages in the event details cache.
const shinyDebutPage = `<h2 id="shiny" class="event-section-header shiny">Shiny</h2>
<h2 id="shiny-debut">Shiny Debut</h2><p>Shiny Applin will be available for the first time in Pokémon GO!</p>
<ul class="pkmn-list-flex">
<li class="pkmn-list-item"><div class="pkmn-list-img grass"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm840.s.icon.png"/></div><div class="pkmn-name">Applin</div></li>
<li class="pkmn-list-item"><div class="pkmn-list-img grass"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm841.s.icon.png"/></div><div class="pkmn-name">Flapple</div></li>
<li class="pkmn-list-item"><div class="pkmn-list-img grass"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm1019.s.icon.png"/></div><div class="pkmn-name">Hydrapple</div></li>
</ul>
<h2 id="spawns" class="event-section-header spawns">Wild Encounters</h2>
<ul class="pkmn-list-flex">
<li class="pkmn-list-item"><div class="pkmn-list-img ground"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pokemon_icon_111_00.png"/></div><img class="shiny-icon" src="https://cdn.leekduck.com/assets/img/icons/shiny-icon.png" alt="shiny"/><div class="pkmn-name">Rhyhorn</div></li>
<li class="pkmn-list-item"><div class="pkmn-list-img ice"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm872.icon.png"/></div><img class="shiny-icon" src="https://cdn.leekduck.com/assets/img/icons/shiny-icon.png" alt="shiny"/><div class="pkmn-name">Snom</div></li>
<li class="pkmn-list-item"><div class="pkmn-list-img fire"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm4.icon.png"/></div><div class="pkmn-name">Charmander</div></li>
<li class="pkmn-list-item"><div class="pkmn-list-img electric"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm25.fTSHIRT_03.s.icon.png"/></div><div class="pkmn-name">Pikachu</div></li>
<li class="pkmn-list-item"><div class="pkmn-list-img dragon"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm149.fMEGA.s.icon.png"/></div><img class="shiny-icon" src="https://cdn.leekduck.com/assets/img/icons/shiny-icon.png"/><div class="pkmn-name">Mega Dragonite</div></li>
<li class="pkmn-list-item"><div class="pkmn-list-img ghost"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pokemon_icon_094_51.png"/></div><img class="shiny-icon" src="https://cdn.leekduck.com/assets/img/icons/shiny-icon.png"/><div class="pkmn-name">Mega Gengar</div></li>
</ul>`

func TestParseEventPageShiniesReadsSpeciesShiniesOnly(t *testing.T) {
	got := parseEventPageShinies(shinyDebutPage)
	// Applin, Flapple and Hydrapple from the debut sprites; Rhyhorn (legacy sprite) and Snom from
	// shiny-icon. Not Charmander (no marker), and no form or costume sprite, shiny or not.
	want := []int{111, 840, 841, 872, 1019}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseEventPageShinies = %v, want %v", got, want)
	}
	if got := parseEventPageShinies(`<p>No Pokémon here.</p>`); len(got) != 0 {
		t.Errorf("a page with no list items should observe nothing, got %v", got)
	}
}

// eventShinyStore is a store holding one event whose page shows a shiny Ivysaur (dex 2).
func eventShinyStore(t *testing.T, start string) *Store {
	t.Helper()
	s := &Store{cacheDir: t.TempDir()}
	applyShinies(s, `{}`)
	s.events = json.RawMessage(fmt.Sprintf(`[{"eventID":"ivy-debut","name":"Ivy Debut","eventType":"event","start":%q,"end":"2026-12-31T20:00:00.000"}]`, start))
	s.eventDetails = map[string]eventDetail{"ivy-debut": {HTML: `<ul><li class="pkmn-list-item"><div class="pkmn-list-img"><img src="https://cdn.leekduck.com/assets/img/pokemon_icons/pm2.s.icon.png"/></div></li></ul>`}}
	s.mu.Lock()
	s.observeEventShiniesLocked()
	s.mu.Unlock()
	return s
}

func TestEventPageShinyReleasesOnItsStartDay(t *testing.T) {
	withBaseline(t, tinyBaseline())
	atDay(t, "2026-10-15")
	s := eventShinyStore(t, "2026-10-01T10:00:00.000")

	if !dexTableOf(t, s)["2"].ShinyReleased {
		t.Fatal("an event page showing shiny Ivysaur should have released it")
	}
	if _, ok := releasedKeys(t, s)["2"]; !ok {
		t.Error("and it should be served in the released shinies blob")
	}
	if _, shiny, _ := s.ShinyEffectiveDefaults(2); !shiny {
		t.Error("the effective default must include the observation, or an admin untick is dropped as a no-op")
	}
}

func TestEventPageShinyWaitsForAFutureEvent(t *testing.T) {
	withBaseline(t, tinyBaseline())
	atDay(t, "2026-10-15")
	s := eventShinyStore(t, "2026-10-20T10:00:00.000")

	if dexTableOf(t, s)["2"].ShinyReleased {
		t.Fatal("a debut announced for next week must stay locked until its day")
	}
	atDay(t, "2026-10-20")
	s.RefreshShinyDates()
	if !dexTableOf(t, s)["2"].ShinyReleased {
		t.Error("it should release when its start day arrives")
	}
}

func TestAdminUntickBeatsAnEventPageShiny(t *testing.T) {
	withBaseline(t, tinyBaseline())
	atDay(t, "2026-10-15")
	s := eventShinyStore(t, "2026-10-01T10:00:00.000")
	s.SetShinyOverrides([]ShinyOverride{{Dex: 2, ShinyReleased: boolPtr(false)}})

	if dexTableOf(t, s)["2"].ShinyReleased {
		t.Error("an explicit admin false must hold the shiny back, whatever the event page says")
	}
}

// The page is evicted when its event leaves the feed. The release must not go with it.
func TestEventPageShinySurvivesEvictionAndRestart(t *testing.T) {
	withBaseline(t, tinyBaseline())
	atDay(t, "2026-10-15")
	s := eventShinyStore(t, "2026-10-01T10:00:00.000")

	restarted := &Store{cacheDir: s.cacheDir}
	restarted.mu.Lock()
	restarted.loadShinyObservedLocked()
	restarted.mu.Unlock()
	applyShinies(restarted, `{}`) // no events, no pages: only the persisted sighting is left

	if !dexTableOf(t, restarted)["2"].ShinyReleased {
		t.Error("a restart with the page gone must still remember the shiny was released")
	}
}

func TestEarliestEventPageSightingWins(t *testing.T) {
	withBaseline(t, tinyBaseline())
	atDay(t, "2026-10-15")
	s := eventShinyStore(t, "2026-11-01T10:00:00.000")
	s.events = json.RawMessage(`[{"eventID":"ivy-debut","eventType":"event","start":"2026-10-05T10:00:00.000","end":"2026-12-31T20:00:00.000"}]`)
	s.mu.Lock()
	s.observeEventShiniesLocked()
	s.mu.Unlock()

	if got := s.shinyObserved[2].Date; got != "2026-10-05" {
		t.Errorf("observation date = %q, want the earlier 2026-10-05", got)
	}
	if !dexTableOf(t, s)["2"].ShinyReleased {
		t.Error("the earlier sighting has passed, so the shiny should be released")
	}
}

// A shiny shown for a base species carries forward to its evolutions, never back, and never into a
// species that is not in the game.
func TestEventPageShinyCarriesForwardAlongEvolutions(t *testing.T) {
	withBaseline(t, map[int]BaselineSpecies{
		1: {ID: 1, Name: "Bulbasaur", InGo: true},
		2: {ID: 2, Name: "Ivysaur", InGo: true},
		3: {ID: 3, Name: "Venusaur", InGo: false},
	})
	atDay(t, "2026-10-15")
	s := eventShinyStore(t, "2026-10-01T10:00:00.000") // the page shows shiny Ivysaur (dex 2)

	dex := dexTableOf(t, s)
	if !dex["2"].ShinyReleased {
		t.Fatal("the species on the page should be released")
	}
	if dex["1"].ShinyReleased {
		t.Error("a pre-evolution must not be released by its evolution's sighting")
	}
	if dex["3"].ShinyReleased || dex["3"].InGo {
		t.Error("an evolution the baseline says is not in the game must stay out")
	}

	withBaseline(t, map[int]BaselineSpecies{
		2: {ID: 2, Name: "Ivysaur", InGo: true},
		3: {ID: 3, Name: "Venusaur", InGo: true},
	})
	s.SetShinyOverrides(nil) // any rebuild re-derives the forwarded set
	if !dexTableOf(t, s)["3"].ShinyReleased {
		t.Error("once Venusaur is in the game, the Ivysaur sighting should release it too")
	}
}

func TestEvolutionChildrenFollowsNormalFormsOnly(t *testing.T) {
	if !slices.Contains(evolutionChildren[872], 873) {
		t.Errorf("Snom should evolve into Frosmoth, got %v", evolutionChildren[872])
	}
	if !slices.Contains(evolutionChildren[52], 53) {
		t.Errorf("Meowth should evolve into Persian, got %v", evolutionChildren[52])
	}
	if slices.Contains(evolutionChildren[52], 863) {
		t.Error("Perrserker comes from Galarian Meowth only, so a shiny Meowth must not reach it")
	}
}
