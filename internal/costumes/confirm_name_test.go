package costumes

import "testing"

// rename makes upstream call a held costume something else, which is what blocks its release.
func rename(t *testing.T, to string) {
	t.Helper()
	note, err := RecordCheck(testCode, to, "", false, "")
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if !note.Blocked {
		t.Fatalf("a rename to %q did not block the release; note = %+v", to, note)
	}
}

// TestConfirmNameIsTheWayOutOfARename is the regression test for a state with no exit.
//
// A held costume that upstream renames is by definition already labelled, so naming refuses it
// (add-only), un-naming refuses it as soon as a trainer has recorded it, and releasing refuses it
// too. Three doors, all locked, while the 409 told the admin to rename it, which is the one thing
// the package will never do. The mobile team found this by trying to build the button.
func TestConfirmNameIsTheWayOutOfARename(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Rename Hat", "2026-10-04")
	rename(t, "Test Rename Hat v2")

	label, was, err := ConfirmName(testCode, "hails")
	if err != nil {
		t.Fatalf("ConfirmName: %v", err)
	}
	if label != "Test Rename Hat" {
		t.Errorf("label = %q, want our label unchanged", label)
	}
	if was != "Test Rename Hat" {
		t.Errorf("previous upstream name = %q, want what we had verified against", was)
	}

	// The release now works, which is the whole point.
	note, err := RecordCheck(testCode, "", "", false, "hails")
	if err != nil {
		t.Fatalf("RecordCheck after confirm: %v", err)
	}
	if note.Blocked || !note.Released {
		t.Errorf("release after confirming = %+v, want released and not blocked", note)
	}
}

// TestConfirmNameArmsTheGuardAgain is the half that makes this safe to offer at all. Confirming
// must adopt the new upstream name as the baseline, not merely silence the warning: otherwise the
// NEXT rename, the one that might really have moved the artwork, sails through unnoticed.
func TestConfirmNameArmsTheGuardAgain(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Rename Hat", "2026-10-04")
	rename(t, "Test Rename Hat v2")

	if _, _, err := ConfirmName(testCode, "hails"); err != nil {
		t.Fatalf("ConfirmName: %v", err)
	}
	// Upstream saying the same thing again is not news.
	note, err := RecordCheck(testCode, "Test Rename Hat v2", "", false, "")
	if err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if note.Blocked {
		t.Error("the name we just confirmed blocked the release again")
	}
	// A different name is.
	rename(t, "Something Else Entirely")
}

// TestConfirmNameRefusesWhatItCannotAnswer: the button must not be a way to poke at holds in
// general, and "nothing to confirm" has to say so rather than silently succeeding, or an admin
// pressing it on the wrong row believes they fixed something.
func TestConfirmNameRefusesWhatItCannotAnswer(t *testing.T) {
	onDay(t, "2026-09-20")
	hold(t, "Test Rename Hat", "2026-10-04")

	if _, _, err := ConfirmName(testCode, "hails"); err == nil {
		t.Error("confirming a costume nobody renamed should fail, it means the admin misread the row")
	}
	if _, _, err := ConfirmName("f:NOT_HELD", "hails"); err == nil {
		t.Error("confirming a code that is not held should fail")
	}
}
