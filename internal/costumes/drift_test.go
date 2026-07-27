package costumes

import (
	"slices"
	"strings"
	"testing"
)

// fakeForms stands in for the masterfile. Only the codes listed are costumes; everything else is
// an ordinary alternate form, which is exactly the distinction the real file draws.
type fakeForms map[string]bool

func (f fakeForms) IsCostumeForm(dex int, code string) (string, bool) {
	if !f[code] {
		return "", false
	}
	return code, true
}

// The bug this whole rule exists for. Every costume the game has shipped since roughly 2024 is a
// .f full form code, and the check used to discard all of them and keep only .c, so it answered
// "nothing new upstream" no matter what the game released. A trainer reported a costume as missing
// and the admin panel agreed with them, wrongly.
func TestNewCodesSeesFullFormCostumes(t *testing.T) {
	files := []string{
		"pm25.fANNIVERSARY_2026.s.icon.png", // a costume, flagged upstream
		"pm25.fANNIVERSARY_2026.g2.s.icon.png",
		"pm25.fGIGANTAMAX.s.icon.png",     // a battle form, must NOT be reported
		"pm26.fALOLA.s.icon.png",          // a regional form, must NOT be reported
		"pm888.fCROWNED_SWORD.s.icon.png", // a battle form, must NOT be reported
		"pm25.cBRAND_NEW_HAT.s.icon.png",  // .c is unambiguous, always reported
		"pm25.fVISOR_2026.icon.png",       // not shiny: we never render it, so it does not count
	}
	forms := fakeForms{"ANNIVERSARY_2026": true}

	added, unverified := newCodes(files, map[string]bool{}, map[string]bool{}, forms)

	want := []string{"c:BRAND_NEW_HAT", "f:ANNIVERSARY_2026"}
	if !slices.Equal(added, want) {
		t.Errorf("added = %v, want %v", added, want)
	}
	if unverified != 0 {
		t.Errorf("unverified = %d, want 0 when the masterfile answered", unverified)
	}
}

// A code already in the catalog is not news, however it got there.
func TestNewCodesIgnoresWhatWeAlreadyHave(t *testing.T) {
	files := []string{"pm25.fANNIVERSARY_2026.s.icon.png", "pm25.cANNIVERSARY.s.icon.png"}
	known := map[string]bool{"f:ANNIVERSARY_2026": true, "c:ANNIVERSARY": true}

	added, _ := newCodes(files, known, map[string]bool{}, fakeForms{"ANNIVERSARY_2026": true})
	if len(added) != 0 {
		t.Errorf("added = %v, want nothing: both codes are already synced", added)
	}
}

// isCostume has false negatives: Clone Pikachu (PIKACHU_COPY_2019) carries no flag at all despite
// its shiny asset being live. A curated label vouching for the code has to be enough on its own,
// or the check would drop a costume trainers have already recorded.
func TestNewCodesTrustsACuratedLabelOverTheFlag(t *testing.T) {
	files := []string{"pm25.fCOPY_2019.s.icon.png"}
	curated := map[string]bool{"f:COPY_2019": true}

	added, _ := newCodes(files, map[string]bool{}, curated, fakeForms{})
	if !slices.Equal(added, []string{"f:COPY_2019"}) {
		t.Errorf("added = %v, want [f:COPY_2019]: a curated label vouches for it", added)
	}
}

// The masterfile is a third party. If it is unreachable the .c half of the answer is still sound,
// so the check reports that half and counts what it could not judge, rather than failing outright.
func TestNewCodesDegradesWhenTheMasterfileIsUnreachable(t *testing.T) {
	files := []string{
		"pm25.cBRAND_NEW_HAT.s.icon.png",
		"pm25.fANNIVERSARY_2026.s.icon.png",
		"pm26.fALOLA.s.icon.png",
	}

	added, unverified := newCodes(files, map[string]bool{}, map[string]bool{}, nil)

	if !slices.Equal(added, []string{"c:BRAND_NEW_HAT"}) {
		t.Errorf("added = %v, want just the .c code", added)
	}
	if unverified != 2 {
		t.Errorf("unverified = %d, want 2 (the two .f codes nobody could judge)", unverified)
	}
}

// The catalog and the labels must agree that the costume behind the July 2026 Willow event is
// real, has art on Pikachu, and is reachable by the name the game and every event site use for it.
func TestWillowCostumeResolves(t *testing.T) {
	url, ok := SpriteURL(25, "Pikachu", "Willow's Lab Coat")
	if !ok {
		t.Fatal("Willow's Lab Coat did not resolve for Pikachu")
	}
	if !strings.Contains(url, ".fANNIVERSARY_2026.") {
		t.Errorf("resolved to %s, want the f:ANNIVERSARY_2026 art", url)
	}
}

// Trainers type the name they read in the event notes, not ours. Both apostrophes matter: a phone
// keyboard produces the curly one, a desktop keyboard the straight one, and alias lookup is an
// exact map hit with no normalisation, so every spelling has to be listed.
func TestWillowAliasesResolveToTheSameArt(t *testing.T) {
	canonical, ok := SpriteURL(25, "Pikachu", "Willow's Lab Coat")
	if !ok {
		t.Fatal("Willow's Lab Coat did not resolve for Pikachu")
	}
	for _, alias := range []string{
		"Professor Willow's Assistant",
		"Professor Willow's assistant",
		"Professor Willow’s Assistant",
		"Professor Willow’s assistant",
	} {
		url, ok := SpriteURL(25, "Pikachu", alias)
		if !ok {
			t.Errorf("%q did not resolve", alias)
			continue
		}
		if url != canonical {
			t.Errorf("%q -> %s, want %s", alias, url, canonical)
		}
	}
}
