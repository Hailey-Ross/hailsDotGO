package pogodata

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TrainerClass holds a Pokémon game trainer class for the avatar picker.
// Sprites are served from Pokémon Showdown's public sprite CDN.
type TrainerClass struct {
	Slug      string `json:"slug"`
	Label     string `json:"label"`
	SpriteURL string `json:"sprite_url"`
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

type Store struct {
	mu             sync.RWMutex
	raids          json.RawMessage
	pokemon        json.RawMessage
	pokemonMoves   json.RawMessage
	fastMoves      json.RawMessage
	chargedMoves   json.RawMessage
	shinies        json.RawMessage
	shadowPokemon  json.RawMessage
	typeChart      json.RawMessage
	cpMults        json.RawMessage
	trainerClasses []TrainerClass
	spriteCache    sync.Map
	client         *http.Client
}

func New() *Store {
	classes := make([]TrainerClass, len(builtinTrainerClasses))
	for i, tc := range builtinTrainerClasses {
		classes[i] = TrainerClass{Slug: tc.Slug, Label: tc.Label, SpriteURL: "/api/trainer-sprite/" + tc.Slug}
	}
	return &Store{
		client:         &http.Client{Timeout: 15 * time.Second},
		trainerClasses: classes,
	}
}

func (s *Store) Start() {
	s.refresh()
	go func() {
		for range time.NewTicker(6 * time.Hour).C {
			s.refresh()
		}
	}()
}

func (s *Store) refresh() {
	type ep struct {
		key  string
		urls []string
	}
	endpoints := []ep{
		{"pokemon",        []string{"https://pogoapi.net/api/v1/pokemon_stats.json"}},
		{"pokemon_moves",  []string{"https://pogoapi.net/api/v1/current_pokemon_moves.json"}},
		{"fast_moves",     []string{"https://pogoapi.net/api/v1/fast_moves.json"}},
		{"charged_moves",  []string{"https://pogoapi.net/api/v1/charged_moves.json"}},
		{"shinies",         []string{"https://pogoapi.net/api/v1/shiny_pokemon.json"}},
		{"shadow_pokemon",  []string{"https://pogoapi.net/api/v1/shadow_pokemon.json"}},
		{"type_chart",      []string{"https://pogoapi.net/api/v1/type_effectiveness.json"}},
		{"cp_multipliers",  []string{"https://pogoapi.net/api/v1/cp_multiplier.json"}},
	}

	results := make(map[string]json.RawMessage, len(endpoints)+1)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Raids from ScrapedDuck (LeekDuck mirror)
	wg.Add(1)
	go func() {
		defer wg.Done()
		data, err := s.fetchScrapedDuckRaids()
		if err != nil {
			log.Printf("pogodata: raids: %v", err)
			return
		}
		mu.Lock()
		results["raids"] = data
		mu.Unlock()
	}()

	// Other endpoints from PoGoAPI
	for _, e := range endpoints {
		wg.Add(1)
		go func(e ep) {
			defer wg.Done()
			for _, url := range e.urls {
				data, err := s.fetch(url)
				if err != nil {
					log.Printf("pogodata: %s @ %s: %v", e.key, url, err)
					continue
				}
				mu.Lock()
				results[e.key] = data
				mu.Unlock()
				return
			}
			log.Printf("pogodata: %s: all URLs failed", e.key)
		}(e)
	}
	wg.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := results["raids"]; ok {
		s.raids = v
	}
	if v, ok := results["pokemon"]; ok {
		s.pokemon = v
	}
	if v, ok := results["pokemon_moves"]; ok {
		s.pokemonMoves = v
	}
	if v, ok := results["fast_moves"]; ok {
		s.fastMoves = v
	}
	if v, ok := results["charged_moves"]; ok {
		s.chargedMoves = v
	}
	if v, ok := results["shinies"]; ok {
		s.shinies = v
	}
	if v, ok := results["shadow_pokemon"]; ok {
		// shadow_pokemon.json is an object keyed by base Pokémon name.
		// Normalise to []string so the frontend can do a simple Set lookup.
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(v, &raw); err == nil {
			names := make([]string, 0, len(raw))
			for name := range raw {
				names = append(names, name)
			}
			if encoded, err := json.Marshal(names); err == nil {
				s.shadowPokemon = encoded
			}
		}
	}
	if v, ok := results["type_chart"]; ok {
		s.typeChart = v
	}
	if v, ok := results["cp_multipliers"]; ok {
		s.cpMults = v
	}
	log.Println("pogodata: refresh complete")
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
		Pokemon       json.RawMessage `json:"pokemon"`
		PokemonMoves  json.RawMessage `json:"pokemonMoves"`
		FastMoves     json.RawMessage `json:"fastMoves"`
		ChargedMoves  json.RawMessage `json:"chargedMoves"`
		Raids         json.RawMessage `json:"raids"`
		Shinies       json.RawMessage `json:"shinies"`
		ShadowPokemon json.RawMessage `json:"shadowPokemon"`
		TypeChart     json.RawMessage `json:"typeChart"`
		CPMultipliers json.RawMessage `json:"cpMultipliers"`
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
	})
	return data
}
