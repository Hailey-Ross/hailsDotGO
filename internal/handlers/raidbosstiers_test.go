package handlers

import (
	"encoding/json"
	"testing"
)

// The raids blob is now reconciled against the event schedule, which can both drop
// bosses and add ones the raid feed has not listed yet. Lobby creation and queue
// joins match the display name exactly, so the spellings have to survive that.
func TestBossTiersFrom(t *testing.T) {
	raw := json.RawMessage(`{
		"1":[{"pokemon_name":"Pikachu"}],
		"5":[{"pokemon_name":"Shadow Giratina (Altered Forme)"},
		     {"pokemon_name":"Regirock","source":"events"},
		     {"pokemon_name":"Shadow Thundurus (Incarnate)","source":"events"}],
		"6":[]
	}`)
	got := bossTiersFrom(raw)

	for name, wantTier := range map[string]uint8{
		"Pikachu":                          1,
		"Shadow Giratina (Altered Forme)":  5,
		"Regirock":                         5,
		"Shadow Thundurus (Incarnate)":     5,
	} {
		if got[name] != wantTier {
			t.Errorf("%q resolved to tier %d, want %d", name, got[name], wantTier)
		}
	}
	// A boss the schedule dropped must not be joinable any more.
	if _, ok := got["Lunala"]; ok {
		t.Error("a boss absent from the served blob was still treated as current")
	}
	if len(got) != 4 {
		t.Errorf("got %d bosses, want 4", len(got))
	}
}

func TestBossTiersFromRejectsJunk(t *testing.T) {
	// An empty map, never a nil one: every caller indexes it directly.
	for _, raw := range []string{``, `not json`, `[]`, `{"5":"nope"}`} {
		got := bossTiersFrom(json.RawMessage(raw))
		if got == nil {
			t.Errorf("%q returned a nil map", raw)
		}
		if len(got) != 0 {
			t.Errorf("%q returned %d entries, want none", raw, len(got))
		}
	}
}
