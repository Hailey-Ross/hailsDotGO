package costumes

import (
	"slices"
	"strings"
	"testing"
	"time"

	"pogo.hails.cc/internal/masterfile"
)

// A code the embedded catalog will never carry, standing in for the Halloween 2026 Zubat line: a
// costume upstream flagged before anyone mined a single file of its art.
const batCode = "f:TEST_BATS_2026_01"

// formsWithCostumes is the masterfile stub plus the list of flagged forms, which is what the
// art-less scan reads.
type formsWithCostumes struct {
	stubForms
	list []masterfile.CostumeForm
}

func (f formsWithCostumes) CostumeForms() []masterfile.CostumeForm { return f.list }

func bats() formsWithCostumes {
	return formsWithCostumes{
		stubForms: stubForms{},
		list: []masterfile.CostumeForm{
			{Dex: 41, Code: "TEST_BATS_2026_01", Name: "Bats 2026 01"},
			{Dex: 42, Code: "TEST_BATS_2026_01", Name: "Bats 2026 01"},
			{Dex: 169, Code: "TEST_BATS_2026_01", Name: "Bats 2026 01"},
		},
	}
}

// discoverNoArt runs a pass with the art-less scan due, as it is on the first pass after a boot.
func discoverNoArt(t *testing.T, files []string, forms formLookup) DiscoveryReport {
	t.Helper()
	lastNoArtScan = time.Time{}
	t.Cleanup(func() { lastNoArtScan = time.Time{} })
	return discoverWith(t, files, forms, &stubNames{})
}

// The feature: a costume with no art at all can be named and recorded, and never builds a sprite.
func TestCostumeWithNoArtIsRecordable(t *testing.T) {
	seedOverlay(t, nil, nil)
	rep := discoverNoArt(t, nil, bats())

	if !slices.ContainsFunc(rep.Admitted, func(d Discovered) bool { return d.Code == batCode }) {
		t.Fatalf("a flagged costume with no art should be admitted, got %+v", rep.Admitted)
	}

	_, c := view()
	for _, dex := range []int{41, 42, 169} {
		if !c.covers(batCode, dex) {
			t.Errorf("dex %d should be able to wear it", dex)
		}
		if c.hasArt(batCode, dex) {
			t.Errorf("dex %d has no art and must not claim any", dex)
		}
		if _, ok := SpriteURLFor(dex, batCode); ok {
			t.Errorf("no sprite URL may be built for dex %d: it would 404", dex)
		}
		if AllowedFile(assetFile(dex, batCode)) {
			t.Errorf("the proxy must not fetch art that does not exist (dex %d)", dex)
		}
	}
	if _, ok := CatalogDex()[batCode]; ok {
		t.Error("CatalogDex lists art; an art-less code there would hide its art from the drift check once mined")
	}

	u := Unlabelled()
	idx := slices.IndexFunc(u, func(n Unnamed) bool { return n.Code == batCode })
	if idx < 0 {
		t.Fatal("an art-less costume still needs a name, so it belongs in the review queue")
	}
	if !u[idx].ArtPending || u[idx].SpriteURL != "" {
		t.Errorf("the queue row should say there is no art and carry no sprite, got %+v", u[idx])
	}

	if err := Name(batCode, "Test Bat Hat", "tester"); err != nil {
		t.Fatalf("an art-less costume must be nameable: %v", err)
	}
	if !slices.Contains(LabelsForDex(41, "Zubat"), "Test Bat Hat") {
		t.Error("once named, the picker must offer it")
	}
	if _, ok := SpriteURL(41, "Zubat", "Test Bat Hat"); ok {
		t.Error("a recorded art-less costume must fall back to the plain shiny, not a costume URL")
	}
	if !ArtPending(41, "Zubat", "Test Bat Hat") {
		t.Error("ArtPending is how the mobile picker knows to send the plain shiny instead")
	}
}

// Regular art without shiny art means the shiny is not in the game. Offering that costume would let
// a trainer record a shiny that cannot exist, which is the line this whole feature must not cross.
func TestCostumeWithRegularArtButNoShinyIsNotAdmitted(t *testing.T) {
	seedOverlay(t, nil, nil)
	files := []string{"pm41.fTEST_BATS_2026_01.icon.png"}
	discoverNoArt(t, files, bats())

	_, c := view()
	if c.covers(batCode, 41) {
		t.Error("mined regular art with no shiny means no shiny exists; it must not be admitted")
	}
	// The siblings with no art at all are still fair game.
	if !c.covers(batCode, 42) {
		t.Error("a species with no art of any kind should still be admitted")
	}
}

// When the art turns up, the costume switches over by itself, species by species, each pinned to
// the commit its art actually exists at.
func TestArtArrivingLaterLightsTheCostumeUp(t *testing.T) {
	seedOverlay(t, nil, nil)
	discoverNoArt(t, nil, bats())
	if err := Name(batCode, "Test Bat Hat", "tester"); err != nil {
		t.Fatal(err)
	}

	rep := discoverWith(t, []string{shiny(41, batCode)}, bats(), &stubNames{})
	if !slices.ContainsFunc(rep.ArtArrived, func(d Discovered) bool { return d.Code == batCode }) {
		t.Fatalf("the art arriving should be reported, got %+v", rep.ArtArrived)
	}

	url, ok := SpriteURL(41, "Zubat", "Test Bat Hat")
	if !ok {
		t.Fatal("the label trainers already recorded should now show the costume art")
	}
	if !AllowedFile(strings.TrimPrefix(url, SpritePath)) {
		t.Error("the proxy must serve the newly mined sprite")
	}
	if got := OriginURL(assetFile(41, batCode)); !strings.Contains(got, testSHA) {
		t.Errorf("the art should be pinned to the commit it was found at, got %s", got)
	}
	if _, ok := SpriteURL(42, "Golbat", "Test Bat Hat"); ok {
		t.Error("a sibling whose art has not arrived must keep falling back")
	}
	if got := CatalogDex()[batCode]; !slices.Equal(got, []int{41}) {
		t.Errorf("CatalogDex should list exactly the species with art, got %v", got)
	}
}

// A species whose art arrives at a later commit than its siblings' must not be served off theirs.
func TestLaterArtGetsItsOwnPin(t *testing.T) {
	const laterSHA = "0123456789abcdef0123456789abcdef01234567"
	seedOverlay(t, nil, nil)
	if err := AdmitWithoutArt(batCode, []int{41, 42}, "Bats 2026 01", "test"); err != nil {
		t.Fatal(err)
	}
	if err := Admit(batCode, []int{41}, testSHA, "", "", SourceNoArt, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := Admit(batCode, []int{42}, laterSHA, "", "", SourceNoArt, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := OriginURL(assetFile(41, batCode)); !strings.Contains(got, testSHA) {
		t.Errorf("dex 41 should keep its own pin, got %s", got)
	}
	if got := OriginURL(assetFile(42, batCode)); !strings.Contains(got, laterSHA) {
		t.Errorf("dex 42 should use the later pin, got %s", got)
	}

	// And it survives a restart, which is where a per-species pin held only in memory would vanish.
	Init(dir)
	if got := OriginURL(assetFile(42, batCode)); !strings.Contains(got, laterSHA) {
		t.Errorf("after a reload dex 42 should still use the later pin, got %s", got)
	}
}

// A dismissed code stays dismissed, art or no art.
func TestDismissedCodeIsNotAdmittedWithoutArt(t *testing.T) {
	seedOverlay(t, nil, &discoveryState{Version: 1, Dismissed: map[string]dismissal{batCode: {By: "tester"}}})
	discoverNoArt(t, nil, bats())
	if _, c := view(); c.covers(batCode, 41) {
		t.Error("an admin already ruled this out; the scan must respect that")
	}
}
