package costumenames

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsEcho pins the corroboration rule the discovery job turns on, using the real names
// Dittobase serves for each of these pages. Read the table as the answer to one question: did a
// human anywhere give this thing a name?
//
// Getting this wrong in either direction is expensive. A false "real name" auto-admits a Vivillon
// pattern as a costume; a false "echo" sends a perfectly good costume back to the manual queue,
// which is the bug this whole feature exists to kill.
func TestIsEcho(t *testing.T) {
	cases := []struct {
		name    string // what dittobase's og:title gives, after the species is stripped
		species string
		code    string
		echo    bool
		why     string
	}{
		// Real names. A human named these, so they are costumes.
		{"Friede's Goggles", "Charmander", "f:GOGGLES_2026", false,
			"the costume that went missing for months; the whole point of the feature"},
		{"Friede's Goggles", "Charizard", "f:GOGGLES_2026", false, "same code, later evolution"},
		{"Clone", "Pikachu", "f:COPY_2019", false,
			"Clone Pikachu, the masterfile's other known false negative"},
		{"Visor", "Charizard", "c:SPRING_2020_NOEVOLVE", false,
			"the code name says Spring 2020; the costume is a visor"},
		{"Party Hat", "Pikachu", "c:ANNIVERSARY", false, "an ordinary curated costume"},

		// Echoes of the code. Dittobase has no name, so a human must look.
		{"Vivillon Meadow", "Vivillon", "f:MEADOW", true, "a wing pattern, not a costume"},
		{"Unown A", "Unown", "f:UNOWN_A", true, "a letter, not a costume"},
		{"Shellos East Sea", "Shellos", "f:EAST_SEA", true, "a regional colour, not a costume"},
		{"Giratina Altered", "Giratina", "f:ALTERED", true, "a forme, not a costume"},
		{"Furfrou Star", "Furfrou", "f:STAR", true, "a trim, not a costume"},

		// Echoes that ARE costumes. This is why an echo must never mean "not a costume":
		// upstream flags these, so they are admitted on that alone and never reach this test.
		{"Pikachu K 2026 A 01", "Pikachu", "f:K_2026_A_01", true,
			"a real costume Dittobase has not named; must land in the review queue, not the catalog"},
		{"Pikachu Anniversary 2026 Malaysia 01", "Pikachu", "f:ANNIVERSARY_2026_MALAYSIA_01", true,
			"species-first echo: stripSpecies only strips a trailing species"},

		// Shape edge cases.
		{"", "Pikachu", "f:WHATEVER", true, "no name at all cannot corroborate anything"},
		{"Hat", "Pikachu", "f:SOMETHING", true, "nothing survives the stopword list"},
		{"Goggles 2026", "Charmander", "f:GOGGLES_2026", true,
			"the masterfile's machine name is an echo even though the costume is real"},
	}

	for _, c := range cases {
		if got := IsEcho(c.name, c.species, c.code); got != c.echo {
			t.Errorf("IsEcho(%q, %q, %q) = %v, want %v\n  %s",
				c.name, c.species, c.code, got, c.echo, c.why)
		}
	}
}

// A costume named differently per species still counts as agreement, which is why the name check
// compares against every species rather than the first one.
func TestAnySharesWord(t *testing.T) {
	ditto := []string{"Spooky Festival", "Cempasúchil Crown"}
	if !AnySharesWord("Spooky Festival", ditto) {
		t.Error("a label matching one species' name should agree")
	}
	if AnySharesWord("Detective Hat", ditto) {
		t.Error("a label matching neither should disagree")
	}
	// "Hat" is a stopword, so a label made only of stopwords has nothing to compare and must not
	// cry wolf.
	if !SharesWord("Hat", "Straw Hat") {
		t.Error("an uncomparable pair should pass rather than report a false mismatch")
	}
}

func TestCodeSlugDropsNoEvolve(t *testing.T) {
	if got := CodeSlug("c:FALL_2022_NOEVOLVE"); got != "fall-2022" {
		t.Errorf("CodeSlug = %q, want fall-2022", got)
	}
	// And this is exactly why Ambiguous exists: the twin slugifies identically.
	if CodeSlug("c:FALL_2022") != CodeSlug("c:FALL_2022_NOEVOLVE") {
		t.Error("the _NOEVOLVE twin must collide, or Ambiguous has nothing to catch")
	}
}

func TestAmbiguousRefusesBothTwins(t *testing.T) {
	slugs := []string{"vulpix-fall-2022"}
	codes := map[string][]int{"c:FALL_2022": {37}, "c:FALL_2022_NOEVOLVE": {37}}
	amb := Ambiguous(codes, map[int]string{37: "Vulpix"}, slugs)

	for _, code := range []string{"c:FALL_2022", "c:FALL_2022_NOEVOLVE"} {
		if _, clash := amb[SlugKey(37, code)]; !clash {
			t.Errorf("%s should be refused: naming either from a shared page is a guess", code)
		}
	}
}

func TestNamesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "names.json")

	n := Names{}
	n.Set("f:GOGGLES_2026", 4, "Friede's Goggles")
	if err := Save(path, n); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := Load(path)
	if got.Get("f:GOGGLES_2026", 4) != "Friede's Goggles" {
		t.Errorf("round trip lost the name: %v", got)
	}

	// A BOM in a file the sync tool commits has taken the service down before.
	data, _ := os.ReadFile(path)
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		t.Error("names cache was written with a BOM")
	}

	// A missing cache is a cold start, not an error.
	if len(Load(filepath.Join(dir, "absent.json"))) != 0 {
		t.Error("a missing cache should load empty")
	}
}
