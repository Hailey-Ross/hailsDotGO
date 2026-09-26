package costumes

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"pogo.hails.cc/internal/costumenames"
	"pogo.hails.cc/internal/masterfile"
)

// stubForms is the masterfile, without the network. Keyed by the code as the asset tree spells it.
type stubForms map[string]masterfile.Form

// Keyed by the BARE code, exactly as the real masterfile is: its protos are SPECIES_CODE, so a
// lookup that still carries the "f:" prefix matches nothing. Keying the stub the same way is what
// makes that mistake fail here instead of silently answering "unknown form" in production.
func (s stubForms) Lookup(dex int, code string) masterfile.Form {
	if strings.HasPrefix(code, "f:") || strings.HasPrefix(code, "c:") {
		panic("masterfile lookup got a prefixed code " + code + "; it wants the bare form name")
	}
	return s[code]
}
func (s stubForms) CostumeName(proto string) string {
	if strings.HasPrefix(proto, "c:") {
		panic("costume name lookup got a prefixed code " + proto + "; it wants the bare proto")
	}
	return s[proto].Name
}

func (s stubForms) NameToDex() map[string]int {
	return map[string]int{"Charmander": 4, "Charmeleon": 5, "Charizard": 6,
		"Pikachu": 25, "Raichu": 26, "Vivillon": 666, "Unown": 201, "Corsola": 222}
}

// stubNames is Dittobase, without the network.
type stubNames struct {
	names map[string]string            // code -> what dittobase calls it
	pages map[string]costumenames.Page // richer answers, for the release tests
	hits  int
	asked []string // which codes were looked up, so a test can name the ones that matter
}

func (s *stubNames) Page(dex int, species, code string) (costumenames.Page, error) {
	s.hits++
	s.asked = append(s.asked, code)
	if p, ok := s.pages[code]; ok {
		return p, nil
	}
	n, ok := s.names[code]
	if !ok {
		return costumenames.Page{}, nil
	}
	// Released by default: the decision table above is about whether something IS a costume,
	// not about when it ships. The release tests drive that path directly.
	return costumenames.Page{Name: n, Released: true, HasReleased: true}, nil
}

// stubUpstream drives a whole pass from a list of filenames.
func stubUpstream(t *testing.T, files []string, forms formLookup, names map[string]string) (DiscoveryReport, *stubNames) {
	t.Helper()
	resetAssetCache(t)

	realLoad, realNames := loadMasterfile, newNameSource
	t.Cleanup(func() { loadMasterfile, newNameSource = realLoad, realNames })

	fetchAssets = func() ([]string, string, error) { return files, testSHA, nil }
	loadMasterfile = func() (formLookup, error) {
		if forms == nil {
			return nil, errors.New("masterfile unreachable")
		}
		return forms, nil
	}
	sn := &stubNames{names: names}
	newNameSource = func(string) (nameSource, func(), error) { return sn, func() {}, nil }

	rep, err := Discover(false, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return rep, sn
}

// discoverWith drives a pass against a prepared name source.
func discoverWith(t *testing.T, files []string, forms formLookup, sn *stubNames) DiscoveryReport {
	t.Helper()
	resetAssetCache(t)
	realLoad, realNames := loadMasterfile, newNameSource
	t.Cleanup(func() { loadMasterfile, newNameSource = realLoad, realNames })

	fetchAssets = func() ([]string, string, error) { return files, testSHA, nil }
	loadMasterfile = func() (formLookup, error) { return forms, nil }
	newNameSource = func(string) (nameSource, func(), error) { return sn, func() {}, nil }

	rep, err := Discover(false, "")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return rep
}

func shiny(dex int, code string) string {
	p, name, _ := strings.Cut(code, ":")
	return fmt.Sprintf("pm%d.%s%s.s.icon.png", dex, p, name)
}

// The decision table, against the real cases. This is the test that says whether the feature
// works: every row is a code that actually exists upstream today.
func TestDiscoveryDecisionTable(t *testing.T) {
	costume := masterfile.Form{Name: "x", Found: true, Flagged: true}
	unflagged := masterfile.Form{Name: "Goggles 2026", Found: true}
	regional := masterfile.Form{Name: "Alola", Found: true, Battle: true}

	cases := []struct {
		name  string
		code  string
		dex   int
		form  masterfile.Form
		ditto string
		admit bool
		ask   bool // lands in the review queue
		why   string
	}{
		{"the costume that went missing", "f:TEST_GOGGLES_2026", 4, unflagged, "Friede's Goggles",
			true, false, "unflagged, no battle override, and upstream gave it a real name"},
		{"clone pikachu, the other false negative", "f:TEST_COPY_2019", 25, unflagged, "Clone",
			true, false, "same path: isCostume is missing but a human named it"},
		{"an ordinary flagged costume", "f:TEST_PARTY_2026", 25, costume, "",
			true, false, "upstream's own flag settles it without asking anyone"},
		{"a costume overlay code", "c:TEST_HALLOWEEN_2026", 25, masterfile.Form{}, "",
			true, false, "a c: code can only ever be a costume"},

		{"a vivillon wing pattern", "f:TEST_MEADOW", 666, unflagged, "Vivillon Meadow",
			false, false, "dittobase only echoes the code back, and it carries no year"},
		{"an unown letter", "f:TEST_UNOWN_A", 201, unflagged, "Unown A",
			false, false, "same: an echo is not corroboration"},
		{"a regional form", "f:TEST_ALOLA", 26, regional, "Alolan Raichu",
			false, false, "the battle-identity veto stops this before dittobase is asked"},

		{"a new costume nobody has named", "f:TEST_K_2026_A_01", 25, unflagged, "Pikachu K 2026 A 01",
			false, true, "an echo means uncertain, so a human looks at the sprite"},
		{"a year-shaped code upstream has no record of", "f:TEST_MYSTERY_2026", 25, masterfile.Form{}, "",
			false, true, "worth a human's time even though nothing knows it"},
		{"an unknown code with no year", "f:TEST_SOMETHING", 25, masterfile.Form{}, "",
			false, false, "almost certainly an ordinary form; stay quiet"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seedOverlay(t, nil, nil)
			forms := stubForms{}
			if c.form.Found || c.form.Flagged {
				forms[strings.TrimPrefix(c.code, "f:")] = c.form
			}
			ditto := map[string]string{}
			if c.ditto != "" {
				ditto[c.code] = c.ditto
			}
			rep, _ := stubUpstream(t, []string{shiny(c.dex, c.code)}, forms, ditto)

			admitted := slices.ContainsFunc(rep.Admitted, func(d Discovered) bool { return d.Code == c.code })
			queued := slices.ContainsFunc(rep.Candidates, func(d Discovered) bool { return d.Code == c.code })

			if admitted != c.admit || queued != c.ask {
				t.Errorf("%s: admitted=%v queued=%v, want admitted=%v queued=%v\n  %s",
					c.code, admitted, queued, c.admit, c.ask, c.why)
			}
			if c.admit && !covers(c.code, c.dex) {
				t.Errorf("%s was admitted but does not resolve", c.code)
			}
			// Admission must never create a label. Asked of the CODE, not of the name: the real
			// curated label may legitimately resolve that same name through a different code.
			if c.admit && !slices.ContainsFunc(Unlabelled(), func(u Unnamed) bool { return u.Code == c.code }) {
				t.Errorf("%s: admission must leave the code unnamed and in the review queue", c.code)
			}
		})
	}
}

// The end-to-end shape of the thing that went wrong: the goggles arrive, get a name suggestion,
// and wait for one click.
func TestGogglesArriveReadyToName(t *testing.T) {
	seedOverlay(t, nil, nil)
	forms := stubForms{"TEST_GOGGLES_2026": {Name: "Goggles 2026", Found: true}}
	files := []string{shiny(4, "f:TEST_GOGGLES_2026"), shiny(5, "f:TEST_GOGGLES_2026"), shiny(6, "f:TEST_GOGGLES_2026")}

	rep, _ := stubUpstream(t, files, forms, map[string]string{"f:TEST_GOGGLES_2026": "Friede's Goggles"})

	if len(rep.Admitted) != 1 {
		t.Fatalf("expected one admission, got %+v", rep)
	}
	got := rep.Admitted[0]
	if got.Label != "Friede's Goggles" {
		t.Errorf("suggested name = %q, want Friede's Goggles", got.Label)
	}
	if !slices.Equal(got.Dex, []int{4, 5, 6}) {
		t.Errorf("dex = %v, want the whole Charmander line", got.Dex)
	}
	// Resolvable, sprite servable, and waiting on a human for its name.
	for _, dex := range []int{4, 5, 6} {
		if !AllowedFile(assetFile(dex, "f:TEST_GOGGLES_2026")) {
			t.Errorf("sprite for dex %d should be servable", dex)
		}
	}
	u := Unlabelled()
	idx := slices.IndexFunc(u, func(n Unnamed) bool { return n.Code == "f:TEST_GOGGLES_2026" })
	if idx < 0 {
		t.Fatal("an admitted, unnamed costume belongs in the review queue")
	}
	if u[idx].Suggested != "Friede's Goggles" {
		t.Errorf("the queue row should prefill the suggested name, got %q", u[idx].Suggested)
	}
}

// The admin tab turns Pending plus Suggested into its one-click approve, so if the server stops
// sending them the automation silently degrades to ordinary typing and nothing else complains.
// The browser check cannot catch that: it builds its own payload, so it would happily keep testing
// a field the server never sends. This is the test that keeps the two honest.
func TestDiscoveredRowCarriesItsProvenance(t *testing.T) {
	seedOverlay(t, nil, nil)
	forms := stubForms{"TEST_GOGGLES_2026": {Name: "Goggles 2026", Found: true}}
	stubUpstream(t, []string{shiny(4, "f:TEST_GOGGLES_2026")}, forms,
		map[string]string{"f:TEST_GOGGLES_2026": "Friede's Goggles"})

	u := Unlabelled()
	i := slices.IndexFunc(u, func(n Unnamed) bool { return n.Code == "f:TEST_GOGGLES_2026" })
	if i < 0 {
		t.Fatal("the discovered costume is missing from the review queue")
	}
	row := u[i]
	if !row.Pending {
		t.Error("a discovered row must be marked pending, or the tab never offers the one-click approve")
	}
	if row.Suggested == "" {
		t.Error("a corroborated row must carry the suggested name the approve button prints")
	}
	if row.Source != SourceCorroborated || row.Why == "" || row.DiscoveredAt == "" {
		t.Errorf("a discovered row should say where it came from: source=%q why=%q at=%q",
			row.Source, row.Why, row.DiscoveredAt)
	}

	// A code from the embedded catalog has no provenance to report and must not claim any.
	for _, n := range u {
		if n.Code != "f:TEST_GOGGLES_2026" && n.Pending {
			t.Errorf("%s is from the embedded catalog and must not be marked pending", n.Code)
		}
	}
}

// Female-only art never creates a code, exactly as the catalog builder and drift check agree.
func TestFemaleOnlyArtIsNotACostume(t *testing.T) {
	seedOverlay(t, nil, nil)
	forms := stubForms{"TEST_FRILL_2026": {Name: "Frill 2026", Found: true, Flagged: true}}

	rep, _ := stubUpstream(t, []string{"pm25.fTEST_FRILL_2026.g2.s.icon.png"}, forms, nil)

	if rep.Changed() {
		t.Errorf("a .g2-only asset must not create a code: %+v", rep)
	}
}

// A second pass over the same upstream must decide nothing new, or an hourly job re-alerts hourly.
func TestDiscoveryIsIdempotent(t *testing.T) {
	seedOverlay(t, nil, nil)
	forms := stubForms{"TEST_GOGGLES_2026": {Name: "Goggles 2026", Found: true}}
	files := []string{shiny(4, "f:TEST_GOGGLES_2026"), shiny(25, "f:TEST_K_2026_A_01")}
	names := map[string]string{"f:TEST_GOGGLES_2026": "Friede's Goggles"}

	first, _ := stubUpstream(t, files, forms, names)
	if !first.Changed() {
		t.Fatal("the first pass should find both codes")
	}

	second, sn := stubUpstream(t, files, forms, names)
	if second.Changed() {
		t.Errorf("a second pass decided again: %+v", second)
	}
	// It must not ask again about anything it already settled. It MAY ask about costumes still
	// awaiting a name whose release state nobody has stated, which is the point of that check;
	// in production those are answered from the name cache without touching the network.
	for _, code := range sn.asked {
		if code == "f:TEST_GOGGLES_2026" || code == "f:TEST_K_2026_A_01" {
			t.Errorf("a second pass re-asked about %s, which it had already decided", code)
		}
	}
}

// With the masterfile down, event-shaped codes still reach a human. Nothing is admitted on a guess.
func TestMasterfileDownStillRaisesAHand(t *testing.T) {
	seedOverlay(t, nil, nil)
	files := []string{shiny(4, "f:TEST_GOGGLES_2026"), shiny(666, "f:TEST_MEADOW")}

	rep, _ := stubUpstream(t, files, nil, nil)

	if len(rep.Admitted) != 0 {
		t.Errorf("nothing should be admitted without the masterfile: %+v", rep.Admitted)
	}
	if len(rep.Candidates) != 1 || rep.Candidates[0].Code != "f:TEST_GOGGLES_2026" {
		t.Errorf("the event-shaped code should reach a human, got %+v", rep.Candidates)
	}
	if len(rep.Notes) == 0 {
		t.Error("a degraded pass should say why")
	}
}

// A dismissed code must never come back, however many times upstream still lists it.
func TestDismissedCodeStaysDismissed(t *testing.T) {
	seedOverlay(t, nil, nil)
	forms := stubForms{}
	files := []string{shiny(25, "f:TEST_MYSTERY_2026")}

	if rep, _ := stubUpstream(t, files, forms, nil); len(rep.Candidates) != 1 {
		t.Fatalf("expected one candidate, got %+v", rep)
	}
	if err := Dismiss("f:TEST_MYSTERY_2026", "tester"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	if rep, _ := stubUpstream(t, files, forms, nil); rep.Changed() {
		t.Errorf("a dismissed code came back: %+v", rep)
	}
}

// The Dittobase budget bounds a big event drop, and the rest is picked up next pass.
func TestNameLookupsAreBounded(t *testing.T) {
	seedOverlay(t, nil, nil)
	forms := stubForms{}
	var files []string
	for i := range maxNameLookups + 5 {
		code := fmt.Sprintf("f:TEST_DROP_2026_%02d", i)
		forms[strings.TrimPrefix(code, "f:")] = masterfile.Form{Name: code, Found: true}
		files = append(files, shiny(25, code))
	}

	_, sn := stubUpstream(t, files, forms, nil)

	if sn.hits > maxNameLookups {
		t.Errorf("made %d lookups, want at most %d: a drop must not arrive as a burst", sn.hits, maxNameLookups)
	}
}

// A costume whose art is mined before the event that ships it must arrive SHOWN but not
// recordable, rather than arriving live and letting trainers record something they cannot have.
func TestAnUnreleasedCostumeArrivesHeldBack(t *testing.T) {
	onDay(t, "2026-09-20")
	seedOverlay(t, nil, nil)

	forms := stubForms{"TEST_SOON_2026": {Name: "Soon 2026", Found: true}}
	sn := &stubNames{pages: map[string]costumenames.Page{
		"f:TEST_SOON_2026": {Name: "Aurora Crown", Released: false, HasReleased: true, ReleaseDate: "2026-10-04"},
	}}
	rep := discoverWith(t, []string{shiny(25, "f:TEST_SOON_2026")}, forms, sn)

	if len(rep.Admitted) != 1 || !rep.Admitted[0].Upcoming {
		t.Fatalf("expected one upcoming admission, got %+v", rep.Admitted)
	}
	if rep.Admitted[0].ReleaseDate != "2026-10-04" {
		t.Errorf("release date = %q, want 2026-10-04", rep.Admitted[0].ReleaseDate)
	}
	if !slices.ContainsFunc(UpcomingCostumes(), func(u Upcoming) bool { return u.Code == "f:TEST_SOON_2026" }) {
		t.Error("it should be listed as coming soon")
	}
	// Named by an admin, it is still not recordable until its day.
	if err := Name("f:TEST_SOON_2026", "Aurora Crown", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	if _, ok := SpriteURL(25, "Pikachu", "Aurora Crown"); ok {
		t.Error("a named but unreleased costume must still refuse to resolve")
	}

	onDay(t, "2026-10-04")
	if _, ok := SpriteURL(25, "Pikachu", "Aurora Crown"); !ok {
		t.Error("it should become recordable on its day, with no further action")
	}
}

// A costume upstream already calls released arrives ready to use, not held back.
func TestAReleasedCostumeArrivesUsable(t *testing.T) {
	onDay(t, "2026-09-20")
	seedOverlay(t, nil, nil)

	forms := stubForms{"TEST_LIVE_2026": {Name: "Live 2026", Found: true}}
	sn := &stubNames{pages: map[string]costumenames.Page{
		"f:TEST_LIVE_2026": {Name: "Nebula Visor", Released: true, HasReleased: true},
	}}
	rep := discoverWith(t, []string{shiny(25, "f:TEST_LIVE_2026")}, forms, sn)

	if len(rep.Admitted) != 1 || rep.Admitted[0].Upcoming {
		t.Fatalf("a released costume should not be held back: %+v", rep.Admitted)
	}
	if len(UpcomingCostumes()) != 0 {
		t.Errorf("nothing should be waiting: %+v", UpcomingCostumes())
	}
}
