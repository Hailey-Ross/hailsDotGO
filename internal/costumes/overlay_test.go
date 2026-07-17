package costumes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// anUnnamedCode returns a costume nobody has named, and the species that can wear it.
func anUnnamedCode(t *testing.T) (string, int) {
	t.Helper()
	list := Unlabelled()
	if len(list) == 0 {
		t.Skip("every costume is named")
	}
	return list[0].Code, list[0].Dex[0]
}

// The whole point of the feature: naming a costume makes it selectable straight away, with no
// rebuild and no deploy.
func TestNameMakesACostumeSelectableImmediately(t *testing.T) {
	Init(t.TempDir())
	code, dex := anUnnamedCode(t)

	if _, ok := SpriteURL(dex, "", "Test Cap"); ok {
		t.Fatal("the label resolved before it was created")
	}

	if err := Name(code, "Test Cap", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}

	url, ok := SpriteURL(dex, "", "Test Cap")
	if !ok {
		t.Fatal("the costume did not resolve after being named")
	}
	if !AllowedFile(url[len(SpritePath):]) {
		t.Errorf("named costume resolved to %s, which the sprite proxy would refuse", url)
	}
	if !slices.Contains(LabelsForDex(dex, ""), "Test Cap") {
		t.Error("the picker does not offer the costume that was just named")
	}
	for _, u := range Unlabelled() {
		if u.Code == code {
			t.Error("the costume is still in the review backlog after being named")
		}
	}
}

// The overlay is on disk, not in memory: a restart (or a deploy) must not lose it.
func TestNameSurvivesAReload(t *testing.T) {
	dir := t.TempDir()
	Init(dir)
	code, dex := anUnnamedCode(t)

	if err := Name(code, "Persisted Cap", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}

	Init(dir) // simulate a restart after a deploy

	if _, ok := SpriteURL(dex, "", "Persisted Cap"); !ok {
		t.Fatal("the name was lost across a reload; it is not really persisted")
	}
}

// Add-only, enforced in the package and not merely hidden in the UI. A costume label is user data
// (user_shinies.costume is free text), so renaming one orphans the art on entries people saved.
func TestNameRefusesToRenameAnExistingLabel(t *testing.T) {
	Init(t.TempDir())

	// "Visor" is curated, pointing at the Spring 2020 costume.
	if err := Name("c:SPRING_2020_NOEVOLVE", "Something Else", "tester"); err == nil {
		t.Fatal("renaming a curated costume was allowed")
	}

	code, _ := anUnnamedCode(t)
	if err := Name(code, "First", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	if err := Name(code, "Second", "tester"); err == nil {
		t.Error("renaming a costume named through the overlay was allowed")
	}
	if _, ok := SpriteURL(0, "", "Second"); ok {
		t.Error("the second name took effect")
	}
}

// A duplicate label is fine when the two codes cover different species (this is how "Party Hat"
// already maps to two codes), but not when they overlap, because then one shadows the other.
func TestNameRejectsAShadowingCollision(t *testing.T) {
	Init(t.TempDir())

	// c:FALL_2018 covers Pikachu (25), and so does the curated "Party Hat" (c:ANNIVERSARY).
	if err := Name("c:FALL_2018", "Party Hat", "tester"); err == nil {
		t.Error("a label that would shadow an existing one on the same species was allowed")
	}

	// A disjoint dex set is legitimate. c:ROYAL_NOEVOLVE is Nidoqueen/Nidoking (31, 34), which no
	// "Party Hat" covers.
	if err := Name("c:ROYAL_NOEVOLVE", "Party Hat", "tester"); err != nil {
		t.Errorf("a disjoint duplicate label was rejected: %v", err)
	}
	if _, ok := SpriteURL(31, "", "Party Hat"); !ok {
		t.Error("the disjoint duplicate did not resolve")
	}
	// ...and it must not have disturbed the original.
	url, ok := SpriteURL(25, "Pikachu", "Party Hat")
	if !ok || !containsStr(url, "cANNIVERSARY") {
		t.Errorf("the curated Party Hat changed to %s", url)
	}
}

// Unname is the escape hatch for a typo. It only ever removes an overlay entry; a curated label
// cannot be removed. (The caller is responsible for checking that no trainer uses it first.)
func TestUnnameOnlyRemovesOverlayLabels(t *testing.T) {
	Init(t.TempDir())
	code, dex := anUnnamedCode(t)

	if err := Unname("c:SPRING_2020_NOEVOLVE"); err == nil {
		t.Error("a curated label was removable")
	}

	if err := Name(code, "Typo Cap", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	if err := Unname(code); err != nil {
		t.Fatalf("Unname: %v", err)
	}
	if _, ok := SpriteURL(dex, "", "Typo Cap"); ok {
		t.Error("the label still resolves after being removed")
	}
}

// Hiding drops a non-costume out of the review queue.
func TestHideRemovesFromTheBacklog(t *testing.T) {
	Init(t.TempDir())
	code, _ := anUnnamedCode(t)

	if err := Hide(code); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	for _, u := range Unlabelled() {
		if u.Code == code {
			t.Fatal("a hidden costume is still in the backlog")
		}
	}
}

// A curated label must always beat one added at runtime: an admin must not be able to shadow a
// name trainers already rely on.
func TestCuratedLabelsWinOverTheOverlay(t *testing.T) {
	Init(t.TempDir())

	before, ok := SpriteURL(6, "Charizard", "Visor")
	if !ok {
		t.Fatal("the curated Visor did not resolve")
	}
	// Naming an unrelated code cannot disturb it.
	if err := Name("c:ROYAL_NOEVOLVE", "Royal Crown", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}
	after, _ := SpriteURL(6, "Charizard", "Visor")
	if before != after {
		t.Errorf("the curated Visor changed from %s to %s", before, after)
	}
}

// A truncated or corrupt overlay must not take the site down; it must fall back to the embedded
// set, which is what the picker had before anyone named anything.
func TestCorruptOverlayFallsBackInsteadOfCrashing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "labels.json"), []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	Init(dir)

	if _, ok := SpriteURL(6, "Charizard", "Visor"); !ok {
		t.Error("a corrupt overlay broke the curated labels")
	}
}

// The merged set is what the browser and the GitHub sync both consume, and it must keep the
// hand-written _comment blocks in labels.json rather than silently deleting them.
func TestLabelsJSONKeepsTheComments(t *testing.T) {
	Init(t.TempDir())
	if err := Name("c:ROYAL_NOEVOLVE", "Royal Crown", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}

	b, err := LabelsJSON()
	if err != nil {
		t.Fatalf("LabelsJSON: %v", err)
	}
	s := string(b)
	if !containsStr(s, "_comment") {
		t.Error("the sync would delete the notes in labels.json")
	}
	if !containsStr(s, "Royal Crown") {
		t.Error("the merged set does not contain the runtime name")
	}
	if !containsStr(s, "Visor") {
		t.Error("the merged set lost the curated labels")
	}
}

// The sync PRs LabelsJSON() straight back into labels.json, so with nothing named at runtime it has
// to reproduce that file byte for byte. It once did not: marshalling the parsed set re-sorted the
// hand-ordered species map and HTML-escaped the notes, which turned a no-op sync into a whole-file
// rewrite that could not be merged. This pins ordering, escaping, indentation and the comments all
// at once.
func TestLabelsJSONMatchesTheFileVerbatim(t *testing.T) {
	Init(t.TempDir())

	// Checked separately because the comparison below cannot see it: a CRLF checkout embeds CRLF,
	// so both sides match while the sync pushes CRLF at GitHub's LF blob and rewrites every line.
	// deploy.ps1 builds on Windows, so this is a real path. .gitattributes pins the file to LF.
	if bytes.Contains(labelsJSON, []byte("\r\n")) {
		t.Fatal("labels.json reached the compiler with CRLF line endings; the sync would rewrite " +
			"the whole file. Check .gitattributes, then: git add --renormalize internal/costumes/labels.json")
	}

	b, err := LabelsJSON()
	if err != nil {
		t.Fatalf("LabelsJSON: %v", err)
	}
	if string(b) != string(labelsJSON) {
		t.Errorf("LabelsJSON does not round-trip labels.json.\nA sync would rewrite the whole file.\n%s",
			firstDiff(string(labelsJSON), string(b)))
	}
}

// A name added in the admin panel must reach the file as ONE appended line, leaving every other byte
// alone, and must still parse back to the merged set.
func TestLabelsJSONAppendsAName(t *testing.T) {
	Init(t.TempDir())
	code, _ := anUnnamedCode(t)
	if err := Name(code, "Royal Crown", "tester"); err != nil {
		t.Fatalf("Name: %v", err)
	}

	b, err := LabelsJSON()
	if err != nil {
		t.Fatalf("LabelsJSON: %v", err)
	}

	before, after := splitLines(string(labelsJSON)), splitLines(string(b))
	if len(after) != len(before)+1 {
		t.Fatalf("naming one costume changed the line count by %d; want exactly 1 added",
			len(after)-len(before))
	}

	// The entry lands at the end of "shared", so the first line to change is the previous last one
	// picking up a comma, and the new entry carries none.
	at := -1
	for i := range before {
		if before[i] != after[i] {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatal("the name was not written into the file")
	}
	if after[at] != before[at]+"," {
		t.Errorf("the previous last entry was rewritten, not just comma'd\n got: %q\nwant: %q",
			after[at], before[at]+",")
	}
	want := `    { "label": "Royal Crown", "code": "` + code + `" }`
	if after[at+1] != want {
		t.Errorf("the appended line does not match the file's style\n got: %q\nwant: %q",
			after[at+1], want)
	}
	for i := at + 1; i < len(before); i++ {
		if before[i] != after[i+1] {
			t.Fatalf("line %d past the insertion was disturbed\n got: %q\nwant: %q",
				i+1, after[i+1], before[i])
		}
	}

	// It has to be valid JSON that carries the name, or the sync PRs a broken file.
	var round labelSet
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("the spliced file is not valid JSON: %v", err)
	}
	if !slices.Contains(round.Shared, shared{Label: "Royal Crown", Code: code}) {
		t.Error("the appended file lost the runtime name")
	}
}

// Once a sync PR merges, the label is in labels.json AND still in the overlay on disk. Nothing
// prunes it, so without dedup every sync would append another copy of it, for ever.
func TestLabelsJSONDoesNotReAppendAMergedName(t *testing.T) {
	Init(t.TempDir())
	if len(lab.Shared) == 0 {
		t.Skip("no curated shared labels to imitate")
	}
	merged := lab.Shared[0]

	// Stand in for the post-merge state: the overlay still holds an entry labels.json now carries.
	mu.Lock()
	ov = overlayFile{Shared: []overlayEntry{{Label: merged.Label, Code: merged.Code, By: "tester"}}}
	rebuildLocked()
	mu.Unlock()

	b, err := LabelsJSON()
	if err != nil {
		t.Fatalf("LabelsJSON: %v", err)
	}
	if string(b) != string(labelsJSON) {
		t.Error("a name already merged into labels.json was appended to it a second time")
	}
}

func splitLines(s string) []string {
	out := []string{}
	for start := 0; ; {
		i := start
		for i < len(s) && s[i] != '\n' {
			i++
		}
		out = append(out, s[start:i])
		if i >= len(s) {
			return out
		}
		start = i + 1
	}
}

// firstDiff points at the first line that differs, so a failure names the problem instead of dumping
// two copies of a 400-line file.
func firstDiff(want, got string) string {
	w, g := splitLines(want), splitLines(got)
	for i := 0; i < len(w) && i < len(g); i++ {
		if w[i] != g[i] {
			return fmt.Sprintf("first difference at line %d:\n want: %q\n  got: %q", i+1, w[i], g[i])
		}
	}
	return fmt.Sprintf("line counts differ: want %d, got %d", len(w), len(g))
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
