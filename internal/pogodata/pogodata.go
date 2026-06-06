package pogodata

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed fallback/*.json
var fallbackFS embed.FS

// TrainerClass holds a Pokémon game trainer class for the avatar picker.
type TrainerClass struct {
	Slug      string `json:"slug"`
	Label     string `json:"label"`
	SpriteURL string `json:"sprite_url"`
	Group     string `json:"group,omitempty"`
}

const showdownBase = "https://play.pokemonshowdown.com/sprites/trainers/"

// showdownSources maps slug → Showdown URL for the sprite proxy to fetch from.
var showdownSources = map[string]string{}

func init() {
	for _, tc := range builtinTrainerClasses {
		showdownSources[tc.Slug] = tc.SpriteURL
	}
}

var builtinTrainerClasses = []TrainerClass{
	{Slug: "youngster",    Label: "Youngster",    SpriteURL: showdownBase + "youngster.png"},
	{Slug: "lass",         Label: "Lass",          SpriteURL: showdownBase + "lass.png"},
	{Slug: "bug-catcher",  Label: "Bug Catcher",   SpriteURL: showdownBase + "bugcatcher.png"},
	{Slug: "biker",        Label: "Biker",          SpriteURL: showdownBase + "biker.png"},
	{Slug: "blackbelt",    Label: "Black Belt",     SpriteURL: showdownBase + "blackbelt.png"},
	{Slug: "hiker",        Label: "Hiker",          SpriteURL: showdownBase + "hiker.png"},
	{Slug: "fisherman",    Label: "Fisherman",      SpriteURL: showdownBase + "fisherman.png"},
	{Slug: "sailor",       Label: "Sailor",         SpriteURL: showdownBase + "sailor.png"},
	{Slug: "firebreather", Label: "Fire Breather",  SpriteURL: showdownBase + "firebreather.png"},
	{Slug: "birdkeeper",   Label: "Bird Keeper",    SpriteURL: showdownBase + "birdkeeper.png"},
	{Slug: "juggler",      Label: "Juggler",        SpriteURL: showdownBase + "juggler.png"},
	{Slug: "gambler",      Label: "Gambler",        SpriteURL: showdownBase + "gambler.png"},
	{Slug: "burglar",      Label: "Burglar",        SpriteURL: showdownBase + "burglar.png"},
	{Slug: "beauty",       Label: "Beauty",         SpriteURL: showdownBase + "beauty.png"},
	{Slug: "picnicker",    Label: "Picnicker",      SpriteURL: showdownBase + "picnicker.png"},
	{Slug: "camper",       Label: "Camper",         SpriteURL: showdownBase + "camper.png"},
	{Slug: "swimmer",      Label: "Swimmer",        SpriteURL: showdownBase + "swimmer.png"},
	{Slug: "battlegirl",   Label: "Battle Girl",    SpriteURL: showdownBase + "battlegirl.png"},
	{Slug: "dancer",       Label: "Dancer",         SpriteURL: showdownBase + "dancer.png"},
	{Slug: "psychic",      Label: "Psychic",        SpriteURL: showdownBase + "psychic.png"},
	{Slug: "ace-trainer",  Label: "Ace Trainer",    SpriteURL: showdownBase + "acetrainer.png"},
	{Slug: "dragontamer",  Label: "Dragon Tamer",   SpriteURL: showdownBase + "dragontamer.png"},
	{Slug: "pokemaniac",   Label: "Pokemaniac",     SpriteURL: showdownBase + "pokemaniac.png"},
	{Slug: "super-nerd",   Label: "Super Nerd",     SpriteURL: showdownBase + "supernerd.png"},
	{Slug: "gentleman",    Label: "Gentleman",      SpriteURL: showdownBase + "gentleman.png"},
	{Slug: "medium",       Label: "Medium",         SpriteURL: showdownBase + "medium.png"},
	{Slug: "worker",       Label: "Worker",         SpriteURL: showdownBase + "worker.png"},
}

// dreamstoneTrainerClasses are trainer front sprites from Pokémon Dreamstone Mysteries
// (github.com/dsmyst/dreamstone-mysteries), an open-source pokeemerald ROM hack.
// Sprites are stored locally under static/sprites/dreamstone/ and served as static files.
var dreamstoneTrainerClasses = []TrainerClass{
	{Slug: "ds-aqua_admin_f",         Label: "Aqua Admin F (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_admin_f.png"},
	{Slug: "ds-aqua_admin_m",         Label: "Aqua Admin M (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_admin_m.png"},
	{Slug: "ds-aqua_grunt_f",         Label: "Aqua Grunt F (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_grunt_f.png"},
	{Slug: "ds-aqua_grunt_m",         Label: "Aqua Grunt M (DS)",         SpriteURL: "/static/sprites/dreamstone/aqua_grunt_m.png"},
	{Slug: "ds-aqua_leader_archie",   Label: "Aqua Leader Archie (DS)",   SpriteURL: "/static/sprites/dreamstone/aqua_leader_archie.png"},
	{Slug: "ds-arena_tycoon_greta",   Label: "Arena Tycoon Greta (DS)",   SpriteURL: "/static/sprites/dreamstone/arena_tycoon_greta.png"},
	{Slug: "ds-aroma_lady",           Label: "Aroma Lady (DS)",           SpriteURL: "/static/sprites/dreamstone/aroma_lady.png"},
	{Slug: "ds-battle_girl",          Label: "Battle Girl (DS)",          SpriteURL: "/static/sprites/dreamstone/battle_girl.png"},
	{Slug: "ds-beauty",               Label: "Beauty (DS)",               SpriteURL: "/static/sprites/dreamstone/beauty.png"},
	{Slug: "ds-bird_keeper",          Label: "Bird Keeper (DS)",          SpriteURL: "/static/sprites/dreamstone/bird_keeper.png"},
	{Slug: "ds-black_belt",           Label: "Black Belt (DS)",           SpriteURL: "/static/sprites/dreamstone/black_belt.png"},
	{Slug: "ds-brendan",              Label: "Brendan (DS)",              SpriteURL: "/static/sprites/dreamstone/brendan.png"},
	{Slug: "ds-brendan_oras",         Label: "Brendan ORAS (DS)",         SpriteURL: "/static/sprites/dreamstone/brendan_oras.png"},
	{Slug: "ds-brendan_rs",           Label: "Brendan RS (DS)",           SpriteURL: "/static/sprites/dreamstone/brendan_rs.png"},
	{Slug: "ds-bug_catcher",          Label: "Bug Catcher (DS)",          SpriteURL: "/static/sprites/dreamstone/bug_catcher.png"},
	{Slug: "ds-bug_maniac",           Label: "Bug Maniac (DS)",           SpriteURL: "/static/sprites/dreamstone/bug_maniac.png"},
	{Slug: "ds-camper",               Label: "Camper (DS)",               SpriteURL: "/static/sprites/dreamstone/camper.png"},
	{Slug: "ds-champion_wallace",     Label: "Champion Wallace (DS)",     SpriteURL: "/static/sprites/dreamstone/champion_wallace.png"},
	{Slug: "ds-collector",            Label: "Collector (DS)",            SpriteURL: "/static/sprites/dreamstone/collector.png"},
	{Slug: "ds-cooltrainer_f",        Label: "Cool Trainer F (DS)",       SpriteURL: "/static/sprites/dreamstone/cooltrainer_f.png"},
	{Slug: "ds-cooltrainer_m",        Label: "Cool Trainer M (DS)",       SpriteURL: "/static/sprites/dreamstone/cooltrainer_m.png"},
	{Slug: "ds-cycling_triathlete_f", Label: "Cycling Triathlete F (DS)", SpriteURL: "/static/sprites/dreamstone/cycling_triathlete_f.png"},
	{Slug: "ds-cycling_triathlete_m", Label: "Cycling Triathlete M (DS)", SpriteURL: "/static/sprites/dreamstone/cycling_triathlete_m.png"},
	{Slug: "ds-dome_ace_tucker",      Label: "Dome Ace Tucker (DS)",      SpriteURL: "/static/sprites/dreamstone/dome_ace_tucker.png"},
	{Slug: "ds-dragon_tamer",         Label: "Dragon Tamer (DS)",         SpriteURL: "/static/sprites/dreamstone/dragon_tamer.png"},
	{Slug: "ds-elite_four_drake",     Label: "Elite Four Drake (DS)",     SpriteURL: "/static/sprites/dreamstone/elite_four_drake.png"},
	{Slug: "ds-elite_four_glacia",    Label: "Elite Four Glacia (DS)",    SpriteURL: "/static/sprites/dreamstone/elite_four_glacia.png"},
	{Slug: "ds-elite_four_phoebe",    Label: "Elite Four Phoebe (DS)",    SpriteURL: "/static/sprites/dreamstone/elite_four_phoebe.png"},
	{Slug: "ds-elite_four_sidney",    Label: "Elite Four Sidney (DS)",    SpriteURL: "/static/sprites/dreamstone/elite_four_sidney.png"},
	{Slug: "ds-expert_f",             Label: "Expert F (DS)",             SpriteURL: "/static/sprites/dreamstone/expert_f.png"},
	{Slug: "ds-expert_m",             Label: "Expert M (DS)",             SpriteURL: "/static/sprites/dreamstone/expert_m.png"},
	{Slug: "ds-factory_head_noland",  Label: "Factory Head Noland (DS)",  SpriteURL: "/static/sprites/dreamstone/factory_head_noland.png"},
	{Slug: "ds-fisherman",            Label: "Fisherman (DS)",            SpriteURL: "/static/sprites/dreamstone/fisherman.png"},
	{Slug: "ds-gentleman",            Label: "Gentleman (DS)",            SpriteURL: "/static/sprites/dreamstone/gentleman.png"},
	{Slug: "ds-guitarist",            Label: "Guitarist (DS)",            SpriteURL: "/static/sprites/dreamstone/guitarist.png"},
	{Slug: "ds-hex_maniac",           Label: "Hex Maniac (DS)",           SpriteURL: "/static/sprites/dreamstone/hex_maniac.png"},
	{Slug: "ds-hiker",                Label: "Hiker (DS)",                SpriteURL: "/static/sprites/dreamstone/hiker.png"},
	{Slug: "ds-interviewer",          Label: "Interviewer (DS)",          SpriteURL: "/static/sprites/dreamstone/interviewer.png"},
	{Slug: "ds-kindler",              Label: "Kindler (DS)",              SpriteURL: "/static/sprites/dreamstone/kindler.png"},
	{Slug: "ds-lady",                 Label: "Lady (DS)",                 SpriteURL: "/static/sprites/dreamstone/lady.png"},
	{Slug: "ds-lass",                 Label: "Lass (DS)",                 SpriteURL: "/static/sprites/dreamstone/lass.png"},
	{Slug: "ds-leader_brawly",        Label: "Leader Brawly (DS)",        SpriteURL: "/static/sprites/dreamstone/leader_brawly.png"},
	{Slug: "ds-leader_flannery",      Label: "Leader Flannery (DS)",      SpriteURL: "/static/sprites/dreamstone/leader_flannery.png"},
	{Slug: "ds-leader_juan",          Label: "Leader Juan (DS)",          SpriteURL: "/static/sprites/dreamstone/leader_juan.png"},
	{Slug: "ds-leader_norman",        Label: "Leader Norman (DS)",        SpriteURL: "/static/sprites/dreamstone/leader_norman.png"},
	{Slug: "ds-leader_roxanne",       Label: "Leader Roxanne (DS)",       SpriteURL: "/static/sprites/dreamstone/leader_roxanne.png"},
	{Slug: "ds-leader_tate_and_liza", Label: "Leader Tate & Liza (DS)",   SpriteURL: "/static/sprites/dreamstone/leader_tate_and_liza.png"},
	{Slug: "ds-leader_wattson",       Label: "Leader Wattson (DS)",       SpriteURL: "/static/sprites/dreamstone/leader_wattson.png"},
	{Slug: "ds-leader_winona",        Label: "Leader Winona (DS)",        SpriteURL: "/static/sprites/dreamstone/leader_winona.png"},
	{Slug: "ds-leaf",                 Label: "Leaf (DS)",                 SpriteURL: "/static/sprites/dreamstone/leaf.png"},
	{Slug: "ds-magma_admin",          Label: "Magma Admin (DS)",          SpriteURL: "/static/sprites/dreamstone/magma_admin.png"},
	{Slug: "ds-magma_grunt_f",        Label: "Magma Grunt F (DS)",        SpriteURL: "/static/sprites/dreamstone/magma_grunt_f.png"},
	{Slug: "ds-magma_grunt_m",        Label: "Magma Grunt M (DS)",        SpriteURL: "/static/sprites/dreamstone/magma_grunt_m.png"},
	{Slug: "ds-magma_leader_maxie",   Label: "Magma Leader Maxie (DS)",   SpriteURL: "/static/sprites/dreamstone/magma_leader_maxie.png"},
	{Slug: "ds-may",                  Label: "May (DS)",                  SpriteURL: "/static/sprites/dreamstone/may.png"},
	{Slug: "ds-may_oras",             Label: "May ORAS (DS)",             SpriteURL: "/static/sprites/dreamstone/may_oras.png"},
	{Slug: "ds-may_rs",               Label: "May RS (DS)",               SpriteURL: "/static/sprites/dreamstone/may_rs.png"},
	{Slug: "ds-ninja_boy",            Label: "Ninja Boy (DS)",            SpriteURL: "/static/sprites/dreamstone/ninja_boy.png"},
	{Slug: "ds-old_couple",           Label: "Old Couple (DS)",           SpriteURL: "/static/sprites/dreamstone/old_couple.png"},
	{Slug: "ds-palace_maven_spenser", Label: "Palace Maven Spenser (DS)", SpriteURL: "/static/sprites/dreamstone/palace_maven_spenser.png"},
	{Slug: "ds-parasol_lady",         Label: "Parasol Lady (DS)",         SpriteURL: "/static/sprites/dreamstone/parasol_lady.png"},
	{Slug: "ds-picnicker",            Label: "Picnicker (DS)",            SpriteURL: "/static/sprites/dreamstone/picnicker.png"},
	{Slug: "ds-pike_queen_lucy",      Label: "Pike Queen Lucy (DS)",      SpriteURL: "/static/sprites/dreamstone/pike_queen_lucy.png"},
	{Slug: "ds-pokefan_f",            Label: "Pokefan F (DS)",            SpriteURL: "/static/sprites/dreamstone/pokefan_f.png"},
	{Slug: "ds-pokefan_m",            Label: "Pokefan M (DS)",            SpriteURL: "/static/sprites/dreamstone/pokefan_m.png"},
	{Slug: "ds-pokemaniac",           Label: "Pokemaniac (DS)",           SpriteURL: "/static/sprites/dreamstone/pokemaniac.png"},
	{Slug: "ds-pokemon_breeder_f",    Label: "Pokemon Breeder F (DS)",    SpriteURL: "/static/sprites/dreamstone/pokemon_breeder_f.png"},
	{Slug: "ds-pokemon_breeder_m",    Label: "Pokemon Breeder M (DS)",    SpriteURL: "/static/sprites/dreamstone/pokemon_breeder_m.png"},
	{Slug: "ds-pokemon_ranger_f",     Label: "Pokemon Ranger F (DS)",     SpriteURL: "/static/sprites/dreamstone/pokemon_ranger_f.png"},
	{Slug: "ds-pokemon_ranger_m",     Label: "Pokemon Ranger M (DS)",     SpriteURL: "/static/sprites/dreamstone/pokemon_ranger_m.png"},
	{Slug: "ds-psychic_f",            Label: "Psychic F (DS)",            SpriteURL: "/static/sprites/dreamstone/psychic_f.png"},
	{Slug: "ds-psychic_m",            Label: "Psychic M (DS)",            SpriteURL: "/static/sprites/dreamstone/psychic_m.png"},
	{Slug: "ds-pyramid_king_brandon", Label: "Pyramid King Brandon (DS)", SpriteURL: "/static/sprites/dreamstone/pyramid_king_brandon.png"},
	{Slug: "ds-red",                  Label: "Red (DS)",                  SpriteURL: "/static/sprites/dreamstone/red.png"},
	{Slug: "ds-rich_boy",             Label: "Rich Boy (DS)",             SpriteURL: "/static/sprites/dreamstone/rich_boy.png"},
	{Slug: "ds-ruin_maniac",          Label: "Ruin Maniac (DS)",          SpriteURL: "/static/sprites/dreamstone/ruin_maniac.png"},
	{Slug: "ds-running_triathlete_f", Label: "Running Triathlete F (DS)", SpriteURL: "/static/sprites/dreamstone/running_triathlete_f.png"},
	{Slug: "ds-running_triathlete_m", Label: "Running Triathlete M (DS)", SpriteURL: "/static/sprites/dreamstone/running_triathlete_m.png"},
	{Slug: "ds-sailor",               Label: "Sailor (DS)",               SpriteURL: "/static/sprites/dreamstone/sailor.png"},
	{Slug: "ds-salon_maiden_anabel",  Label: "Salon Maiden Anabel (DS)",  SpriteURL: "/static/sprites/dreamstone/salon_maiden_anabel.png"},
	{Slug: "ds-school_kid_f",         Label: "School Kid F (DS)",         SpriteURL: "/static/sprites/dreamstone/school_kid_f.png"},
	{Slug: "ds-school_kid_m",         Label: "School Kid M (DS)",         SpriteURL: "/static/sprites/dreamstone/school_kid_m.png"},
	{Slug: "ds-sis_and_bro",          Label: "Sis & Bro (DS)",            SpriteURL: "/static/sprites/dreamstone/sis_and_bro.png"},
	{Slug: "ds-somber_grunt_f",       Label: "Somber Grunt F (DS)",       SpriteURL: "/static/sprites/dreamstone/somber_grunt_f.png"},
	{Slug: "ds-sr_and_jr",            Label: "Sr. & Jr. (DS)",            SpriteURL: "/static/sprites/dreamstone/sr_and_jr.png"},
	{Slug: "ds-steven",               Label: "Steven (DS)",               SpriteURL: "/static/sprites/dreamstone/steven.png"},
	{Slug: "ds-swimmer_f",            Label: "Swimmer F (DS)",            SpriteURL: "/static/sprites/dreamstone/swimmer_f.png"},
	{Slug: "ds-swimmer_m",            Label: "Swimmer M (DS)",            SpriteURL: "/static/sprites/dreamstone/swimmer_m.png"},
	{Slug: "ds-swimming_triathlete_f",Label: "Swimming Triathlete F (DS)",SpriteURL: "/static/sprites/dreamstone/swimming_triathlete_f.png"},
	{Slug: "ds-swimming_triathlete_m",Label: "Swimming Triathlete M (DS)",SpriteURL: "/static/sprites/dreamstone/swimming_triathlete_m.png"},
	{Slug: "ds-tuber_f",              Label: "Tuber F (DS)",              SpriteURL: "/static/sprites/dreamstone/tuber_f.png"},
	{Slug: "ds-tuber_m",              Label: "Tuber M (DS)",              SpriteURL: "/static/sprites/dreamstone/tuber_m.png"},
	{Slug: "ds-twins",                Label: "Twins (DS)",                SpriteURL: "/static/sprites/dreamstone/twins.png"},
	{Slug: "ds-wally",                Label: "Wally (DS)",                SpriteURL: "/static/sprites/dreamstone/wally.png"},
	{Slug: "ds-young_couple",         Label: "Young Couple (DS)",         SpriteURL: "/static/sprites/dreamstone/young_couple.png"},
	{Slug: "ds-youngster",            Label: "Youngster (DS)",            SpriteURL: "/static/sprites/dreamstone/youngster.png"},
}

// ScrapedDuck (github.com/bigfoott/ScrapedDuck) scrapes LeekDuck and
// publishes clean JSON on every game event update.
type scrapedTypeEntry struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
}

type scrapedCPRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type scrapedBoss struct {
	Name        string          `json:"name"`
	Image       string          `json:"image"`
	Types       json.RawMessage `json:"types"`
	Tier        string          `json:"tier"`
	CanBeShiny  bool            `json:"canBeShiny"`
	CombatPower struct {
		Normal  scrapedCPRange `json:"normal"`
		Boosted scrapedCPRange `json:"boosted"`
	} `json:"combatPower"`
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// parseTypes handles []string, [{"type":"Fire"}], and [{"name":"fire","image":"..."}]
func parseTypes(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var strs []string
	if json.Unmarshal(raw, &strs) == nil {
		for i, s := range strs {
			strs[i] = capitalize(s)
		}
		return strs
	}
	var objs []scrapedTypeEntry
	if json.Unmarshal(raw, &objs) == nil {
		out := make([]string, 0, len(objs))
		for _, o := range objs {
			if o.Name != "" {
				out = append(out, capitalize(o.Name))
			} else if o.Type != "" {
				out = append(out, capitalize(o.Type))
			}
		}
		return out
	}
	return nil
}

type raidBoss struct {
	PokemonName   string   `json:"pokemon_name"`
	CP            int      `json:"cp"`
	CPMax         int      `json:"cp_max,omitempty"`
	CPBoostedMin  int      `json:"cp_boosted_min,omitempty"`
	CPBoostedMax  int      `json:"cp_boosted_max,omitempty"`
	ImageURL      string   `json:"image_url,omitempty"`
	Types         []string `json:"types,omitempty"`
	CanBeShiny    bool     `json:"can_be_shiny,omitempty"`
}

func tierKey(t string) string {
	lower := strings.ToLower(t)
	switch {
	case strings.Contains(lower, "mega") || strings.Contains(lower, "primal"):
		return "6"
	case strings.Contains(lower, "legendary") || strings.Contains(lower, "5"):
		return "5"
	case strings.Contains(lower, "3"):
		return "3"
	case strings.Contains(lower, "1"):
		return "1"
	default:
		return "5"
	}
}

// ── Store ─────────────────────────────────────────────────────

// PokemonEntry holds a Pokémon's name and national Pokédex ID for sprite resolution.
type PokemonEntry struct {
	Name string
	ID   int
}

// pokemonNameEntry mirrors one entry in PoGoAPI's pokemon_names.json per-id array.
type pokemonNameEntry struct {
	LanguageID int    `json:"language_id"`
	Name       string `json:"name"`
}

// langIDToCode maps PoGoAPI language_id values to our supported locale codes.
var langIDToCode = map[int]string{
	5: "fr",
	6: "de",
	7: "es",
}

type Store struct {
	mu                sync.RWMutex
	raids             json.RawMessage
	pokemon           json.RawMessage
	pokemonMoves      json.RawMessage
	fastMoves         json.RawMessage
	chargedMoves      json.RawMessage
	shinies           json.RawMessage
	shadowPokemon     json.RawMessage
	typeChart         json.RawMessage
	cpMults           json.RawMessage
	pokemonTypes      json.RawMessage
	pokemonIDMap      map[string]int
	pokemonNamesById  map[int]map[string]string // dex ID → {lang_code → translated name}
	trainerClasses    []TrainerClass
	spriteCache       sync.Map
	client            *http.Client
	cacheDir          string
}

func New() *Store {
	classes := make([]TrainerClass, 0, len(builtinTrainerClasses)+len(dreamstoneTrainerClasses))
	for _, tc := range builtinTrainerClasses {
		classes = append(classes, TrainerClass{Slug: tc.Slug, Label: tc.Label, SpriteURL: "/api/trainer-sprite/" + tc.Slug, Group: "classic"})
	}
	for _, tc := range dreamstoneTrainerClasses {
		tc.Group = "dreamstone"
		classes = append(classes, tc)
	}
	cacheDir := os.Getenv("CACHE_DIR")
	if cacheDir == "" {
		cacheDir = "cache"
	}
	os.MkdirAll(cacheDir, 0755)
	return &Store{
		client:         &http.Client{Timeout: 15 * time.Second},
		trainerClasses: classes,
		cacheDir:       cacheDir,
	}
}

// cachedFetch fetches a URL and writes the result to disk cache on success.
func (s *Store) cachedFetch(key, url string) (json.RawMessage, error) {
	data, err := s.fetch(url)
	if err != nil {
		return nil, err
	}
	os.WriteFile(filepath.Join(s.cacheDir, key+".json"), data, 0644)
	return data, nil
}

// loadFromCache reads any previously saved JSON blobs from disk into the store.
// Fast and synchronous — called before the HTTP server starts listening.
func (s *Store) loadFromCache() {
	keys := []string{"raids", "pokemon", "pokemon_moves", "fast_moves", "charged_moves", "shinies", "shadow_pokemon", "type_chart", "cp_multipliers", "pokemon_types", "pokemon_names"}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range keys {
		data, err := os.ReadFile(filepath.Join(s.cacheDir, key+".json"))
		if err != nil {
			continue
		}
		log.Printf("pogodata: %s: loaded from disk cache", key)
		s.applyResult(key, json.RawMessage(data))
	}
}

// applyResult writes a single data blob into the in-memory store. Caller must hold s.mu.
func (s *Store) applyResult(key string, data json.RawMessage) {
	switch key {
	case "raids":
		s.raids = data
	case "pokemon":
		s.pokemon = data
		var arr []struct {
			Name string `json:"pokemon_name"`
			ID   int    `json:"pokemon_id"`
		}
		if err := json.Unmarshal(data, &arr); err == nil && len(arr) > 0 {
			idMap := make(map[string]int, len(arr))
			for _, e := range arr {
				idMap[e.Name] = e.ID
			}
			s.pokemonIDMap = idMap
		} else {
			var obj map[string]struct {
				ID int `json:"pokemon_id"`
			}
			if err2 := json.Unmarshal(data, &obj); err2 == nil && len(obj) > 0 {
				idMap := make(map[string]int, len(obj))
				for name, e := range obj {
					idMap[name] = e.ID
				}
				s.pokemonIDMap = idMap
			} else {
				log.Printf("pogodata: pokemonIDMap: could not parse pokemon data (arr=%v obj=%v)", err, err2)
			}
		}
	case "pokemon_moves":
		s.pokemonMoves = data
	case "fast_moves":
		s.fastMoves = data
	case "charged_moves":
		s.chargedMoves = data
	case "shinies":
		s.shinies = data
	case "shadow_pokemon":
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(data, &raw); err == nil {
			names := make([]string, 0, len(raw))
			for name := range raw {
				names = append(names, name)
			}
			if encoded, err := json.Marshal(names); err == nil {
				s.shadowPokemon = encoded
			}
		}
	case "type_chart":
		s.typeChart = data
	case "cp_multipliers":
		s.cpMults = data
	case "pokemon_types":
		var arr []struct {
			Form  string   `json:"form"`
			Name  string   `json:"pokemon_name"`
			Types []string `json:"type"`
		}
		if err := json.Unmarshal(data, &arr); err != nil {
			log.Printf("pogodata: pokemon_types: parse error: %v", err)
			break
		}
		typeMap := make(map[string][]string)
		for _, entry := range arr {
			if _, exists := typeMap[entry.Name]; !exists || entry.Form == "Normal" {
				typeMap[entry.Name] = entry.Types
			}
		}
		if encoded, err := json.Marshal(typeMap); err == nil {
			s.pokemonTypes = encoded
		}
	case "pokemon_names":
		// PoGoAPI returns a top-level map keyed by Pokémon ID string.
		// The per-ID value has varied across API versions; we handle all known shapes:
		//   A: [{"language_id":5,"name":"Bulbizarre"}, ...]          (array of structs)
		//   B: {"5":{"language_id":5,"name":"Bulbizarre"}, ...}      (map of lang_id → struct)
		//   C: {"5":"Bulbizarre", "6":"Bisasam", ...}                (map of lang_id → string)
		//   D: {"language_id":5,"name":"Bulbizarre"}                 (single struct)
		var rawTop map[string]json.RawMessage
		if err := json.Unmarshal(data, &rawTop); err != nil {
			log.Printf("pogodata: pokemon_names: parse error: %v", err)
			break
		}
		nameMap := make(map[int]map[string]string, len(rawTop))
		loggedFormat := false
		for idStr, inner := range rawTop {
			var id int
			fmt.Sscanf(idStr, "%d", &id)
			langs := make(map[string]string)

			// Format A: array of structs
			var arr []pokemonNameEntry
			if json.Unmarshal(inner, &arr) == nil {
				if !loggedFormat { log.Printf("pogodata: pokemon_names: format=A (array of structs)"); loggedFormat = true }
				for _, e := range arr {
					if code, ok := langIDToCode[e.LanguageID]; ok {
						langs[code] = e.Name
					}
				}
			} else if inner[0] == '{' {
				// Format B: map of lang_id_str → struct
				var mEntry map[string]pokemonNameEntry
				if json.Unmarshal(inner, &mEntry) == nil {
					if !loggedFormat { log.Printf("pogodata: pokemon_names: format=B (map of structs)"); loggedFormat = true }
					for _, e := range mEntry {
						if code, ok := langIDToCode[e.LanguageID]; ok {
							langs[code] = e.Name
						}
					}
				} else {
					// Format C: map of lang_id_str → name string
					var mStr map[string]string
					if json.Unmarshal(inner, &mStr) == nil {
						if !loggedFormat { log.Printf("pogodata: pokemon_names: format=C (map of strings)"); loggedFormat = true }
						for langIDStr, name := range mStr {
							var langID int
							fmt.Sscanf(langIDStr, "%d", &langID)
							if code, ok := langIDToCode[langID]; ok {
								langs[code] = name
							}
						}
					} else {
						// Format D: single struct
						var single pokemonNameEntry
						if json.Unmarshal(inner, &single) == nil {
							if !loggedFormat { log.Printf("pogodata: pokemon_names: format=D (single struct)"); loggedFormat = true }
							if code, ok := langIDToCode[single.LanguageID]; ok {
								langs[code] = single.Name
							}
						}
					}
				}
			}

			if len(langs) > 0 {
				nameMap[id] = langs
			}
		}
		if !loggedFormat {
			log.Printf("pogodata: pokemon_names: unknown format, sample: %s", string(data[:min(200, len(data))]))
		}
		s.pokemonNamesById = nameMap
	}
}

// loadFallback loads the JSON files embedded in the binary at compile time.
// This guarantees the store always has baseline data even with no network and no cache.
func (s *Store) loadFallback() {
	keyToFile := map[string]string{
		"pokemon":        "fallback/pokemon.json",
		"pokemon_moves":  "fallback/pokemon_moves.json",
		"fast_moves":     "fallback/fast_moves.json",
		"charged_moves":  "fallback/charged_moves.json",
		"shinies":        "fallback/shinies.json",
		"shadow_pokemon": "fallback/shadow_pokemon.json",
		"type_chart":     "fallback/type_chart.json",
		"cp_multipliers": "fallback/cp_multipliers.json",
		"pokemon_types":  "fallback/pokemon_types.json",
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, path := range keyToFile {
		data, err := fallbackFS.ReadFile(path)
		if err != nil {
			log.Printf("pogodata: %s: embedded fallback missing: %v", key, err)
			continue
		}
		s.applyResult(key, json.RawMessage(data))
	}
	log.Println("pogodata: loaded embedded fallback data")
}

func (s *Store) Start() {
	s.loadFallback()    // always works: data is compiled into the binary
	s.loadFromCache()   // overlay with anything fresher saved to disk
	go s.refresh()      // try PoGoAPI in background
	go s.refreshRaids() // try ScrapedDuck raids in background
	go func() {
		for range time.NewTicker(6 * time.Hour).C {
			s.refresh()
		}
	}()
	go s.scheduleRaidRefresh() // daily refresh at noon Mountain Time
}

func (s *Store) refresh() {
	type ep struct {
		key  string
		urls []string
	}
	endpoints := []ep{
		{"pokemon",       []string{"https://pogoapi.net/api/v1/pokemon_stats.json"}},
		{"pokemon_moves", []string{"https://pogoapi.net/api/v1/current_pokemon_moves.json"}},
		{"fast_moves",    []string{"https://pogoapi.net/api/v1/fast_moves.json"}},
		{"charged_moves", []string{"https://pogoapi.net/api/v1/charged_moves.json"}},
		{"shinies",       []string{"https://pogoapi.net/api/v1/shiny_pokemon.json"}},
		{"shadow_pokemon", []string{"https://pogoapi.net/api/v1/shadow_pokemon.json"}},
		{"type_chart",    []string{"https://pogoapi.net/api/v1/type_effectiveness.json"}},
		{"cp_multipliers", []string{"https://pogoapi.net/api/v1/cp_multiplier.json"}},
		{"pokemon_types",  []string{"https://pogoapi.net/api/v1/pokemon_types.json"}},
		{"pokemon_names",  []string{"https://pogoapi.net/api/v1/pokemon_names.json"}},
	}

	results := make(map[string]json.RawMessage, len(endpoints))

	// PoGoAPI endpoints: sequential with 400 ms delay to avoid rate limiting.
	for i, e := range endpoints {
		if i > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		var data json.RawMessage
		var lastErr error
		for _, url := range e.urls {
			data, lastErr = s.cachedFetch(e.key, url)
			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			log.Printf("pogodata: %s: all URLs failed", e.key)
		}
		if data != nil {
			results[e.key] = data
		}
	}

	s.mu.Lock()
	for key, data := range results {
		s.applyResult(key, data)
	}
	s.mu.Unlock()
	log.Println("pogodata: refresh complete")
}

// refreshRaids fetches current raid bosses from ScrapedDuck and writes to disk cache.
func (s *Store) refreshRaids() {
	data, err := s.fetchScrapedDuckRaids()
	if err != nil {
		log.Printf("pogodata: raids refresh: %v", err)
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, "raids.json"), data, 0644)
	s.mu.Lock()
	s.applyResult("raids", data)
	s.mu.Unlock()
}

// scheduleRaidRefresh loops forever, sleeping until the next scheduled refresh
// time in Mountain Time (DST-aware via America/Denver), then fetching raids.
// Schedule: 12:01 AM, 4:00 AM, 8:00 AM, 12:01 PM, 4:00 PM, 8:00 PM.
func (s *Store) scheduleRaidRefresh() {
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		log.Printf("pogodata: scheduleRaidRefresh: %v", err)
		return
	}
	type target struct{ h, m int }
	targets := []target{{0, 1}, {4, 0}, {8, 0}, {12, 1}, {16, 0}, {20, 0}}
	for {
		now := time.Now().In(loc)
		y, mo, d := now.Date()
		var next time.Time
		for _, t := range targets {
			candidate := time.Date(y, mo, d, t.h, t.m, 0, 0, loc)
			if now.Before(candidate) {
				next = candidate
				break
			}
		}
		if next.IsZero() {
			next = time.Date(y, mo, d+1, targets[0].h, targets[0].m, 0, 0, loc)
		}
		log.Printf("pogodata: raids: next scheduled refresh at %s", next.Format("2006-01-02 15:04 MST"))
		time.Sleep(time.Until(next))
		s.refreshRaids()
	}
}

func (s *Store) fetchScrapedDuckRaids() (json.RawMessage, error) {
	urls := []string{
		"https://raw.githubusercontent.com/bigfoott/ScrapedDuck/data/raids.json",
		"https://raw.githubusercontent.com/bigfoott/ScrapedDuck/main/data/raids.json",
		"https://raw.githubusercontent.com/bigfoott/ScrapedDuck/refs/heads/data/raids.json",
		"https://raw.githubusercontent.com/bigfoott/ScrapedDuck/master/data/raids.json",
	}

	for _, url := range urls {
		raw, err := s.fetch(url)
		if err != nil {
			log.Printf("pogodata: raids @ %s: %v", url, err)
			continue
		}

		var bosses []scrapedBoss
		if err := json.Unmarshal(raw, &bosses); err != nil {
			log.Printf("pogodata: raids parse @ %s: %v", url, err)
			continue
		}
		if len(bosses) == 0 {
			continue
		}

		grouped := make(map[string][]raidBoss)
		for _, b := range bosses {
			key := tierKey(b.Tier)
			grouped[key] = append(grouped[key], raidBoss{
				PokemonName:  b.Name,
				CP:           b.CombatPower.Normal.Min,
				CPMax:        b.CombatPower.Normal.Max,
				CPBoostedMin: b.CombatPower.Boosted.Min,
				CPBoostedMax: b.CombatPower.Boosted.Max,
				ImageURL:     b.Image,
				Types:        parseTypes(b.Types),
				CanBeShiny:   b.CanBeShiny,
			})
		}

		out, err := json.Marshal(grouped)
		if err != nil {
			return nil, err
		}
		log.Printf("pogodata: raids: fetched %d bosses from ScrapedDuck", len(bosses))
		return out, nil
	}

	return nil, fmt.Errorf("all ScrapedDuck URLs failed")
}

func (s *Store) fetch(url string) (json.RawMessage, error) {
	resp, err := s.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 120))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Refresh triggers an immediate data re-fetch outside the normal 6h schedule.
func (s *Store) Refresh() { go s.refresh() }

// ── Accessors ─────────────────────────────────────────────────

func (s *Store) Raids() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.raids
}
func (s *Store) Pokemon() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.pokemon
}
func (s *Store) PokemonMoves() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.pokemonMoves
}
func (s *Store) Moves() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock()
	type combined struct {
		Fast    json.RawMessage `json:"fast"`
		Charged json.RawMessage `json:"charged"`
	}
	data, _ := json.Marshal(combined{Fast: s.fastMoves, Charged: s.chargedMoves})
	return data
}
func (s *Store) TrainerClasses() []TrainerClass {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.trainerClasses
}
func (s *Store) TrainerSpriteSourceURL(slug string) string { return showdownSources[slug] }
// PokemonDexID returns the national Pokédex ID for the given Pokémon name (0 if unknown).
func (s *Store) PokemonDexID(name string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pokemonIDMap[name]
}

// PokemonList returns all known Pokémon sorted by name, for the settings autocomplete list.
func (s *Store) PokemonList() []PokemonEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PokemonEntry, 0, len(s.pokemonIDMap))
	for name, id := range s.pokemonIDMap {
		out = append(out, PokemonEntry{Name: name, ID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) PokemonTypes() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.pokemonTypes
}
func (s *Store) SpriteCacheGet(slug string) ([]byte, bool) {
	v, ok := s.spriteCache.Load(slug)
	if !ok {
		return nil, false
	}
	return v.([]byte), true
}
func (s *Store) SpriteCacheSet(slug string, data []byte) { s.spriteCache.Store(slug, data) }
func (s *Store) AllData() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock()
	type all struct {
		Pokemon       json.RawMessage            `json:"pokemon"`
		PokemonMoves  json.RawMessage            `json:"pokemonMoves"`
		FastMoves     json.RawMessage            `json:"fastMoves"`
		ChargedMoves  json.RawMessage            `json:"chargedMoves"`
		Raids         json.RawMessage            `json:"raids"`
		Shinies       json.RawMessage            `json:"shinies"`
		ShadowPokemon json.RawMessage            `json:"shadowPokemon"`
		TypeChart     json.RawMessage            `json:"typeChart"`
		CPMultipliers json.RawMessage            `json:"cpMultipliers"`
		PokemonTypes  json.RawMessage            `json:"pokemonTypes"`
		PokemonNames  map[string]map[string]string `json:"pokemonNames,omitempty"`
	}
	// Build englishName → {lang_code → translated} from pokemonNamesById + pokemonIDMap.
	var pokemonNames map[string]map[string]string
	if len(s.pokemonNamesById) > 0 && len(s.pokemonIDMap) > 0 {
		pokemonNames = make(map[string]map[string]string, len(s.pokemonIDMap))
		for englishName, id := range s.pokemonIDMap {
			if langs, ok := s.pokemonNamesById[id]; ok && len(langs) > 0 {
				pokemonNames[englishName] = langs
			}
		}
	}
	data, _ := json.Marshal(all{
		Pokemon:       s.pokemon,
		PokemonMoves:  s.pokemonMoves,
		FastMoves:     s.fastMoves,
		ChargedMoves:  s.chargedMoves,
		Raids:         s.raids,
		Shinies:       s.shinies,
		ShadowPokemon: s.shadowPokemon,
		TypeChart:     s.typeChart,
		CPMultipliers: s.cpMults,
		PokemonTypes:  s.pokemonTypes,
		PokemonNames:  pokemonNames,
	})
	return data
}
