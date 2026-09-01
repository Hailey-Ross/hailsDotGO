package pogodata

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// A Mega raid boss is FOUGHT as the Mega and CAUGHT as the base species, so the
// card's four CP fields come off the base stat line while its typing stays the
// Mega's. Upstream settles it: pokemon-go-api's own Mega Latios card reads
// 2090 to 2178, which is base Latios at level 20, and its Mega Falinks card reads
// 1278 to 1347, which is base Falinks. Every Mega this app builds itself was
// reading the Mega's own line instead, so six live cards were between 20 and 90
// percent too high.
//
// The tests below pin both halves of that: the AFTER numbers are what the card
// must now say, and the BEFORE numbers are what the Mega's battle line still
// produces. Asserting both is what makes the fixture honest, because a wrong stat
// line in the table would fail the BEFORE assertion rather than quietly agreeing
// with itself.

// megaCatchCase is one Mega's two stat lines and the two CP ranges they produce.
//
// megaAtk/Def/Sta is the line the boss fights with, baseAtk/Def/Sta the line a
// trainer catches. wantCP..wantBoostMax are the measured AFTER column, and
// beforeCP..beforeBoostMax the measured BEFORE column, both at level 20 and level
// 25 with IVs floored at 10 and ceilinged at 15. A zero boosted pair means only
// the unboosted range was measured for that card.
type megaCatchCase struct {
	name                      string
	primary, secondary        string
	megaAtk, megaDef, megaSta int
	baseAtk, baseDef, baseSta int
	wantTypes                 []string

	wantCP, wantCPMax              int
	wantBoostMin, wantBoostMax     int
	beforeCP, beforeCPMax          int
	beforeBoostMin, beforeBoostMax int
}

// megaCatchCases are the six cards this app builds itself, with the base line read
// off the enclosing pokedex record and the Mega line off the nested form.
//
// Both Raichu forms land on the same CP because you catch a Raichu either way,
// which is the clearest single demonstration that the base line is what is being
// read: their battle lines are 277/203 and 339/157 and could not be further apart.
var megaCatchCases = []megaCatchCase{
	{
		name: "Mega Skarmory", primary: "STEEL", secondary: "FLYING",
		megaAtk: 273, megaDef: 228, megaSta: 163,
		baseAtk: 148, baseDef: 226, baseSta: 163,
		wantTypes: []string{"Steel", "Flying"},
		wantCP:    1139, wantCPMax: 1204, wantBoostMin: 1424, wantBoostMax: 1506,
		beforeCP: 2049, beforeCPMax: 2137, beforeBoostMin: 2561, beforeBoostMax: 2672,
	},
	{
		name: "Mega Starmie", primary: "WATER", secondary: "PSYCHIC",
		megaAtk: 276, megaDef: 229, megaSta: 155,
		baseAtk: 210, baseDef: 184, baseSta: 155,
		wantTypes: []string{"Water", "Psychic"},
		wantCP:    1404, wantCPMax: 1476, wantBoostMin: 1756, wantBoostMax: 1846,
		beforeCP: 2026, beforeCPMax: 2115, beforeBoostMin: 2533, beforeBoostMax: 2644,
	},
	{
		name: "Mega Raichu X", primary: "ELECTRIC",
		megaAtk: 277, megaDef: 203, megaSta: 155,
		baseAtk: 193, baseDef: 151, baseSta: 155,
		wantTypes: []string{"Electric"},
		wantCP:    1180, wantCPMax: 1247, wantBoostMin: 1476, wantBoostMax: 1558,
		beforeCP: 1920, beforeCPMax: 2006, beforeBoostMin: 2400, beforeBoostMax: 2507,
	},
	{
		name: "Mega Raichu Y", primary: "ELECTRIC",
		megaAtk: 339, megaDef: 157, megaSta: 155,
		baseAtk: 193, baseDef: 151, baseSta: 155,
		wantTypes: []string{"Electric"},
		wantCP:    1180, wantCPMax: 1247, wantBoostMin: 1476, wantBoostMax: 1558,
		beforeCP: 2067, beforeCPMax: 2160, beforeBoostMin: 2584, beforeBoostMax: 2700,
	},
	{
		// The one row whose typing actually differs from its base species: base
		// Mewtwo is Psychic alone, so a card built off the base line all the way
		// through would lose the Fighting half and the counter table would rank
		// against the wrong weaknesses. Only the CP fields may use the base line.
		name: "Mega Mewtwo X", primary: "PSYCHIC", secondary: "FIGHTING",
		megaAtk: 399, megaDef: 215, megaSta: 228,
		baseAtk: 300, baseDef: 182, baseSta: 214,
		wantTypes: []string{"Psychic", "Fighting"},
		wantCP:    2294, wantCPMax: 2387,
		beforeCP: 3377, beforeCPMax: 3492,
	},
	{
		name: "Mega Beedrill", primary: "BUG", secondary: "POISON",
		megaAtk: 303, megaDef: 148, megaSta: 163,
		baseAtk: 169, baseDef: 130, baseSta: 163,
		wantTypes: []string{"Bug", "Poison"},
		wantCP:    994, wantCPMax: 1054,
		beforeCP: 1846, beforeCPMax: 1933,
	},
}

// megaCatchImage is the sprite the Mega table offers for one of these, used by the
// image fallback test below.
func megaCatchImage(name string) string {
	return "https://example.invalid/" + strings.ReplaceAll(strings.ToLower(name), " ", "-") + ".png"
}

// megaCatchPokedex shapes the six cases the way pokemon-go-api sends them: a
// pokedex record carrying the BASE species' stats at its top level with the Mega
// nested inside it. Reading that nesting is the whole of the fix, so the fixture
// has to have it.
func megaCatchPokedex() json.RawMessage {
	records := make([]string, 0, len(megaCatchCases))
	for _, c := range megaCatchCases {
		second := "null"
		if c.secondary != "" {
			second = fmt.Sprintf(`{"type":"POKEMON_TYPE_%s"}`, c.secondary)
		}
		records = append(records, fmt.Sprintf(
			`{"stats":{"attack":%d,"defense":%d,"stamina":%d},"megaEvolutions":{"FORM":{`+
				`"names":{"English":%q},"stats":{"attack":%d,"defense":%d,"stamina":%d},`+
				`"primaryType":{"type":"POKEMON_TYPE_%s"},"secondaryType":%s,`+
				`"assets":{"image":%q}}}}`,
			c.baseAtk, c.baseDef, c.baseSta,
			c.name, c.megaAtk, c.megaDef, c.megaSta,
			c.primary, second, megaCatchImage(c.name)))
	}
	return json.RawMessage("[" + strings.Join(records, ",") + "]")
}

// fallbackRaw reads one of the committed fallback blobs, BOM trimmed. testLookup
// does the same thing privately; this is here so a lookup can be built over a
// different Mega table without touching that helper.
func fallbackRaw(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("fallback/" + name)
	if err != nil {
		t.Fatalf("read fallback/%s: %v", name, err)
	}
	return json.RawMessage(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}))
}

// megaCatchLookup is the full chain: parseMegaForms over the fixture pokedex, then
// newSpeciesLookup over it and the committed species data.
func megaCatchLookup(t *testing.T) speciesLookup {
	t.Helper()
	megas := parseMegaForms(megaCatchPokedex())
	if len(megas) != len(megaCatchCases) {
		t.Fatalf("fixture produced %d Mega forms, want %d", len(megas), len(megaCatchCases))
	}
	return newSpeciesLookup(fallbackRaw(t, "pokemon.json"), fallbackRaw(t, "pokemon_types.json"), megas)
}

// TestMegaCardCPIsCaughtAsTheBaseSpecies is the regression: every Mega card this
// app builds was quoting the CP of the thing a trainer had just fought rather than
// the thing they were about to catch. Mega Skarmory read 2049 to 2137 on a Pokemon
// that comes out of the ball at 1139 to 1204, and a trainer reading the card would
// have thrown away perfectly good catches.
//
// If this fails, the card is being built off the wrong stat line again.
func TestMegaCardCPIsCaughtAsTheBaseSpecies(t *testing.T) {
	lookup, cpms := megaCatchLookup(t), testCPMs(t)
	for _, c := range megaCatchCases {
		t.Run(c.name, func(t *testing.T) {
			rb, ok := synthesizeBoss(WindowBoss{Name: c.name}, RaidWindow{Tier: "6"}, lookup, cpms)
			if !ok {
				t.Fatalf("%s could not be synthesized at all", c.name)
			}
			if rb.CP != c.wantCP || rb.CPMax != c.wantCPMax {
				t.Errorf("CP %d-%d, want %d-%d (the base species at level 20)",
					rb.CP, rb.CPMax, c.wantCP, c.wantCPMax)
			}
			if c.wantBoostMin != 0 && (rb.CPBoostedMin != c.wantBoostMin || rb.CPBoostedMax != c.wantBoostMax) {
				t.Errorf("boosted CP %d-%d, want %d-%d (the base species at level 25)",
					rb.CPBoostedMin, rb.CPBoostedMax, c.wantBoostMin, c.wantBoostMax)
			}

			// The fixture's own Mega line still has to reproduce the measured BEFORE
			// column, or these expectations are agreeing with themselves rather than
			// with the game. This is also what proves the assertion above can fail:
			// the two answers are genuinely different numbers.
			gotBefore := CPForLevel(c.megaAtk, c.megaDef, c.megaSta, raidIVFloor, raidIVFloor, raidIVFloor, cpms.Normal)
			gotBeforeMax := CPForLevel(c.megaAtk, c.megaDef, c.megaSta, raidIVCeiling, raidIVCeiling, raidIVCeiling, cpms.Normal)
			if gotBefore != c.beforeCP || gotBeforeMax != c.beforeCPMax {
				t.Errorf("the Mega battle line gives %d-%d, but the measured before column is %d-%d; this fixture no longer describes the card that was live",
					gotBefore, gotBeforeMax, c.beforeCP, c.beforeCPMax)
			}
			if c.beforeBoostMin != 0 {
				gotBoost := CPForLevel(c.megaAtk, c.megaDef, c.megaSta, raidIVFloor, raidIVFloor, raidIVFloor, cpms.Boosted)
				gotBoostMax := CPForLevel(c.megaAtk, c.megaDef, c.megaSta, raidIVCeiling, raidIVCeiling, raidIVCeiling, cpms.Boosted)
				if gotBoost != c.beforeBoostMin || gotBoostMax != c.beforeBoostMax {
					t.Errorf("the Mega battle line gives boosted %d-%d, but the measured before column is %d-%d",
						gotBoost, gotBoostMax, c.beforeBoostMin, c.beforeBoostMax)
				}
			}
			if rb.CP == c.beforeCP {
				t.Errorf("the card still quotes the battle line's %d", c.beforeCP)
			}

			// Typing is the half that must NOT move to the base species.
			if len(rb.Types) != len(c.wantTypes) {
				t.Fatalf("types %v, want the Mega's %v", rb.Types, c.wantTypes)
			}
			for i := range c.wantTypes {
				if rb.Types[i] != c.wantTypes[i] {
					t.Fatalf("types %v, want the Mega's %v", rb.Types, c.wantTypes)
				}
			}
		})
	}

	// Mega Mewtwo X spelled out, because it is the only one of the six whose Mega
	// typing differs from its base species: the CP comes from Mewtwo and the typing
	// must not.
	base, ok := lookup("Mewtwo")
	if !ok {
		t.Fatal("base Mewtwo did not resolve out of the committed species data")
	}
	if len(base.Types) != 1 || base.Types[0] != "Psychic" {
		t.Fatalf("base Mewtwo types %v, want Psychic alone; this assertion has stopped meaning anything", base.Types)
	}
	mx, ok := synthesizeBoss(WindowBoss{Name: "Mega Mewtwo X"}, RaidWindow{Tier: "6"}, lookup, testCPMs(t))
	if !ok {
		t.Fatal("Mega Mewtwo X could not be synthesized")
	}
	if len(mx.Types) != 2 {
		t.Errorf("Mega Mewtwo X types %v, want two: the base species' single Psychic would lose the Fighting half", mx.Types)
	}
}

// TestMegaCatchLineComesFromTheEnclosingPokedexRecord pins where the base line is
// read from. The Mega's own stats block is nested inside the species record, and
// the base line is the record's TOP LEVEL stats: reading the nested one is what
// produced the wrong CP in the first place, and they are adjacent in the payload.
func TestMegaCatchLineComesFromTheEnclosingPokedexRecord(t *testing.T) {
	raw := json.RawMessage(`[
	{"stats":{"stamina":163,"attack":148,"defense":226},
	 "megaEvolutions":{"SKARMORY_MEGA":{
		"names":{"English":"Mega Skarmory"},
		"stats":{"stamina":163,"attack":273,"defense":228},
		"primaryType":{"type":"POKEMON_TYPE_STEEL"},
		"secondaryType":{"type":"POKEMON_TYPE_FLYING"},
		"assets":{"image":"https://example.invalid/mega-skarmory.png"}}}},
	{"megaEvolutions":{"NOBASE_MEGA":{
		"names":{"English":"Mega Nobase"},
		"stats":{"stamina":100,"attack":200,"defense":150},
		"primaryType":{"type":"POKEMON_TYPE_NORMAL"}}}}
	]`)
	forms := parseMegaForms(raw)

	m := forms["mega skarmory"]
	if m.Atk != 273 || m.Def != 228 || m.Sta != 163 {
		t.Errorf("battle line %d/%d/%d, want the nested Mega's 273/228/163", m.Atk, m.Def, m.Sta)
	}
	if m.BaseAtk != 148 || m.BaseDef != 226 || m.BaseSta != 163 {
		t.Errorf("catch line %d/%d/%d, want the record's own 148/226/163", m.BaseAtk, m.BaseDef, m.BaseSta)
	}

	// A record with no top level stats leaves the base line at zero, which
	// catchLine reads as "the same as the battle line" rather than as a CP of 10.
	n := forms["mega nobase"]
	if n.BaseAtk != 0 || n.BaseDef != 0 || n.BaseSta != 0 {
		t.Errorf("a record with no top level stats invented a catch line: %d/%d/%d", n.BaseAtk, n.BaseDef, n.BaseSta)
	}

	// And the lookup carries both lines through, or synthesizeBoss has nothing to
	// choose between.
	lookup := newSpeciesLookup(nil, nil, forms)
	st, ok := lookup("Mega Skarmory")
	if !ok {
		t.Fatal("Mega Skarmory did not resolve out of the Mega table")
	}
	if st.Atk != 273 || st.CatchAtk != 148 {
		t.Errorf("lookup gave battle attack %d and catch attack %d, want 273 and 148", st.Atk, st.CatchAtk)
	}
}

// TestMegaFormWireShapeIsUnchanged: megaForm is served as "megas" on /api/data and
// read by the browser's counter table and by the Android app. The three base stat
// fields are json:"-" precisely so that adding them changed no served byte, and a
// tag quietly dropped in a refactor would ship three new keys to every client.
func TestMegaFormWireShapeIsUnchanged(t *testing.T) {
	data, err := json.Marshal(megaForm{
		Name: "Mega Skarmory", Types: []string{"Steel", "Flying"},
		Atk: 273, Def: 228, Sta: 163, Image: "https://example.invalid/s.png",
		BaseAtk: 148, BaseDef: 226, BaseSta: 163,
	})
	if err != nil {
		t.Fatalf("marshal megaForm: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("unmarshal megaForm: %v", err)
	}
	want := map[string]bool{"name": true, "types": true, "atk": true, "def": true, "sta": true, "image": true}
	for k := range keys {
		if !want[k] {
			t.Errorf("megaForm now serves %q; the wire shape has to stay byte identical", k)
		}
	}
	for k := range want {
		if _, ok := keys[k]; !ok {
			t.Errorf("megaForm no longer serves %q", k)
		}
	}
	// Named explicitly, because these are the three the tag exists for.
	for _, gone := range []string{"baseAtk", "baseDef", "baseSta", "BaseAtk", "BaseDef", "BaseSta"} {
		if _, ok := keys[gone]; ok {
			t.Errorf("%q reached the wire; the json:\"-\" tag is what keeps /api/data unchanged", gone)
		}
	}
	// The Mega table is also served with its image omitted when empty, which is the
	// only omitempty on the struct. A form with no sprite must not gain an "image".
	bare, err := json.Marshal(megaForm{Name: "Mega Nobase", Types: []string{"Normal"}, Atk: 1, Def: 1, Sta: 1})
	if err != nil {
		t.Fatalf("marshal bare megaForm: %v", err)
	}
	if strings.Contains(string(bare), `"image"`) {
		t.Errorf("a Mega with no sprite serves %s", bare)
	}
}

// TestCatchLineFallsBackToTheBattleLine: every ordinary species has no separate
// catch line at all, so a zero in any one of the three fields has to mean "use the
// battle line". Reading a zero literally would put a CP of 10 on every non Mega
// card the synthesizer builds, which is every 5 star boss upstream has not caught
// up to yet.
func TestCatchLineFallsBackToTheBattleLine(t *testing.T) {
	cases := []struct {
		name          string
		st            speciesStats
		atk, def, sta int
	}{
		{"no catch line at all, which is every ordinary species",
			speciesStats{Atk: 210, Def: 184, Sta: 155}, 210, 184, 155},
		{"a full catch line, which is a Mega",
			speciesStats{Atk: 276, Def: 229, Sta: 155, CatchAtk: 210, CatchDef: 184, CatchSta: 155}, 210, 184, 155},
		{"attack missing",
			speciesStats{Atk: 276, Def: 229, Sta: 155, CatchDef: 184, CatchSta: 155}, 276, 229, 155},
		{"defense missing",
			speciesStats{Atk: 276, Def: 229, Sta: 155, CatchAtk: 210, CatchSta: 155}, 276, 229, 155},
		{"stamina missing",
			speciesStats{Atk: 276, Def: 229, Sta: 155, CatchAtk: 210, CatchDef: 184}, 276, 229, 155},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			atk, def, sta := c.st.catchLine()
			if atk != c.atk || def != c.def || sta != c.sta {
				t.Errorf("catchLine() = %d/%d/%d, want %d/%d/%d", atk, def, sta, c.atk, c.def, c.sta)
			}
		})
	}
}

// TestPendingReasonTellsTheTwoMegaFailuresApart: the two are fixed by different
// things and only one of them is ours. "mega stats not loaded" sends an admin to
// refreshMegas, which is right when the table is empty and useless when the table
// already holds sixty one forms and simply does not carry this one. Mega Staraptor
// is the live case: a brand new Mega, debuting on a headline event day, that no
// fetch of ours will ever produce.
func TestPendingReasonTellsTheTwoMegaFailuresApart(t *testing.T) {
	loaded := megaCatchLookup(t)
	empty := newSpeciesLookup(fallbackRaw(t, "pokemon.json"), fallbackRaw(t, "pokemon_types.json"), nil)

	cases := []struct {
		name   string
		boss   string
		lookup speciesLookup
		want   string
	}{
		{"a Mega absent from a loaded table", "Mega Staraptor", loaded, "not in the mega table yet"},
		{"a Primal absent from a loaded table", "Primal Groudon", loaded, "not in the mega table yet"},
		{"a Mega with no table at all", "Mega Skarmory", empty, "mega stats not loaded"},
		{"no species data whatsoever", "Mega Skarmory", nil, "no species data loaded"},
		{"a species no dataset knows", "Notapokemon", loaded, "species unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pendingReason(c.boss, c.lookup); got != c.want {
				t.Errorf("pendingReason(%q) = %q, want %q", c.boss, got, c.want)
			}
		})
	}

	// The flag rides on the zero value of a FAILED lookup, so a hit must never
	// carry it: a Mega that resolved is not evidence that the table is empty.
	if st, ok := loaded("Mega Skarmory"); !ok || st.MegaTableEmpty {
		t.Errorf("a resolved Mega reports ok=%v MegaTableEmpty=%v, want true and false", ok, st.MegaTableEmpty)
	}
	if st, ok := loaded("Mega Staraptor"); ok || st.MegaTableEmpty {
		t.Errorf("a Mega missing from a loaded table reports ok=%v MegaTableEmpty=%v, want false and false", ok, st.MegaTableEmpty)
	}
}

// TestSynthesizeBossFallsBackToTheMegaTableSprite: a boss read out of a page's
// prose carries no sprite, because a sentence has no img tag, and a card with no
// image renders as a hole in the grid. Mega Staraptor is the live example. The
// window's own image still wins whenever it has one, or every habitat roster would
// lose the sprite upstream actually published for it.
func TestSynthesizeBossFallsBackToTheMegaTableSprite(t *testing.T) {
	lookup, cpms := megaCatchLookup(t), testCPMs(t)

	fromTable, ok := synthesizeBoss(WindowBoss{Name: "Mega Beedrill"}, RaidWindow{Tier: "6"}, lookup, cpms)
	if !ok {
		t.Fatal("Mega Beedrill could not be synthesized")
	}
	if want := megaCatchImage("Mega Beedrill"); fromTable.ImageURL != want {
		t.Errorf("image %q, want the Mega table's %q for a boss whose window carried none", fromTable.ImageURL, want)
	}

	const own = "https://cdn.leekduck.com/from-the-page.png"
	fromWindow, ok := synthesizeBoss(WindowBoss{Name: "Mega Beedrill", Image: own}, RaidWindow{Tier: "6"}, lookup, cpms)
	if !ok {
		t.Fatal("Mega Beedrill with its own image could not be synthesized")
	}
	if fromWindow.ImageURL != own {
		t.Errorf("image %q, want the window's own %q", fromWindow.ImageURL, own)
	}

	// An ordinary species has no sprite to offer, so the card is built without one
	// rather than refused. That is the pre-existing behaviour and the fallback must
	// not have changed it.
	plain, ok := synthesizeBoss(WindowBoss{Name: "Lunala"}, RaidWindow{Tier: "5"}, lookup, cpms)
	if !ok {
		t.Fatal("Lunala could not be synthesized")
	}
	if plain.ImageURL != "" {
		t.Errorf("an ordinary species produced image %q from nowhere", plain.ImageURL)
	}
}
