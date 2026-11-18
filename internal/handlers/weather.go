package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"
)

type weatherEntry struct {
	PoGOWeather string
	FetchedAt   time.Time
}

type latLon struct {
	Lat float64
	Lon float64
}

var (
	weatherMu    sync.Mutex
	weatherCache = map[string]weatherEntry{}

	geoCacheMu sync.Mutex
	geoCache   = map[string]latLon{}

	omClient = &http.Client{Timeout: 5 * time.Second}
)

var pogoBoostedTypes = map[string][]string{
	"Clear":         {"Fire", "Grass", "Ground"},
	"Partly Cloudy": {"Normal", "Rock"},
	"Overcast":      {"Fairy", "Fighting", "Poison"},
	"Rainy":         {"Water", "Electric", "Bug"},
	"Snow":          {"Ice", "Steel"},
	"Fog":           {"Dark", "Ghost"},
	"Windy":         {"Dragon", "Flying", "Psychic"},
}

func weatherStale(fetched time.Time) bool {
	boundary := fetched.UTC().Truncate(30 * time.Minute).Add(30 * time.Minute)
	return time.Now().UTC().After(boundary)
}

// wmoToPoGO converts a WMO weather code and wind speed (km/h) to a Pokémon GO weather condition.
// Wind overrides clear/partly-cloudy/overcast only — rain, snow, and fog always take priority.
func wmoToPoGO(code int, windKmh float64) string {
	var base string
	switch code {
	case 0, 1:
		base = "Clear"
	case 2:
		base = "Partly Cloudy"
	case 3:
		base = "Overcast"
	case 45, 48:
		return "Fog"
	case 51, 53, 55, 61, 63, 65, 80, 81, 82, 95, 96, 99:
		return "Rainy"
	case 56, 57, 66, 67, 71, 73, 75, 77, 85, 86:
		return "Snow"
	default:
		return ""
	}
	// Wind takes priority over clear-sky conditions; PoGO threshold ≈ 36 km/h
	if windKmh >= 36 {
		return "Windy"
	}
	return base
}

func geocode(city, region, country string) (float64, float64, error) {
	key := city + "|" + region + "|" + country

	geoCacheMu.Lock()
	if ll, ok := geoCache[key]; ok {
		geoCacheMu.Unlock()
		return ll.Lat, ll.Lon, nil
	}
	geoCacheMu.Unlock()

	// Search by the city name alone: Open-Meteo's name search returns nothing for "Denver, USA" (it
	// only understands some country spellings after a comma), which left every trainer whose country
	// is saved as "USA" without weather. The region and country pick among the candidates instead.
	query := city
	u := "https://geocoding-api.open-meteo.com/v1/search?name=" + neturl.QueryEscape(query) + "&count=10&language=en&format=json"

	resp, err := omClient.Get(u)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("geocode: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	var result struct {
		Results []geoPlace `json:"results"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Results) == 0 {
		return 0, 0, fmt.Errorf("geocode: no results for %q", query)
	}

	best := pickPlace(result.Results, region, country)
	ll := latLon{Lat: best.Latitude, Lon: best.Longitude}
	geoCacheMu.Lock()
	geoCache[key] = ll
	geoCacheMu.Unlock()

	return ll.Lat, ll.Lon, nil
}

// geoPlace is one Open-Meteo geocoding candidate.
type geoPlace struct {
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Admin1      string  `json:"admin1"` // state, province or region
}

// countryAliases maps how trainers type a country to its ISO code, for the common short forms that
// do not match the country's English name.
var countryAliases = map[string]string{
	"usa": "US", "us": "US", "u.s.": "US", "u.s.a.": "US", "united states of america": "US", "america": "US",
	"uk": "GB", "u.k.": "GB", "england": "GB", "scotland": "GB", "wales": "GB", "great britain": "GB", "britain": "GB",
	"uae": "AE", "south korea": "KR", "korea": "KR",
}

func countryMatches(p geoPlace, country string) bool {
	c := strings.ToLower(strings.TrimSpace(country))
	if c == "" {
		return false
	}
	if code, ok := countryAliases[c]; ok {
		return strings.EqualFold(p.CountryCode, code)
	}
	return strings.EqualFold(p.Country, c) || strings.EqualFold(p.CountryCode, c)
}

// pickPlace chooses among same-named places: the one in the saved region and country, else the
// one in the saved country, else the most prominent (Open-Meteo lists by population).
func pickPlace(places []geoPlace, region, country string) geoPlace {
	r := strings.TrimSpace(region)
	if r != "" {
		for _, p := range places {
			if strings.EqualFold(p.Admin1, r) && (country == "" || countryMatches(p, country)) {
				return p
			}
		}
	}
	for _, p := range places {
		if countryMatches(p, country) {
			return p
		}
	}
	return places[0]
}

func fetchOpenMeteo(lat, lon float64) string {
	u := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.6f&longitude=%.6f&current=weather_code,wind_speed_10m&wind_speed_unit=kmh&timezone=auto",
		lat, lon,
	)

	resp, err := omClient.Get(u)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var om struct {
		Current struct {
			WeatherCode  int     `json:"weather_code"`
			WindSpeed10m float64 `json:"wind_speed_10m"`
		} `json:"current"`
	}
	if err := json.Unmarshal(body, &om); err != nil {
		return ""
	}

	return wmoToPoGO(om.Current.WeatherCode, om.Current.WindSpeed10m)
}

func (h *Handlers) getCityWeather(city, region, country string) string {
	if city == "" {
		return ""
	}

	cacheKey := city + "|" + region + "|" + country

	weatherMu.Lock()
	if entry, ok := weatherCache[cacheKey]; ok && !weatherStale(entry.FetchedAt) {
		result := entry.PoGOWeather
		weatherMu.Unlock()
		return result
	}
	weatherMu.Unlock()

	lat, lon, err := geocode(city, region, country)
	if err != nil {
		return ""
	}

	pogo := fetchOpenMeteo(lat, lon)

	weatherMu.Lock()
	weatherCache[cacheKey] = weatherEntry{PoGOWeather: pogo, FetchedAt: time.Now().UTC()}
	weatherMu.Unlock()

	return pogo
}

func (h *Handlers) APIWeather(w http.ResponseWriter, r *http.Request) {
	u := h.currentUser(r)
	if u == nil {
		writeJSONError(w, h.t(r, "error.unauthorized"), http.StatusUnauthorized)
		return
	}

	var city, region, country string
	h.db.QueryRow(`SELECT COALESCE(city,''), COALESCE(region,''), COALESCE(country,'') FROM users WHERE id = ?`, u.ID).Scan(&city, &region, &country)

	pogo := h.getCityWeather(city, region, country)
	boosted := pogoBoostedTypes[pogo]
	if boosted == nil {
		boosted = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		PoGOWeather  string   `json:"pogo_weather"`
		BoostedTypes []string `json:"boosted_types"`
	}{pogo, boosted})
}
