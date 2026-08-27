package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The privacy gates on a trainer profile live in the templates, not in the query:
// TrainersPage returns the trainer name, friend code and pronouns of every account
// that is not hidden or disabled, and trainers.html decides what to draw. The
// comment at templates/trainer.html records what that costs when something forgets,
// a private profile handing its friend code to any logged in visitor.
//
// toMobileTrainer is the only thing standing between that query and a JSON client,
// so it gets tested directly.

func privateTrainer() trainerEntry {
	return trainerEntry{
		Username:             "someone",
		ProfilePublic:        false,
		TrainerName:          "Secretive",
		TrainerCode:          "123456789012",
		TrainerCodeFormatted: "1234 5678 9012",
		Pronouns:             "they/them",
		Region:               "Yorkshire",
		Country:              "United Kingdom",
		LocationDisplay:      "full",
		JoinedAt:             time.Unix(0, 0).UTC(),
		Tags:                 []tagEntry{},
	}
}

func TestPrivateProfileLeaksNothing(t *testing.T) {
	got := toMobileTrainer(privateTrainer(), false)

	if got.TrainerCode != "" {
		t.Errorf("friend code leaked from a private profile: %q", got.TrainerCode)
	}
	if got.TrainerName != "" {
		t.Errorf("trainer name leaked from a private profile: %q", got.TrainerName)
	}
	if got.Pronouns != "" {
		t.Errorf("pronouns leaked from a private profile: %q", got.Pronouns)
	}
	if got.Region != "" || got.Country != "" {
		t.Errorf("location leaked from a private profile: %q / %q", got.Region, got.Country)
	}
	if got.DisplayName != "someone" {
		t.Errorf("display name = %q, want the username", got.DisplayName)
	}

	// Belt and braces: the marshalled body must not contain the secrets either,
	// in case a field is added later without a gate.
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Secretive", "123456789012", "1234 5678 9012", "they/them", "Yorkshire", "United Kingdom"} {
		if strings.Contains(string(body), secret) {
			t.Errorf("%q appears in the JSON for a private profile:\n%s", secret, body)
		}
	}
}

// The owner still sees their own details on their own profile, which is what the
// template does.
func TestOwnPrivateProfileStillShowsItself(t *testing.T) {
	got := toMobileTrainer(privateTrainer(), true)

	if got.TrainerCode != "1234 5678 9012" {
		t.Errorf("own friend code = %q, want it shown", got.TrainerCode)
	}
	if got.TrainerName != "Secretive" || got.DisplayName != "Secretive" {
		t.Errorf("own name = %q / %q", got.TrainerName, got.DisplayName)
	}
	if got.Pronouns != "they/them" {
		t.Errorf("own pronouns = %q", got.Pronouns)
	}
}

func TestPublicProfileShowsWhatItShould(t *testing.T) {
	in := privateTrainer()
	in.ProfilePublic = true
	got := toMobileTrainer(in, false)

	if got.TrainerCode != "1234 5678 9012" {
		t.Errorf("trainer code = %q, want the formatted code", got.TrainerCode)
	}
	if got.DisplayName != "Secretive" {
		t.Errorf("display name = %q, want the trainer name", got.DisplayName)
	}
	if got.Region != "Yorkshire" || got.Country != "United Kingdom" {
		t.Errorf("location = %q / %q, want both on a full display", got.Region, got.Country)
	}
}

// location_display is the trainer's own choice about precision, and it has to be
// honoured before the response is built. "none" must not put a country on the wire
// for a client to ignore.
func TestLocationDisplayIsAppliedServerSide(t *testing.T) {
	cases := []struct {
		display     string
		wantRegion  string
		wantCountry string
	}{
		{"full", "Yorkshire", "United Kingdom"},
		{"country", "", "United Kingdom"},
		{"none", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.display, func(t *testing.T) {
			in := privateTrainer()
			in.ProfilePublic = true
			in.LocationDisplay = tc.display
			got := toMobileTrainer(in, false)

			if got.Region != tc.wantRegion {
				t.Errorf("region = %q, want %q", got.Region, tc.wantRegion)
			}
			if got.Country != tc.wantCountry {
				t.Errorf("country = %q, want %q", got.Country, tc.wantCountry)
			}
			if tc.display == "none" {
				body, _ := json.Marshal(got)
				for _, s := range []string{"Yorkshire", "United Kingdom"} {
					if strings.Contains(string(body), s) {
						t.Errorf("%q is on the wire despite location_display=none:\n%s", s, body)
					}
				}
			}
		})
	}
}

// A public profile with no trainer name set falls back to the username, the way
// the directory card does.
func TestDisplayNameFallsBackToUsername(t *testing.T) {
	in := privateTrainer()
	in.ProfilePublic = true
	in.TrainerName = ""
	if got := toMobileTrainer(in, false); got.DisplayName != "someone" {
		t.Errorf("display name = %q, want the username", got.DisplayName)
	}
}

// Tags must serialise as [] rather than null, so a client can iterate without a
// nil check.
func TestTagsAreNeverNull(t *testing.T) {
	in := privateTrainer()
	in.Tags = nil
	body, err := json.Marshal(toMobileTrainer(in, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"tags":[]`) {
		t.Errorf("tags did not serialise as an empty array:\n%s", body)
	}
}
