package handlers

import "testing"

// The partial-write contract. An omitted key must be left alone, which is the bug
// the FORM path needed hidden _settings inputs to work around: an unchecked
// checkbox sends nothing, so "this form does not own the key" and "the key is off"
// arrived identical, and saving the registration toggle switched the store off.
//
// A JSON body does not need that hack, because an absent key is genuinely absent.
// This is the test that stops someone copying the _settings pattern across for
// symmetry and reintroducing the shape it exists to work around.
func TestCollectToggleWritesOnlyTouchesWhatWasSent(t *testing.T) {
	yes, no := true, false

	got, err := collectToggleWrites(map[string]*bool{"registration_open": &yes}, siteSettingKeys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("writing one toggle produced %d writes: %v", len(got), got)
	}
	if !got["registration_open"] {
		t.Errorf("registration_open = false, want true")
	}
	if _, touched := got["store_enabled"]; touched {
		t.Error("a body naming only registration_open also wrote store_enabled; that is the exact bug this shape exists to prevent")
	}

	// Both, and false must be writable: an omitted key means no opinion, but an
	// explicit false means switch it off.
	got, err = collectToggleWrites(map[string]*bool{"registration_open": &no, "store_enabled": &no}, siteSettingKeys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got["registration_open"] || got["store_enabled"] {
		t.Errorf("explicit false was not written: %v", got)
	}
}

func TestCollectToggleWritesRejectsNonsense(t *testing.T) {
	yes := true

	if _, err := collectToggleWrites(map[string]*bool{"nonsense": &yes}, siteSettingKeys); err == nil {
		t.Error("an unknown setting name was accepted; silently dropping it would have a client believe it had saved")
	}
	if _, err := collectToggleWrites(map[string]*bool{}, siteSettingKeys); err == nil {
		t.Error("an empty body was accepted; that is a client bug and should not look like a successful save")
	}
	// An explicit null is "no opinion", same as absent, so a body of only nulls is
	// an empty write rather than an error about the name.
	if _, err := collectToggleWrites(map[string]*bool{"registration_open": nil}, siteSettingKeys); err == nil {
		t.Error("a body of only nulls was accepted as a write")
	}
}

// The page toggles use the client-facing names GET /maintenance already answers
// with, so the app reads and writes one vocabulary. A name in one and not the other
// is a toggle the app can see and cannot change.
func TestPageSettingNamesMatchTheMaintenanceResponse(t *testing.T) {
	// The keys MobileMaintenance emits, restated here so a change to either side
	// has to be a deliberate change to both.
	maintenance := []string{
		"raids", "dps", "pvp", "events", "iv",
		"trainers", "trainer_directory", "raid_finder", "shinies", "translator_apps",
	}
	if len(pageSettingKeys) != len(maintenance) {
		t.Errorf("pageSettingKeys has %d entries, the maintenance response has %d", len(pageSettingKeys), len(maintenance))
	}
	for _, name := range maintenance {
		if _, ok := pageSettingKeys[name]; !ok {
			t.Errorf("%q is readable from /maintenance but not writable through the admin pages endpoint", name)
		}
	}
	// And every database key is distinct, or one toggle would overwrite another.
	seen := map[string]string{}
	for name, key := range pageSettingKeys {
		if prev, dup := seen[key]; dup {
			t.Errorf("%q and %q both write %q", name, prev, key)
		}
		seen[key] = name
	}
}

// Both invite rules exist so an invite link that leaks cannot mint more than one
// moderator. They are shared between the form path and the JSON path, so this
// covers both.
func TestInviteRules(t *testing.T) {
	cases := []struct {
		name     string
		role     string
		uses     int
		wantRole string
		wantUses int
		refused  bool
	}{
		{"plain single use", "user", 1, "user", 1, false},
		{"multi use for users is fine", "user", 25, "user", 25, false},
		{"unknown role falls back to user", "wizard", 1, "user", 1, false},
		{"tester single use", "tester", 1, "tester", 1, false},
		{"multi use may only grant user", "tester", 5, "tester", 5, true},
		{"staff invite must be single use", "moderator", 2, "moderator", 2, true},
		{"admin invite must be single use", "admin", 3, "admin", 3, true},
		{"admin single use is allowed", "admin", 1, "admin", 1, false},
		{"zero uses normalises to one", "user", 0, "user", 1, false},
		{"negative uses normalises to one", "user", -4, "user", 1, false},
		{"oversized uses normalises to one", "user", 999, "user", 1, false},
	}

	for _, c := range cases {
		role, uses, msgKey := inviteRules(c.role, c.uses)
		if role != c.wantRole || uses != c.wantUses {
			t.Errorf("%s: got (%q, %d), want (%q, %d)", c.name, role, uses, c.wantRole, c.wantUses)
		}
		if refused := msgKey != ""; refused != c.refused {
			t.Errorf("%s: refused = %v (%q), want %v", c.name, refused, msgKey, c.refused)
		}
	}
}
