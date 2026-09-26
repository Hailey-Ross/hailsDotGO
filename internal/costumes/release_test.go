package costumes

import (
	"slices"
	"testing"

	"pogo.hails.cc/internal/costumenames"
)

// onDay stands the clock on a fixed day for the duration of a test.
func onDay(t *testing.T, day string) {
	t.Helper()
	real := costumeNow
	costumeNow = func() string { return day }
	t.Cleanup(func() { costumeNow = real })
}

// hold admits a costume, names it, then marks it as not in the game yet.
func hold(t *testing.T, label, releaseDate string) {
	t.Helper()
	seedOverlay(t, nil, nil)
	if err := Admit(testCode, []int{4, 5, 6}, testSHA, "Goggles 2026", label,
		SourceCorroborated, "corroborated", ""); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if err := Name(testCode, label, "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	if err := HoldForRelease(testCode, label, releaseDate); err != nil {
		t.Fatalf("HoldForRelease: %v", err)
	}
}

// The whole point: visible, but not something a trainer can put in their collection.
func TestAnUpcomingCostumeIsShownButNotRecordable(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Upcoming Hat", "2026-10-04")

	if _, ok := SpriteURL(4, "Charmander", "Test Upcoming Hat"); ok {
		t.Error("an upcoming costume must not resolve, or trainers can record something that does not exist")
	}
	if slices.Contains(LabelsForDex(4, "Charmander"), "Test Upcoming Hat") {
		t.Error("an upcoming costume must not be offered as a recordable label")
	}
	// But it IS shown, with its art and its date, which is what "coming soon" means.
	up := UpcomingCostumes()
	i := slices.IndexFunc(up, func(u Upcoming) bool { return u.Code == testCode })
	if i < 0 {
		t.Fatal("an upcoming costume should be listed so trainers can see it is coming")
	}
	if up[i].Label != "Test Upcoming Hat" || up[i].ReleaseDate != "2026-10-04" {
		t.Errorf("row = %+v, want the label and the date", up[i])
	}
	if up[i].SpriteURL == "" || !AllowedFile(assetFile(4, testCode)) {
		t.Error("its art must be servable, or there is nothing to show")
	}
}

// The date arriving has to take effect on its own. The shiny dex shipped this bug once: the
// announced day passed and nothing happened until the next admin write.
func TestTheDateReleasesItWithoutAnyWrite(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Dated Hat", "2026-10-04")

	if _, ok := SpriteURL(4, "Charmander", "Test Dated Hat"); ok {
		t.Fatal("it should be held back the day before")
	}

	onDay(t, "2026-10-04") // the day arrives, nothing else happens

	if _, ok := SpriteURL(4, "Charmander", "Test Dated Hat"); !ok {
		t.Error("the costume should release itself when its day arrives")
	}
	if slices.ContainsFunc(UpcomingCostumes(), func(u Upcoming) bool { return u.Code == testCode }) {
		t.Error("a released costume should leave the upcoming list")
	}
}

// Upstream saying "released" releases it even with no date at all, which is the common case:
// most costumes never get a date, they just turn up.
func TestUpstreamCanReleaseItWithNoDate(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Undated Hat", "")

	if _, ok := SpriteURL(4, "Charmander", "Test Undated Hat"); ok {
		t.Fatal("it should start held back")
	}
	note, err := RecordCheck(testCode, "Test Undated Hat", "", true, "")
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if !note.Released {
		t.Error("upstream saying it is in the game should release it")
	}
	if _, ok := SpriteURL(4, "Charmander", "Test Undated Hat"); !ok {
		t.Error("it should be recordable once released")
	}
}

// The guard you asked for. Upstream renaming a costume after we labelled it means the label may
// now describe something else, so releasing it would put the wrong picture on everything recorded
// afterwards. It must refuse and ask for a human.
func TestARenameUpstreamBlocksTheRelease(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Guarded Hat", "")

	note, err := RecordCheck(testCode, "Something Else Entirely", "", true, "")
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if note.Released {
		t.Error("a costume upstream has renamed must NOT be released automatically")
	}
	if !note.Blocked || note.NameChanged != "Something Else Entirely" {
		t.Errorf("the rename should be reported for a human: %+v", note)
	}
	if _, ok := SpriteURL(4, "Charmander", "Test Guarded Hat"); ok {
		t.Error("it must stay held back while the name is in doubt")
	}
	// And it says so on the row an admin reads.
	up := UpcomingCostumes()
	i := slices.IndexFunc(up, func(u Upcoming) bool { return u.Code == testCode })
	if i < 0 || up[i].NameChanged != "Something Else Entirely" {
		t.Errorf("the upcoming row should carry the new name: %+v", up)
	}
}

// An upstream that has gone quiet is not a rename. Treating silence as a change would freeze
// every waiting costume the moment the naming source had a bad day.
func TestSilenceFromUpstreamIsNotARename(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Quiet Hat", "")

	note, err := RecordCheck(testCode, "", "", true, "")
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if note.Blocked {
		t.Error("an empty upstream name must not be read as a rename")
	}
	if !note.Released {
		t.Error("a costume upstream says is live should still release when it simply has no name to give")
	}
}

// An admin can release it by hand, for a costume that is live in the game before upstream notices.
func TestAnAdminCanReleaseItEarly(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Manual Hat", "2026-12-25")

	note, err := RecordCheck(testCode, "Test Manual Hat", "", false, "hails")
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if !note.Released {
		t.Error("an admin saying so should release it regardless of the date")
	}
	if _, ok := SpriteURL(4, "Charmander", "Test Manual Hat"); !ok {
		t.Error("it should be recordable after a manual release")
	}
}

// Everything that predates this state stays recordable. Holding back by default would have
// silently emptied the picker.
func TestCostumesWithNoReleaseStateStayAvailable(t *testing.T) {
	onDay(t, "2026-09-20")
	seedOverlay(t, goggles(), nil)

	_, c := view()
	if !c.available(testCode) {
		t.Error("an overlay costume with no release state must remain recordable")
	}
	for code := range cat.Codes {
		if !c.available(code) {
			t.Errorf("%s is from the embedded catalog and must remain recordable", code)
			break
		}
	}
}

// The case that broke the first design. f:K_2026_A_01 has shipped in the embedded catalog for a
// while AND upstream says it is not in the game, so release has to be independent of where the
// catalog entry came from. Holding it used to fail outright with "not a runtime costume".
func TestAnEmbeddedCostumeCanBeHeldBack(t *testing.T) {
	onDay(t, "2026-09-21")
	seedOverlay(t, nil, nil)

	// Any code the embedded catalog carries, with a label already pointing at it.
	var code string
	var dex int
	var label string
	for _, u := range Unlabelled() {
		if _, runtime := ovCat.Codes[u.Code]; !runtime && len(u.Dex) > 0 {
			code, dex = u.Code, u.Dex[0]
			break
		}
	}
	if code == "" {
		t.Skip("no unlabelled embedded costume to stand in for the real one")
	}
	label = "Test Embedded Hold"
	if err := Name(code, label, "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	if _, ok := SpriteURL(dex, speciesFor(t, dex), label); !ok {
		t.Skip("this code does not resolve for its first species; nothing to prove here")
	}

	if err := HoldForRelease(code, label, ""); err != nil {
		t.Fatalf("an embedded costume must be holdable: %v", err)
	}
	if _, ok := SpriteURL(dex, speciesFor(t, dex), label); ok {
		t.Error("a held embedded costume must stop being recordable")
	}
	if !slices.ContainsFunc(UpcomingCostumes(), func(u Upcoming) bool { return u.Code == code }) {
		t.Error("it should be listed as coming soon")
	}

	// And upstream saying it landed releases it, exactly as for a discovered one.
	if _, err := RecordCheck(code, label, "", true, ""); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if _, ok := SpriteURL(dex, speciesFor(t, dex), label); !ok {
		t.Error("it should be recordable again once released")
	}
}

// speciesFor finds a species name the label resolver will accept for a dex.
func speciesFor(t *testing.T, dex int) string {
	t.Helper()
	for name, d := range map[string]int{"Pikachu": 25, "Charmander": 4, "Ditto": 132} {
		if d == dex {
			return name
		}
	}
	return ""
}

// The bug production found, and the suite did not: a pass with nothing new upstream returned
// before it ever reached the release work.
//
// That is the STEADY STATE. New costumes are rare; waiting ones need re-asking every hour. So the
// early return silently disabled the entire releasing half, and it would have looked fine forever
// because a quiet pass and a skipped pass are indistinguishable from outside.
func TestAQuietPassStillDoesTheReleaseWork(t *testing.T) {
	onDay(t, "2026-09-20")
	seedOverlay(t, nil, nil)

	// A costume that is already known (so nothing is "fresh") and is waiting on a release.
	forms := stubForms{"TEST_QUIET_2026": {Name: "Quiet 2026", Found: true}}
	files := []string{shiny(25, "f:TEST_QUIET_2026")}
	first := &stubNames{pages: map[string]costumenames.Page{
		"f:TEST_QUIET_2026": {Name: "Harlequin Mask", Released: false, HasReleased: true},
	}}
	if rep := discoverWith(t, files, forms, first); len(rep.Admitted) != 1 || !rep.Admitted[0].Upcoming {
		t.Fatalf("setup: expected one held admission, got %+v", rep.Admitted)
	}
	if err := Name("f:TEST_QUIET_2026", "Harlequin Mask", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}

	// Second pass: upstream has nothing new, but the costume has now landed.
	landed := &stubNames{pages: map[string]costumenames.Page{
		"f:TEST_QUIET_2026": {Name: "Harlequin Mask", Released: true, HasReleased: true},
	}}
	rep := discoverWith(t, files, forms, landed)

	if len(rep.Admitted) != 0 || len(rep.Candidates) != 0 {
		t.Errorf("a quiet pass should discover nothing: %+v", rep)
	}
	if len(rep.Released) != 1 || !rep.Released[0].Released {
		t.Fatalf("a quiet pass must still release what was waiting, got %+v", rep.Released)
	}
	if _, ok := SpriteURL(25, "Pikachu", "Harlequin Mask"); !ok {
		t.Error("the costume should be recordable now that upstream says it landed")
	}
}

// And the other half of the same bug: a costume already in the catalog and awaiting a name has to
// be noticed as unreleased on an otherwise quiet pass. This is f:K_2026_A_01's exact situation.
func TestAQuietPassNoticesAnUnreleasedCostumeAwaitingAName(t *testing.T) {
	onDay(t, "2026-09-20")
	seedOverlay(t, nil, nil)

	forms := stubForms{}
	unnamed := Unlabelled()
	if len(unnamed) == 0 {
		t.Skip("nothing is awaiting a name")
	}
	target := unnamed[0]

	sn := &stubNames{pages: map[string]costumenames.Page{
		target.Code: {Name: "Some Real Name", Released: false, HasReleased: true},
	}}
	// No files at all: nothing whatsoever is new upstream.
	rep := discoverWith(t, nil, forms, sn)

	if !slices.ContainsFunc(rep.Held, func(d Discovered) bool { return d.Code == target.Code }) {
		t.Errorf("a quiet pass should still notice %s is not in the game yet: %+v", target.Code, rep)
	}
	if !slices.ContainsFunc(UpcomingCostumes(), func(u Upcoming) bool { return u.Code == target.Code }) {
		t.Error("it should now be listed as coming soon")
	}
}
