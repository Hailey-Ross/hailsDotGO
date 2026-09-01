package pogodata

import "testing"

// The compiled-in table is the whole point: it must be there without a fetch.
func TestEvolutionTableIsEmbedded(t *testing.T) {
	if n := EvolutionTableSize(); n < 300 {
		t.Fatalf("evolution table has %d species, want the embedded table (about 400)", n)
	}
}

func TestCanEvolveTo(t *testing.T) {
	cases := []struct {
		name, from, into string
		want             bool
	}{
		// The hole this closes. Both are real species and the old handler wrote
		// either one verbatim.
		{"the actual evolution", "Magikarp", "Gyarados", true},
		{"Magikarp into Rayquaza", "Magikarp", "Rayquaza", false},

		{"a branch", "Eevee", "Vaporeon", true},
		{"another branch", "Eevee", "Umbreon", true},
		{"not a branch", "Eevee", "Charizard", false},

		// A fully evolved species has no entry, so it evolves into nothing.
		{"already final", "Gyarados", "Magikarp", false},
		{"skipping a stage", "Bulbasaur", "Venusaur", false},
		{"one stage at a time", "Bulbasaur", "Ivysaur", true},

		// Regional entries store the bare species name with the region in its own
		// column, and the table unions the forms, so this must pass.
		{"regional form", "Rattata", "Raticate", true},

		// Free text in, so it must not be case or whitespace sensitive.
		{"lowercase", "magikarp", "gyarados", true},
		{"padded", "  Magikarp  ", " Gyarados ", true},

		{"empty source", "", "Gyarados", false},
		{"empty target", "Magikarp", "", false},
		{"nonsense", "Notapokemon", "Gyarados", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanEvolveTo(c.from, c.into); got != c.want {
				t.Errorf("CanEvolveTo(%q, %q) = %v, want %v", c.from, c.into, got, c.want)
			}
		})
	}
}
