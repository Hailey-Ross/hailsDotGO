package handlers

import (
	"testing"
)

// ?sort=relationship groups the directory around the caller. Nothing covered the
// directory's ordering before this, so these pin both halves of it: the tiers
// themselves, and the promise that the order INSIDE a tier is untouched.

// dirTrainer is one row as the directory would have built it, already through the
// privacy gate. Location is what the trainer PUBLISHES, which is the only thing
// the tiering is allowed to see.
func dirTrainer(username, region, country string) mobileTrainer {
	return mobileTrainer{Username: username, Region: region, Country: country}
}

func usernames(in []mobileTrainer) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		out = append(out, t.Username)
	}
	return out
}

func assertOrder(t *testing.T, got []mobileTrainer, want ...string) {
	t.Helper()
	names := usernames(got)
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}

func TestRelationshipTiersGroupTheDirectory(t *testing.T) {
	stranger := dirTrainer("stranger", "Kanto", "Japan")
	neighbour := dirTrainer("neighbour", "Yorkshire", "United Kingdom")

	follower := dirTrainer("follower", "Kanto", "Japan")
	follower.FollowsMe = true

	followed := dirTrainer("followed", "Kanto", "Japan")
	followed.IsFollowing = true

	friend := dirTrainer("friend", "Kanto", "Japan")
	friend.IsFollowing, friend.FollowsMe, friend.IsFriend = true, true, true

	in := []mobileTrainer{stranger, neighbour, follower, followed, friend}
	got := sortTrainersForViewer(in, "Yorkshire", "United Kingdom")

	assertOrder(t, got, "friend", "followed", "follower", "neighbour", "stranger")

	// The input must not be reordered under the caller. MobileTrainers pages the
	// result it is handed back, and a sort that also scrambled its argument would
	// be a trap for the next caller.
	assertOrder(t, in, "stranger", "neighbour", "follower", "followed", "friend")
}

// The tier is a prefix key on top of the directory's own order, not a
// replacement for it. Everything inside one tier must come out exactly as it
// went in, which is what listTrainers' online, staff, supporter, raid XP, name
// ordering depends on.
func TestOrderInsideATierIsUntouched(t *testing.T) {
	mk := func(name string, friend bool) mobileTrainer {
		e := dirTrainer(name, "", "")
		e.IsFriend, e.IsFollowing, e.FollowsMe = friend, friend, friend
		return e
	}
	in := []mobileTrainer{
		mk("online-staff", false),
		mk("friend-one", true),
		mk("supporter", false),
		mk("friend-two", true),
		mk("newcomer", false),
	}

	got := sortTrainersForViewer(in, "Yorkshire", "United Kingdom")
	assertOrder(t, got, "friend-one", "friend-two", "online-staff", "supporter", "newcomer")
}

// The tiering runs on the DTO, so a private trainer's real country cannot reach
// it. If it ever ran on the row instead, this trainer would float above the
// strangers and their POSITION would tell the caller they share a country, which
// is exactly the field the privacy gate exists to hide.
func TestTieringCannotSeeAHiddenLocation(t *testing.T) {
	hidden := privateTrainer() // Yorkshire, United Kingdom, profile_public = 0
	shown := privateTrainer()
	shown.Username = "openbook"
	shown.ProfilePublic = true

	in := []mobileTrainer{
		toMobileTrainer(hidden, false),
		toMobileTrainer(shown, false),
	}
	got := sortTrainersForViewer(in, "Yorkshire", "United Kingdom")

	assertOrder(t, got, "openbook", "someone")
	if tier := viewerTier(in[0], "Yorkshire", "United Kingdom"); tier != tierEveryoneElse {
		t.Errorf("a trainer who publishes no location reached tier %d, want %d", tier, tierEveryoneElse)
	}

	// location_display = "country" publishes no region, so it cannot reach the
	// same area tier either. Recorded because it looks like a bug from outside.
	countryOnly := privateTrainer()
	countryOnly.ProfilePublic = true
	countryOnly.LocationDisplay = "country"
	if tier := viewerTier(toMobileTrainer(countryOnly, false), "Yorkshire", "United Kingdom"); tier != tierEveryoneElse {
		t.Errorf("a country only trainer reached tier %d, want %d", tier, tierEveryoneElse)
	}
}

// Every trainer who publishes nothing would otherwise tier together above real
// strangers, and a caller who publishes nothing themselves would tier the whole
// directory that way.
func TestBlankLocationNeverMatchesBlank(t *testing.T) {
	nowhere := dirTrainer("nowhere", "", "")

	if tier := viewerTier(nowhere, "", ""); tier != tierEveryoneElse {
		t.Errorf("two blank locations matched: tier %d, want %d", tier, tierEveryoneElse)
	}
	if tier := viewerTier(nowhere, "Yorkshire", "United Kingdom"); tier != tierEveryoneElse {
		t.Errorf("a blank trainer location matched the caller's: tier %d", tier)
	}
	if tier := viewerTier(dirTrainer("somewhere", "Yorkshire", "United Kingdom"), "", ""); tier != tierEveryoneElse {
		t.Errorf("a blank caller location matched a trainer's: tier %d", tier)
	}
}

// Country and region are free text with no validation, so the comparison is
// forgiving about spacing and case and about nothing else. Guessing that UK
// means United Kingdom is not this function's job.
func TestSameLocalityComparesLoosely(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"United Kingdom", "united kingdom", true},
		{"  United Kingdom  ", "United Kingdom", true},
		{"UK", "United Kingdom", false},
		{"England", "Scotland", false},
		{"", "", false},
		{"   ", "", false},
		{"Japan", "", false},
	}
	for _, c := range cases {
		if got := sameLocality(c.a, c.b); got != c.want {
			t.Errorf("sameLocality(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// The same area tier needs both halves. Same country, different region is a
// stranger, which is what keeps the tier meaning "near me" rather than "roughly
// the same timezone".
func TestSameCountryAloneIsNotTheSameArea(t *testing.T) {
	sameCountry := dirTrainer("faraway", "Cornwall", "United Kingdom")
	if tier := viewerTier(sameCountry, "Yorkshire", "United Kingdom"); tier != tierEveryoneElse {
		t.Errorf("same country, different region reached tier %d, want %d", tier, tierEveryoneElse)
	}

	sameRegionName := dirTrainer("coincidence", "Yorkshire", "Australia")
	if tier := viewerTier(sameRegionName, "Yorkshire", "United Kingdom"); tier != tierEveryoneElse {
		t.Errorf("a matching region name in another country reached tier %d, want %d", tier, tierEveryoneElse)
	}
}

// A relationship outranks a location, and the strongest relationship wins.
func TestRelationshipOutranksLocation(t *testing.T) {
	friendAbroad := dirTrainer("friend", "Kanto", "Japan")
	friendAbroad.IsFriend, friendAbroad.IsFollowing, friendAbroad.FollowsMe = true, true, true
	if tier := viewerTier(friendAbroad, "Yorkshire", "United Kingdom"); tier != tierFriend {
		t.Errorf("a friend abroad tiered %d, want %d", tier, tierFriend)
	}

	// A friend is a mutual follow, so both directions are set on one. The friend
	// tier has to win, or a friend would be filed under "following".
	if tier := viewerTier(friendAbroad, "Kanto", "Japan"); tier != tierFriend {
		t.Errorf("a nearby friend tiered %d, want %d", tier, tierFriend)
	}
}
