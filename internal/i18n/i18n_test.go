package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// anyEnKey returns an arbitrary real key from the embedded English bundle, so
// these tests stay valid even when individual translation keys are renamed.
func anyEnKey(t *testing.T) string {
	t.Helper()
	for k := range EnglishBundle() {
		return k
	}
	t.Fatal("embedded en bundle is empty")
	return ""
}

func writeOverlay(t *testing.T, dir, lang string, m map[string]string) {
	t.Helper()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal overlay: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, lang+".json"), data, 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
}

// An overlay file on disk is loaded by Init and wins over the embedded locale.
// This is the read path that makes approved translations live after a restart.
func TestInitLoadsOverlayOverride(t *testing.T) {
	dir := t.TempDir()
	key := anyEnKey(t)
	const want = "OVERLAY-WINS"
	writeOverlay(t, dir, "de", map[string]string{key: want})

	Init(dir)

	if got := TFunc("de")(key); got != want {
		t.Fatalf("TFunc(de)(%q) = %q, want overlay value %q", key, got, want)
	}
}

// Hardened residual risk #1: a key that no longer exists in embedded English is
// removed from the live overlay, but its translated text is archived to backup/
// (not silently lost) and the overlay file is rewritten so it is not archived
// again on the next start.
func TestInitArchivesStaleOverlayKey(t *testing.T) {
	dir := t.TempDir()
	real := anyEnKey(t)
	const stale = "__definitely_not_a_real_en_key__"
	const staleVal = "SHOULD-BE-ARCHIVED"
	writeOverlay(t, dir, "es", map[string]string{
		real:  "REAL-OK",
		stale: staleVal,
	})

	Init(dir)

	if got := OverlayCount("es"); got != 1 {
		t.Fatalf("OverlayCount(es) = %d, want 1 (stale key should be removed)", got)
	}
	if got := TFunc("es")(stale); got != stale {
		t.Fatalf("stale key should fall through to the key itself, got %q", got)
	}
	if got := TFunc("es")(real); got != "REAL-OK" {
		t.Fatalf("real overlay key = %q, want REAL-OK", got)
	}

	// The stale value must survive in a backup file, not vanish.
	entries, err := os.ReadDir(filepath.Join(dir, "backup"))
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "es_stale_") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "backup", e.Name()))
		if err != nil {
			t.Fatalf("read backup file: %v", err)
		}
		var m map[string]string
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("parse backup file: %v", err)
		}
		if m[stale] == staleVal {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale key %q was not archived to backup/ with its value", stale)
	}

	// The overlay file on disk must be rewritten without the stale key, so a
	// second Init does not re-archive it.
	raw, err := os.ReadFile(filepath.Join(dir, "es.json"))
	if err != nil {
		t.Fatalf("read overlay file: %v", err)
	}
	var onDisk map[string]string
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse overlay file: %v", err)
	}
	if _, ok := onDisk[stale]; ok {
		t.Fatalf("overlay file on disk still contains stale key %q", stale)
	}
}

// The core guarantee: an approved override written by ApplyOverride is on disk
// and survives a fresh Init, which is exactly what a deploy + service restart
// does. This is the write+reload path that keeps approved translations alive.
func TestApplyOverrideSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	Init(dir)
	key := anyEnKey(t)
	const want = "PERSISTED-VALUE"

	if err := ApplyOverride("fr", key, want); err != nil {
		t.Fatalf("ApplyOverride: %v", err)
	}
	if got := TFunc("fr")(key); got != want {
		t.Fatalf("after ApplyOverride TFunc(fr)(%q) = %q, want %q", key, got, want)
	}

	if _, err := os.Stat(filepath.Join(dir, "fr.json")); err != nil {
		t.Fatalf("overlay file was not written: %v", err)
	}

	// Simulate a deploy + restart: reload overlays from the same directory.
	Init(dir)
	if got := TFunc("fr")(key); got != want {
		t.Fatalf("after reload TFunc(fr)(%q) = %q, override did not survive restart", key, got)
	}
}
