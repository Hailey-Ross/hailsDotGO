package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// ── Directory search ──────────────────────────────────────────────────────────

// The search parameter must not become a way to read a name the profile refuses
// to show. Filtering the gated DTO is what prevents it; this pins that, because
// moving the filter onto trainerEntry would be an easy and quiet optimisation.
func TestSearchCannotFindAPrivateTrainerName(t *testing.T) {
	priv := toMobileTrainer(privateTrainer(), false)

	pub := privateTrainer()
	pub.Username = "openbook"
	pub.TrainerName = "Secretive"
	pub.ProfilePublic = true

	list := []mobileTrainer{priv, toMobileTrainer(pub, false)}

	// "Secretive" is the private trainer's real trainer name AND the public
	// trainer's, so a leak shows up as two hits instead of one.
	got := filterMobileTrainers(list, "secretive")
	if len(got) != 1 {
		t.Fatalf("searching a private trainer name matched %d trainers, want only the public one", len(got))
	}
	if got[0].Username != "openbook" {
		t.Errorf("matched %q, want the public profile", got[0].Username)
	}

	// The username is public on every profile, so it stays searchable.
	if got := filterMobileTrainers(list, "someone"); len(got) != 1 || got[0].Username != "someone" {
		t.Errorf("username search did not find the private trainer: %+v", got)
	}
}

func TestSearchIsCaseInsensitiveAndOrderPreserving(t *testing.T) {
	mk := func(username, name string) mobileTrainer {
		e := privateTrainer()
		e.Username = username
		e.TrainerName = name
		e.ProfilePublic = true
		return toMobileTrainer(e, false)
	}
	list := []mobileTrainer{mk("zeta", "Alpha"), mk("alpha", "Zeta"), mk("beta", "Gamma")}

	got := filterMobileTrainers(list, "  ALPHA  ")
	if len(got) != 2 {
		t.Fatalf("matched %d, want 2 (one by username, one by trainer name)", len(got))
	}
	if got[0].Username != "zeta" || got[1].Username != "alpha" {
		t.Errorf("filter reordered results: %q then %q", got[0].Username, got[1].Username)
	}

	if got := filterMobileTrainers(list, "   "); len(got) != 3 {
		t.Errorf("a blank query filtered the list down to %d, want all 3", len(got))
	}
}

// The rank colour is public ornamentation and already renders on a private
// trainer's card on the website, so it must survive the privacy gate. It sits
// above the early return; a later edit that moves it below would drop it.
func TestRaidRankClassSurvivesThePrivacyGate(t *testing.T) {
	in := privateTrainer()
	in.RaidRank = "Youngster"
	in.RaidRankClass = "youngster"

	if got := toMobileTrainer(in, false); got.RaidRankClass != "youngster" {
		t.Errorf("raid_rank_class = %q on a private profile, want it kept", got.RaidRankClass)
	}
}

func TestClampQueryInt(t *testing.T) {
	cases := []struct {
		query string
		def   int
		max   int
		want  int
	}{
		{"", 10, 100, 10},        // absent falls back
		{"n=abc", 10, 100, 10},   // unparseable falls back rather than erroring
		{"n=5", 10, 100, 5},      // parsed
		{"n=-3", 10, 100, 0},     // negative clamps to 0, which out[offset:] needs
		{"n=9999", 10, 100, 100}, // oversized clamps to the ceiling
		{"n=0", 10, 100, 0},      // an explicit zero is honoured, not treated as absent
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/trainers?"+c.query, nil)
		if got := clampQueryInt(r, "n", c.def, 0, c.max); got != c.want {
			t.Errorf("clampQueryInt(%q) = %d, want %d", c.query, got, c.want)
		}
	}
}

// ── Social lists ─────────────────────────────────────────────────────────────

// MobileSocialLists and SocialPage must apply the same visibility clause. They are
// two entry points to one screen, and the mobile one was written second, which is
// exactly how a gate gets dropped from a copy.
//
// A source-level assertion rather than a live request: both handlers query, and
// this package has no database harness. What is worth guarding is the clause
// itself, which is text.
//
// The answer this pins, recorded so nobody has to re-derive it: NEITHER gates on
// profile_public or directory_hidden. Both serve any non-disabled trainer's
// follower and following lists to any signed-in caller, which is what the website
// has always done. The mobile endpoint is not more permissive than the page.
func TestSocialListsShareTheirVisibilityClause(t *testing.T) {
	src := readHandlerSource(t, "social.go")

	// The full query, not just its WHERE: the bare clause appears four times in
	// this file, because APIGetSocialState and APIFriend resolve a target the same
	// way. Only the two list handlers select the trainer name alongside the id.
	const query = "SELECT id, COALESCE(trainer_name,'') FROM users WHERE username = ? AND disabled = 0"
	if n := strings.Count(src, query); n != 2 {
		t.Fatalf("found %d copies of the social list lookup, want exactly 2 (SocialPage and MobileSocialLists):\n  %s", n, query)
	}

	// Both must reach the lists through the same helper. A second query built by
	// hand is how the two would diverge without either clause changing.
	if n := strings.Count(src, "h.socialLists(targetID)"); n != 2 {
		t.Errorf("socialLists is called %d times in social.go, want 2; one of the handlers has grown its own query", n)
	}

	// And the mobile one must still require a caller. It is the only difference
	// between them that is meant to exist: the page is readable logged out.
	mobile := src[strings.Index(src, "func (h *Handlers) MobileSocialLists"):]
	if !strings.Contains(mobile[:400], "requireUserAPI") {
		t.Error("MobileSocialLists no longer requires a caller")
	}
}
