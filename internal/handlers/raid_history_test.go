package handlers

import (
	"strings"
	"testing"
)

// The natural key of the dimension is (species, form, tier, shadow), so the form
// has to come out of the display name rather than being swallowed into the species
// column.
//
// This is not cosmetic. Zacian in its Crowned Sword forme has different stats from
// Zacian in Hero of Many Battles, and Giratina Origin from Giratina Altered. Losing
// the parenthetical would collapse them onto one dimension row, and every rotation
// of the second forme would then look like a REBALANCE of the first: the archiver
// would rewrite the stats back and forth forever and log a change every time.
func TestSplitBossName(t *testing.T) {
	cases := []struct {
		in          string
		wantSpecies string
		wantForm    string
	}{
		{"Mewtwo", "Mewtwo", ""},
		{"Shadow Mewtwo", "Shadow Mewtwo", ""},
		{"Mega Gyarados", "Mega Gyarados", ""},
		{"Zacian (Crowned Sword)", "Zacian", "Crowned Sword"},
		{"Zacian (Hero of Many Battles)", "Zacian", "Hero of Many Battles"},
		{"Giratina (Origin)", "Giratina", "Origin"},
		{"Shadow Zacian (Crowned Sword)", "Shadow Zacian", "Crowned Sword"},
		// Degenerate shapes must not lose the name.
		{"  Mewtwo  ", "Mewtwo", ""},
		{"Mewtwo (", "Mewtwo (", ""},
		{"(Origin)", "(Origin)", ""},
		{"", "", ""},
	}

	for _, c := range cases {
		species, form := splitBossName(c.in)
		if species != c.wantSpecies || form != c.wantForm {
			t.Errorf("splitBossName(%q) = (%q, %q), want (%q, %q)", c.in, species, form, c.wantSpecies, c.wantForm)
		}
	}
}

// The typing is stored as one comma separated column rather than a join table: a
// boss has one or two types and never changes shape. Empty must round trip as
// empty, not as a one element list containing "", which is what a bare
// strings.Split gives and what would render an unnamed type chip.
func TestSplitTypes(t *testing.T) {
	if got := splitTypes(""); got != nil {
		t.Errorf("splitTypes(\"\") = %#v, want nil", got)
	}
	if got := splitTypes("Water"); len(got) != 1 || got[0] != "Water" {
		t.Errorf("splitTypes(\"Water\") = %#v", got)
	}
	if got := splitTypes("Water,Flying"); len(got) != 2 || got[1] != "Flying" {
		t.Errorf("splitTypes(\"Water,Flying\") = %#v", got)
	}
	// And the join the archiver does is the inverse.
	if got := strings.Join(splitTypes("Water,Flying"), ","); got != "Water,Flying" {
		t.Errorf("round trip = %q", got)
	}
}
