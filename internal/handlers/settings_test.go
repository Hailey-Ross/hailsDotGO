package handlers

import (
	"strings"
	"testing"

	"pogo.hails.cc/internal/pogodata"
)

// None of these rules was reachable from a test before validateSettingsInput was
// pulled out of SettingsUpdate: they were inline in a handler that needs h.db,
// h.store, h.render and an i18n capable request. That matters most for the trainer
// name pair, which exists because the name lands in innerHTML and in a
// data-trainer attribute.

// A small stand-in catalogue. Rank 2 unlocks "gentleman"; "elite" needs rank 5.
func testClasses() []pogodata.TrainerClass {
	return []pogodata.TrainerClass{
		{Slug: "gentleman", Label: "Gentleman"},
		{Slug: "elite", Label: "Elite Four"},
		{Slug: "prof-oak", Label: "Prof. Oak"},
	}
}

func testLocks() map[string]int {
	return map[string]int{"elite": 5}
}

// dexID stands in for h.store.PokemonDexID: only Lunala is a real species here.
func testDexID(name string) int {
	if strings.EqualFold(name, "Lunala") {
		return 792
	}
	return 0
}

func validate(in settingsInput, rank int) (settingsInput, string) {
	return validateSettingsInput(in, testClasses(), testLocks(), rank, testDexID)
}

func TestValidateSettingsInputRefusals(t *testing.T) {
	cases := []struct {
		name string
		in   settingsInput
		want string
	}{
		{"a clean write passes", settingsInput{TrainerName: "Hails", TrainerCode: "123456789012", TrainerLevel: 50}, ""},
		{"empty is allowed, it means unset", settingsInput{}, ""},

		// The XSS pair. Both must refuse, and for their own distinct reasons.
		{"a tag in the trainer name", settingsInput{TrainerName: "<svg onload=a()>"}, "error.trainer_name_chars"},
		{"a base tag in the trainer name", settingsInput{TrainerName: "<base href=//a.bc"}, "error.trainer_name_length"},
		{"an attribute breakout", settingsInput{TrainerName: `" onfocus="x`}, "error.trainer_name_chars"},
		{"an ampersand", settingsInput{TrainerName: "Hails&Co"}, "error.trainer_name_chars"},
		{"an apostrophe", settingsInput{TrainerName: "it's me"}, "error.trainer_name_chars"},

		{"a name over sixteen bytes", settingsInput{TrainerName: "abcdefghijklmnopq"}, "error.trainer_name_length"},
		{"sixteen bytes exactly", settingsInput{TrainerName: "abcdefghijklmnop"}, ""},

		{"a trainer code that is not twelve digits", settingsInput{TrainerCode: "12345"}, "error.trainer_code_format"},
		{"a trainer code with letters", settingsInput{TrainerCode: "12345678901a"}, "error.trainer_code_format"},
		{"an empty trainer code is fine", settingsInput{TrainerCode: ""}, ""},

		{"level above eighty", settingsInput{TrainerLevel: 81}, "error.trainer_level_range"},
		{"a negative level", settingsInput{TrainerLevel: -1}, "error.trainer_level_range"},
		{"level zero means unset", settingsInput{TrainerLevel: 0}, ""},
		{"level eighty is the ceiling", settingsInput{TrainerLevel: 80}, ""},

		{"a city over a hundred bytes", settingsInput{City: strings.Repeat("a", 101)}, "error.location_too_long"},
		{"a region over a hundred bytes", settingsInput{Region: strings.Repeat("a", 101)}, "error.location_too_long"},
		{"a country over a hundred bytes", settingsInput{Country: strings.Repeat("a", 101)}, "error.location_too_long"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := validate(tc.in, 0); got != tc.want {
				t.Errorf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

// Non Latin names must keep working. The character rule matches by Unicode
// category rather than an ASCII range precisely so it does not exclude people.
func TestValidateSettingsInputAcceptsNonLatinNames(t *testing.T) {
	for _, name := range []string{"Zoe", "Lady Hails", "Hails_92", "Ampharos-2", "ハイルズ", "Привет", "Ekaterina"} {
		if _, key := validate(settingsInput{TrainerName: name}, 0); key != "" {
			t.Errorf("refused %q with %q", name, key)
		}
	}
}

// Everything here blanks the field instead of refusing the write, which is what
// the settings form has always done.
func TestValidateSettingsInputCoercions(t *testing.T) {
	t.Run("an avatar above the caller's rank is dropped", func(t *testing.T) {
		out, key := validate(settingsInput{Avatar: "elite"}, 0)
		if key != "" || out.Avatar != "" {
			t.Errorf("avatar = %q, key = %q, want both empty", out.Avatar, key)
		}
	})
	t.Run("an avatar within rank is kept", func(t *testing.T) {
		out, _ := validate(settingsInput{Avatar: "elite"}, 5)
		if out.Avatar != "elite" {
			t.Errorf("avatar = %q, want elite", out.Avatar)
		}
	})
	t.Run("a professor needs rank one even when unlocked", func(t *testing.T) {
		out, _ := validate(settingsInput{Avatar: "prof-oak"}, 0)
		if out.Avatar != "" {
			t.Errorf("avatar = %q, want empty: professors are hard locked", out.Avatar)
		}
	})
	t.Run("an unknown avatar is dropped", func(t *testing.T) {
		out, _ := validate(settingsInput{Avatar: "not-a-real-slug"}, 100)
		if out.Avatar != "" {
			t.Errorf("avatar = %q, want empty", out.Avatar)
		}
	})

	t.Run("an unknown location display becomes none", func(t *testing.T) {
		out, _ := validate(settingsInput{LocationDisplay: "everything"}, 0)
		if out.LocationDisplay != "none" {
			t.Errorf("location_display = %q, want none", out.LocationDisplay)
		}
	})

	t.Run("an unknown favourite blanks its form too", func(t *testing.T) {
		out, _ := validate(settingsInput{FavPokemon: "Missingno", FavPokemonForm: "shiny"}, 0)
		if out.FavPokemon != "" || out.FavPokemonForm != "" {
			t.Errorf("fav = %q/%q, want both empty", out.FavPokemon, out.FavPokemonForm)
		}
	})
	t.Run("a real favourite survives", func(t *testing.T) {
		out, _ := validate(settingsInput{FavPokemon: "Lunala", FavPokemonForm: "shiny"}, 0)
		if out.FavPokemon != "Lunala" || out.FavPokemonForm != "shiny" {
			t.Errorf("fav = %q/%q, want Lunala/shiny", out.FavPokemon, out.FavPokemonForm)
		}
	})
	t.Run("an unknown form is dropped but the species stays", func(t *testing.T) {
		out, _ := validate(settingsInput{FavPokemon: "Lunala", FavPokemonForm: "mega"}, 0)
		if out.FavPokemon != "Lunala" || out.FavPokemonForm != "" {
			t.Errorf("fav = %q/%q, want Lunala and an empty form", out.FavPokemon, out.FavPokemonForm)
		}
	})

	t.Run("a trainer code keeps working with spaces in it", func(t *testing.T) {
		out, key := validate(settingsInput{TrainerCode: "1234 5678 9012"}, 0)
		if key != "" || out.TrainerCode != "123456789012" {
			t.Errorf("code = %q, key = %q", out.TrainerCode, key)
		}
	})
	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		out, _ := validate(settingsInput{TrainerName: "  Hails  ", City: " York "}, 0)
		if out.TrainerName != "Hails" || out.City != "York" {
			t.Errorf("got %q / %q", out.TrainerName, out.City)
		}
	})
}

// Pronouns are capped in runes, not bytes. A byte cap would cut a non Latin
// pronoun mid character and hand MySQL invalid utf8mb4.
func TestValidateSettingsInputPronouns(t *testing.T) {
	t.Run("a predefined pronoun passes through", func(t *testing.T) {
		out, _ := validate(settingsInput{Pronouns: "they/them"}, 0)
		if out.Pronouns != "they/them" {
			t.Errorf("pronouns = %q", out.Pronouns)
		}
	})
	t.Run("a custom pronoun is capped at 32 characters", func(t *testing.T) {
		out, _ := validate(settingsInput{Pronouns: strings.Repeat("あ", 40)}, 0)
		if got := len([]rune(out.Pronouns)); got != 32 {
			t.Errorf("kept %d characters, want 32", got)
		}
		if len(out.Pronouns) != 96 {
			t.Errorf("byte length = %d, want 96: a byte cap would have cut mid character", len(out.Pronouns))
		}
	})
	t.Run("a short custom pronoun is untouched", func(t *testing.T) {
		out, _ := validate(settingsInput{Pronouns: "ze/zir"}, 0)
		if out.Pronouns != "ze/zir" {
			t.Errorf("pronouns = %q", out.Pronouns)
		}
	})
}

// The favourite is capped in runes for the same reason.
func TestValidateSettingsInputFavouriteIsCappedInRunes(t *testing.T) {
	// Long enough to trip the cap, and a real species so it is not blanked first.
	long := strings.Repeat("あ", 80)
	out, _ := validateSettingsInput(
		settingsInput{FavPokemon: long}, testClasses(), testLocks(), 0,
		func(string) int { return 1 },
	)
	if got := len([]rune(out.FavPokemon)); got != 64 {
		t.Errorf("kept %d characters, want 64", got)
	}
}
