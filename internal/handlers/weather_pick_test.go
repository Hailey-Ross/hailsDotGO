package handlers

import "testing"

func TestPickPlace(t *testing.T) {
	places := []geoPlace{
		{Latitude: 39.8, Country: "United States", CountryCode: "US", Admin1: "Illinois"},
		{Latitude: 37.2, Country: "United States", CountryCode: "US", Admin1: "Missouri"},
		{Latitude: 53.4, Country: "United Kingdom", CountryCode: "GB", Admin1: "England"},
	}
	cases := []struct {
		region, country string
		want            float64
	}{
		{"Missouri", "USA", 37.2},           // region and the short country form
		{"missouri", "United States", 37.2}, // case does not matter
		{"", "UK", 53.4},                    // country only, by alias
		{"Nowhere", "US", 39.8},             // unknown region falls back to the country
		{"", "", 39.8},                      // nothing saved: the most prominent
	}
	for _, c := range cases {
		if got := pickPlace(places, c.region, c.country).Latitude; got != c.want {
			t.Errorf("pickPlace(%q, %q) = %v, want %v", c.region, c.country, got, c.want)
		}
	}
}
