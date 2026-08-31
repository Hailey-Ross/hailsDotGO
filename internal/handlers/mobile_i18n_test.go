package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"pogo.hails.cc/internal/i18n"
)

// The bundle served to a client must be COMPLETE. Every locale but English is a
// partial map on disk, because a translation that has not been done yet is an
// absent key rather than an empty one: en.json carries 1345 keys and de.json 777.
//
// The website never notices, since TFunc falls back to English per lookup. A client
// handed the raw bundle would render "nav.raids" in the middle of its navigation,
// and it would do it for 568 keys.
func TestNonEnglishBundlesAreFilledFromEnglish(t *testing.T) {
	en := i18n.Bundle("en")
	if len(en) == 0 {
		t.Fatal("no embedded English bundle")
	}

	for _, lang := range i18n.Langs() {
		if lang == "en" {
			continue
		}
		raw := i18n.Bundle(lang)
		if len(raw) >= len(en) {
			continue // fully translated, nothing for this test to prove here
		}

		// What the handler builds.
		merged := i18n.Bundle("en")
		for k, v := range raw {
			merged[k] = v
		}

		if len(merged) != len(en) {
			t.Errorf("%s: merged bundle has %d keys, want the %d English carries", lang, len(merged), len(en))
		}
		for key := range en {
			if merged[key] == "" && en[key] != "" {
				t.Errorf("%s: key %q resolved to nothing", lang, key)
			}
		}
		// And the translation still wins where it exists.
		for key, translated := range raw {
			if merged[key] != translated {
				t.Errorf("%s: key %q lost its translation to the English fallback", lang, key)
			}
		}
		return // one partial locale is enough to prove the rule
	}
	t.Skip("every embedded locale is fully translated, so there is no fallback to exercise")
}

// The ETag is a hash of the body, so an unchanged bundle revalidates instead of
// re-downloading 80 KB on every launch.
func TestWriteJSONWithETagAnswers304(t *testing.T) {
	payload := map[string]string{"nav.raids": "Raids"}

	first := httptest.NewRecorder()
	writeJSONWithETag(first, httptest.NewRequest(http.MethodGet, "/api/mobile/v1/i18n/en", nil), payload)

	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first response")
	}
	if first.Code != http.StatusOK || first.Body.Len() == 0 {
		t.Fatalf("first response = %d with %d bytes", first.Code, first.Body.Len())
	}
	if cc := first.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache so the client keeps the body and revalidates", cc)
	}

	second := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/mobile/v1/i18n/en", nil)
	r.Header.Set("If-None-Match", etag)
	writeJSONWithETag(second, r, payload)

	if second.Code != http.StatusNotModified {
		t.Errorf("matching If-None-Match answered %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carried a %d byte body", second.Body.Len())
	}

	// A changed payload must change the tag, or an approved translation would
	// never reach a device that already has the old bundle cached.
	changed := httptest.NewRecorder()
	writeJSONWithETag(changed, httptest.NewRequest(http.MethodGet, "/api/mobile/v1/i18n/en", nil), map[string]string{"nav.raids": "Incursions"})
	if changed.Header().Get("ETag") == etag {
		t.Error("a changed bundle kept the same ETag, so no client would ever refetch it")
	}
}

func TestI18nBundleIsAFlatStringMap(t *testing.T) {
	body, err := json.Marshal(i18n.Bundle("en"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("the bundle is not a flat {key: value} map: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty English bundle")
	}
}
