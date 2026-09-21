package costumes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A code the embedded catalog will never carry, so these tests cannot collide with real data.
const (
	testCode = "f:TEST_GOGGLES_2026"
	testSHA  = "d4432a333e123bfe8f44153f51bf58aa8d34d5b1"
	testDex  = 4
)

// seedOverlay writes a catalog overlay into a fresh directory and Inits from it. The returned
// cleanup restores the package to the embedded-only state the other tests expect.
func seedOverlay(t *testing.T, codes map[string]*catEntry, state *discoveryState) string {
	t.Helper()
	d := t.TempDir()
	if codes != nil {
		write(t, filepath.Join(d, "catalog.json"), catalogOverlay{Version: 1, Codes: codes})
	}
	if state != nil {
		write(t, filepath.Join(d, "discovery.json"), state)
	}
	Init(d)
	t.Cleanup(func() { Init(t.TempDir()) })
	return d
}

func write(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func goggles() map[string]*catEntry {
	return map[string]*catEntry{testCode: {
		AssetBase:    AssetBaseFor(testSHA),
		SourceCommit: testSHA,
		Pretty:       "Goggles 2026",
		Suggested:    "Test Overlay Costume",
		Dex:          []int{4, 5, 6},
		Source:       SourceCorroborated,
	}}
}

// The whole point: a costume found after the last deploy resolves without one.
func TestOverlayCodeIsResolvable(t *testing.T) {
	seedOverlay(t, goggles(), nil)

	if !covers(testCode, testDex) {
		t.Fatal("an overlay code must have art for its species")
	}
	if _, ok := SpriteURLFor(testDex, testCode); !ok {
		t.Error("SpriteURLFor should produce a URL for an overlay code")
	}
	if !AllowedFile(assetFile(testDex, testCode)) {
		t.Error("the proxy must serve an overlay sprite, or the costume is invisible")
	}
	// It has no label, so it is inert: nothing a trainer can type points at it yet.
	if slices.Contains(LabelsForDex(testDex, "Charmander"), "Test Overlay Costume") {
		t.Error("admitting a code must NOT create a label; naming stays a human decision")
	}
	// And it shows up in the review queue, which is where a human finds it.
	if !slices.ContainsFunc(Unlabelled(), func(u Unnamed) bool { return u.Code == testCode }) {
		t.Error("an unnamed overlay code belongs in the review backlog")
	}
}

// The constraint most likely to regress: a discovered sprite exists only at a NEWER commit than
// the embedded catalog pins, so serving it off the embedded base 404s.
func TestOverlayCodeUsesItsOwnAssetPin(t *testing.T) {
	seedOverlay(t, goggles(), nil)

	got := OriginURL(assetFile(testDex, testCode))
	if !strings.Contains(got, testSHA) {
		t.Errorf("overlay sprite should be pinned to its own commit %s, got %s", testSHA, got)
	}

	// An embedded code keeps the embedded pin, even while an overlay exists.
	var embedded string
	for code, e := range cat.Codes {
		if len(e.Dex) > 0 {
			embedded = OriginURL(assetFile(e.Dex[0], code))
			break
		}
	}
	if !strings.HasPrefix(embedded, cat.AssetBase) {
		t.Errorf("an embedded code must keep the embedded pin, got %s", embedded)
	}
}

// A costume already in the embedded catalog can gain a species whose art is newer than the rest of
// it. The new species must use the new pin while the old ones keep theirs.
func TestGrownCodeSplitsItsPinsPerSpecies(t *testing.T) {
	var code string
	var oldDex int
	for c, e := range cat.Codes {
		if len(e.Dex) > 0 {
			code, oldDex = c, e.Dex[0]
			break
		}
	}
	if code == "" {
		t.Skip("empty embedded catalog")
	}
	const newDex = 999 // no real species; only the pin resolution is under test

	seedOverlay(t, map[string]*catEntry{code: {
		AssetBase: AssetBaseFor(testSHA), SourceCommit: testSHA, Dex: []int{newDex},
	}}, nil)

	if got := OriginURL(assetFile(newDex, code)); !strings.Contains(got, testSHA) {
		t.Errorf("the grown species should use the overlay pin, got %s", got)
	}
	if got := OriginURL(assetFile(oldDex, code)); !strings.HasPrefix(got, cat.AssetBase) {
		t.Errorf("the original species must keep the embedded pin, got %s", got)
	}
	if !covers(code, newDex) {
		t.Error("the grown species should be covered by the merged catalog")
	}
}

// A candidate may be LOOKED at but never resolved. Two questions, two answers.
func TestCandidateIsShowableButNotResolvable(t *testing.T) {
	const cand = "f:TEST_MYSTERY_2026"
	seedOverlay(t, nil, &discoveryState{
		Version: 1,
		Candidates: map[string]*Candidate{cand: {
			Code: cand, Dex: []int{25}, Why: "upstream does not flag it and nothing corroborates it",
		}},
	})

	if !AllowedFile(assetFile(25, cand)) {
		t.Error("an admin must be able to see a candidate's sprite to judge it")
	}
	if covers(cand, 25) {
		t.Error("a candidate must NOT resolve: nothing a trainer types may point at it")
	}
	if err := Name(cand, "Mystery Hat", "tester"); err == nil {
		t.Error("naming a candidate should be refused until it is admitted")
	}
	// The gate is still closed to anything upstream never handed us.
	if AllowedFile(assetFile(25, "f:TOTALLY_MADE_UP")) {
		t.Error("an unknown code must still be refused, or the proxy is an open proxy")
	}
}

// Once a deploy brings the code into the embedded catalog, the overlay copy is redundant. Left
// alone it would keep winning with its own stale pin.
func TestOverlayEntryDropsOnceEmbedded(t *testing.T) {
	var code string
	var dex int
	for c, e := range cat.Codes {
		if len(e.Dex) > 0 {
			code, dex = c, e.Dex[0]
			break
		}
	}
	if code == "" {
		t.Skip("empty embedded catalog")
	}

	d := seedOverlay(t, map[string]*catEntry{code: {
		AssetBase: AssetBaseFor(testSHA), SourceCommit: testSHA, Dex: []int{dex},
	}}, nil)

	if got := OriginURL(assetFile(dex, code)); !strings.HasPrefix(got, cat.AssetBase) {
		t.Errorf("the embedded pin must win once it covers the species, got %s", got)
	}
	// And the redundant entry is gone from memory, so the next write does not persist it.
	if _, ok := ovCat.Codes[code]; ok {
		t.Error("a superseded overlay entry should be dropped on load")
	}
	_ = d
}

// The landmine: the label loader drops labels whose code is not in the catalog. Load the catalog
// overlay second and every boot silently deletes the name an admin gave a discovered costume.
func TestLabelForAnOverlayCodeSurvivesARestart(t *testing.T) {
	d := seedOverlay(t, goggles(), nil)

	if err := Name(testCode, "Test Overlay Costume", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	if !slices.Contains(LabelsForDex(testDex, "Charmander"), "Test Overlay Costume") {
		t.Fatal("the label should be live immediately")
	}

	Init(d) // restart

	if !slices.Contains(LabelsForDex(testDex, "Charmander"), "Test Overlay Costume") {
		t.Error("the label did not survive a restart: load order regressed")
	}
	// And it is still on disk, not quietly pruned away.
	b, err := os.ReadFile(filepath.Join(d, "labels.json"))
	if err != nil || !strings.Contains(string(b), testCode) {
		t.Error("the label must still be persisted after a reload")
	}
}

// Even when the catalog overlay is lost, a label is user data and must not be deleted from disk.
func TestAnInertLabelIsKeptOnDisk(t *testing.T) {
	d := seedOverlay(t, goggles(), nil)
	if err := Name(testCode, "Test Overlay Costume", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}

	// The catalog overlay vanishes; the label now points at a code with no art.
	if err := os.Remove(filepath.Join(d, "catalog.json")); err != nil {
		t.Fatal(err)
	}
	Init(d)

	if slices.Contains(LabelsForDex(testDex, "Charmander"), "Test Overlay Costume") {
		t.Error("a label with no art must not resolve")
	}
	// Force a write, which is what used to persist the pruned list.
	code, dex := anUnnamedCode(t)
	_ = dex
	if err := Name(code, "Test Label For Persistence", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(d, "labels.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), testCode) {
		t.Error("an inert label was deleted from disk: that is user data loss, not a tidy-up")
	}
}

func TestCorruptCatalogOverlayFallsBackInsteadOfCrashing(t *testing.T) {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "catalog.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "discovery.json"), []byte("["), 0o644); err != nil {
		t.Fatal(err)
	}
	Init(d)
	t.Cleanup(func() { Init(t.TempDir()) })

	if len(Unlabelled()) == 0 {
		t.Error("a corrupt overlay must fall back to the embedded catalog, not empty the site")
	}
}

// Admit and Dismiss are durable, and a restart must not re-ask a question already answered.
func TestAdmitAndDismissSurviveARestart(t *testing.T) {
	const cand = "f:TEST_MYSTERY_2026"
	d := seedOverlay(t, nil, &discoveryState{
		Version:    1,
		Candidates: map[string]*Candidate{cand: {Code: cand, Dex: []int{25}, Why: "unjudged"}},
	})

	if err := Admit(testCode, []int{4, 5, 6}, testSHA, "Goggles 2026", "Friede's Goggles",
		SourceCorroborated, "dittobase names it", ""); err != nil {
		t.Fatalf("Admit: %v", err)
	}
	if err := Dismiss(cand, "tester"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}

	Init(d) // restart

	if !covers(testCode, 4) {
		t.Error("an admitted code should survive a restart")
	}
	if !Dismissed(cand) {
		t.Error("a dismissal should survive a restart, or the queue refills every hour")
	}
	if len(Candidates()) != 0 {
		t.Error("a dismissed candidate must not come back")
	}
}

// An hourly job must not alert hourly about the same costume.
func TestAlertsAreNotRepeatedAfterARestart(t *testing.T) {
	d := seedOverlay(t, goggles(), nil)

	pending := PendingAlerts()
	if len(pending) != 1 || pending[0].Code != testCode {
		t.Fatalf("expected one pending alert, got %v", pending)
	}
	if pending[0].Label != "Test Overlay Costume" {
		t.Errorf("an alert should name the costume, got %q", pending[0].Label)
	}
	if err := MarkAlerted([]string{testCode}); err != nil {
		t.Fatalf("MarkAlerted: %v", err)
	}
	if len(PendingAlerts()) != 0 {
		t.Fatal("a marked alert should not be pending")
	}

	Init(d) // restart

	if got := PendingAlerts(); len(got) != 0 {
		t.Errorf("a restart re-alerted: %v (this is what the in-memory drift cache got wrong)", got)
	}
}

// AddCandidate must not reset an alert stamp, or the queue re-alerts every pass.
func TestAddCandidateIsIdempotent(t *testing.T) {
	const cand = "f:TEST_MYSTERY_2026"
	seedOverlay(t, nil, nil)

	c := Candidate{Code: cand, Dex: []int{25}, Why: "unjudged"}
	for range 3 {
		if err := AddCandidate(c); err != nil {
			t.Fatalf("AddCandidate: %v", err)
		}
	}
	if len(Candidates()) != 1 {
		t.Errorf("repeated passes should not duplicate a candidate: %v", Candidates())
	}
	if err := MarkAlerted([]string{cand}); err != nil {
		t.Fatal(err)
	}
	if err := AddCandidate(c); err != nil {
		t.Fatal(err)
	}
	if len(PendingAlerts()) != 0 {
		t.Error("re-adding an existing candidate must not make it pending again")
	}
}

// The browser resolver compiles catalog.json in, so anything the overlay adds has to be injected
// or the picker will not offer it however well the server resolves it.
func TestCatalogDeltaCarriesOnlyWhatTheBrowserLacks(t *testing.T) {
	seedOverlay(t, goggles(), nil)

	delta := CatalogDelta()
	if _, ok := delta[testCode]; !ok {
		t.Error("a discovered code must reach the browser, or it stays untypeable in the picker")
	}
	for code := range delta {
		if _, embedded := cat.Codes[code]; embedded {
			t.Errorf("%s is already compiled into the page; sending it again is waste", code)
		}
	}
}

// The CDN template is duplicated from cmd/synccostumes because that tool cannot import this
// package. Pin the copies together here, so a divergence fails on a dev box instead of serving
// 404 sprites in production.
func TestEmbeddedAssetBaseMatchesTheCDNTemplate(t *testing.T) {
	if want := AssetBaseFor(cat.SourceCommit); cat.AssetBase != want {
		t.Errorf("catalog.json assetBase and cmd/synccostumes's cdnBase have diverged:\n got %s\nwant %s",
			cat.AssetBase, want)
	}
}

func TestReviewCountCountsBothQueues(t *testing.T) {
	const cand = "f:TEST_MYSTERY_2026"
	seedOverlay(t, goggles(), &discoveryState{
		Version:    1,
		Candidates: map[string]*Candidate{cand: {Code: cand, Dex: []int{25}, Why: "unjudged"}},
	})

	base := len(Unlabelled()) + len(Candidates())
	if got := ReviewCount(); got != base {
		t.Errorf("ReviewCount = %d, want %d", got, base)
	}
	if !slices.ContainsFunc(Candidates(), func(c Candidate) bool {
		return c.Code == cand && strings.HasPrefix(c.SpriteURL, SpritePath)
	}) {
		t.Error("a candidate row needs a sprite URL, since looking at the art is the whole point")
	}
}

func TestAdmitRefusesNonsense(t *testing.T) {
	seedOverlay(t, nil, nil)
	if err := Admit("", []int{1}, testSHA, "", "", SourceAdmin, "", "x"); err == nil {
		t.Error("an empty code should be refused")
	}
	if err := Admit(testCode, nil, testSHA, "", "", SourceAdmin, "", "x"); err == nil {
		t.Error("a code with no species should be refused")
	}
	if got := fmt.Sprint(Candidates()); got != "[]" {
		t.Errorf("nothing should have been recorded, got %s", got)
	}
}
