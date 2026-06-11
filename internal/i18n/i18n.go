package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed locales/*.json
var localeFS embed.FS

type bundle map[string]string

var (
	mu       sync.RWMutex
	embedded = map[string]bundle{} // compiled-in locales; immutable after init()
	runtime  = map[string]bool{}   // locales registered at runtime (DB-backed)
	overlay  = map[string]bundle{} // sparse approved overrides loaded from disk; never "en"
	dir      string                // overlay directory; set by Init
)

var codeRe = regexp.MustCompile(`^[a-z]{2}$`)

// init loads every embedded locale file, so a repo-synced new locale becomes
// a compiled-in language on the next build with no code change.
func init() {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		log.Fatalf("i18n: read embedded locales: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		lang := strings.TrimSuffix(name, ".json")
		data, err := localeFS.ReadFile("locales/" + name)
		if err != nil {
			log.Fatalf("i18n: read locale file %q: %v", name, err)
		}
		var b bundle
		if err := json.Unmarshal(data, &b); err != nil {
			log.Fatalf("i18n: parse %s: %v", name, err)
		}
		embedded[lang] = b
	}
	if _, ok := embedded["en"]; !ok {
		log.Fatal("i18n: embedded en.json is missing")
	}
}

// IsSupported reports whether code is an embedded or runtime-registered locale.
func IsSupported(code string) bool {
	mu.RLock()
	defer mu.RUnlock()
	if _, ok := embedded[code]; ok {
		return true
	}
	return runtime[code]
}

// RegisterLocale adds a runtime locale created through the translator
// workflow. It is a no-op error for en, invalid codes, and duplicates.
func RegisterLocale(code string) error {
	if !codeRe.MatchString(code) {
		return fmt.Errorf("i18n: invalid locale code %q", code)
	}
	if code == "en" {
		return fmt.Errorf("i18n: en is not registerable")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := embedded[code]; ok {
		return fmt.Errorf("i18n: locale %q already exists", code)
	}
	if runtime[code] {
		return fmt.Errorf("i18n: locale %q already exists", code)
	}
	runtime[code] = true
	return nil
}

// DropLocale removes a runtime locale: its overlay file (if any) is moved to
// the backup directory and the in-memory registration and overrides are
// dropped. Embedded locales are refused.
func DropLocale(code string) error {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := embedded[code]; ok {
		return fmt.Errorf("i18n: locale %q is compiled in and cannot be removed", code)
	}
	if !runtime[code] {
		return fmt.Errorf("i18n: locale %q is not registered", code)
	}
	livePath := filepath.Join(dir, code+".json")
	if _, err := os.Stat(livePath); err == nil {
		backupDir := filepath.Join(dir, "backup")
		if err := os.MkdirAll(backupDir, 0o755); err != nil {
			return fmt.Errorf("i18n: create backup dir: %w", err)
		}
		backupPath := filepath.Join(backupDir, fmt.Sprintf("%s_%s.json", code, time.Now().Format("20060102-150405")))
		if err := os.Rename(livePath, backupPath); err != nil {
			return fmt.Errorf("i18n: archive overlay: %w", err)
		}
	}
	delete(runtime, code)
	delete(overlay, code)
	return nil
}

// IsEmbedded reports whether code is compiled into the binary.
func IsEmbedded(code string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := embedded[code]
	return ok
}

// Langs returns all supported locale codes, en first then sorted.
func Langs() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := []string{"en"}
	rest := make([]string, 0, len(embedded)+len(runtime))
	for lang := range embedded {
		if lang != "en" {
			rest = append(rest, lang)
		}
	}
	for lang := range runtime {
		rest = append(rest, lang)
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// OverlayLangs returns the locales that currently have at least one approved
// override on disk, sorted. These are the files worth syncing to the repo.
func OverlayLangs() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(overlay))
	for lang, b := range overlay {
		if len(b) > 0 {
			out = append(out, lang)
		}
	}
	slices.Sort(out)
	return out
}

// Init loads approved translation overrides from localesDir (default "locales").
// Overlay files are sparse: they contain only keys that differ from the
// embedded locales, so a newer binary's strings are never shadowed except for
// keys explicitly changed through the translator workflow. Parse errors are
// logged and skipped, never fatal.
func Init(localesDir string) {
	if localesDir == "" {
		localesDir = "locales"
	}
	mu.Lock()
	defer mu.Unlock()
	dir = localesDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("i18n: create overlay dir %q: %v", dir, err)
		return
	}
	en := embedded["en"]
	langs := make([]string, 0, len(embedded)+len(runtime))
	for lang := range embedded {
		langs = append(langs, lang)
	}
	for lang := range runtime {
		langs = append(langs, lang)
	}
	for _, lang := range langs {
		if lang == "en" {
			continue
		}
		path := filepath.Join(dir, lang+".json")
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			log.Printf("i18n: read overlay %s: %v", path, err)
			continue
		}
		var b bundle
		if err := json.Unmarshal(data, &b); err != nil {
			log.Printf("i18n: parse overlay %s: %v (skipped)", path, err)
			continue
		}
		for key := range b {
			if _, ok := en[key]; !ok {
				log.Printf("i18n: overlay %s: dropping stale key %q (not in embedded en)", path, key)
				delete(b, key)
			}
		}
		overlay[lang] = b
		log.Printf("i18n: loaded %d overlay keys for %q from %s", len(b), lang, path)
	}
}

// snapshot returns the current maps for lang under a read lock. The returned
// maps are replaced wholesale on reload, never mutated in place, so callers
// may read them without further locking.
func snapshot(lang string) (ov, emb, en bundle) {
	mu.RLock()
	defer mu.RUnlock()
	emb, ok := embedded[lang]
	if !ok {
		emb = embedded["en"]
	}
	return overlay[lang], emb, embedded["en"]
}

// TFunc returns a translation function for the given language code.
// Lookup order: disk overlay, embedded locale, embedded English, the key itself.
func TFunc(lang string) func(string) string {
	return TFuncWithOverrides(lang, nil)
}

// TFuncWithOverrides is TFunc with an extra highest-priority overrides map,
// used to preview a translator's own pending edits. A nil map behaves
// exactly like TFunc.
func TFuncWithOverrides(lang string, overrides map[string]string) func(string) string {
	ov, emb, en := snapshot(lang)
	return func(key string) string {
		if v, ok := overrides[key]; ok && v != "" {
			return v
		}
		if v, ok := ov[key]; ok && v != "" {
			return v
		}
		if v, ok := emb[key]; ok && v != "" {
			return v
		}
		if v, ok := en[key]; ok && v != "" {
			return v
		}
		return key
	}
}

// Bundle returns a merged copy (embedded plus overlay) of the locale.
func Bundle(lang string) map[string]string {
	ov, emb, _ := snapshot(lang)
	out := make(map[string]string, len(emb))
	maps.Copy(out, emb)
	maps.Copy(out, ov)
	return out
}

// EnglishBundle returns a copy of the embedded English locale.
func EnglishBundle() map[string]string {
	return Bundle("en")
}

// HasKey reports whether key exists in the embedded English locale.
func HasKey(key string) bool {
	_, ok := embedded["en"][key]
	return ok
}

// OverlayCount returns the number of approved override keys for lang.
func OverlayCount(lang string) int {
	mu.RLock()
	defer mu.RUnlock()
	return len(overlay[lang])
}

// ApplyOverride hot-applies an approved translation: it backs up the current
// overlay file (or the merged bundle on first approval) to dir/backup/, writes
// the updated overlay atomically, and only then updates the in-memory store.
// On any error the in-memory state is untouched and the caller must not mark
// the edit approved.
func ApplyOverride(lang, key, value string) error {
	if lang == "en" || !IsSupported(lang) {
		return fmt.Errorf("i18n: locale %q is not editable", lang)
	}
	if !HasKey(key) {
		return fmt.Errorf("i18n: unknown key %q", key)
	}

	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return fmt.Errorf("i18n: overlay directory not initialized")
	}

	next := make(bundle, len(overlay[lang])+1)
	maps.Copy(next, overlay[lang])
	next[key] = value

	backupDir := filepath.Join(dir, "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("i18n: create backup dir: %w", err)
	}
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s_%s.json", lang, time.Now().Format("20060102-150405")))
	livePath := filepath.Join(dir, lang+".json")
	if prev, err := os.ReadFile(livePath); err == nil {
		if err := os.WriteFile(backupPath, prev, 0o644); err != nil {
			return fmt.Errorf("i18n: write backup: %w", err)
		}
	} else if os.IsNotExist(err) {
		// First approval for this locale: snapshot the merged state so a
		// revert target exists even though no overlay file did. Runtime
		// locales have no embedded bundle, so the English source is the
		// effective current state.
		src := embedded[lang]
		if src == nil {
			src = embedded["en"]
		}
		merged := make(bundle, len(src))
		maps.Copy(merged, src)
		data, jerr := json.MarshalIndent(merged, "", "  ")
		if jerr != nil {
			return fmt.Errorf("i18n: marshal backup: %w", jerr)
		}
		if werr := os.WriteFile(backupPath, data, 0o644); werr != nil {
			return fmt.Errorf("i18n: write backup: %w", werr)
		}
	} else {
		return fmt.Errorf("i18n: read current overlay: %w", err)
	}

	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("i18n: marshal overlay: %w", err)
	}
	tmpPath := filepath.Join(dir, "."+lang+".json.tmp")
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("i18n: write overlay temp file: %w", err)
	}
	if err := os.Rename(tmpPath, livePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("i18n: replace overlay file: %w", err)
	}

	overlay[lang] = next
	return nil
}
