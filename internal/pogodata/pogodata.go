package pogodata

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// pokemon-go-api (github.com/pokemon-go-api/pokemon-go-api) aggregates LeekDuck raid
// bosses and snacknap Max Battles into clean JSON, refreshed regularly. Both the
// raidboss and maxbattles endpoints share this entry shape under "currentList".
type pgapiList struct {
	CurrentList json.RawMessage `json:"currentList"`
}

type pgapiBoss struct {
	Names struct {
		English string `json:"English"`
	} `json:"names"`
	Assets struct {
		Image string `json:"image"`
	} `json:"assets"`
	Shiny        bool     `json:"shiny"`
	Types        []string `json:"types"`
	CPRange      []int    `json:"cpRange"`
	CPRangeBoost []int    `json:"cpRangeBoost"`
}

// bossFromPGAPI maps a pokemon-go-api entry onto our raidBoss shape. Shadow tiers carry
// the plain species name, so prefix "Shadow " to preserve the prior display behavior.
func bossFromPGAPI(b pgapiBoss, shadow bool) raidBoss {
	name := b.Names.English
	if shadow {
		name = "Shadow " + name
	}
	rb := raidBoss{
		PokemonName: name,
		ImageURL:    b.Assets.Image,
		Types:       b.Types,
		CanBeShiny:  b.Shiny,
	}
	if len(b.CPRange) == 2 {
		rb.CP, rb.CPMax = b.CPRange[0], b.CPRange[1]
	}
	if len(b.CPRangeBoost) == 2 {
		rb.CPBoostedMin, rb.CPBoostedMax = b.CPRangeBoost[0], b.CPRangeBoost[1]
	}
	return rb
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

type PokemonEntry struct {
	Name string
	ID   int
}

// pokemonNameEntry mirrors one entry in PoGoAPI's pokemon_names.json per-id array.
type pokemonNameEntry struct {
	LanguageID int    `json:"language_id"`
	Name       string `json:"name"`
}

// langIDToCode maps PokeAPI local_language_id values (also used by the legacy
// PoGoAPI payloads) to our supported locale codes.
var langIDToCode = map[int]string{
	5:  "fr",
	6:  "de",
	7:  "es",
	11: "ja",
}

// supportedLangCodes is the set of locale codes we accept in the cached
// pokemon_names.json payload, derived from langIDToCode.
var supportedLangCodes = func() map[string]bool {
	m := make(map[string]bool, len(langIDToCode))
	for _, code := range langIDToCode {
		m[code] = true
	}
	return m
}()

type Store struct {
	mu                sync.RWMutex
	raids             json.RawMessage
	maxBattles        json.RawMessage
	events            json.RawMessage
	eventDetails      map[string]eventDetail
	detailsRunning    atomic.Bool
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
		eventDetails:   make(map[string]eventDetail),
	}
}

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
	keys := []string{"raids", "max_battles", "events", "pokemon", "pokemon_moves", "fast_moves", "charged_moves", "shinies", "shadow_pokemon", "type_chart", "cp_multipliers", "pokemon_types", "pokemon_names"}
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
	s.loadEventDetailsFromDisk()
}

// applyResult writes a single data blob into the in-memory store. Caller must hold s.mu.
func (s *Store) applyResult(key string, data json.RawMessage) {
	switch key {
	case "raids":
		s.raids = data
	case "max_battles":
		s.maxBattles = data
	case "events":
		s.events = data
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
		// Top-level map keyed by Pokémon ID string. The primary shape is what
		// refreshPokemonNames writes from PokeAPI's species names CSV:
		//   {"1":{"fr":"Bulbizarre","de":"Bisasam","es":"Bulbasaur","ja":"フシギダネ"}, ...}
		// Legacy PoGoAPI per-ID shapes are still accepted so an old cached file parses:
		//   A: [{"language_id":5,"name":"Bulbizarre"}, ...]          (array of structs)
		//   B: {"5":{"language_id":5,"name":"Bulbizarre"}, ...}      (map of lang_id → struct)
		//   C: {"5":"Bulbizarre", "6":"Bisasam", ...}                (map of lang_id → string)
		//   D: {"language_id":5,"name":"Bulbizarre"}                 (single struct)
		// The primary shape and C share Go types; numeric inner keys mean C.
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
					// Primary shape (lang code keys) or format C (numeric lang id keys).
					var mStr map[string]string
					if json.Unmarshal(inner, &mStr) == nil {
						if !loggedFormat { log.Printf("pogodata: pokemon_names: format=map of strings (lang codes or ids)"); loggedFormat = true }
						for k, name := range mStr {
							if langID, err := strconv.Atoi(k); err == nil {
								if code, ok := langIDToCode[langID]; ok {
									langs[code] = name
								}
							} else if supportedLangCodes[k] {
								langs[k] = name
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
		if len(nameMap) == 0 && len(s.pokemonNamesById) > 0 {
			log.Printf("pogodata: pokemon_names: empty result, keeping existing %d entries", len(s.pokemonNamesById))
			break
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
	go s.refresh()          // try PoGoAPI in background
	go s.refreshRaids()     // try pokemon-go-api raids in background
	go s.refreshMaxBattles() // try pokemon-go-api max battles in background
	go s.refreshEvents()     // try ScrapedDuck events in background
	go s.refreshPokemonNames() // try PokeAPI localized names in background
	go func() {
		for range time.NewTicker(6 * time.Hour).C {
			s.refresh()
			s.refreshPokemonNames()
		}
	}()
	go func() {
		// ScrapedDuck asks for at least 5 minutes between fetches; 30 is plenty.
		for range time.NewTicker(30 * time.Minute).C {
			s.refreshEvents()
		}
	}()
	go s.scheduleRaidRefresh() // raids + max battles refresh on Mountain Time schedule
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

// refreshRaids fetches current raid bosses from pokemon-go-api and writes to disk cache.
func (s *Store) refreshRaids() {
	data, err := s.fetchPGAPIRaids()
	if err != nil {
		log.Printf("pogodata: raids refresh: %v", err)
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, "raids.json"), data, 0644)
	s.mu.Lock()
	s.applyResult("raids", data)
	s.mu.Unlock()
}

// refreshMaxBattles fetches current Max Battle bosses from pokemon-go-api and writes to
// disk cache.
func (s *Store) refreshMaxBattles() {
	data, err := s.fetchMaxBattles()
	if err != nil {
		log.Printf("pogodata: max battles refresh: %v", err)
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, "max_battles.json"), data, 0644)
	s.mu.Lock()
	s.applyResult("max_battles", data)
	s.mu.Unlock()
}

// refreshEvents fetches current and upcoming events from ScrapedDuck (LeekDuck data)
// and writes to disk cache. On failure the last good payload is kept.
func (s *Store) refreshEvents() {
	data, err := s.cachedFetch("events", "https://raw.githubusercontent.com/bigfoott/ScrapedDuck/data/events.min.json")
	if err != nil {
		log.Printf("pogodata: events refresh: %v", err)
		return
	}
	s.mu.Lock()
	s.applyResult("events", data)
	s.mu.Unlock()
	log.Println("pogodata: events refresh complete")
	s.refreshEventDetails(data)
}

// refreshPokemonNames fetches localized species names from PokeAPI's CSV dataset,
// converts them to the JSON shape {"1":{"fr":"Bulbizarre",...},...}, and writes
// to disk cache. PoGoAPI's pokemon_names.json no longer carries language data,
// so this is the sole source of translated Pokémon names. On failure the last
// good payload is kept.
func (s *Store) refreshPokemonNames() {
	const url = "https://raw.githubusercontent.com/PokeAPI/pokeapi/master/data/v2/csv/pokemon_species_names.csv"
	resp, err := s.client.Get(url)
	if err != nil {
		log.Printf("pogodata: pokemon names refresh: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("pogodata: pokemon names refresh: HTTP %d", resp.StatusCode)
		return
	}
	names, err := parsePokemonNamesCSV(resp.Body)
	if err != nil {
		log.Printf("pogodata: pokemon names refresh: parse: %v", err)
		return
	}
	if len(names) == 0 {
		log.Printf("pogodata: pokemon names refresh: no names parsed")
		return
	}
	keyed := make(map[string]map[string]string, len(names))
	for id, langs := range names {
		keyed[strconv.Itoa(id)] = langs
	}
	data, err := json.Marshal(keyed)
	if err != nil {
		log.Printf("pogodata: pokemon names refresh: marshal: %v", err)
		return
	}
	os.WriteFile(filepath.Join(s.cacheDir, "pokemon_names.json"), data, 0644)
	s.mu.Lock()
	s.applyResult("pokemon_names", json.RawMessage(data))
	s.mu.Unlock()
	log.Printf("pogodata: pokemon names: fetched %d species from PokeAPI", len(names))
}

// parsePokemonNamesCSV parses PokeAPI's pokemon_species_names.csv
// (pokemon_species_id,local_language_id,name,genus) into a map of dex ID to
// {lang_code: name} for the languages in langIDToCode. The header row and any
// row with a malformed id are skipped.
func parsePokemonNamesCSV(r io.Reader) (map[int]map[string]string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	out := make(map[int]map[string]string)
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) < 3 {
			continue
		}
		id, errID := strconv.Atoi(rec[0])
		langID, errLang := strconv.Atoi(rec[1])
		if errID != nil || errLang != nil || id <= 0 || rec[2] == "" {
			continue
		}
		code, ok := langIDToCode[langID]
		if !ok {
			continue
		}
		if out[id] == nil {
			out[id] = make(map[string]string, len(langIDToCode))
		}
		out[id][code] = rec[2]
	}
	return out, nil
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
		s.refreshMaxBattles()
	}
}

// fetchPGAPIRaids fetches current raid bosses from pokemon-go-api and groups them by our
// numeric tier key (6=Mega, 5/3/1). Shadow tiers fold into their numeric tier with a
// "Shadow " name prefix.
func (s *Store) fetchPGAPIRaids() (json.RawMessage, error) {
	const url = "https://pokemon-go-api.github.io/pokemon-go-api/api/raidboss.json"
	tierMap := []struct {
		src    string
		dst    string
		shadow bool
	}{
		{"mega", "6", false},
		{"lvl5", "5", false},
		{"lvl3", "3", false},
		{"lvl1", "1", false},
		{"shadow_lvl5", "5", true},
		{"shadow_lvl3", "3", true},
		{"shadow_lvl1", "1", true},
	}
	grouped, total, err := s.fetchPGAPIGrouped(url, tierMap)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(grouped)
	if err != nil {
		return nil, err
	}
	log.Printf("pogodata: raids: fetched %d bosses from pokemon-go-api", total)
	return out, nil
}

// fetchMaxBattles fetches current Max Battle (Dynamax) bosses from pokemon-go-api, grouped
// by Max Battle tier (1/2/3).
func (s *Store) fetchMaxBattles() (json.RawMessage, error) {
	const url = "https://pokemon-go-api.github.io/pokemon-go-api/api/maxbattles.json"
	tierMap := []struct {
		src    string
		dst    string
		shadow bool
	}{
		{"tier_1", "1", false},
		{"tier_2", "2", false},
		{"tier_3", "3", false},
	}
	grouped, total, err := s.fetchPGAPIGrouped(url, tierMap)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(grouped)
	if err != nil {
		return nil, err
	}
	log.Printf("pogodata: max battles: fetched %d bosses from pokemon-go-api", total)
	return out, nil
}

// fetchPGAPIGrouped fetches a pokemon-go-api "currentList" endpoint and maps the named
// source tiers onto our destination tier keys, returning the grouped bosses and a count.
func (s *Store) fetchPGAPIGrouped(url string, tierMap []struct {
	src    string
	dst    string
	shadow bool
}) (map[string][]raidBoss, int, error) {
	raw, err := s.fetch(url)
	if err != nil {
		return nil, 0, err
	}
	var resp pgapiList
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, 0, err
	}
	// currentList changed from a tier-keyed map to a flat array in the max battles endpoint.
	// If it's an array, return empty grouped data without error (correct when none are active).
	if cl := bytes.TrimSpace(resp.CurrentList); len(cl) > 0 && cl[0] == '[' {
		return make(map[string][]raidBoss), 0, nil
	}
	var currentList map[string]json.RawMessage
	if err := json.Unmarshal(resp.CurrentList, &currentList); err != nil {
		return nil, 0, err
	}
	grouped := make(map[string][]raidBoss)
	total := 0
	for _, m := range tierMap {
		blob, ok := currentList[m.src]
		if !ok {
			continue
		}
		var bosses []pgapiBoss
		if err := json.Unmarshal(blob, &bosses); err != nil {
			log.Printf("pogodata: %s parse @ %s: %v", m.src, url, err)
			continue
		}
		for _, b := range bosses {
			grouped[m.dst] = append(grouped[m.dst], bossFromPGAPI(b, m.shadow))
			total++
		}
	}
	if total == 0 {
		return nil, 0, fmt.Errorf("no bosses parsed from %s", url)
	}
	return grouped, total, nil
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
func (s *Store) Refresh() {
	go s.refresh()
	go s.refreshPokemonNames()
}

func (s *Store) Raids() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.raids
}
func (s *Store) MaxBattles() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.maxBattles
}
func (s *Store) Events() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.events
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
func (s *Store) CPMultipliers() json.RawMessage {
	s.mu.RLock(); defer s.mu.RUnlock(); return s.cpMults
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
		MaxBattles    json.RawMessage            `json:"maxBattles"`
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
		MaxBattles:    s.maxBattles,
		Shinies:       s.shinies,
		ShadowPokemon: s.shadowPokemon,
		TypeChart:     s.typeChart,
		CPMultipliers: s.cpMults,
		PokemonTypes:  s.pokemonTypes,
		PokemonNames:  pokemonNames,
	})
	return data
}
